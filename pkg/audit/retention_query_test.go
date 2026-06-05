package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// queryOf returns the query field of the event at index i, plus whether
// the field is present.
func queryOf(events []Event, i int) (string, bool) {
	v, ok := events[i].Context[queryContextField]
	return v, ok
}

// Spec: §8.4 — "Query text: 30 days (redacted to placeholders after 7
// days)". Enforce with a QueryRetention keeps every search event but
// ages out its query field: verbatim under 7 days, a placeholder between
// 7 and 30 days, and removed past 30 days. The event metadata survives
// and the chain stays valid.
func TestQueryRetention_PlaceholderThenDrop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	now := time.Now().UTC()
	ages := []time.Duration{
		1 * 24 * time.Hour,  // 0: fresh, query kept verbatim
		8 * 24 * time.Hour,  // 1: past 7d, query → placeholder
		31 * 24 * time.Hour, // 2: past 30d, query dropped
	}
	for _, age := range ages {
		if err := sink.Append(context.Background(), Event{
			Type:      EventArtifactsSearched,
			Timestamp: now.Add(-age),
			Caller:    "alice@acme.com",
			Context:   map[string]string{"query": "secret project apollo", "scope": "all"},
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// A non-search event with a "query" key must be left untouched.
	if err := sink.Append(context.Background(), Event{
		Type:      EventArtifactLoaded,
		Timestamp: now.Add(-31 * 24 * time.Hour),
		Caller:    "alice@acme.com",
		Context:   map[string]string{"query": "not-a-search-field"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	dropped, err := Enforce(context.Background(), sink, now, nil, DefaultQueryRetention())
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0 (query retention keeps events)", dropped)
	}

	events, err := readAllEvents(path)
	if err != nil {
		t.Fatalf("readAllEvents: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (none dropped)", len(events))
	}
	if q, _ := queryOf(events, 0); q != "secret project apollo" {
		t.Errorf("fresh query = %q, want verbatim", q)
	}
	if q, _ := queryOf(events, 1); q != "[redacted]" {
		t.Errorf("8-day query = %q, want placeholder", q)
	}
	if q, ok := queryOf(events, 2); ok {
		t.Errorf("31-day query present = %q, want dropped", q)
	}
	// The scope field on the dropped-query event remains.
	if events[2].Context["scope"] != "all" {
		t.Errorf("scope field lost on query-dropped event: %+v", events[2].Context)
	}
	// The non-search event keeps its query-keyed context verbatim.
	if events[3].Context["query"] != "not-a-search-field" {
		t.Errorf("non-search event query mutated: %+v", events[3].Context)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("chain invalid after query retention: %v", err)
	}
}

// Spec: §8.4 — query retention is idempotent: a second pass over an
// already-placeholdered or already-dropped log makes no further change
// and reports nothing dropped.
func TestQueryRetention_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, _ := NewFileSink(path)
	now := time.Now().UTC()
	_ = sink.Append(context.Background(), Event{
		Type:      EventDomainsSearched,
		Timestamp: now.Add(-10 * 24 * time.Hour),
		Context:   map[string]string{"query": "find me"},
	})
	if _, err := Enforce(context.Background(), sink, now, nil, DefaultQueryRetention()); err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	before, _ := readAllEvents(path)
	if _, err := Enforce(context.Background(), sink, now, nil, DefaultQueryRetention()); err != nil {
		t.Fatalf("Enforce (second): %v", err)
	}
	after, _ := readAllEvents(path)
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("event count changed: before=%d after=%d", len(before), len(after))
	}
	if before[0].Hash != after[0].Hash {
		t.Errorf("idempotent pass rewrote the chain: %s -> %s", before[0].Hash, after[0].Hash)
	}
}
