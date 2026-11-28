package sync

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/materialize"
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
// is reconciled (Podium's entries stripped) rather than removed. FileHash
// records, for a standalone path, the materialize.FileHash of the bytes sync
// left on disk; the cleanup deletes the path only while its bytes still hash
// to it, so a forged entry or an operator's edit keeps the file. A
// config-merge path records none, because the cleanup never deletes it. Type
// records the artifact type the run's §7.5.1 type filter judged, so a later
// run can judge the filter for an artifact whose manifest no longer parses
// (§13.11.3). PackageDigest and Attestations are the lock version 2 members
// (§7.5.3): the package digest the consumer computed itself, and the
// bundles that met a requirement with the signer the consumer verified.
// Every entry of one artifact carries the same values.
type LockArtifact struct {
	ID               string `yaml:"id"`
	Version          string `yaml:"version,omitempty"`
	Type             string `yaml:"type,omitempty"`
	ContentHash      string `yaml:"content_hash,omitempty"`
	Layer            string `yaml:"layer,omitempty"`
	MaterializedPath string `yaml:"materialized_path,omitempty"`
	FileHash         string `yaml:"file_hash,omitempty"`
	Merge            string `yaml:"merge,omitempty"`
	PackageDigest    string `yaml:"package_digest,omitempty"`
	// Attestations are never copied from a served attestations member.
	Attestations []LockAttestation `yaml:"attestations,omitempty"`
}

// LockAttestation is one bundle that met a requirement of the §4.7.11
// attestation check, with the signer the consumer verified (§7.5.3).
type LockAttestation struct {
	Digest        string     `yaml:"digest"`
	PredicateType string     `yaml:"predicate_type,omitempty"`
	Signer        LockSigner `yaml:"signer"`
}

// LockSigner is a signer the consumer verified: the instance name its own
// policy gives the trusted root, and the issuer and identity of the
// certificate, or, for a key-based signer, an empty instance and issuer and
// the key's identifier as the identity.
type LockSigner struct {
	Instance string `yaml:"instance,omitempty"`
	Issuer   string `yaml:"issuer,omitempty"`
	Identity string `yaml:"identity"`
}

// String renders the signer for standard error.
func (s LockSigner) String() string {
	if s.Instance == "" && s.Issuer == "" {
		return "key " + s.Identity
	}
	return s.Instance + " " + s.Issuer + " " + s.Identity
}

// LockVersion is the version of the lock format this release writes.
// Version 2 adds the attestation members of an entry (§7.5.3). A lock of
// version 1, or one that records no version, is read as before.
const LockVersion = 2

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
// file returns (nil, nil); other errors are returned. The lock is read through
// materialize.ReadStateFile, so a committed lock link that resolves outside
// the target or into .git is refused rather than followed (§7.5.3, §7.8). A
// .podium that is a symbolic link or not a directory returns an error that
// wraps ErrStateDirUnusable.
func ReadLock(target string) (*LockFile, error) {
	data, err := materialize.ReadStateFile(target, lockRelPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, stateDirError(target, err)
	}
	var lf LockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("lock: invalid yaml: %w", err)
	}
	// Spec: §7.5.3 — a lock of a later version is a lock this release
	// cannot read, so its members are never taken as a version 2 lock's.
	if lf.Version > LockVersion || lf.Version < 0 {
		return nil, fmt.Errorf("lock: version %d is not a lock version this release reads (1 to %d)", lf.Version, LockVersion)
	}
	return &lf, nil
}

// WriteLock writes the lock file atomically (`.tmp` + rename) so readers
// see either the previous or the new content.
//
// Spec: §7.5.3 states the list order: entries are ordered by id, and the
// entries one artifact contributes are ordered among themselves by
// materialized_path. The sort lands here because WriteLock is the single point
// every writer of the file passes, covering sync.Run, the §7.5.5 override
// paths, and the §7.8 publish render.
//
// The sync path hands over its materialized set in this order and appends the
// entries of any artifact whose previous output it kept (§13.11.3), which the
// sort places. The sort also normalizes a lock an override rewrites in place
// and a lock an older build wrote in a different order.
//
// The normalization applies to a copy. Every step after it can fail, and a
// caller that sorted the slice it passed in would find its own entries
// reordered by a call that wrote nothing, so the caller's LockFile and its
// artifacts slice are left exactly as they were handed over.
func WriteLock(target string, lf *LockFile) error {
	out := *lf
	// Spec: §7.5.3 — every write records the version this release writes,
	// so a lock of version 1 that a run or an override rewrites becomes
	// version 2.
	out.Version = LockVersion
	out.Artifacts = slices.Clone(lf.Artifacts)
	slices.SortStableFunc(out.Artifacts, func(a, b LockArtifact) int {
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return strings.Compare(a.MaterializedPath, b.MaterializedPath)
	})
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	// The write goes through materialize.WriteStateFile: a committed .podium
	// link refuses the write, and the temporary is created exclusively, so a
	// committed sync.lock.tmp link cannot redirect it outside the target
	// (§7.8).
	return writeStateFile(target, lockRelPath, data)
}

// lockRelPath is the lock file's slash-separated path below a target.
const lockRelPath = ".podium/sync.lock"
