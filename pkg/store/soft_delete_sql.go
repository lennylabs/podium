package store

import (
	"context"
	"database/sql"
)

// softDeleteBatchSize is the most artifact IDs one SoftDeleteArtifacts
// statement binds. SQLite refuses a statement with more than 32,766 bound
// parameters and Postgres one with more than 65,535, so a removal larger
// than either, such as an empty snapshot of a large layer, would fail on
// every later cycle. The SQL stores therefore tombstone in batches of this
// size inside one transaction, which keeps the removal atomic.
const softDeleteBatchSize = 1000

// idBatches splits ids into consecutive slices of at most size entries.
func idBatches(ids []string, size int) [][]string {
	out := make([][]string, 0, (len(ids)+size-1)/size)
	for len(ids) > size {
		out = append(out, ids[:size])
		ids = ids[size:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

// queryRower is the single-row read *sql.DB, *sql.Conn, and *sql.Tx offer, so
// an ingest-mark check runs in or outside the tombstoning transaction.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
