package core

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// TestLayerFallback_TheLastRecordedAnswerWins covers two reads that began
// in one order and were answered in the other: the read whose answer was
// recorded last holds the list, so a read that began earlier and read the
// rows after a cycle narrowed them is not replaced by the older rows of a
// read that began later. A loaded list is a copy, and a tenant with no read
// has an empty list.
//
// Spec: §4.6 (Fallback list)
func TestLayerFallback_TheLastRecordedAnswerWins(t *testing.T) {
	t.Parallel()
	f := &layerFallback{lists: map[string][]layer.Layer{}}
	// The read that began later answered first, with the rows before the
	// narrowing; the read that began earlier answered after it.
	f.replace("acme", []layer.Layer{{ID: "l", Visibility: layer.Visibility{Public: true}}})
	f.replace("acme", []layer.Layer{{ID: "l", Visibility: layer.Visibility{Groups: []string{"g"}}}})
	got := f.load("acme")
	if len(got) != 1 || got[0].Visibility.Public || got[0].Visibility.Groups[0] != "g" {
		t.Fatalf("list = %+v, want the narrowed rows of the last answer", got)
	}
	got[0].Visibility.Groups[0] = "edited"
	if again := f.load("acme"); again[0].Visibility.Groups[0] != "g" {
		t.Fatalf("load returned the stored slice: %+v", again)
	}
	if none := f.load("globex"); none == nil || len(none) != 0 {
		t.Fatalf("an unknown tenant's list = %#v, want an empty non-nil list", none)
	}
}

// TestEventAudience_SuccessfulReadReplacesTheFallback covers the §7.6
// audience of a registry whose store keeps layer rows: a successful read is
// authoritative, an empty one included, and replaces the tenant's fallback
// list, as a read on the request path does.
//
// Spec: §4.6 (Fallback list), §7.6
func TestEventAudience_SuccessfulReadReplacesTheFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "acme"}); err != nil {
		t.Fatal(err)
	}
	r, err := New(st, "acme", []layer.Layer{{ID: "boot", Visibility: layer.Visibility{Public: true}}}).WithLayerRows(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "acme", ID: "team", Organization: true}); err != nil {
		t.Fatal(err)
	}
	a := r.NewEventAudience(EventScope{TenantID: "acme", Layers: []string{"team"}})
	if visible, _, ok := a.Visible(ctx, layer.Identity{IsAuthenticated: true, Sub: "bob"}, "acme"); !ok || len(visible) != 1 {
		t.Fatalf("event for team: visible %v ok %v", visible, ok)
	}
	if got := r.fallback.load("acme"); len(got) != 1 || got[0].ID != "team" {
		t.Fatalf("fallback after the event read = %+v", got)
	}
	if err := st.DeleteLayerConfig(ctx, "acme", "team"); err != nil {
		t.Fatal(err)
	}
	b := r.NewEventAudience(EventScope{TenantID: "acme", Layers: []string{"boot"}})
	if _, _, ok := b.Visible(ctx, layer.Identity{IsAuthenticated: true, Sub: "bob"}, "acme"); ok {
		t.Fatal("an event for a layer no row lists was delivered through the boot-time list")
	}
	if got := r.fallback.load("acme"); len(got) != 0 {
		t.Fatalf("fallback after an empty read = %+v", got)
	}
}
