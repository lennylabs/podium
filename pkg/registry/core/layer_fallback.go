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
	// mu guards next and lists. next numbers the reads in the order they
	// began, and each entry keeps the number of the read that wrote it, so a
	// read that began earlier and finished later does not replace the list
	// of a newer read.
	mu    sync.Mutex
	next  uint64
	lists map[string]fallbackEntry
}

// fallbackEntry is the list of one tenant and the number of the read that
// wrote it.
type fallbackEntry struct {
	seq    uint64
	layers []layer.Layer
}

// begin numbers a read of the layer rows that is about to start.
func (f *layerFallback) begin() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	return f.next
}

// replace makes list the fallback of tenant, unless a read that began after
// seq has already replaced it.
func (f *layerFallback) replace(tenant string, seq uint64, list []layer.Layer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.lists[tenant]; ok && e.seq > seq {
		return
	}
	f.lists[tenant] = fallbackEntry{seq: seq, layers: cloneLayers(list)}
}

// load returns the fallback of tenant: a copy of its list, or an empty list
// for a tenant with no successful read. The list is never nil, because a nil
// list is the one that leaves a read unfiltered (unfiltered).
func (f *layerFallback) load(tenant string) []layer.Layer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneLayers(f.lists[tenant].layers)
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
	r.fallback = &layerFallback{lists: map[string]fallbackEntry{}}
	r.fallback.replace(tenant, r.fallback.begin(), list)
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
