package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// nullProfile serializes the empty profile name as the YAML null literal
// (`profile: null`) instead of omitting the key, matching the §7.5.3 lock
// schema annotation ("null when no profile was used"). A present name
// serializes as a plain string. The default reflection-based decoder reads
// both `profile: null` and `profile: finance-team` back into a string, so no
// custom unmarshaler is needed.
//
// spec: §7.5.3 — the lock's `profile:` field is null when no profile was used.
type nullProfile string

// MarshalYAML emits null for the empty name and the bare string otherwise.
func (p nullProfile) MarshalYAML() (any, error) {
	if p == "" {
		return nil, nil
	}
	return string(p), nil
}

// LockFile is the per-target sync state stored at <target>/.podium/sync.lock
// (spec §7.5.3).
type LockFile struct {
	Version int `yaml:"version"`
	// Profile is the active profile for this target. It is written without
	// omitempty so a target synced with no active profile records the explicit
	// `profile: null` the §7.5.3 schema documents rather than dropping the key.
	Profile      nullProfile    `yaml:"profile"`
	Scope        LockScope      `yaml:"scope,omitempty"`
	Harness      string         `yaml:"harness,omitempty"`
	Target       string         `yaml:"target,omitempty"`
	LastSyncedAt time.Time      `yaml:"last_synced_at,omitempty"`
	LastSyncedBy string         `yaml:"last_synced_by,omitempty"`
	Artifacts    []LockArtifact `yaml:"artifacts,omitempty"`
	Toggles      LockToggles    `yaml:"toggles,omitempty"`
}

// LockScope captures the resolved scope from the active profile or CLI
// flags (§7.5.3).
type LockScope struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	Type    []string `yaml:"type,omitempty"`
}

// LockArtifact is one entry in artifacts: per §7.5.3. Merge records the
// §6.7 config-merge kind for the materialized path ("json" for a JSON
// config-merge, "inject" for a marker-based inject, empty for a standalone
// file). Stale-file cleanup uses it: a standalone path is deleted when no
// longer written, but a config-merge path is shared with the operator, so it
// is reconciled (Podium's entries stripped) rather than removed.
type LockArtifact struct {
	ID               string `yaml:"id"`
	Version          string `yaml:"version,omitempty"`
	ContentHash      string `yaml:"content_hash,omitempty"`
	Layer            string `yaml:"layer,omitempty"`
	MaterializedPath string `yaml:"materialized_path,omitempty"`
	Merge            string `yaml:"merge,omitempty"`
}

// LockToggles tracks ephemeral overrides applied since the last full
// sync (§7.5.5).
type LockToggles struct {
	Add    []LockToggle `yaml:"add,omitempty"`
	Remove []LockToggle `yaml:"remove,omitempty"`
}

// LockToggle is one entry in toggles.add or toggles.remove.
type LockToggle struct {
	ID        string    `yaml:"id"`
	Version   string    `yaml:"version,omitempty"`
	AddedAt   time.Time `yaml:"added_at,omitempty"`
	RemovedAt time.Time `yaml:"removed_at,omitempty"`
}

// LockFilePath returns the canonical lock-file path for a target directory.
func LockFilePath(target string) string {
	return filepath.Join(target, ".podium", "sync.lock")
}

// ReadLock reads and parses an existing lock file at target. A missing
// file returns (nil, nil); other errors are returned.
func ReadLock(target string) (*LockFile, error) {
	data, err := os.ReadFile(LockFilePath(target))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lf LockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("lock: invalid yaml: %w", err)
	}
	return &lf, nil
}

// WriteLock writes the lock file atomically (`.tmp` + rename) so readers
// see either the previous or the new content.
//
// The artifacts list is ordered here rather than by each caller. §11 requires a
// sync against a filesystem registry and one against a standalone server on the
// same directory to produce an identical artifacts list, and the two walked
// their sources in different orders, so every position in the list disagreed
// while the records themselves matched. Ordering at the single write point makes
// the two agree without either consumer knowing about the other, and it keeps a
// committed lock (§14.11) diffing on what changed rather than on what moved.
//
// The key is the id and then the materialized path, because one artifact holds
// one entry per file it writes and a skill writes two.
func WriteLock(target string, lf *LockFile) error {
	if lf.Version == 0 {
		lf.Version = 1
	}
	slices.SortStableFunc(lf.Artifacts, func(a, b LockArtifact) int {
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return strings.Compare(a.MaterializedPath, b.MaterializedPath)
	})
	data, err := yaml.Marshal(lf)
	if err != nil {
		return err
	}
	dir := filepath.Join(target, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := LockFilePath(target)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
