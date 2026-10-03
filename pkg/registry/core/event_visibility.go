package core

import (
	"context"
	"sync"

	"github.com/lennylabs/podium/pkg/layer"
)

// EventScope names what a §7.6 change event concerns. The publisher sets it,
// because payload keys differ by event type (artifact.deprecated and
// layer.config_changed carry no tenant) and a reorder's `layer` value is a
// joined string that no per-layer lookup resolves.
//
// Spec: §7.6
type EventScope struct {
	// TenantID is the tenant the event belongs to. An empty value withholds
	// the event from every subscriber.
	TenantID string
	// Layers lists the layers the event names, in reorder sequence for a
	// reorder. An empty list withholds the event from every subscriber.
	Layers []string
	// Path is the artifact ID or domain path the event names. An empty value
	// skips the §6.3.1 scope gate.
	Path string
	// Prior is the visibility Layers[0] held before a layer.config_changed,
	// snapshotted at publish time because an unregister tombstones the record
	// before delivery. It is nil on every other event.
	Prior *layer.Visibility
}

// EventAudience evaluates one published event for each subscriber that
// dequeues it. Every subscriber of the event shares one layer-list read and
// one group memo, so the store reads per event are one ListLayerConfigs plus
// one per distinct group, independent of the subscriber count.
//
// Spec: §7.6
type EventAudience struct {
	r     *Registry
	scope EventScope

	// mu guards the single-flight state below. No caller holds it across a
	// store read: a sync.Mutex wait ignores context, so a subscriber whose
	// client disconnected could not leave while another subscriber's read
	// hangs. Waiters select on done together with their own ctx.Done().
	mu sync.Mutex
	// started is set when a caller becomes the evaluator, and cleared only
	// when that evaluator's read failed because its own context ended.
	started bool
	// resolved is set once a result (layers or err) is published, and never
	// cleared.
	resolved bool
	// waiters counts callers blocked on done; tests observe it.
	waiters int
	// done closes when a result is published or the evaluator gives up. The
	// evaluator that gives up swaps in a fresh channel before closing the old
	// one, so every waiter re-checks the state under mu.
	done   chan struct{}
	layers map[string]layer.Layer
	err    error

	// groups is the per-event group memo, or nil when the registry has no
	// group resolver, which VisibleWith treats as the JWT-only path.
	groups layer.GroupResolver
}

// NewEventAudience builds the evaluator for one event. It performs no I/O, so
// the publish path (Server.PublishEvent) never waits on the store; the shared
// reads happen at the first delivery that needs them.
//
// Spec: §7.6
func (r *Registry) NewEventAudience(scope EventScope) *EventAudience {
	return &EventAudience{
		r:      r,
		scope:  scope,
		done:   make(chan struct{}),
		groups: newMemoResolver(r.resolveGroup),
	}
}

// Visible reports whether the event reaches the subscriber id routed to
// tenant. visible lists the layers in the scope that id can see, in scope
// order. rewrite is true when the scope names more than one layer, which
// tells the caller to rewrite the payload's `layer` value to visible. An event
// that cannot be evaluated is withheld (ok false): the stream fails closed.
//
// The IsPublic arm admits every named layer before any store read. It mirrors
// visibleManifests, where the §4.6 public-mode and no-identity bypasses admit
// every layer, and it is deliberately looser than the §7.3.1 layer-list
// readableBy gate in the server package.
//
// Spec: §4.6, §6.3.1
func (a *EventAudience) Visible(ctx context.Context, id layer.Identity, tenant string) (visible []string, rewrite bool, ok bool) {
	s := a.scope
	if s.TenantID == "" || tenant != s.TenantID || len(s.Layers) == 0 {
		return nil, false, false
	}
	if s.Path != "" && !layer.ParseScopes(id.Scopes).AllowsRead(s.Path) {
		return nil, false, false
	}
	rewrite = len(s.Layers) > 1
	if id.IsPublic {
		return append([]string(nil), s.Layers...), rewrite, true
	}
	resolved, err := a.resolve(ctx)
	if err != nil {
		return nil, false, false
	}
	for i, name := range s.Layers {
		if a.layerVisible(resolved, i, name, id) {
			visible = append(visible, name)
		}
	}
	if len(visible) == 0 {
		return nil, false, false
	}
	return visible, rewrite, true
}

