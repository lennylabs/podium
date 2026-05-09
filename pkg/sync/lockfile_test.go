package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §7.5.3 Lock File — every field in the schema round-trips through
// ReadLock / WriteLock without loss.
// Phase: 3
func TestLockFile_RoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 3)
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
// Phase: 3
func TestLockFile_MissingFileReturnsNil(t *testing.T) {
	testharness.RequirePhase(t, 3)
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
// Phase: 3
func TestLockFile_WriteAtomicCreatesDir(t *testing.T) {
	testharness.RequirePhase(t, 3)
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
// Phase: 3
func TestLockFile_InvalidYAML(t *testing.T) {
	testharness.RequirePhase(t, 3)
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
