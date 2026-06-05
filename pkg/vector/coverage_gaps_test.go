package vector_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// openVec opens a fresh on-disk sqlite-vec store under a temp directory and
// registers cleanup. Matches the setup the conformance and basics tests use.
func openVec(t *testing.T, dim int) *vector.SQLiteVec {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vec.db")
	v, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Path: path, Dimensions: dim})
	if err != nil {
		t.Fatalf("OpenSQLiteVec: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

// Spec: §13.10 — OpenSQLiteVec creates the file and applies the schema on the
// success path. Putting and reading a row back confirms the vec0 table and the
// companion index exist after construction.
func TestSQLiteVec_OpenAppliesSchema(t *testing.T) {
	t.Parallel()
	v := openVec(t, 4)
	ctx := context.Background()
	if err := v.Put(ctx, "default", "a/1", "1.0.0", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Put after open: %v", err)
	}
	got, err := v.Query(ctx, "default", []float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].ArtifactID != "a/1" {
		t.Fatalf("Query = %v, want one match for a/1", got)
	}
}

// Spec: §13.10 — OpenSQLiteVec returns ErrUnreachable when the database file
// cannot be opened. A path inside a non-existent directory fails at Ping
// because sqlite creates the file lazily on the first connection.
func TestSQLiteVec_OpenUnwritablePathUnreachable(t *testing.T) {
	t.Parallel()
	bad := filepath.Join(t.TempDir(), "no", "such", "dir", "vec.db")
	_, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Path: bad, Dimensions: 4})
	if err == nil {
		t.Fatal("OpenSQLiteVec on an unwritable path: want error, got nil")
	}
	if !errors.Is(err, vector.ErrUnreachable) {
		t.Errorf("OpenSQLiteVec error = %v, want ErrUnreachable", err)
	}
}

// Spec: §4.7 — applySchema is idempotent across re-opens. The second open of an
// existing file re-runs CREATE ... IF NOT EXISTS and the model_id ALTER, which
// hits the duplicate-column branch and is ignored. Rows written before the
// re-open survive.
func TestSQLiteVec_ReopenIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "vec.db")
	ctx := context.Background()

	first, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Path: path, Dimensions: 4})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Put(ctx, "default", "a/1", "1.0.0", []float32{0, 1, 0, 0}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Path: path, Dimensions: 4})
	if err != nil {
		t.Fatalf("second open (schema re-applied): %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	got, err := second.Query(ctx, "default", []float32{0, 1, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Query after re-open: %v", err)
	}
	if len(got) != 1 || got[0].ArtifactID != "a/1" {
		t.Fatalf("Query after re-open = %v, want the row written before re-open", got)
	}
}

// Spec: §4.7 — PutModel rejects an empty identifier with ErrInvalidArgument
// before opening a transaction.
func TestSQLiteVec_PutModelEmptyArgsRejected(t *testing.T) {
	t.Parallel()
	v := openVec(t, 4)
	ctx := context.Background()
	cases := []struct {
		name                           string
		tenant, artifact, ver, modelID string
	}{
		{"empty tenant", "", "a/1", "1.0.0", "model-A"},
		{"empty artifact", "default", "", "1.0.0", "model-A"},
		{"empty version", "default", "a/1", "", "model-A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.PutModel(ctx, tc.tenant, tc.artifact, tc.ver, []float32{1, 0, 0, 0}, tc.modelID)
			if !errors.Is(err, vector.ErrInvalidArgument) {
				t.Errorf("PutModel(%q,%q,%q) = %v, want ErrInvalidArgument",
					tc.tenant, tc.artifact, tc.ver, err)
			}
		})
	}
}

// Spec: §4.7 — PutModel rejects a vector whose length differs from the
// configured dimension with ErrDimensionMismatch.
func TestSQLiteVec_PutModelDimensionMismatch(t *testing.T) {
	t.Parallel()
	v := openVec(t, 4)
	ctx := context.Background()
	err := v.PutModel(ctx, "default", "a/1", "1.0.0", []float32{1, 0, 0}, "model-A")
	if !errors.Is(err, vector.ErrDimensionMismatch) {
		t.Errorf("PutModel with a 3-d vector into a 4-d store = %v, want ErrDimensionMismatch", err)
	}
}

// Spec: §4.7 — PutModel inserts a tagged row on the first write and updates the
// same row in place on a re-Put, leaving one row that a model-restricted query
// returns.
func TestSQLiteVec_PutModelInsertThenUpdate(t *testing.T) {
	t.Parallel()
	v := openVec(t, 4)
	ctx := context.Background()

	// First write takes the INSERT branch and records a fresh rowid.
	if err := v.PutModel(ctx, "default", "a/1", "1.0.0", []float32{1, 0, 0, 0}, "model-A"); err != nil {
		t.Fatalf("PutModel insert: %v", err)
	}
	// Re-Put the same tuple under a new model: the UPDATE branch rewrites the
	// vector and re-tags the row rather than inserting a second one.
	if err := v.PutModel(ctx, "default", "a/1", "1.0.0", []float32{0, 0, 1, 0}, "model-B"); err != nil {
		t.Fatalf("PutModel update: %v", err)
	}

	// The row is gone from model-A's view and present in model-B's.
	if got := mustQueryModel(t, v, "default", []float32{0, 0, 1, 0}, "model-B"); !got["a/1"] {
		t.Errorf("model-B query missing the re-tagged a/1: %v", got)
	}
	if got := mustQueryModel(t, v, "default", []float32{1, 0, 0, 0}, "model-A"); got["a/1"] {
		t.Errorf("a/1 still visible under model-A after re-tag to model-B: %v", got)
	}
}

