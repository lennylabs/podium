package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// countingLayerStore counts ListLayerConfigs calls and injects faults. It
// returns ctx.Err() when the context is done: the memory store ignores the
// context, and without this return a cancelled evaluator would read
// successfully and the no-cache branch would go unexercised.
type countingLayerStore struct {
	store.Store

	// mu guards calls, the only field that changes after construction.
	mu    sync.Mutex
	calls int
	// firstErr fails the first read only; errAll fails every read.
	firstErr error
	errAll   error
	// block holds every read until it closes; blockFirst holds the first
	// read only. A held read returns ctx.Err() when its context ends.
	block      chan struct{}
	blockFirst chan struct{}
}

func (s *countingLayerStore) ListLayerConfigs(ctx context.Context, tenant string) ([]store.LayerConfig, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	if hold := s.holdFor(n); hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.errAll != nil {
		return nil, s.errAll
	}
	if n == 1 && s.firstErr != nil {
		return nil, s.firstErr
	}
	return s.Store.ListLayerConfigs(ctx, tenant)
}

// holdFor returns the channel read n waits on, or nil when it does not wait.
func (s *countingLayerStore) holdFor(n int) chan struct{} {
	if s.block != nil {
		return s.block
	}
	if n == 1 {
		return s.blockFirst
	}
	return nil
}

func (s *countingLayerStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// eventRegistryOpts configures newEventRegistryWith. The fault fields are
// copied onto the countingLayerStore.
type eventRegistryOpts struct {
	firstErr   error
	errAll     error
	block      chan struct{}
	blockFirst chan struct{}
	// noConfigs leaves the store without layer configs.
	noConfigs bool
	// boot is the boot-time layer list passed to core.New.
	boot []layer.Layer
}

// newEventRegistry seeds tenant "t" with a groups:[eng] layer and a public
// layer, wrapped in a counting store.
func newEventRegistry(t *testing.T, firstErr error) (*core.Registry, *countingLayerStore) {
	t.Helper()
	return newEventRegistryWith(t, eventRegistryOpts{firstErr: firstErr})
}

// newEventRegistryWith is newEventRegistry with fault modes, an optional empty
// layer-config list, and boot-time layers.
func newEventRegistryWith(t *testing.T, o eventRegistryOpts) (*core.Registry, *countingLayerStore) {
	t.Helper()
	mem := store.NewMemory()
	ctx := context.Background()
	if err := mem.CreateTenant(ctx, store.Tenant{ID: "t", Name: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if !o.noConfigs {
		for _, c := range []store.LayerConfig{
			{TenantID: "t", ID: "eng", SourceType: "local", Order: 1, Groups: []string{"eng"}},
			{TenantID: "t", ID: "pub", SourceType: "local", Order: 2, Public: true},
		} {
			if err := mem.PutLayerConfig(ctx, c); err != nil {
				t.Fatalf("PutLayerConfig(%s): %v", c.ID, err)
			}
		}
	}
	cs := &countingLayerStore{
		Store: mem, firstErr: o.firstErr, errAll: o.errAll,
		block: o.block, blockFirst: o.blockFirst,
	}
	return core.New(cs, "t", o.boot), cs
}

// switchResolver is a counting §6.3.1 group resolver whose member list the
// test can switch between calls.
type switchResolver struct {
	// mu guards members and calls.
	mu      sync.Mutex
	members []string
	calls   int
}

func (g *switchResolver) resolve(string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return append([]string(nil), g.members...)
}

func (g *switchResolver) set(members ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.members = members
}

func (g *switchResolver) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// waitFor polls cond until it holds or a bounded deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// visibleAsync runs a.Visible in a goroutine and reports ok on the channel.
func visibleAsync(ctx context.Context, a *core.EventAudience, id layer.Identity) <-chan bool {
	out := make(chan bool, 1)
	go func() {
		_, _, ok := a.Visible(ctx, id, "t")
		out <- ok
	}()
	return out
}

// recv returns the value on ch, failing the test after a bounded deadline.
func recv(t *testing.T, what string, ch <-chan bool) bool {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return false
	}
}

var (
	alice = layer.Identity{Sub: "alice", Groups: []string{"eng"}, IsAuthenticated: true}
	bob   = layer.Identity{Sub: "bob", IsAuthenticated: true}
	carol = layer.Identity{Sub: "carol", IsAuthenticated: true}
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

// Spec: §7.6
// Spec: §4.6
// Spec: §6.3.1
func TestEventAudience_PublicBypassAndEmptyPath(t *testing.T) {
	r, cs := newEventRegistry(t, nil)
	ctx := context.Background()
	eng := core.EventScope{TenantID: "t", Layers: []string{"eng"}}
	if _, _, ok := r.NewEventAudience(eng).Visible(ctx, layer.Identity{IsPublic: true}, "t"); !ok {
		t.Fatalf("IsPublic identity withheld from a groups:[eng] event")
	}
	if n := cs.count(); n != 0 {
		t.Fatalf("ListLayerConfigs calls = %d, want 0 before the public bypass", n)
	}
	scoped := alice
	scoped.Scopes = []string{"podium:read:finance/*"}
	if _, _, ok := r.NewEventAudience(eng).Visible(ctx, scoped, "t"); !ok {
		t.Fatalf("an empty Path applied the scope gate")
	}
}

// Spec: §7.6
// Spec: §4.6
func TestEventAudience_PriorWithdrawalAndAbsentLayer(t *testing.T) {
	r, _ := newEventRegistry(t, nil)
	ctx := context.Background()
	withdrawn := r.NewEventAudience(core.EventScope{
		TenantID: "t", Layers: []string{"eng"},
		Prior: &layer.Visibility{Users: []string{"bob"}},
	})
	if _, _, ok := withdrawn.Visible(ctx, bob, "t"); !ok {
		t.Fatalf("bob lost visibility of eng and was not told")
	}
	if _, _, ok := withdrawn.Visible(ctx, carol, "t"); ok {
		t.Fatalf("carol admitted under neither the current nor the prior record")
	}
	absent := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"gone"}})
	if _, _, ok := absent.Visible(ctx, alice, "t"); ok {
		t.Fatalf("absent layer with no Prior admitted")
	}
}

