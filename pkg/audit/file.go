package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileSink writes audit events as JSON Lines to ~/.podium/audit.log
// or a configured override path (§8.3 LocalAuditSink). Concurrent
// appends are safe under typical event sizes per §8.3:
// "POSIX PIPE_BUF-bounded atomic writes." A single shared log written by
// multiple processes is a forest of per-writer hash chains; Verify
// validates it accordingly (see Verify, F-14.13.2).
type FileSink struct {
	mu       sync.Mutex
	path     string
	lastHash string
}

// NewFileSink opens (or creates) path and returns a hash-chained
// FileSink. If path is empty, defaults to ~/.podium/audit.log.
//
// On open, the sink scans the existing file (when present) and
// recovers the last event's hash so the chain continues across
// server restarts.
func NewFileSink(path string) (*FileSink, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".podium", "audit.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	sink := &FileSink{path: path}
	last, err := lastChainHash(path)
	if err != nil {
		return nil, err
	}
	sink.lastHash = last
	return sink, nil
}

// lastChainHash reads the existing log file (if any) and returns
// the last event's Hash. Missing file returns "" so the next
// Append seeds a fresh chain. Empty / corrupt last lines fall
// back to the most-recent parseable line.
func lastChainHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var last string
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			continue
		}
		last = je.Hash
	}
	return last, nil
}

// Append writes the next event in the chain.
func (f *FileSink) Append(_ context.Context, e Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	e.PrevHash = f.lastHash
	hash := sha256.Sum256(append(e.canonicalBody(), []byte(f.lastHash)...))
	e.Hash = hex.EncodeToString(hash[:])

	line, err := json.Marshal(eventForJSON(e))
	if err != nil {
		return err
	}
	line = append(line, '\n')

	file, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(line); err != nil {
		return err
	}
	f.lastHash = e.Hash
	return nil
}

// Verify re-reads the audit log and validates the hash chain. Returns
// ErrChainBroken on any mismatch.
//
// §9 scopes the local sink's concurrency guarantee to PIPE_BUF-bounded
// atomic appends: several MCP server processes can append to one
// ~/.podium/audit.log concurrently (§14.13). Each process chains off its
// own in-process lastHash, so the file is a forest of per-writer chains
// rather than one linear chain. Verifying it as a single linear chain
// reported a spurious ErrChainBroken the moment two writers interleaved
// (F-14.13.2). Verification therefore checks, per §8.6, that every event's
// own hash satisfies event_hash = sha256(body || prev_hash), and that a
// non-empty PrevHash references the Hash of some earlier event in the log.
// A single-writer log is the degenerate one-chain case and still verifies.
// Tampering with an event body breaks its self-hash, and deleting an
// interior event leaves a later event's PrevHash dangling; both surface as
// ErrChainBroken.
func (f *FileSink) Verify(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	seen := map[string]bool{}
	idx := 0
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			return errChainAt(ErrChainBroken, idx, "unparseable event")
		}
		e := eventFromJSON(je)
		want := sha256.Sum256(append(e.canonicalBody(), []byte(e.PrevHash)...))
		if hex.EncodeToString(want[:]) != e.Hash {
			return errChainAt(ErrChainBroken, idx, "Hash mismatch")
		}
		if e.PrevHash != "" && !seen[e.PrevHash] {
			return errChainAt(ErrChainBroken, idx, "PrevHash references no earlier event")
		}
		seen[e.Hash] = true
		idx++
	}
	return nil
}

// Path returns the file the sink writes to. Used by tests + operator
// commands.
func (f *FileSink) Path() string { return f.path }

// jsonEvent is the wire form of an event in the JSON-Lines log.
type jsonEvent struct {
	Type           string            `json:"type"`
	Timestamp      string            `json:"timestamp"`
	TraceID        string            `json:"trace_id,omitempty"`
	Caller         string            `json:"caller,omitempty"`
	CallerEmail    string            `json:"caller_email,omitempty"`
	CallerGroups   []string          `json:"caller_groups,omitempty"`
	CallerNetwork  *jsonNetwork      `json:"caller_network,omitempty"`
	PublicMode     bool              `json:"caller_public_mode,omitempty"`
	Target         string            `json:"target,omitempty"`
	Context        map[string]string `json:"context,omitempty"`
	ResolvedLayers []string          `json:"resolved_layers,omitempty"`
	ResultSize     int               `json:"result_size,omitempty"`
	Hash           string            `json:"hash"`
	PrevHash       string            `json:"prev_hash,omitempty"`
}

// jsonNetwork is the wire form of CallerNetwork (§8.1 caller.network).
type jsonNetwork struct {
	SourceIP      string `json:"source_ip,omitempty"`
	ForwardedUser string `json:"forwarded_user,omitempty"`
}

func eventForJSON(e Event) jsonEvent {
	return jsonEvent{
		Type:           string(e.Type),
		Timestamp:      e.Timestamp.UTC().Format(time.RFC3339Nano),
		TraceID:        e.TraceID,
		Caller:         e.Caller,
		CallerEmail:    e.CallerEmail,
		CallerGroups:   e.CallerGroups,
		CallerNetwork:  networkForJSON(e.CallerNetwork),
		PublicMode:     e.PublicMode,
		Target:         e.Target,
		Context:        e.Context,
		ResolvedLayers: e.ResolvedLayers,
		ResultSize:     e.ResultSize,
		Hash:           e.Hash,
		PrevHash:       e.PrevHash,
	}
}

func eventFromJSON(je jsonEvent) Event {
	t, _ := time.Parse(time.RFC3339Nano, je.Timestamp)
	return Event{
		Type:           EventType(je.Type),
		Timestamp:      t,
		TraceID:        je.TraceID,
		Caller:         je.Caller,
		CallerEmail:    je.CallerEmail,
		CallerGroups:   je.CallerGroups,
		CallerNetwork:  networkFromJSON(je.CallerNetwork),
		PublicMode:     je.PublicMode,
		Target:         je.Target,
		Context:        je.Context,
		ResolvedLayers: je.ResolvedLayers,
		ResultSize:     je.ResultSize,
		Hash:           je.Hash,
		PrevHash:       je.PrevHash,
	}
}

func networkForJSON(n *CallerNetwork) *jsonNetwork {
	if n == nil {
		return nil
	}
	return &jsonNetwork{SourceIP: n.SourceIP, ForwardedUser: n.ForwardedUser}
}

func networkFromJSON(n *jsonNetwork) *CallerNetwork {
	if n == nil {
		return nil
	}
	return &CallerNetwork{SourceIP: n.SourceIP, ForwardedUser: n.ForwardedUser}
}

// splitLines mirrors strings.Split on '\n' but operates on bytes so
// we don't allocate a string per line.
func splitLines(data []byte) [][]byte {
	out := [][]byte{}
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// fmtErr is a small helper kept available for parity with the other
// audit sink helpers.
func fmtErr(format string, args ...any) error { return fmt.Errorf(format, args...) }
