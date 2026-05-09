package testharness

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// resetActivePhaseForTest clears the cached resolution so tests can override
// PODIUM_PHASE per-case. Test-only — not exported in the production API.
func resetActivePhaseForTest() {
	activePhaseOnce = sync.Once{}
	activePhase = 0
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §10).
// Phase: 0
func TestActivePhase_ReadsEnvVar(t *testing.T) {
	resetActivePhaseForTest()
	t.Setenv(envActivePhase, "5")
	if got := ActivePhase(); got != 5 {
		t.Fatalf("ActivePhase() = %d, want 5", got)
	}
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §10).
// Phase: 0
func TestActivePhase_ReadsPhaseFile(t *testing.T) {
	resetActivePhaseForTest()
	t.Setenv(envActivePhase, "")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, phaseFileName), []byte("3\n"), 0o644); err != nil {
		t.Fatalf("write .phase: %v", err)
	}
	t.Chdir(dir)

	if got := ActivePhase(); got != 3 {
		t.Fatalf("ActivePhase() = %d, want 3", got)
	}
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §10).
// Phase: 0
func TestActivePhase_DefaultsToZero(t *testing.T) {
	resetActivePhaseForTest()
	t.Setenv(envActivePhase, "")
	t.Chdir(t.TempDir())

	if got := ActivePhase(); got != 0 {
		t.Fatalf("ActivePhase() = %d, want 0", got)
	}
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §10).
// Phase: 0
func TestRequirePhase_SkipsBelowActive(t *testing.T) {
	resetActivePhaseForTest()
	t.Setenv(envActivePhase, "2")

	rec := &skipRecorder{TB: t}
	RequirePhase(rec, 5)
	if !rec.skipped {
		t.Fatalf("RequirePhase(5) at active=2 did not skip")
	}
	if want := "requires phase 5 (active phase: 2)"; rec.skipMsg != want {
		t.Fatalf("skip message = %q, want %q", rec.skipMsg, want)
	}
}

// Spec: n/a — internal harness primitive (TEST_INFRASTRUCTURE_PLAN.md §10).
// Phase: 0
func TestRequirePhase_RunsAtOrAboveActive(t *testing.T) {
	resetActivePhaseForTest()
	t.Setenv(envActivePhase, "5")

	rec := &skipRecorder{TB: t}
	RequirePhase(rec, 5)
	if rec.skipped {
		t.Fatalf("RequirePhase(5) at active=5 unexpectedly skipped")
	}
	RequirePhase(rec, 3)
	if rec.skipped {
		t.Fatalf("RequirePhase(3) at active=5 unexpectedly skipped")
	}
}

// skipRecorder is a testing.TB shim that records Skipf calls instead of
// invoking the real test framework's skip machinery.
type skipRecorder struct {
	testing.TB
	skipped bool
	skipMsg string
}

func (s *skipRecorder) Skipf(format string, args ...any) {
	s.skipped = true
	s.skipMsg = sprintfNoNewline(format, args...)
}

func (s *skipRecorder) SkipNow() { s.skipped = true }

func (s *skipRecorder) Helper() {}

func sprintfNoNewline(format string, args ...any) string {
	// minimal stdlib-only fmt.Sprintf wrapper
	return formatPhaseMsg(format, args...)
}

func formatPhaseMsg(format string, args ...any) string {
	// We deliberately avoid pulling in fmt for hot path; use strconv for ints.
	// The only callers use the fixed format string above with two ints.
	if format != "requires phase %d (active phase: %d)" || len(args) != 2 {
		return format
	}
	n1, ok1 := args[0].(int)
	n2, ok2 := args[1].(int)
	if !ok1 || !ok2 {
		return format
	}
	return "requires phase " + strconv.Itoa(n1) + " (active phase: " + strconv.Itoa(n2) + ")"
}
