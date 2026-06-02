package serverboot

import (
	"context"

	"github.com/google/uuid"

	"github.com/lennylabs/podium/pkg/store"
)

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
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("podium:org:"+name)).String()
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
