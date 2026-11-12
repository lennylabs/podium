package serverboot

import (
	"context"
	"fmt"
	"log"

	"github.com/lennylabs/podium/pkg/store"
)

// layerRepoFormMigrator is a store that rewrites stored layer rows into the
// form their next write would take. The SQLite and Postgres stores implement
// it. The memory store holds no row in an earlier form and does not.
type layerRepoFormMigrator interface {
	MigrateLayerRepoForm(ctx context.Context) (int, error)
}

// migrateLayerRepoForm runs the start-time rewrite of every stored layer's repo
// on a store that implements it, and logs the number of rows it rewrote. The
// log line carries the count alone, because a stored repo can carry a
// credential.
//
// Spec: §13.4, §7.3.1 (Repository credentials)
func migrateLayerRepoForm(ctx context.Context, st store.Store) error {
	m, ok := st.(layerRepoFormMigrator)
	if !ok {
		return nil
	}
	n, err := m.MigrateLayerRepoForm(ctx)
	if err != nil {
		return fmt.Errorf("layer repo migration: %w", err)
	}
	if n > 0 {
		log.Printf("layer repo migration: rewrote %d stored layer(s) into the two-column form", n)
	}
	return nil
}
