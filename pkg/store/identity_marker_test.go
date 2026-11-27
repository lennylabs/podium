package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// markerAcrossOpens sets the verified-identity record through one open of a
// store, reads it through a second open, clears it there, and reads it again
// through a third. It returns what the second and third opens observed.
func markerAcrossOpens(t *testing.T, open func() (store.Store, func())) (afterSet, afterClear bool) {
	t.Helper()
	ctx := context.Background()
	name := store.DeploymentMarkerVerifiedIdentity

	first, closeFirst := open()
	mustNoErr(t, first.SetDataMigrationApplied(ctx, name, true))
	closeFirst()

	second, closeSecond := open()
	afterSet, err := second.DataMigrationApplied(ctx, name)
	mustNoErr(t, err)
	mustNoErr(t, second.SetDataMigrationApplied(ctx, name, false))
	closeSecond()

	third, closeThird := open()
	defer closeThird()
	afterClear, err = third.DataMigrationApplied(ctx, name)
	mustNoErr(t, err)
	return afterSet, afterClear
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Spec: §13.10 (Identity provider removed between starts) — the SQLite store
// keeps the verified-identity record across restarts, which is what lets a
// later start without a verifier find it, and keeps its removal the same way.
func TestSQLite_VerifiedIdentityMarkerPersistsAcrossOpens(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "podium.db")
	afterSet, afterClear := markerAcrossOpens(t, func() (store.Store, func()) {
		s, err := store.OpenSQLite(path)
		mustNoErr(t, err)
		return s, func() { _ = s.Close() }
	})
	if !afterSet || afterClear {
		t.Errorf("reopened SQLite store: record after set %v, after clear %v; want true, false", afterSet, afterClear)
	}
}

// Spec: §13.10 (Identity provider removed between starts) — the Postgres
// store keeps the verified-identity record across restarts in the shared
// public schema, one record per deployment. It runs when PODIUM_POSTGRES_DSN
// names a database and skips otherwise.
func TestPostgres_VerifiedIdentityMarkerPersistsAcrossOpens(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres verified-identity record check")
	}
	reset, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres: %v (database unreachable)", err)
	}
	mustNoErr(t, reset.ResetForTest(context.Background()))
	_ = reset.Close()
	afterSet, afterClear := markerAcrossOpens(t, func() (store.Store, func()) {
		s, err := store.OpenPostgres(dsn)
		mustNoErr(t, err)
		return s, func() { _ = s.Close() }
	})
	if !afterSet || afterClear {
		t.Errorf("reopened Postgres store: record after set %v, after clear %v; want true, false", afterSet, afterClear)
	}
}
