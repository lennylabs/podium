package server_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §13.2.1 / §6.10 — write endpoints rejected in read-only
// mode with registry.read_only.
// Phase: 2
// Matrix: §6.10 (registry.read_only)
func TestModeTracker_CheckWriteInReadOnly(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	m := server.NewModeTracker()
	if err := m.CheckWrite(); err != nil {
		t.Errorf("ready mode write: %v", err)
	}
	m.Set(server.ModeReadOnly)
	err := m.CheckWrite()
	if !errors.Is(err, server.ErrReadOnly) {
		t.Errorf("got %v, want ErrReadOnly", err)
	}
}

// Spec: §13.2.1 / §6.10 — configuration edits rejected with
// config.read_only.
// Phase: 2
// Matrix: §6.10 (config.read_only)
func TestModeTracker_CheckConfigInReadOnly(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	m := server.NewModeTracker()
	m.Set(server.ModeReadOnly)
	err := m.CheckConfig()
	if !errors.Is(err, server.ErrReadOnlyConfig) {
		t.Errorf("got %v, want ErrReadOnlyConfig", err)
	}
}

// Spec: §13.2.1 — Mode.String() renders the documented values.
// Phase: 2
func TestMode_StringMatchesSpec(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	cases := []struct {
		m    server.Mode
		want string
	}{
		{server.ModeReady, "ready"},
		{server.ModeReadOnly, "read_only"},
		{server.ModeNotReady, "not_ready"},
	}
	for _, c := range cases {
		if c.m.String() != c.want {
			t.Errorf("Mode(%d).String() = %q, want %q", c.m, c.m.String(), c.want)
		}
	}
}

// Spec: §13.2.1 — toggling back to ready re-enables writes.
// Phase: 2
func TestModeTracker_BackToReadyAllowsWrites(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	m := server.NewModeTracker()
	m.Set(server.ModeReadOnly)
	m.Set(server.ModeReady)
	if err := m.CheckWrite(); err != nil {
		t.Errorf("after recovery: %v", err)
	}
}
