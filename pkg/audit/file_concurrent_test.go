package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §9 / §8.3 / §14.13 — two MCP server processes share one
// ~/.podium/audit.log. Each chains off its own in-process head, so the log
// is a forest of per-writer chains. Verify must accept it instead of
// reporting a spurious ErrChainBroken on the first interleaved event.
// F-14.13.2.
func TestFileSink_ConcurrentWritersVerify(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	ctx := context.Background()

	// Two sinks on the same file model two processes that both opened the
	// (empty) log and append interleaved, forking the chain.
	a, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("sink a: %v", err)
	}
	b, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("sink b: %v", err)
	}
	appenders := []*FileSink{a, b, a, b, a, b}
	for i, s := range appenders {
		if err := s.Append(ctx, Event{Type: EventArtifactLoaded, Target: string(rune('a' + i))}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// A fresh reader of the shared log verifies the forest without error.
	reader, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("reader sink: %v", err)
	}
	if err := reader.Verify(ctx); err != nil {
		t.Errorf("Verify forked log: %v, want nil", err)
	}

	// Sanity: at least one event carries an empty PrevHash (a fresh chain
	// root) and a later one reuses an earlier hash; i.e. it really forked.
	data, _ := os.ReadFile(path)
	if n := strings.Count(string(data), "\n"); n != len(appenders) {
		t.Errorf("got %d lines, want %d", n, len(appenders))
	}
}

// spec: §8.6 — deleting an interior event leaves a later event's PrevHash
// dangling, which Verify reports as ErrChainBroken even under the
// forest-tolerant check (gap detection survives F-14.13.2).
func TestFileSink_InteriorDeletionDetected(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	ctx := context.Background()
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	for _, target := range []string{"a", "b", "c"} {
		if err := sink.Append(ctx, Event{Type: EventArtifactLoaded, Target: target}); err != nil {
			t.Fatalf("append %s: %v", target, err)
		}
	}
	if err := sink.Verify(ctx); err != nil {
		t.Fatalf("baseline verify: %v", err)
	}

	// Drop the middle line; the third event now references a hash that no
	// surviving earlier event carries.
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	kept := lines[0] + "\n" + lines[2] + "\n"
	if err := os.WriteFile(path, []byte(kept), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reader, _ := NewFileSink(path)
	if err := reader.Verify(ctx); !errors.Is(err, ErrChainBroken) {
		t.Errorf("Verify after interior deletion = %v, want ErrChainBroken", err)
	}
}
