package serverboot

import (
	"context"
	"strings"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// multiTenantUnrouted is the reserved tenant a §6.3.1 multi-tenant registry
// binds to. It is never provisioned, so it holds no data: a request that
// resolves to no tenant falls back to it and sees an empty view. It is not a
// UUID, so no org-name alias (which resolves through orgIDForName to a UUID)
// can route to it.
const multiTenantUnrouted = "podium:unrouted"

// tenantResolver maps a caller's organization value (an org ID or an org-name
// alias, §4.7.1) to a provisioned tenant ID, reporting false when no active
// tenant exists for it. A deactivated tenant (§4.7.1) is treated as
// unprovisioned, so a request naming it no longer resolves. It tries the value
// as a direct org ID first, then as an alias resolved through orgIDForName.
func tenantResolver(st store.Store) func(context.Context, string) (string, bool) {
	return func(ctx context.Context, orgValue string) (string, bool) {
		orgValue = strings.TrimSpace(orgValue)
		if orgValue == "" {
			return "", false
		}
		if t, err := st.GetTenant(ctx, orgValue); err == nil && t.Active {
			return orgValue, true
		}
		id := orgIDForName(orgValue)
		if t, err := st.GetTenant(ctx, id); err == nil && t.Active {
			return id, true
		}
		return "", false
	}
}

// defaultOrgName is the human-readable alias for the org the standalone /
// auto-bootstrap deployment creates. spec: §4.7.1 — "org names are
// human-readable aliases."
const defaultOrgName = "default"

// orgIDForName derives the deterministic org ID (a UUID) for an org name.
//
// spec: §4.7.1 — "Org IDs are UUIDs; org names are human-readable aliases."
// The ID is a name-based UUIDv5 rather than a random UUID so it stays stable
// across process restarts. A persistent store (SQLite, Postgres) keys every
// row by org ID, so a fresh random ID on each boot would orphan all
// previously ingested artifacts, layer configs, and admin grants.
func orgIDForName(name string) string {
	return core.OrgIDForName(name)
}

// bootstrapDefaultTenant idempotently creates the single bootstrapped org and
// returns its UUID ID. The org carries the human-readable name "default"
// while its ID is a UUID per §4.7.1. The ExposeScopePreview gate (§3.5) is
// seeded from config at creation; a nil pointer leaves the default. The
// returned ID is always the computed UUID, even when CreateTenant reports a
// store fault, so callers can thread it as the tenant for the rest of boot.
//
// spec: §13.10 auto-bootstrap, §4.7.1 tenancy.
func bootstrapDefaultTenant(ctx context.Context, st store.Store, exposeScopePreview *bool) (string, error) {
	id := orgIDForName(defaultOrgName)
	err := st.CreateTenant(ctx, store.Tenant{
		ID:                 id,
		Name:               defaultOrgName,
		ExposeScopePreview: exposeScopePreview,
	})
	return id, err
}
