package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// Spec: §9.3 — every backend that satisfies Store passes the
// conformance suite. The Postgres backend runs the full suite
// when PODIUM_POSTGRES_DSN is configured; CI and developer
// machines without Postgres skip it.
//
// The DSN follows lib/pq form, e.g.:
//
//	postgres://podium:podium@localhost:5432/podium?sslmode=disable
//
// Each sub-test starts from an empty schema by truncating all
// Podium tables. Tests do not run in parallel because they share
// the same backing database.
func TestPostgres_ConformanceSuite(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres conformance suite")
	}
	// One open per Suite invocation; the factory truncates between
	// sub-tests so each starts clean while reusing the connection
	// pool.
	s, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = s.Close() })

	storetest.Suite(t, func(t *testing.T) store.Store {
		t.Helper()
		if _, err := s.DB().ExecContext(context.Background(),
			`TRUNCATE manifests, dependencies, admin_grants, layer_configs, tenants
			 RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncate before sub-test: %v", err)
		}
		return s
	})
}
