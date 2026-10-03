package core_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// countingLayerStore counts ListLayerConfigs calls and can fail the first one.
type countingLayerStore struct {
	store.Store

	mu       sync.Mutex
	calls    int
	firstErr error
}

func (s *countingLayerStore) ListLayerConfigs(ctx context.Context, tenant string) ([]store.LayerConfig, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == 1 && s.firstErr != nil {
		return nil, s.firstErr
	}
	return s.Store.ListLayerConfigs(ctx, tenant)
}

func (s *countingLayerStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// newEventRegistry seeds tenant "t" with a groups:[eng] layer and a public
// layer, wrapped in a counting store.
func newEventRegistry(t *testing.T, firstErr error) (*core.Registry, *countingLayerStore) {
	t.Helper()
	mem := store.NewMemory()
	ctx := context.Background()
	if err := mem.CreateTenant(ctx, store.Tenant{ID: "t", Name: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, c := range []store.LayerConfig{
		{TenantID: "t", ID: "eng", SourceType: "local", Order: 1, Groups: []string{"eng"}},
		{TenantID: "t", ID: "pub", SourceType: "local", Order: 2, Public: true},
	} {
		if err := mem.PutLayerConfig(ctx, c); err != nil {
			t.Fatalf("PutLayerConfig(%s): %v", c.ID, err)
		}
	}
	cs := &countingLayerStore{Store: mem, firstErr: firstErr}
	return core.New(cs, "t", nil), cs
}

var (
	alice = layer.Identity{Sub: "alice", Groups: []string{"eng"}, IsAuthenticated: true}
	bob   = layer.Identity{Sub: "bob", IsAuthenticated: true}
)

// Spec: §7.6
// Spec: §4.6
func TestEventAudience_GroupsLayerAndReorderRewrite(t *testing.T) {
	r, cs := newEventRegistry(t, nil)
	ctx := context.Background()
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"eng", "pub"}})
	got, rewrite, ok := a.Visible(ctx, alice, "t")
	if !ok || !rewrite || !reflect.DeepEqual(got, []string{"eng", "pub"}) {
		t.Fatalf("alice = %v %v %v", got, rewrite, ok)
	}
	got, rewrite, ok = a.Visible(ctx, bob, "t")
	if !ok || !rewrite || !reflect.DeepEqual(got, []string{"pub"}) {
		t.Fatalf("bob = %v %v %v", got, rewrite, ok)
	}
	if _, _, ok := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"eng"}}).Visible(ctx, bob, "t"); ok {
		t.Fatalf("bob received a groups:[eng] event")
	}
	if n := cs.count(); n != 2 {
		t.Fatalf("ListLayerConfigs calls = %d, want one per event (2)", n)
	}
}

// Spec: §7.6
// Spec: §6.3.1
func TestEventAudience_FailClosedGuardsAndBypass(t *testing.T) {
	r, cs := newEventRegistry(t, nil)
	ctx := context.Background()
	pub := layer.Identity{IsPublic: true, Scopes: []string{"podium:read:finance/*"}}
	cases := []struct {
		name   string
		scope  core.EventScope
		id     layer.Identity
		tenant string
		want   bool
	}{
		{"empty tenant", core.EventScope{Layers: []string{"pub"}}, alice, "", false},
		{"tenant mismatch", core.EventScope{TenantID: "t", Layers: []string{"pub"}}, alice, "B", false},
		{"no layers", core.EventScope{TenantID: "t"}, alice, "t", false},
		{"scope denies path", core.EventScope{TenantID: "t", Layers: []string{"eng"}, Path: "eng/x"}, pub, "t", false},
		{"public bypass", core.EventScope{TenantID: "t", Layers: []string{"eng"}, Path: "finance/x"}, pub, "t", true},
	}
	for _, tc := range cases {
		if _, _, ok := r.NewEventAudience(tc.scope).Visible(ctx, tc.id, tc.tenant); ok != tc.want {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.want)
		}
	}
	if n := cs.count(); n != 0 {
		t.Fatalf("ListLayerConfigs calls = %d, want 0", n)
	}
}

// Spec: §7.6
func TestEventAudience_PriorArmOnFirstLayerOnly(t *testing.T) {
	r, _ := newEventRegistry(t, nil)
	ctx := context.Background()
	prior := &layer.Visibility{Users: []string{"bob"}}
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"gone"}, Prior: prior})
	if _, _, ok := a.Visible(ctx, bob, "t"); !ok {
		t.Fatalf("prior audience withheld")
	}
	if _, _, ok := a.Visible(ctx, alice, "t"); ok {
		t.Fatalf("non-prior audience admitted on an absent layer")
	}
	b := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"old", "gone"}, Prior: prior})
	if got, _, _ := b.Visible(ctx, bob, "t"); !reflect.DeepEqual(got, []string{"old"}) {
		t.Fatalf("visible = %v, want the prior arm on Layers[0] only", got)
	}
}

// Spec: §7.6
func TestEventAudience_StoreErrorCachedCancellationNot(t *testing.T) {
	r, cs := newEventRegistry(t, errors.New("store down"))
	ctx := context.Background()
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"pub"}})
	for i := 0; i < 2; i++ {
		if _, _, ok := a.Visible(ctx, alice, "t"); ok {
			t.Fatalf("call %d admitted despite the store error", i)
		}
	}
	if n := cs.count(); n != 1 {
		t.Fatalf("ListLayerConfigs calls = %d, want 1 (error cached)", n)
	}

	r2, cs2 := newEventRegistry(t, nil)
	b := r2.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"pub"}})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, ok := b.Visible(cancelled, alice, "t"); ok {
		t.Fatalf("cancelled evaluator admitted")
	}
	if _, _, ok := b.Visible(ctx, alice, "t"); !ok {
		t.Fatalf("live caller withheld after a cancelled evaluator")
	}
	if n := cs2.count(); n != 2 {
		t.Fatalf("ListLayerConfigs calls = %d, want 2 (cancellation not cached)", n)
	}
	if w := core.EventAudienceWaiters(b); w != 0 {
		t.Fatalf("waiters = %d, want 0", w)
	}
}

// TestEventAudience_Concurrent exists for go test -race.
//
// Spec: §7.6
func TestEventAudience_Concurrent(t *testing.T) {
	r, cs := newEventRegistry(t, nil)
	r.WithGroupResolver(func(string) []string { return []string{"bob"} })
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"eng"}})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, ok := a.Visible(context.Background(), bob, "t"); !ok {
				t.Errorf("bob withheld despite SCIM membership")
			}
		}()
	}
	wg.Wait()
	if n := cs.count(); n != 1 {
		t.Fatalf("ListLayerConfigs calls = %d, want 1", n)
	}
}
