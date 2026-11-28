package core

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// TestLayerFallback_OlderReadDoesNotReplaceANewerOne covers two reads that
// finish out of order: the read that began first does not replace the list
// a newer read wrote, and a loaded list is a copy.
//
// Spec: §4.6 (Fallback list)
func TestLayerFallback_OlderReadDoesNotReplaceANewerOne(t *testing.T) {
	t.Parallel()
	f := &layerFallback{lists: map[string]fallbackEntry{}}
	older, newer := f.begin(), f.begin()
	f.replace("acme", newer, []layer.Layer{{ID: "narrow", Visibility: layer.Visibility{Groups: []string{"g"}}}})
	f.replace("acme", older, []layer.Layer{{ID: "wide", Visibility: layer.Visibility{Public: true}}})
	got := f.load("acme")
	if len(got) != 1 || got[0].ID != "narrow" {
		t.Fatalf("list = %+v, want the newer read's", got)
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
