package vector

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §4.7 — the sqlite-vec KNN runs once per query. The query plan
// materializes the KNN before it reads vec_index, so a tenant's rows never
// drive a KNN scan each; with vec_index in the outer loop, a query over a few
// thousand vectors took seconds instead of milliseconds.
func TestSQLiteVec_KNNRunsBeforeTheTenantJoin(t *testing.T) {
	t.Parallel()
	const dim = 4
	v, err := OpenSQLiteVec(SQLiteVecConfig{Path: filepath.Join(t.TempDir(), "vec.db"), Dimensions: dim})
	if err != nil {
		t.Fatalf("OpenSQLiteVec: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	ctx := context.Background()
	for i := range 64 {
		vec := []float32{float32(i), 1, float32(i % 3), 2}
		if err := v.Put(ctx, "acme", fmt.Sprintf("a/%d", i), "1.0.0", vec); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	rows, err := v.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+knnJoin(10)+` ORDER BY va.distance ASC LIMIT ?`,
		serialize([]float32{1, 1, 1, 1}), "acme", 5)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var steps []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, detail)
	}
	knn, join := -1, -1
	for i, s := range steps {
		if knn < 0 && strings.Contains(s, "vec_artifacts") {
			knn = i
		}
		if join < 0 && strings.Contains(s, " vi ") {
			join = i
		}
	}
	if knn < 0 || join < 0 || knn > join || !strings.Contains(strings.Join(steps, "\n"), "MATERIALIZE va") {
		t.Fatalf("plan does not materialize the KNN before the vec_index join:\n%s", strings.Join(steps, "\n"))
	}

	got, err := v.QueryModel(ctx, "acme", []float32{5, 1, 2, 2}, 3, "")
	if err != nil || len(got) != 3 || got[0].ArtifactID != "a/5" {
		t.Fatalf("QueryModel = %+v, %v; want 3 matches led by a/5", got, err)
	}
}
