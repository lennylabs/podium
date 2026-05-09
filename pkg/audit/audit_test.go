package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §8.6 Audit Integrity — Append + Verify produce a sound chain
// across multiple events.
// Phase: 16
func TestMemory_AppendThenVerify(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	s := NewMemory()
	ctx := context.Background()
	events := []Event{
		{Type: EventArtifactPublished, Caller: "joan", Target: "x"},
		{Type: EventArtifactLoaded, Caller: "joan", Target: "x"},
		{Type: EventLayerIngested, Caller: "system", Target: "team-finance"},
	}
	for _, e := range events {
		if err := s.Append(ctx, e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := s.Verify(ctx); err != nil {
		t.Errorf("Verify: %v", err)
	}
	got := s.Events()
	if len(got) != 3 {
		t.Errorf("got %d events, want 3", len(got))
	}
	if got[0].PrevHash != "" {
		t.Errorf("first event PrevHash = %q, want empty", got[0].PrevHash)
	}
	if got[1].PrevHash != got[0].Hash {
		t.Errorf("second event PrevHash != first event Hash")
	}
}

// Spec: §8.6 — tampering with an event's body fails Verify.
// Phase: 16
func TestMemory_TamperedEventFailsVerify(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	s := NewMemory()
	ctx := context.Background()
	_ = s.Append(ctx, Event{Type: EventArtifactPublished, Caller: "joan"})
	_ = s.Append(ctx, Event{Type: EventArtifactLoaded, Caller: "joan"})

	// Mutate the second event's caller to break the chain.
	s.events[1].Caller = "bob"
	err := s.Verify(ctx)
	if !errors.Is(err, ErrChainBroken) {
		t.Errorf("got %v, want ErrChainBroken", err)
	}
}

// Spec: §8.1 — every documented event type compiles to a stable string.
// Phase: 16
func TestEventTypes_StableValues(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	cases := []struct {
		evt EventType
		s   string
	}{
		{EventDomainLoaded, "domain.loaded"},
		{EventArtifactPublished, "artifact.published"},
		{EventLayerIngested, "layer.ingested"},
		{EventVisibilityDenied, "visibility.denied"},
		{EventReadOnlyEntered, "registry.read_only_entered"},
	}
	for _, c := range cases {
		if string(c.evt) != c.s {
			t.Errorf("event %q = %q", c.s, c.evt)
		}
	}
}
