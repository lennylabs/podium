package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// OrgIDForName derives a tenant's org ID from its name. The ID is a
// name-derived UUIDv5 so it stays stable across restarts (§4.7.1, §6.3.1).
// The per-request tenant resolver derives the same ID from the caller's
// organization, so the API and the routing path agree on a tenant's ID.
func OrgIDForName(name string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("podium:org:"+name)).String()
}

// OperatorAuthorize verifies that id holds the instance operator role
// (§4.7.1 Operator role) before a tenant-management operation proceeds. The
// operator grant is cross-org, so the check does not consult the request's
// tenant. A public or unauthenticated caller is forbidden, matching
// AdminAuthorize.
func (r *Registry) OperatorAuthorize(ctx context.Context, id layer.Identity) error {
	if id.IsPublic || !id.IsAuthenticated || id.Sub == "" {
		return fmt.Errorf("%w: tenant management requires an authenticated identity", ErrForbidden)
	}
	ok, err := r.store.IsOperator(ctx, id.Sub)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if !ok {
		return fmt.Errorf("%w: %s is not an operator", ErrForbidden, id.Sub)
	}
	return nil
}

// ProvisionTenant creates a tenant from name, quota, and the scope-preview
// gate when it is not already provisioned, deriving the org ID from the
// name. It reports whether the tenant was newly created. Provisioning is
// idempotent: an already-provisioned name returns the stored tenant
// unchanged, including its active state, with created=false (§7.3.3). The
// returned Tenant is read back from the store so it carries the canonical
// stored form (an active flag, the derived ID).
func (r *Registry) ProvisionTenant(ctx context.Context, name string, quota store.Quota, exposeScopePreview *bool) (store.Tenant, bool, error) {
	id := OrgIDForName(name)
	if existing, err := r.store.GetTenant(ctx, id); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, store.ErrTenantNotFound) {
		return store.Tenant{}, false, err
	}
	if err := r.store.CreateTenant(ctx, store.Tenant{
		ID: id, Name: name, Quota: quota, ExposeScopePreview: exposeScopePreview,
	}); err != nil {
		return store.Tenant{}, false, err
	}
	created, err := r.store.GetTenant(ctx, id)
	if err != nil {
		return store.Tenant{}, false, err
	}
	return created, true, nil
}

// ListTenants returns every provisioned tenant (§7.3.3). The read crosses
// org boundaries and is operator-only at the HTTP layer.
func (r *Registry) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	return r.store.ListTenants(ctx)
}

// GetTenant returns a tenant by ID, or store.ErrTenantNotFound.
func (r *Registry) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	return r.store.GetTenant(ctx, id)
}

// UpdateTenant writes a tenant's mutable configuration. The PATCH handler
// merges the supplied fields onto the current row before calling this, so
// UpdateTenant writes the whole mutable record.
func (r *Registry) UpdateTenant(ctx context.Context, t store.Tenant) error {
	return r.store.UpdateTenant(ctx, t)
}

// DeactivateTenant soft-deactivates a tenant: it stops resolving while its
// data persists (§4.7.1). Returns store.ErrTenantNotFound for an unknown ID.
func (r *Registry) DeactivateTenant(ctx context.Context, id string) error {
	return r.store.DeactivateTenant(ctx, id)
}
