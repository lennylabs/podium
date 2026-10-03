package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.6 — deliverable withholds an event with no audience and one the
// audience withholds, and rewrites a reorder's `layer` value to the
// subscriber's visible subset on a copy of the shared payload.
func TestDeliverable(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "a", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "b", Precedence: 2, Visibility: layer.Visibility{Users: []string{"bob"}}},
	})
	srv := New(reg)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	ctx := context.Background()

	if _, ok := srv.deliverable(ctx, registryEvent{Event: "x"}, alice, "t"); ok {
		t.Error("an event with no audience was delivered")
	}

	shared := map[string]any{"layer": "b,a", "action": "reorder"}
	ev := registryEvent{
		Event:    "layer.config_changed",
		Data:     shared,
		audience: reg.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"b", "a"}}),
	}
	if _, ok := srv.deliverable(ctx, ev, alice, "other"); ok {
		t.Error("an event of another tenant was delivered")
	}
	got, ok := srv.deliverable(ctx, ev, alice, "t")
	if !ok {
		t.Fatal("a reorder naming a visible layer was withheld")
	}
	if want := map[string]any{"layer": "a", "action": "reorder"}; !reflect.DeepEqual(got.Data, want) {
		t.Errorf("rewritten data = %v, want %v", got.Data, want)
	}
	if shared["layer"] != "b,a" {
		t.Errorf("the shared payload was mutated to %v", shared)
	}
	ev.Data = nil
	if got, ok := srv.deliverable(ctx, ev, alice, "t"); !ok || got.Data["layer"] != "a" {
		t.Errorf("a reorder with no payload delivered %v (ok %v), want layer a", got.Data, ok)
	}
}