// Spec: §7.6
func TestEventAudience_WrappedCancellationErrorCached(t *testing.T) {
	r, cs := newEventRegistry(t, fmt.Errorf("store: %w", context.Canceled))
	ctx := context.Background()
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"pub"}})
	for i := 0; i < 2; i++ {
		if _, _, ok := a.Visible(ctx, alice, "t"); ok {
			t.Fatalf("call %d admitted despite the store error", i)
		}
	}
	if n := cs.count(); n != 1 {
		t.Fatalf("ListLayerConfigs calls = %d, want 1 (cacheability decided from ctx.Err())", n)
	}
}

// The read path keeps its boot-time fallback on a store error while the event
// path withholds on the same error.
//
// Spec: §7.6
// Spec: §4.6
func TestEventAudience_ReadPathKeepsBootFallback(t *testing.T) {
	r, cs := newEventRegistryWith(t, eventRegistryOpts{
		errAll: errors.New("store down"),
		boot: []layer.Layer{
			{ID: "boot", Visibility: layer.Visibility{Users: []string{"alice"}}},
			{ID: "hidden", Visibility: layer.Visibility{Users: []string{"bob"}}},
		},
	})
	ctx := context.Background()
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "x/seen", Version: "1.0.0", ContentHash: "sha256:a", Type: "skill", Layer: "boot"},
		{TenantID: "t", ArtifactID: "x/hidden", Version: "1.0.0", ContentHash: "sha256:b", Type: "skill", Layer: "hidden"},
	} {
		if err := cs.PutManifest(ctx, m); err != nil {
			t.Fatalf("PutManifest(%s): %v", m.ArtifactID, err)
		}
	}
	res, err := r.SearchArtifacts(ctx, alice, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != "x/seen" {
		t.Fatalf("results = %+v, want [x/seen] under the boot-time layers", res.Results)
	}
	if _, _, ok := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"boot"}}).Visible(ctx, alice, "t"); ok {
		t.Fatalf("event path admitted on a store error")
	}
}

// Spec: §7.6
// Spec: §4.6
func TestEventAudience_EmptyListFallsBackToBootLayers(t *testing.T) {
	r, _ := newEventRegistryWith(t, eventRegistryOpts{
		noConfigs: true,
		boot:      []layer.Layer{{ID: "boot", Visibility: layer.Visibility{Users: []string{"alice"}}}},
	})
	ctx := context.Background()
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"boot"}})
	if _, _, ok := a.Visible(ctx, alice, "t"); !ok {
		t.Fatalf("alice withheld from a boot-time layer she can see")
	}
	if _, _, ok := a.Visible(ctx, bob, "t"); ok {
		t.Fatalf("bob admitted to a boot-time layer he cannot see")
	}
}

// Spec: §7.6
// Spec: §4.6
func TestEventAudience_EvaluatesAtDelivery(t *testing.T) {
	r, cs := newEventRegistry(t, nil)
	ctx := context.Background()
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"eng"}})
	if err := cs.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "eng", SourceType: "local", Order: 1, Users: []string{"bob"},
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if _, _, ok := a.Visible(ctx, bob, "t"); !ok {
		t.Fatalf("bob withheld under the record stored after publish")
	}
	if _, _, ok := a.Visible(ctx, alice, "t"); ok {
		t.Fatalf("alice admitted under the record replaced after publish")
	}
}