// mustQueryModel runs QueryModel and returns the artifact-id set, failing on
// error. Local helper to keep the assertions above readable.
func mustQueryModel(t *testing.T, v *vector.SQLiteVec, tenant string, vec []float32, modelID string) map[string]bool {
	t.Helper()
	got, err := v.QueryModel(context.Background(), tenant, vec, 10, modelID)
	if err != nil {
		t.Fatalf("QueryModel: %v", err)
	}
	out := map[string]bool{}
	for _, m := range got {
		out[m.ArtifactID] = true
	}
	return out
}

// Spec: §4.7 — PurgeModelExcept with an empty modelID purges nothing and
// returns a zero count, leaving every row in place.
func TestSQLiteVec_PurgeEmptyModelIsNoOp(t *testing.T) {
	t.Parallel()
	v := openVec(t, 4)
	ctx := context.Background()
	if err := v.PutModel(ctx, "default", "a/1", "1.0.0", []float32{1, 0, 0, 0}, "model-A"); err != nil {
		t.Fatalf("PutModel: %v", err)
	}
	n, err := v.PurgeModelExcept(ctx, "default", "")
	if err != nil {
		t.Fatalf("PurgeModelExcept: %v", err)
	}
	if n != 0 {
		t.Errorf("purged %d rows for empty modelID, want 0", n)
	}
	if got := mustQueryModel(t, v, "default", []float32{1, 0, 0, 0}, "model-A"); !got["a/1"] {
		t.Errorf("a/1 removed by an empty-modelID purge: %v", got)
	}
}

// Spec: §4.7 — PurgeModelExcept deletes rows whose model_id is not the keep
// model and reports the count. Rows on the keep model survive.
func TestSQLiteVec_PurgeRemovesOtherModels(t *testing.T) {
	t.Parallel()
	v := openVec(t, 4)
	ctx := context.Background()
	if err := v.PutModel(ctx, "default", "keep/1", "1.0.0", []float32{1, 0, 0, 0}, "model-B"); err != nil {
		t.Fatalf("PutModel keep: %v", err)
	}
	if err := v.PutModel(ctx, "default", "stale/1", "1.0.0", []float32{0, 1, 0, 0}, "model-A"); err != nil {
		t.Fatalf("PutModel stale: %v", err)
	}
	if err := v.PutModel(ctx, "default", "stale/2", "1.0.0", []float32{0, 0, 1, 0}, "model-A"); err != nil {
		t.Fatalf("PutModel stale2: %v", err)
	}
	n, err := v.PurgeModelExcept(ctx, "default", "model-B")
	if err != nil {
		t.Fatalf("PurgeModelExcept: %v", err)
	}
	if n != 2 {
		t.Errorf("purged %d rows, want 2 (the two model-A rows)", n)
	}
	got := mustQueryModel(t, v, "default", []float32{1, 0, 0, 0}, "model-B")
	if !got["keep/1"] {
		t.Errorf("keep/1 removed by purge: %v", got)
	}
	if got["stale/1"] || got["stale/2"] {
		t.Errorf("stale model-A rows survived purge: %v", got)
	}
}

// Spec: §13.10 — Delete on a tuple that was never inserted commits cleanly and
// reports no error. The missing-row path is a no-op per the Provider contract.
func TestSQLiteVec_DeleteMissingKeyNoOp(t *testing.T) {
	t.Parallel()
	v := openVec(t, 4)
	ctx := context.Background()
	if err := v.Delete(ctx, "default", "does-not-exist", "1.0.0"); err != nil {
		t.Errorf("Delete of a missing key = %v, want nil (no-op)", err)
	}
}

// Spec: §13.10 — Delete removes an existing row from both the index and the
// vec0 table, after which a query no longer returns it. A second Delete of the
// same key is a no-op.
func TestSQLiteVec_DeleteExistingThenAgain(t *testing.T) {
	t.Parallel()
	v := openVec(t, 4)
	ctx := context.Background()
	if err := v.Put(ctx, "default", "a/1", "1.0.0", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := v.Delete(ctx, "default", "a/1", "1.0.0"); err != nil {
		t.Fatalf("Delete existing: %v", err)
	}
	got, err := v.Query(ctx, "default", []float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Query after delete: %v", err)
	}
	for _, m := range got {
		if m.ArtifactID == "a/1" {
			t.Errorf("a/1 still present after Delete: %v", got)
		}
	}
	// Deleting again exercises the missing-row branch on a now-empty key.
	if err := v.Delete(ctx, "default", "a/1", "1.0.0"); err != nil {
		t.Errorf("second Delete = %v, want nil (no-op)", err)
	}
}
