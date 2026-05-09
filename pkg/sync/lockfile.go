package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// LockFile is the per-target sync state stored at <target>/.podium/sync.lock
// (spec §7.5.3).
type LockFile struct {
	Version       int            `yaml:"version"`
	Profile       string         `yaml:"profile,omitempty"`
	Scope         LockScope      `yaml:"scope,omitempty"`
	Harness       string         `yaml:"harness,omitempty"`
	Target        string         `yaml:"target,omitempty"`
	LastSyncedAt  time.Time      `yaml:"last_synced_at,omitempty"`
	LastSyncedBy  string         `yaml:"last_synced_by,omitempty"`
	Artifacts     []LockArtifact `yaml:"artifacts,omitempty"`
	Toggles       LockToggles    `yaml:"toggles,omitempty"`
}

// LockScope captures the resolved scope from the active profile or CLI
// flags (§7.5.3).
type LockScope struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	Type    []string `yaml:"type,omitempty"`
}

// LockArtifact is one entry in artifacts: per §7.5.3.
type LockArtifact struct {
	ID               string `yaml:"id"`
	Version          string `yaml:"version,omitempty"`
	ContentHash      string `yaml:"content_hash,omitempty"`
	Layer            string `yaml:"layer,omitempty"`
	MaterializedPath string `yaml:"materialized_path,omitempty"`
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
func WriteLock(target string, lf *LockFile) error {
	if lf.Version == 0 {
		lf.Version = 1
	}
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
