package sync_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §6.10 / §13.11 — sync without a configured registry source
// fails with config.no_registry so the user is pointed at
// `podium init` rather than a confusing filesystem-not-found error.
// Phase: 0
// Matrix: §6.10 (config.no_registry)
func TestRun_NoRegistryReturnsConfigError(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	_, err := sync.Run(sync.Options{
		RegistryPath: "",
		Target:       t.TempDir(),
	})
	if !errors.Is(err, sync.ErrNoRegistry) {
		t.Fatalf("got %v, want ErrNoRegistry", err)
	}
}
