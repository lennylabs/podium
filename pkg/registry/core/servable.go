package core

import (
	"context"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
)

// retiredKey keys the memo of the §4.6 no-inheritance verdict.
type retiredKey struct {
	tenant      string
	contentHash string
}

// servableRows drops every row whose stored ARTIFACT.md declares extends:,
// which the §4.6 no-inheritance rule retires. The core listing choke points
// (visibleManifests, adminVisibleManifests, and artifactExistsAnywhere) call
// it on the ListManifests result, so every read path that excludes a
// soft-deleted row also excludes a retired one, and no retired row reaches
// version resolution, admission, or the revalidation bypass. The verdict is
// memoized per (tenant, content hash), because the predicate decodes YAML and
// this runs on every row of every listing.
//
// Spec: §4.6
func (r *Registry) servableRows(ctx context.Context, rows []store.ManifestRecord) []store.ManifestRecord {
	tenant := r.TenantFor(ctx)
	out := make([]store.ManifestRecord, 0, len(rows))
	for _, m := range rows {
		if !r.isRetired(tenant, m) {
			out = append(out, m)
		}
	}
	return out
}

// isRetired reports the §4.6 no-inheritance verdict for m, reading and
// filling the memo. A row with no content hash is decided without the memo,
// because an empty hash does not name one set of bytes.
func (r *Registry) isRetired(tenant string, m store.ManifestRecord) bool {
	if m.ContentHash == "" {
		return manifest.DeclaresExtends(m.Frontmatter)
	}
	key := retiredKey{tenant: tenant, contentHash: m.ContentHash}
	r.retiredMu.RLock()
	verdict, ok := r.retired[key]
	r.retiredMu.RUnlock()
	if ok {
		return verdict
	}
	verdict = manifest.DeclaresExtends(m.Frontmatter)
	r.retiredMu.Lock()
	if r.retired == nil {
		r.retired = map[retiredKey]bool{}
	}
	r.retired[key] = verdict
	r.retiredMu.Unlock()
	return verdict
}
