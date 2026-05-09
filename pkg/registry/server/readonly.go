package server

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// Errors related to §13.2.1 read-only mode.
var (
	// ErrReadOnly signals a write rejected because the registry is
	// running in read-only mode. Maps to registry.read_only in §6.10.
	ErrReadOnly = errors.New("registry.read_only")
	// ErrReadOnlyConfig signals an attempt to change configuration
	// while the registry is in read-only mode. Maps to
	// config.read_only in §6.10.
	ErrReadOnlyConfig = errors.New("config.read_only")
)

// Mode is one of the §13.9 health states.
type Mode int32

// Mode values per §13.2.1.
const (
	ModeReady Mode = iota
	ModeReadOnly
	ModeNotReady
)

// String renders the mode for §13.2.1 X-Podium-Read-Only headers and
// /healthz responses.
func (m Mode) String() string {
	switch m {
	case ModeReady:
		return "ready"
	case ModeReadOnly:
		return "read_only"
	case ModeNotReady:
		return "not_ready"
	}
	return "unknown"
}

// ModeTracker is the §13.2.1 state machine. It records the current
// mode and exposes an atomic check used by write-path handlers
// (ingest webhooks, admin operations, configuration edits).
type ModeTracker struct {
	current atomic.Int32
}

// NewModeTracker returns a tracker initialized to ModeReady.
func NewModeTracker() *ModeTracker { return &ModeTracker{} }

// Set replaces the current mode.
func (m *ModeTracker) Set(mode Mode) { m.current.Store(int32(mode)) }

// Get returns the current mode.
func (m *ModeTracker) Get() Mode { return Mode(m.current.Load()) }

// CheckWrite returns ErrReadOnly when the registry is in read-only
// mode. Write-path handlers call this before mutating state.
func (m *ModeTracker) CheckWrite() error {
	if m.Get() == ModeReadOnly {
		return fmt.Errorf("%w: registry is in read-only mode", ErrReadOnly)
	}
	return nil
}

// CheckConfig returns ErrReadOnlyConfig when the registry is in
// read-only mode. Configuration edits (admin grants, layer config,
// freeze toggles) call this before applying.
func (m *ModeTracker) CheckConfig() error {
	if m.Get() == ModeReadOnly {
		return fmt.Errorf("%w: configuration cannot be changed in read-only mode",
			ErrReadOnlyConfig)
	}
	return nil
}
