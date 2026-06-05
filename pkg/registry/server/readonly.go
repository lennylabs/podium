package server

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
)

// ErrReadOnly signals a write rejected because the registry is running
// in §13.2.1 read-only mode. It maps to the registry.read_only §6.10
// code. Every write endpoint the spec enumerates (ingest webhooks,
// layer admin operations, freeze toggles, admin grants, runtime-key
// issuance) rejects with this single code; the spec defines no separate
// config-rejection code.
var ErrReadOnly = errors.New("registry.read_only")

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

// rejectIfReadOnly writes the §13.2.1 registry.read_only envelope (HTTP
// 503) and returns true when the registry is in read-only mode, so a
// write-path handler can return early. A nil tracker means read-only
// mode tracking is disabled and never rejects. This is the single
// choke point for the §13.2.1 write-rejection code so the wire string
// cannot drift across the enumerated write endpoints.
func rejectIfReadOnly(w http.ResponseWriter, mode *ModeTracker) bool {
	if mode == nil {
		return false
	}
	if err := mode.CheckWrite(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "registry.read_only", err.Error())
		return true
	}
	return false
}
