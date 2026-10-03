package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// publishedEvent is one call the endpoint made to its publish hook.
type publishedEvent struct {
	Type  string
	Scope core.EventScope
	Data  map[string]any
}

type eventRecorder struct {
	mu     sync.Mutex
	events []publishedEvent
}

func (r *eventRecorder) publish(_ context.Context, scope core.EventScope, eventType string, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, publishedEvent{Type: eventType, Scope: scope, Data: data})
}

// scopeFor returns the scope of the last event whose action is action.
func (r *eventRecorder) scopeFor(t *testing.T, action string) core.EventScope {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Data["action"] == action {
			return r.events[i].Scope
		}
	}
	t.Fatalf("no event with action %q among %+v", action, r.events)
	return core.EventScope{}
}

func (r *eventRecorder) typesFor() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Type)
	}
	return out
}

func (r *eventRecorder) actions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, e := range r.events {
		if a, ok := e.Data["action"].(string); ok {
			out = append(out, a)
		}
	}
	return out
}

// layerEventHarness builds a layer endpoint whose publish hook records every
// event, with the given caller identity.
func layerEventHarness(t *testing.T, rec *eventRecorder, id layer.Identity) string {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithEventPublisher(rec.publish).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return id, nil })
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// Spec: §7.5.4 — watch mode re-resolves the profile on every registry change
// event, and the section names `layer.config_changed` among them alongside
// `artifact.published` and `artifact.deprecated`. The watcher subscribes to
// all three (`pkg/sync/watch_server.go`), so an admin layer change that never
// reaches the event bus leaves every watcher serving a stale profile until
// something else wakes it. The endpoint recorded the §8.1 audit event and
// published nothing, so the subscription had no producer.
func TestLayerEndpoint_PublishesConfigChangedOnRegister(t *testing.T) {
	t.Parallel()
	rec := &eventRecorder{}
	base := layerEventHarness(t, rec, layer.Identity{Sub: "admin", IsAuthenticated: true})

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-a", "source_type": "local", "local_path": "/x",
	})

	if got := rec.typesFor(); !containsString(got, "layer.config_changed") {
		t.Errorf("registering an admin layer published %v, want layer.config_changed", got)
	}
	if got := rec.actions(); !containsString(got, "register") {
		t.Errorf("published actions = %v, want one naming register", got)
	}
	// Spec: §7.6 — a register names its tenant and layer and carries no
	// prior visibility, because the layer had none.
	want := core.EventScope{TenantID: "t", Layers: []string{"team-a"}}
	if got := rec.scopeFor(t, "register"); !reflect.DeepEqual(got, want) {
		t.Errorf("register scope = %+v, want %+v", got, want)
	}
}

// Spec: §7.5.4 / §8.1 — reordering admin-defined layers changes the composed
// profile, so it wakes a watcher the same way a registration does.
func TestLayerEndpoint_PublishesConfigChangedOnReorder(t *testing.T) {
	t.Parallel()
	rec := &eventRecorder{}
	base := layerEventHarness(t, rec, layer.Identity{Sub: "admin", IsAuthenticated: true})

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-a", "source_type": "local", "local_path": "/x",
	})
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-b", "source_type": "local", "local_path": "/y",
	})
	resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{
		"order": []string{"team-b", "team-a"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder status %d: %s", resp.StatusCode, body)
	}

	if got := rec.actions(); !containsString(got, "reorder") {
		t.Errorf("published actions = %v, want one naming reorder", got)
	}
	// Spec: §7.6 — the reorder scope lists every named layer in request
	// order, which the stream uses to compute each subscriber's subset.
	want := core.EventScope{TenantID: "t", Layers: []string{"team-b", "team-a"}}
	if got := rec.scopeFor(t, "reorder"); !reflect.DeepEqual(got, want) {
		t.Errorf("reorder scope = %+v, want %+v", got, want)
	}
}

// Spec: §7.5.4 / §8.1 — unregistering an admin-defined layer removes it from
// the composed profile, so it wakes a watcher too.
func TestLayerEndpoint_PublishesConfigChangedOnUnregister(t *testing.T) {
	t.Parallel()
	rec := &eventRecorder{}
	base := layerEventHarness(t, rec, layer.Identity{Sub: "admin", IsAuthenticated: true})

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-a", "source_type": "local", "local_path": "/x", "groups": []string{"eng"},
	})
	mustDelete(t, base, "/v1/layers?id=team-a")

	if got := rec.actions(); !containsString(got, "unregister") {
		t.Errorf("published actions = %v, want one naming unregister", got)
	}
	// Spec: §7.6 — the unregister carries the deleted record's visibility,
	// because the tombstone hides the layer from the delivery-time read.
	want := core.EventScope{TenantID: "t", Layers: []string{"team-a"},
		Prior: &layer.Visibility{Groups: []string{"eng"}}}
	if got := rec.scopeFor(t, "unregister"); !reflect.DeepEqual(got, want) {
		t.Errorf("unregister scope = %+v, want %+v", got, want)
	}
}

// Spec: §7.6 — an update carries the visibility the layer held before the
// update, so a subscriber the update withdraws the layer from still receives
// the layer.config_changed that withdraws it.
func TestLayerEndpoint_UpdateCarriesPriorVisibility(t *testing.T) {
	t.Parallel()
	rec := &eventRecorder{}
	base := layerEventHarness(t, rec, layer.Identity{Sub: "admin", IsAuthenticated: true})

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-a", "source_type": "local", "local_path": "/x", "users": []string{"alice"},
	})
	resp, body := putJSON(t, base, "/v1/layers/update?id=team-a", map[string]any{"users": []string{"bob"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status %d: %s", resp.StatusCode, body)
	}

	want := core.EventScope{TenantID: "t", Layers: []string{"team-a"},
		Prior: &layer.Visibility{Users: []string{"alice"}}}
	if got := rec.scopeFor(t, "update"); !reflect.DeepEqual(got, want) {
		t.Errorf("update scope = %+v, want %+v", got, want)
	}
}

// Spec: §8.1 — a personal layer emits `layer.user_registered` rather than
// `layer.config_changed`, and §7.5.4 names only the latter among the watch
// triggers. A personal layer belongs to one user and does not change the
// admin-defined composition every watcher resolves, so publishing it would
// wake every watcher in the tenant for a change none of them can see.
func TestLayerEndpoint_PersonalLayerPublishesNoConfigChange(t *testing.T) {
	t.Parallel()
	rec := &eventRecorder{}
	base := layerEventHarness(t, rec, layer.Identity{Sub: "alice", IsAuthenticated: true})

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/tmp/alice",
		"user_defined": true, "owner": "alice",
	})

	if got := rec.typesFor(); containsString(got, "layer.config_changed") {
		t.Errorf("a personal layer published %v, which must not include layer.config_changed", got)
	}
}

// Spec: §7.5.4 — the publish hook is optional. An endpoint built without one
// records its audit event and serves the request unchanged, which is what a
// deployment with no event bus does.
func TestLayerEndpoint_NoPublisherIsANoOp(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
			return layer.Identity{Sub: "admin", IsAuthenticated: true}, nil
		})
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	resp, body := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "team-a", "source_type": "local", "local_path": "/x",
	})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("register status %d: %s", resp.StatusCode, body)
	}
}