// Spec: §7.6
// Spec: §6.3.1
func TestEventAudience_GroupMemoBound(t *testing.T) {
	r, cs := newEventRegistry(t, nil)
	ctx := context.Background()
	if err := cs.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "teams", SourceType: "local", Order: 3, Groups: []string{"g1", "g2", "g3"},
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	g := &switchResolver{}
	r.WithGroupResolver(g.resolve)
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"teams"}})
	for i := 0; i < 50; i++ {
		id := layer.Identity{Sub: fmt.Sprintf("user-%d", i), IsAuthenticated: true}
		if _, _, ok := a.Visible(ctx, id, "t"); ok {
			t.Fatalf("%s admitted without membership", id.Sub)
		}
	}
	if n := cs.count(); n != 1 {
		t.Fatalf("ListLayerConfigs calls = %d, want 1", n)
	}
	if n := g.count(); n != 3 {
		t.Fatalf("resolver calls = %d, want 1 per group (3)", n)
	}
}

// Spec: §7.6
func TestEventAudience_CancelledWaiterLeavesPromptly(t *testing.T) {
	block := make(chan struct{})
	r, cs := newEventRegistryWith(t, eventRegistryOpts{block: block})
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"pub"}})
	first := visibleAsync(context.Background(), a, alice)
	waitFor(t, "the first read", func() bool { return cs.count() == 1 })

	wctx, cancel := context.WithCancel(context.Background())
	waiter := visibleAsync(wctx, a, bob)
	waitFor(t, "the waiter", func() bool { return core.EventAudienceWaiters(a) == 1 })
	cancel()
	if recv(t, "the cancelled waiter", waiter) {
		t.Fatalf("cancelled waiter admitted")
	}

	close(block)
	if !recv(t, "the first caller", first) {
		t.Fatalf("first caller withheld")
	}
	if _, _, ok := a.Visible(context.Background(), carol, "t"); !ok {
		t.Fatalf("third caller withheld")
	}
	if n := cs.count(); n != 1 {
		t.Fatalf("ListLayerConfigs calls = %d, want 1 (result reused)", n)
	}
}

// Run under go test -race: B takes over as evaluator after A's cancellation.
//
// Spec: §7.6
func TestEventAudience_WaiterTakesOverCancelledEvaluator(t *testing.T) {
	r, cs := newEventRegistryWith(t, eventRegistryOpts{blockFirst: make(chan struct{})})
	a := r.NewEventAudience(core.EventScope{TenantID: "t", Layers: []string{"pub"}})
	actx, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	callerA := visibleAsync(actx, a, alice)
	waitFor(t, "caller A's read", func() bool { return cs.count() == 1 })

	callerB := visibleAsync(context.Background(), a, bob)
	waitFor(t, "caller B to wait", func() bool { return core.EventAudienceWaiters(a) == 1 })
	select {
	case <-callerB:
		t.Fatalf("caller B returned before the evaluator finished")
	default:
	}

	cancelA()
	if recv(t, "caller A", callerA) {
		t.Fatalf("cancelled evaluator admitted")
	}
	if !recv(t, "caller B", callerB) {
		t.Fatalf("caller B withheld after taking over the read")
	}
	if n := cs.count(); n != 2 {
		t.Fatalf("ListLayerConfigs calls = %d, want 2 (one more read by B)", n)
	}
}

// Spec: §7.6
// Spec: §4.6
func TestEventAudience_GroupsReReadPerEvent(t *testing.T) {
	r, _ := newEventRegistry(t, nil)
	ctx := context.Background()
	g := &switchResolver{members: []string{"carol"}}
	r.WithGroupResolver(g.resolve)
	scope := core.EventScope{TenantID: "t", Layers: []string{"eng"}}

	if _, _, ok := r.NewEventAudience(scope).Visible(ctx, carol, "t"); !ok {
		t.Fatalf("event A withheld from a SCIM member")
	}
	afterA := g.count()
	g.set()
	if _, _, ok := r.NewEventAudience(scope).Visible(ctx, carol, "t"); ok {
		t.Fatalf("event B admitted after SCIM removed carol")
	}
	afterB := g.count()
	g.set("carol")
	if _, _, ok := r.NewEventAudience(scope).Visible(ctx, carol, "t"); !ok {
		t.Fatalf("event C withheld after SCIM re-added carol")
	}
	if afterB <= afterA || g.count() <= afterB {
		t.Fatalf("resolver calls %d, %d, %d did not rise per event", afterA, afterB, g.count())
	}
}
