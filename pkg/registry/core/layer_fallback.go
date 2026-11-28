package core

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/lennylabs/podium/pkg/layer"
)

// layerFallback is the §4.6 fallback list of each tenant: the layers of that
// tenant's last successful read of its layer rows. A read that cannot read
// the rows uses its tenant's list, so it shows a layer of one tenant to no
// caller of another, keeps no layer a successful read no longer lists, and
// shows a layer to no caller the rows of that read do not admit. A list fixed
// at the start would keep the visibility of the start after a cycle narrowed
// a row.
//
// Spec: §4.6 (Fallback list)
type layerFallback struct {
	// mu guards lists. A read replaces its tenant's list under mu once the
	// store has answered, so the list holds the rows of the read whose answer
	// was recorded last, whatever order the reads began in: a read that
	// began earlier and read the rows after a narrowing replaces the list of
	// a read that began later and read them before it.
	mu    sync.Mutex
	lists map[string][]layer.Layer
}

// replace makes list, the rows a read of tenant's layers returned, the
// fallback of tenant. The caller calls it after the store answered.
func (f *layerFallback) replace(tenant string, list []layer.Layer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists[tenant] = cloneLayers(list)
}

// load returns the fallback of tenant: a copy of its list, or an empty list
// for a tenant with no successful read. The list is never nil, because a nil
// list is the one that leaves a read unfiltered (unfiltered).
func (f *layerFallback) load(tenant string) []layer.Layer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneLayers(f.lists[tenant])
}

// cloneLayers copies a layer list and the visibility lists of its layers. The
// result is never nil.
func cloneLayers(in []layer.Layer) []layer.Layer {
	out := make([]layer.Layer, len(in))
	for i, l := range in {
		l.Visibility.Groups = slices.Clone(l.Visibility.Groups)
		l.Visibility.Users = slices.Clone(l.Visibility.Users)
		out[i] = l
	}
	return out
}

// WithLayerRows declares that the store keeps layer rows, as every
// store-backed registry does. A read of the rows that succeeds is then
// authoritative, an empty one included, and a read that fails uses the
// fallback list of its tenant. It seeds the list of tenant, the tenant that
// owns the layers the start bootstrapped, from that tenant's rows as they
// stand after the startup ingests; every other tenant starts with an empty
// list. A failed read of those rows is returned, because a start that cannot
// read its own layers has no list to fall back to.
//
// Spec: §4.6 (Fallback list)
func (r *Registry) WithLayerRows(ctx context.Context, tenant string) (*Registry, error) {
	list, err := r.layerConfigs(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("core: read the layers of tenant %s: %w", tenant, err)
	}
	r.fallback = &layerFallback{lists: map[string][]layer.Layer{}}
	r.fallback.replace(tenant, list)
	return r, nil
}

// unfiltered reports whether a resolved layer list leaves a read unfiltered
// by layer visibility: an empty list on a registry whose store keeps no
// layer rows, which is a filesystem source or a core built without a layer
// list. On a store that keeps layer rows an empty list is a tenant with no
// layer, which shows no layer.
//
// Spec: §4.6
func (r *Registry) unfiltered(resolved []layer.Layer) bool {
	return r.fallback == nil && len(resolved) == 0
}
