// Package audit implements the registry audit log per spec §8 and the
// hash-chained integrity check from §8.6. The package surfaces a small
// Sink SPI plus an in-memory backend used by tests.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// Errors returned by Sink and Verifier functions.
var (
	// ErrChainBroken signals that a hash gap was detected during
	// verification. Maps to §8.6 alarms.
	ErrChainBroken = errors.New("audit: hash chain broken")
)

// EventType is the namespaced event identifier (§8.1).
type EventType string

// EventType values for the events listed in the §8.1 table. Additional
// types can be added as long as they remain stable across releases.
const (
	EventDomainLoaded         EventType = "domain.loaded"
	EventDomainsSearched      EventType = "domains.searched"
	EventArtifactsSearched    EventType = "artifacts.searched"
	EventArtifactLoaded       EventType = "artifact.loaded"
	EventArtifactPublished    EventType = "artifact.published"
	EventArtifactDeprecated   EventType = "artifact.deprecated"
	EventArtifactSigned       EventType = "artifact.signed"
	EventDomainPublished      EventType = "domain.published"
	EventLayerIngested        EventType = "layer.ingested"
	EventLayerHistoryRewritten EventType = "layer.history_rewritten"
	EventLayerConfigChanged   EventType = "layer.config_changed"
	EventLayerUserRegistered  EventType = "layer.user_registered"
	EventAdminGranted         EventType = "admin.granted"
	EventVisibilityDenied     EventType = "visibility.denied"
	EventFreezeBreakGlass     EventType = "freeze.break_glass"
	EventUserErased           EventType = "user.erased"
	EventReadOnlyEntered      EventType = "registry.read_only_entered"
	EventReadOnlyExited       EventType = "registry.read_only_exited"
	EventAuditAnchored        EventType = "audit.anchored"
)

// Event is one audit record. Caller / target / context fields can be
// empty depending on the event type; the renderer is responsible for
// rendering them appropriately for SIEM consumers.
type Event struct {
	Type      EventType
	Timestamp time.Time
	TraceID   string
	Caller    string
	Target    string
	Context   map[string]string

	// Hash is the chain hash sha256(body || prev_hash).
	Hash     string
	PrevHash string
}

// canonicalBody produces a deterministic byte representation of the
// event for hash chaining (excludes Hash and PrevHash by definition).
// Context keys are sorted so the encoding is stable across calls;
// Go's map iteration order is unspecified.
func (e Event) canonicalBody() []byte {
	parts := []string{
		string(e.Type),
		e.Timestamp.UTC().Format(time.RFC3339Nano),
		e.TraceID,
		e.Caller,
		e.Target,
	}
	keys := make([]string, 0, len(e.Context))
	for k := range e.Context {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+e.Context[k])
	}
	out := []byte{}
	for _, p := range parts {
		out = append(out, []byte(p)...)
		out = append(out, 0)
	}
	return out
}

// Sink is the SPI implementations satisfy.
type Sink interface {
	Append(ctx context.Context, e Event) error
	Verify(ctx context.Context) error
}

// Memory is an in-memory hash-chained Sink.
type Memory struct {
	mu       sync.Mutex
	events   []Event
	lastHash string
}

// NewMemory returns a fresh in-memory Sink.
func NewMemory() *Memory { return &Memory{} }

// Append computes the hash chain entry and stores it.
func (m *Memory) Append(_ context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	e.PrevHash = m.lastHash
	hash := sha256.Sum256(append(e.canonicalBody(), []byte(m.lastHash)...))
	e.Hash = hex.EncodeToString(hash[:])
	m.events = append(m.events, e)
	m.lastHash = e.Hash
	return nil
}

// Verify walks the chain and returns ErrChainBroken on any mismatch.
func (m *Memory) Verify(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := ""
	for i, e := range m.events {
		if e.PrevHash != prev {
			return errChainAt(ErrChainBroken, i, "PrevHash mismatch")
		}
		want := sha256.Sum256(append(e.canonicalBody(), []byte(prev)...))
		if hex.EncodeToString(want[:]) != e.Hash {
			return errChainAt(ErrChainBroken, i, "Hash mismatch")
		}
		prev = e.Hash
	}
	return nil
}

// Events returns a copy of the recorded events.
func (m *Memory) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

func errChainAt(err error, idx int, msg string) error {
	return chainErr{base: err, idx: idx, msg: msg}
}

type chainErr struct {
	base error
	idx  int
	msg  string
}

func (c chainErr) Error() string {
	return "audit: chain broken at index " + itoa(c.idx) + ": " + c.msg
}

func (c chainErr) Unwrap() error { return c.base }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
