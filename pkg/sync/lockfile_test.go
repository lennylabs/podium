package sync

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Spec: §7.5.3 Lock File — every field in the schema round-trips through
// ReadLock / WriteLock without loss.
func TestLockFile_RoundTrip(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	in := &LockFile{
		Version:      1,
		Profile:      "finance-team",
		Harness:      "claude-code",
		Target:       target,
		LastSyncedBy: "full",
		Scope: LockScope{
			Include: []string{"finance/**"},
			Exclude: []string{"finance/**/legacy/**"},
			Type:    []string{"skill", "agent"},
		},
		Artifacts: []LockArtifact{
			{
				ID:               "finance/ap/pay-invoice",
				Version:          "1.2.0",
				ContentHash:      "sha256:abc",
				Layer:            "team-finance",
				MaterializedPath: "agents/pay-invoice.md",
			},
		},
	}
	if err := WriteLock(target, in); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	out, err := ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if out.Version != in.Version || out.Profile != in.Profile || out.Harness != in.Harness {
		t.Errorf("scalar mismatch: got %+v", out)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].ID != "finance/ap/pay-invoice" {
		t.Errorf("artifacts mismatch: %+v", out.Artifacts)
	}
	if len(out.Scope.Include) != 1 || out.Scope.Include[0] != "finance/**" {
		t.Errorf("scope.include mismatch: %+v", out.Scope.Include)
	}
}

// Spec: §7.5.3 — ReadLock on a missing file returns (nil, nil); callers
// treat this as "no previous sync against this target."
func TestLockFile_MissingFileReturnsNil(t *testing.T) {
	t.Parallel()
	out, err := ReadLock(t.TempDir())
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil, got %+v", out)
	}
}

// Spec: §7.5.3 — WriteLock creates `.podium/` if absent and writes the
// file atomically (no .tmp left behind on success).
func TestLockFile_WriteAtomicCreatesDir(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := WriteLock(target, &LockFile{Version: 1}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".podium", "sync.lock")); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".podium", "sync.lock.tmp")); !os.IsNotExist(err) {
		t.Errorf("temp file leaked: %v", err)
	}
}

// Spec: §7.5.3 — invalid YAML yields a wrapped error (not a panic).
func TestLockFile_InvalidYAML(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	dir := filepath.Join(target, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sync.lock"), []byte(": bad:"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := ReadLock(target)
	if err == nil {
		t.Fatalf("expected error on invalid YAML")
	}
}

// lockIDPaths flattens a lock's entries to "id path" strings so a test can
// state the expected order as one readable slice.
func lockIDPaths(as []LockArtifact) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.ID+" "+a.MaterializedPath)
	}
	return out
}

// Spec: §7.5.3 — the artifacts list is ordered by id, and the entries one
// artifact contributes are ordered among themselves by materialized_path.
// WriteLock is the single point every writer passes, so it normalizes an
// unordered set rather than trusting its caller to have sorted one.
func TestLockFile_WriteSortsEntriesByIDThenPath(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	in := &LockFile{
		Version: 1,
		Artifacts: []LockArtifact{
			{ID: "team/review", MaterializedPath: "skills/review/SKILL.md"},
			{ID: "team/audit", MaterializedPath: "agents/audit.md"},
			{ID: "team/review", MaterializedPath: "skills/review/ARTIFACT.md"},
			{ID: "acme/deploy", MaterializedPath: "agents/deploy.md"},
		},
	}
	if err := WriteLock(target, in); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	out, err := ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	want := []string{
		"acme/deploy agents/deploy.md",
		"team/audit agents/audit.md",
		"team/review skills/review/ARTIFACT.md",
		"team/review skills/review/SKILL.md",
	}
	got := lockIDPaths(out.Artifacts)
	if !slices.Equal(got, want) {
		t.Errorf("lock order:\n got %v\nwant %v", got, want)
	}
}

// Spec: §7.5.3 — the ordering is a property of the written file. Every step
// after the sort can fail, so WriteLock normalizes a copy and the caller's
// value survives a successful and a failed write exactly as it was handed over.
func TestLockFile_WriteLeavesTheCallersValueUntouched(t *testing.T) {
	t.Parallel()
	unsorted := []LockArtifact{
		{ID: "team/review", MaterializedPath: "skills/review/SKILL.md"},
		{ID: "acme/deploy", MaterializedPath: "agents/deploy.md"},
	}
	wantOrder := lockIDPaths(unsorted)

	// A regular file as the target makes MkdirAll fail, which is the first
	// failable step and the one that used to run after the caller's slice had
	// already been reordered.
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, tc := range []struct {
		name      string
		target    string
		version   int
		wantWrite bool
	}{
		{name: "write succeeds", target: t.TempDir(), version: 1, wantWrite: true},
		{name: "version left to the default", target: t.TempDir(), version: 0, wantWrite: true},
		{name: "write fails", target: notADir, version: 1, wantWrite: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lf := &LockFile{Version: tc.version, Artifacts: slices.Clone(unsorted)}
			err := WriteLock(tc.target, lf)
			if tc.wantWrite != (err == nil) {
				t.Fatalf("WriteLock error = %v, want write = %v", err, tc.wantWrite)
			}
			if !tc.wantWrite {
				// Pin where the write failed. A future up-front validation that
				// returned before MkdirAll would leave this case asserting
				// nothing about the order the sort runs in.
				var pe *os.PathError
				if !errors.As(err, &pe) || pe.Op != "mkdir" {
					t.Errorf("want the failure at MkdirAll, got %v", err)
				}
			}
			if got := lockIDPaths(lf.Artifacts); !slices.Equal(got, wantOrder) {
				t.Errorf("caller's entries were reordered:\n got %v\nwant %v", got, wantOrder)
			}
			if lf.Version != tc.version {
				t.Errorf("caller's Version = %d, want %d", lf.Version, tc.version)
			}
		})
	}
}

// Spec: §7.5.3 — the lock schema carries `version: 1`. A caller that leaves the
// field unset gets the schema version in the written file. WriteLock stamps it
// on the copy it marshals, so the file is the only place the default is
// observable.
func TestLockFile_WriteFillsInTheSchemaVersion(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := WriteLock(target, &LockFile{Target: target}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	out, err := ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if out.Version != 1 {
		t.Errorf("written version = %d, want 1", out.Version)
	}
}
