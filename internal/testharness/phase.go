// Package testharness collects reusable helpers for Podium's test suite.
// It is the only place where test-only code lives outside *_test.go files,
// so that tests across packages can import shared primitives.
package testharness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	envActivePhase = "PODIUM_PHASE"
	phaseFileName  = ".phase"
)

var (
	activePhaseOnce sync.Once
	activePhase     int
)

// ActivePhase returns the active build phase. The value is taken from the
// PODIUM_PHASE environment variable when set; otherwise it is read from the
// .phase file at the repository root. The result is cached for the test
// binary's lifetime.
func ActivePhase() int {
	activePhaseOnce.Do(func() {
		activePhase = resolveActivePhase()
	})
	return activePhase
}

func resolveActivePhase() int {
	if env := strings.TrimSpace(os.Getenv(envActivePhase)); env != "" {
		n, err := strconv.Atoi(env)
		if err == nil && n >= 0 {
			return n
		}
	}
	root, err := repoRoot()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(root, phaseFileName))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// repoRoot walks up from the working directory looking for a .phase file.
// It is best-effort; failure resolves the active phase to 0.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, phaseFileName)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// RequirePhase skips the test when the active phase is below n. Tests that
// depend on machinery built in a later phase call this at the top of the
// test body. Use 0 for tests that always run.
func RequirePhase(t testing.TB, n int) {
	t.Helper()
	if active := ActivePhase(); active < n {
		t.Skipf("requires phase %d (active phase: %d)", n, active)
	}
}
