package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
)

// Spec: §8.6 — "Every audit event carries a hash chain:
// event_hash = sha256(event_body || prev_event_hash). Detection of gaps is
// automated and alerted." Many goroutines in one process append to a single
// FileSink concurrently. The sink serializes the read of lastHash, the hash
// computation, the O_APPEND write, and the lastHash update under one mutex
// (file.go), so the result must be a single unbroken linear chain with no
// lost, torn, or duplicated entries.
//
// The existing TestFileSink_ConcurrentWritersVerify (file_concurrent_test.go)
// appends sequentially through a fixed slice of sinks; it proves Verify
// tolerates a forked chain but never drives real goroutine concurrency. This
// test drives N concurrent Append goroutines and asserts the linear-chain
// invariants a single writer must hold. Run it under -race.
func TestFileSink_ConcurrentAppendSingleSinkLinearChain(t *testing.T) {
	t.Parallel()
	path := tempLog(t)
	ctx := context.Background()
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	const writers, each = 16, 50
	const want = writers * each

	// A barrier so every goroutine contends on the mutex at once, maximizing
	// the window in which a lost update or torn write could surface.
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-ready
			for i := 0; i < each; i++ {
				if err := sink.Append(ctx, Event{
					Type:   EventArtifactLoaded,
					Target: fmt.Sprintf("%d-%d", w, i),
				}); err != nil {
					t.Errorf("append (writer %d, item %d): %v", w, i, err)
				}
			}
		}(w)
	}
	close(ready)
	wg.Wait()

	// A fresh reader of the on-disk log must verify the chain without error.
	// A single writing sink chains every event off its own advancing head, so
	// the log is one linear chain, the degenerate case the forest-tolerant
	// Verify still accepts.
	reader, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("reader sink: %v", err)
	}
	if err := reader.Verify(ctx); err != nil {
		t.Fatalf("Verify concurrent single-sink chain: %v, want nil", err)
	}

	events := readEvents(t, path)

	// (a) No lost or torn writes: exactly want parseable, newline-terminated
	// events landed.
	if len(events) != want {
		t.Fatalf("got %d events, want %d (lost or torn concurrent writes)", len(events), want)
	}

	// (b) No duplicated entries and a single unbroken linear chain: every
	// event's PrevHash equals the immediately preceding event's Hash, every
	// Hash is distinct, and the first event roots the chain with an empty
	// PrevHash. A lost update under the mutex would either repeat a Hash or
	// break a PrevHash link.
	seenHash := make(map[string]bool, want)
	seenTarget := make(map[string]bool, want)
	prev := ""
	for i, e := range events {
		if e.Hash == "" {
			t.Fatalf("event %d has empty Hash", i)
		}
		if seenHash[e.Hash] {
			t.Fatalf("event %d duplicates Hash %s (a concurrent append was lost or replayed)", i, e.Hash)
		}
		seenHash[e.Hash] = true
		if e.PrevHash != prev {
			t.Fatalf("event %d PrevHash = %q, want %q (chain not linear under concurrency)", i, e.PrevHash, prev)
		}
		if seenTarget[e.Target] {
			t.Fatalf("event %d duplicates Target %q (a write was replayed)", i, e.Target)
		}
		seenTarget[e.Target] = true
		prev = e.Hash
	}

	// Every distinct (writer, item) target appended exactly once, so no entry
	// was dropped or duplicated even though the on-disk order is interleaved.
	if len(seenTarget) != want {
		t.Fatalf("distinct targets = %d, want %d (an entry was lost or duplicated)", len(seenTarget), want)
	}
}

// Spec: §8.6 — several MCP server processes append to one shared
// ~/.podium/audit.log; each chains off its own in-process head, so the file is
// a forest of per-writer chains rather than one linear chain (file.go documents
// this under the PIPE_BUF-bounded atomic-append guarantee). Even so, no event
// may be lost, torn, or duplicated, and Verify (forest mode) must return nil.
// This models K independent sinks (K processes) appending concurrently to the
// same path. Run it under -race.
func TestFileSink_ConcurrentAppendMultiSinkForestVerifies(t *testing.T) {
	t.Parallel()
	path := tempLog(t)
	ctx := context.Background()

	const sinks, each = 8, 40
	const want = sinks * each

	// K sinks all opened on the same (empty) path model K processes that share
	// one audit log; each holds its own lastHash, so the chains fork.
	writers := make([]*FileSink, sinks)
	for k := range writers {
		s, err := NewFileSink(path)
		if err != nil {
			t.Fatalf("NewFileSink %d: %v", k, err)
		}
		writers[k] = s
	}

	ready := make(chan struct{})
	var wg sync.WaitGroup
	for k, s := range writers {
		wg.Add(1)
		go func(k int, s *FileSink) {
			defer wg.Done()
			<-ready
			for i := 0; i < each; i++ {
				if err := s.Append(ctx, Event{
					Type:   EventArtifactLoaded,
					Target: fmt.Sprintf("sink%d-%d", k, i),
				}); err != nil {
					t.Errorf("append (sink %d, item %d): %v", k, i, err)
				}
			}
		}(k, s)
	}
	close(ready)
	wg.Wait()

	// Forest-tolerant Verify accepts the forked log: every self-hash holds and
	// every non-empty PrevHash references some earlier line's Hash.
	reader, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("reader sink: %v", err)
	}
	if err := reader.Verify(ctx); err != nil {
		t.Fatalf("Verify concurrent forked log: %v, want nil", err)
	}

	events := readEvents(t, path)

	// No lost or torn writes across the forked writers: every appended event
	// is present exactly once.
	if len(events) != want {
		t.Fatalf("got %d events, want %d (a forked-chain write was lost or torn)", len(events), want)
	}
	seenHash := make(map[string]bool, want)
	seenTarget := make(map[string]bool, want)
	roots := 0
	for i, e := range events {
		if seenHash[e.Hash] {
			t.Fatalf("event %d duplicates Hash %s (a concurrent append was replayed)", i, e.Hash)
		}
		seenHash[e.Hash] = true
		if seenTarget[e.Target] {
			t.Fatalf("event %d duplicates Target %q", i, e.Target)
		}
		seenTarget[e.Target] = true
		if e.PrevHash == "" {
			roots++
		}
	}
	if len(seenTarget) != want {
		t.Fatalf("distinct targets = %d, want %d (an entry was lost or duplicated)", len(seenTarget), want)
	}
	// Each of the K sinks seeds its own chain from the empty head, so the
	// forest has one root per writer. This confirms the chains genuinely
	// forked rather than collapsing into a single mutex-shared chain.
	if roots != sinks {
		t.Fatalf("chain roots (empty PrevHash) = %d, want %d (one per forked writer)", roots, sinks)
	}
}

// tempLog returns a fresh audit-log path under a per-test temp dir.
func tempLog(t *testing.T) string {
	t.Helper()
	return t.TempDir() + string(os.PathSeparator) + "audit.log"
}

// readEvents parses every JSON-Lines record in the log in file order.
func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	out := []Event{}
	for i, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			t.Fatalf("line %d is not parseable JSON (a concurrent write was torn): %v", i, err)
		}
		out = append(out, eventFromJSON(je))
	}
	return out
}