// layerVisible applies the §4.6 evaluator to one named layer: under its
// current stored record, or, for Layers[0] only, under the publisher's
// pre-change snapshot. A layer absent from the resolved list is not visible
// under the current arm.
func (a *EventAudience) layerVisible(resolved map[string]layer.Layer, i int, name string, id layer.Identity) bool {
	if l, ok := resolved[name]; ok && layer.VisibleWith(l, id, a.groups) {
		return true
	}
	if i != 0 || a.scope.Prior == nil {
		return false
	}
	return layer.VisibleWith(layer.Layer{ID: name, Visibility: *a.scope.Prior}, id, a.groups)
}

// resolve returns the event tenant's layer list, read once per event by the
// first caller that needs it. The evaluating caller's request context bounds
// the read, because no request-path store read in the registry carries a
// timeout; a waiter leaves on its own context. A read that failed because the
// evaluator's context ended is not cached, so one disconnecting subscriber
// cannot withhold the event from the others. Any other read error is cached
// and withholds the event from every subscriber.
func (a *EventAudience) resolve(ctx context.Context) (map[string]layer.Layer, error) {
	for {
		a.mu.Lock()
		if a.resolved {
			layers, err := a.layers, a.err
			a.mu.Unlock()
			return layers, err
		}
		if !a.started {
			a.started = true
			a.mu.Unlock()
			return a.evaluate(ctx)
		}
		// Capture done under mu so a waiter never waits on a channel that
		// replaced the one it saw.
		done := a.done
		a.waiters++
		a.mu.Unlock()
		var cancelled bool
		select {
		case <-done:
		case <-ctx.Done():
			cancelled = true
		}
		a.mu.Lock()
		a.waiters--
		a.mu.Unlock()
		if cancelled {
			return nil, ctx.Err()
		}
	}
}

// evaluate performs the shared layer read with mu released, then publishes
// the result or, when its own context ended, hands the read to a waiter.
// Cacheability is decided from ctx.Err() on the evaluating context and never
// by matching the store error, because backends wrap errors.
func (a *EventAudience) evaluate(ctx context.Context) (map[string]layer.Layer, error) {
	list, err := a.r.layerConfigs(ctx, a.scope.TenantID)
	if err == nil && len(list) == 0 {
		// Same fallback as the read path on an empty list, so both surfaces
		// resolve the same layers whenever the read succeeds.
		list = a.r.layers
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil && ctx.Err() != nil {
		a.started = false
		old := a.done
		a.done = make(chan struct{})
		close(old)
		return nil, err
	}
	if err == nil {
		a.layers = make(map[string]layer.Layer, len(list))
		for _, l := range list {
			a.layers[l.ID] = l
		}
	}
	a.err = err
	a.resolved = true
	close(a.done)
	return a.layers, a.err
}

// memoResolver memoizes a §6.3.1 group resolver for the life of one event.
// layer.GroupResolver takes no context, and the SCIM resolver reports an
// error as nil members, which denies, so caching nil for the event is
// fail-closed. The memo lives on the audience and never on the Registry, so
// every event re-reads group membership.
type memoResolver struct {
	next layer.GroupResolver

	// mu guards members and is held across the underlying call so each group
	// is resolved once per event. The resolver takes no context, so a caller
	// that waited on mu could not have left the call early either.
	mu      sync.Mutex
	members map[string][]string
}

// newMemoResolver wraps next in a per-event memo. A nil next passes through as
// nil so VisibleWith keeps its JWT-only path.
func newMemoResolver(next layer.GroupResolver) layer.GroupResolver {
	if next == nil {
		return nil
	}
	m := &memoResolver{next: next, members: map[string][]string{}}
	return m.resolve
}

// resolve returns the memoized members of group.
func (m *memoResolver) resolve(group string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.members[group]; ok {
		return v
	}
	v := m.next(group)
	m.members[group] = v
	return v
}
