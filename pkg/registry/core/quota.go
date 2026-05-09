package core

import (
	"context"
	"fmt"

	"github.com/lennylabs/podium/pkg/store"
)

// QuotaInfo carries the §4.7.8 quota envelope for one tenant: both
// the operator-configured limits and the current measured usage.
type QuotaInfo struct {
	TenantID string
	Limits   store.Quota
	// StorageBytes is the sum of frontmatter+body bytes across every
	// manifest in the tenant. v1 derives this from the manifest list
	// on every call; for large catalogs the operator should switch to
	// an incremental counter maintained on PutManifest.
	StorageBytes int64
}

// Quota returns the quota envelope for the registry's tenant.
// Read-only and cheap for catalogs up to a few thousand artifacts;
// for larger catalogs, this should be replaced with an incremental
// counter (deferred until the bottleneck materializes).
func (r *Registry) Quota(ctx context.Context) (*QuotaInfo, error) {
	t, err := r.store.GetTenant(ctx, r.tenantID)
	if err != nil {
		return nil, fmt.Errorf("quota: %w", err)
	}
	out := &QuotaInfo{TenantID: t.ID, Limits: t.Quota}
	manifests, err := r.store.ListManifests(ctx, r.tenantID)
	if err != nil {
		return nil, fmt.Errorf("quota: list manifests: %w", err)
	}
	for _, m := range manifests {
		out.StorageBytes += int64(len(m.Frontmatter)) + int64(len(m.Body))
	}
	return out, nil
}
