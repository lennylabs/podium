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
// "POSIX PIPE_BUF-bounded atomic writes."
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

// Verify re-reads the audit log and walks the hash chain. Returns
// ErrChainBroken on any mismatch.
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
	prev := ""
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
		if e.PrevHash != prev {
			return errChainAt(ErrChainBroken, idx, "PrevHash mismatch")
		}
		want := sha256.Sum256(append(e.canonicalBody(), []byte(prev)...))
		if hex.EncodeToString(want[:]) != e.Hash {
			return errChainAt(ErrChainBroken, idx, "Hash mismatch")
		}
		prev = e.Hash
		idx++
	}
	return nil
}

// Path returns the file the sink writes to. Used by tests + operator
// commands.
func (f *FileSink) Path() string { return f.path }

// jsonEvent is the wire shape of an event in the JSON-Lines log.
type jsonEvent struct {
	Type      string            `json:"type"`
	Timestamp string            `json:"timestamp"`
	TraceID   string            `json:"trace_id,omitempty"`
	Caller    string            `json:"caller,omitempty"`
	Target    string            `json:"target,omitempty"`
	Context   map[string]string `json:"context,omitempty"`
	Hash      string            `json:"hash"`
	PrevHash  string            `json:"prev_hash,omitempty"`
}

func eventForJSON(e Event) jsonEvent {
	return jsonEvent{
		Type:      string(e.Type),
		Timestamp: e.Timestamp.UTC().Format(time.RFC3339Nano),
		TraceID:   e.TraceID,
		Caller:    e.Caller,
		Target:    e.Target,
		Context:   e.Context,
		Hash:      e.Hash,
		PrevHash:  e.PrevHash,
	}
}

func eventFromJSON(je jsonEvent) Event {
	t, _ := time.Parse(time.RFC3339Nano, je.Timestamp)
	return Event{
		Type:      EventType(je.Type),
		Timestamp: t,
		TraceID:   je.TraceID,
		Caller:    je.Caller,
		Target:    je.Target,
		Context:   je.Context,
		Hash:      je.Hash,
		PrevHash:  je.PrevHash,
	}
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
