package vector_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/lennylabs/podium/pkg/vector"
)

// pgvector depth tests. All gate on PODIUM_POSTGRES_DSN[_VECTOR] via the existing
// pgVectorDSN() helper (pgvector_test.go) so a run without Postgres skips
// cleanly. These go beyond the conformance suite, which asserts only near-zero
// self-distance and axis-aligned ordering at dim=8: they assert cosine-metric
// correctness on non-trivial vectors, exercise an ANN index path, round-trip a
// production dimension, run the model-versioning methods against real Postgres,
// and extend tenant isolation to the vec_artifacts table.
//
// Schema isolation: each test runs inside its own ephemeral Postgres schema so
// it can create vec_artifacts at its own dimension without colliding with the
// conformance suite's shared dim=8 table. This mirrors the §4.7.1 schema-per-org
// isolation model the metadata store already uses (pkg/store/postgres.go SET
// search_path). The schema is created on an admin connection, the PgVector is
// pointed at it via the search_path connection option, and the schema is dropped
// in cleanup.

// pgSchemaScope is one ephemeral-schema fixture: an admin connection used to
// create and drop the schema, the schema name, and an open PgVector whose
// vec_artifacts table lives inside it.
type pgSchemaScope struct {
	admin  *sql.DB
	schema string
	pg     *vector.PgVector
}

// openPgSchema creates a uniquely-named schema on the configured Postgres and
// returns a PgVector bound to it at the requested dimension, or skips when no
// DSN is set or Postgres is unreachable. The caller defers scope.close.
//
// Spec: §4.7.1 — Postgres schema-per-org isolation (reused here to isolate each
// depth test's vec_artifacts table).
func openPgSchema(t *testing.T, dim int) *pgSchemaScope {
	t.Helper()
	dsn, ok := pgVectorDSN()
	if !ok {
		t.Skip("PODIUM_POSTGRES_DSN[_VECTOR] unset; skipping pgvector depth test")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("open postgres: %v", err)
	}
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Skipf("pgvector unreachable: %v", err)
	}
	schema := "podium_vec_" + uniqueSuffix()
	// Identifier is hex from uniqueSuffix plus a fixed prefix, so direct
	// interpolation is safe (no user input, no quoting hazard).
	if _, err := admin.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		_ = admin.Close()
		t.Skipf("create schema: %v", err)
	}
	scopedDSN := withSearchPath(dsn, schema)
	pg, err := vector.OpenPgVector(vector.PgVectorConfig{DSN: scopedDSN, Dimensions: dim})
	if err != nil {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = admin.Close()
		t.Skipf("OpenPgVector on schema %s: %v", schema, err)
	}
	return &pgSchemaScope{admin: admin, schema: schema, pg: pg}
}

// close drops the ephemeral schema and releases both connections. Registered via
// t.Cleanup by every depth test.
func (s *pgSchemaScope) close() {
	if s.pg != nil {
		_ = s.pg.Close()
	}
	if s.admin != nil {
		_, _ = s.admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+s.schema+` CASCADE`)
		_ = s.admin.Close()
	}
}

// withSearchPath appends a search_path connection option to a DSN so a fresh
// PgVector creates and queries vec_artifacts inside the named schema. The path
// includes public so the vector type (installed by CREATE EXTENSION into public)
// resolves. Handles both URL-form DSNs (postgres://...) and keyword/value DSNs.
// For URL form the option value is percent-encoded; libpq reads `options` as
// server command-line flags, so `-c search_path=<schema>,public` becomes
// `-csearch_path%3D<schema>%2Cpublic`.
func withSearchPath(dsn, schema string) string {
	opt := "-csearch_path=" + schema + ",public"
	if strings.Contains(dsn, "://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		encoded := strings.ReplaceAll(opt, "=", "%3D")
		encoded = strings.ReplaceAll(encoded, ",", "%2C")
		return dsn + sep + "options=" + encoded
	}
	// Keyword/value DSN: options takes a quoted value in libpq syntax.
	return dsn + " options='" + opt + "'"
}

// cosineDistance is the reference metric the pgvector `<=>` operator must match:
// 1 - (a·b)/(‖a‖‖b‖), bounded [0, 2].
func cosineDistance(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 1
	}
	return 1 - dot/(math.Sqrt(na)*math.Sqrt(nb))
}

// TestPgVector_Depth_CosineCorrectness asserts the `<=>` operator returns the
// cosine distance computed independently for non-trivial (non-axis-aligned)
// vectors, and that ordering by `<=>` ranks the nearest vector first
// (metric correctness and recall). The conformance suite only checks near-zero
// self-distance and axis-aligned ordering.
//
// Spec: §4.7 — hybrid retrieval's vector half (the metric is left to the
// backend; pgvector standardizes on cosine).
func TestPgVector_Depth_CosineCorrectness(t *testing.T) {
	const dim = 16
	scope := openPgSchema(t, dim)
	t.Cleanup(scope.close)
	p := scope.pg
	ctx := context.Background()
	tenant := "acme"

	// Non-trivial corpus: each vector mixes several coordinates so the cosine
	// distance is a fraction, not 0 or 1.
	corpus := map[string][]float32{
		"alice/a": ramp(dim, 1.0, 0.5),
		"alice/b": ramp(dim, -1.0, 0.25),
		"alice/c": ramp(dim, 0.3, -0.7),
	}
	for id, v := range corpus {
		if err := p.Put(ctx, tenant, id, "1.0.0", v); err != nil {
			t.Fatalf("Put %s: %v", id, err)
		}
	}

	// Query close to alice/a but not identical.
	query := ramp(dim, 1.0, 0.45)
	matches, err := p.Query(ctx, tenant, query, len(corpus))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != len(corpus) {
		t.Fatalf("got %d matches, want %d", len(matches), len(corpus))
	}
	// Recall: the nearest by independent cosine is alice/a, and so must be the
	// top match.
	if matches[0].ArtifactID != "alice/a" {
		t.Errorf("top match = %q, want alice/a", matches[0].ArtifactID)
	}
	// Ordering: distances are non-decreasing.
	for i := 1; i < len(matches); i++ {
		if matches[i].Distance < matches[i-1].Distance {
			t.Errorf("matches not ordered by distance: %+v", matches)
		}
	}
	// Metric correctness: each reported distance matches the independent cosine
	// distance. pgvector computes `<=>` in float32 while the reference here is
	// float64, so the tolerance absorbs float32 rounding (1e-3 on a [0,2] range
	// is still a tight correctness bound, far below any wrong-metric gap).
	for _, m := range matches {
		want := cosineDistance(query, corpus[m.ArtifactID])
		if math.Abs(float64(m.Distance)-want) > 1e-3 {
			t.Errorf("%s: <=> distance %v, independent cosine %v (delta %v)",
				m.ArtifactID, m.Distance, want, math.Abs(float64(m.Distance)-want))
		}
	}
}

// TestPgVector_Depth_ANNIndexRecall creates an HNSW index over the cosine
// operator class and asserts the index path ranks the planted nearest neighbour
// first (ANN path). enable_seqscan is disabled on the session so the
// planner uses the index rather than an exact scan over the small table.
//
// Spec: §4.7 — vector retrieval (the ANN index is a backend implementation
// detail the spec leaves open).
func TestPgVector_Depth_ANNIndexRecall(t *testing.T) {
	const dim = 32
	scope := openPgSchema(t, dim)
	t.Cleanup(scope.close)
	p := scope.pg
	ctx := context.Background()
	tenant := "acme"

	// HNSW needs no training data and indexes the cosine operator class so the
	// `<=>` ORDER BY in PgVector.Query can use it. The index is schema-local, so
	// it is dropped with the schema in cleanup and never touches production DDL.
	if _, err := p.DB().ExecContext(ctx,
		`CREATE INDEX idx_vec_hnsw ON vec_artifacts USING hnsw (embedding vector_cosine_ops)`); err != nil {
		t.Skipf("HNSW index unavailable (needs pgvector >= 0.5.0): %v", err)
	}

	// Plant a clearly-nearest target and a few hundred decoys so the index path
	// is a realistic choice.
	target := ramp(dim, 1.0, 0.1)
	if err := p.Put(ctx, tenant, "alice/target", "1.0.0", target); err != nil {
		t.Fatalf("Put target: %v", err)
	}
	for i := 0; i < 300; i++ {
		// Decoys spread across orthogonal-ish directions, all distinct from the
		// target's ramp.
		v := make([]float32, dim)
		v[i%dim] = 1
		v[(i*7+3)%dim] += 0.5
		if err := p.Put(ctx, tenant, fmt.Sprintf("alice/decoy-%d", i), "1.0.0", v); err != nil {
			t.Fatalf("Put decoy %d: %v", i, err)
		}
	}

	query := ramp(dim, 1.0, 0.09)

	// Force the index path on a dedicated connection so the test genuinely
	// exercises ANN rather than the exact scan the planner would pick for a
	// small table. The settings are connection-scoped, so the recall query runs
	// on this same conn with the same cosine ORDER BY that PgVector.Query uses.
	//
	// enable_seqscan=off alone is not enough: on a small, un-analyzed table the
	// planner falls back to a bitmap index scan on the tenant btree plus an
	// explicit sort. Disabling bitmap scan and sort as well leaves the
	// distance-ordered HNSW index scan as the only penalty-free plan that
	// satisfies ORDER BY <=> ... LIMIT without a sort node. The tenant filter
	// matches every planted row here, so the HNSW post-filter drops nothing and
	// recall@1 is unaffected.
	conn, err := p.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	for _, stmt := range []string{
		`SET enable_seqscan = off`,
		`SET enable_bitmapscan = off`,
		`SET enable_sort = off`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// Confirm the planner chose the HNSW index for the cosine ORDER BY.
	plan, err := explainTopK(ctx, conn, query, tenant, 5)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	if !strings.Contains(plan, "idx_vec_hnsw") {
		t.Errorf("query plan did not use the HNSW index:\n%s", plan)
	}
	// Recall@1 over the index path must be the planted target.
	top, err := topKOnConn(ctx, conn, query, tenant, 5)
	if err != nil {
		t.Fatalf("indexed query: %v", err)
	}
	if top != "alice/target" {
		t.Errorf("ANN recall@1 = %q, want alice/target", top)
	}

	// The public Query path agrees on recall@1 (index present, planner's choice).
	matches, err := p.Query(ctx, tenant, query, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) == 0 || matches[0].ArtifactID != "alice/target" {
		t.Fatalf("public Query recall@1 = %+v, want alice/target first", matches)
	}
}

// explainTopK returns the EXPLAIN text for the cosine top-K query pgvector runs,
// on the supplied connection (so a session-level enable_seqscan=off applies).
func explainTopK(ctx context.Context, conn *sql.Conn, vec []float32, tenant string, topK int) (string, error) {
	rows, err := conn.QueryContext(ctx, `
		EXPLAIN SELECT artifact_id, version, embedding <=> $1::vector AS distance
		FROM vec_artifacts WHERE tenant_id = $2
		ORDER BY embedding <=> $1::vector ASC LIMIT $3`,
		vecLiteralTest(vec), tenant, topK)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String(), rows.Err()
}

// topKOnConn runs the cosine top-K query on the supplied connection and returns
// the nearest artifact id, mirroring PgVector.Query's SQL so the index path is
// exercised under the connection's enable_seqscan=off.
func topKOnConn(ctx context.Context, conn *sql.Conn, vec []float32, tenant string, topK int) (string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT artifact_id, version, embedding <=> $1::vector AS distance
		FROM vec_artifacts WHERE tenant_id = $2
		ORDER BY embedding <=> $1::vector ASC LIMIT $3`,
		vecLiteralTest(vec), tenant, topK)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", fmt.Errorf("no rows")
	}
	var id, ver string
	var dist float64
	if err := rows.Scan(&id, &ver, &dist); err != nil {
		return "", err
	}
	return id, rows.Err()
}

// vecLiteralTest renders a float32 slice as the pgvector text literal the same
// way the production vecLiteral does (pgvector.go); duplicated here because that
// helper is unexported.
func vecLiteralTest(vec []float32) string {
	parts := make([]string, len(vec))
	for i, f := range vec {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// TestPgVector_Depth_ProductionDimensionRoundTrip round-trips a vector at the
// reference production dimension (1536, the text-embedding-3-small size) to
// catch encoding, schema, and large-row regressions that dims 4/8/16/32 do not.
//
// Spec: §4.7 — the standard-deployment default embedder is text-embedding-3-small
// (1536 dim).
func TestPgVector_Depth_ProductionDimensionRoundTrip(t *testing.T) {
	const dim = 1536
	scope := openPgSchema(t, dim)
	t.Cleanup(scope.close)
	p := scope.pg
	ctx := context.Background()
	tenant := "acme"

	if p.Dimensions() != dim {
		t.Fatalf("Dimensions() = %d, want %d", p.Dimensions(), dim)
	}
	// A deterministic dense vector exercises the full 1536-wide literal path.
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(math.Sin(float64(i) * 0.01))
	}
	if err := p.Put(ctx, tenant, "alice/prod", "1.0.0", v); err != nil {
		t.Fatalf("Put 1536-dim: %v", err)
	}
	matches, err := p.Query(ctx, tenant, v, 1)
	if err != nil {
		t.Fatalf("Query 1536-dim: %v", err)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "alice/prod" {
		t.Fatalf("got %+v, want one self-match for alice/prod", matches)
	}
	if matches[0].Distance > 1e-3 {
		t.Errorf("1536-dim self-distance = %v, want ~0", matches[0].Distance)
	}
	// A mismatched dimension is still rejected at this size.
	if err := p.Put(ctx, tenant, "alice/bad", "1.0.0", make([]float32, dim-1)); err == nil {
		t.Error("Put accepted a dimension-mismatched vector at 1536")
	}
}

// TestPgVector_Depth_ModelVersioning replays the §4.7 re-embed-on-model-change
// scenario against real Postgres: PutModel tags rows, QueryModel
// restricts to the current model so a stale model never scores, PurgeModelExcept
// drops the stale rows, and a legacy untagged row stays served. The
// model-versioning suite otherwise covers only memory and sqlite-vec.
//
// Spec: §4.7 — model versioning and re-embedding (query-time restriction to the
// currently-configured model; stale-row purge on completion).
func TestPgVector_Depth_ModelVersioning(t *testing.T) {
	const dim = 4
	scope := openPgSchema(t, dim)
	t.Cleanup(scope.close)
	mv, ok := vector.ModelVersionedOf(scope.pg)
	if !ok {
		t.Fatalf("pgvector does not implement ModelVersioned")
	}
	ctx := context.Background()
	tenant := "acme"

	if err := mv.PutModel(ctx, tenant, "a/1", "1.0.0", vec4(1, 0, 0, 0), "model-A"); err != nil {
		t.Fatalf("PutModel a/1 A: %v", err)
	}
	if err := mv.PutModel(ctx, tenant, "a/2", "1.0.0", vec4(0, 1, 0, 0), "model-A"); err != nil {
		t.Fatalf("PutModel a/2 A: %v", err)
	}
	// model-A restriction sees both A rows.
	got, err := mv.QueryModel(ctx, tenant, vec4(1, 0, 0, 0), 10, "model-A")
	if err != nil {
		t.Fatalf("QueryModel A: %v", err)
	}
	if g := ids(got); !g["a/1"] || !g["a/2"] {
		t.Fatalf("model-A query = %v, want a/1 and a/2", g)
	}

	// Re-embed a/1 under model-B. A model-B restriction must exclude the still-A
	// a/2 so a model switch never scores a stale vector.
	if err := mv.PutModel(ctx, tenant, "a/1", "1.0.0", vec4(1, 0, 0, 0), "model-B"); err != nil {
		t.Fatalf("PutModel a/1 B: %v", err)
	}
	got, err = mv.QueryModel(ctx, tenant, vec4(1, 0, 0, 0), 10, "model-B")
	if err != nil {
		t.Fatalf("QueryModel B: %v", err)
	}
	if g := ids(got); !g["a/1"] || g["a/2"] {
		t.Fatalf("model-B query = %v, want a/1 only (a/2 is stale model-A)", g)
	}

	// Purge everything not on model-B; a/2 (model-A) is removed and the count is 1.
	n, err := mv.PurgeModelExcept(ctx, tenant, "model-B")
	if err != nil {
		t.Fatalf("PurgeModelExcept: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d rows, want 1", n)
	}
	got, _ = mv.QueryModel(ctx, tenant, vec4(0, 1, 0, 0), 10, "model-B")
	if ids(got)["a/2"] {
		t.Error("a/2 survived the purge")
	}

	// A legacy untagged row (plain Put) still surfaces under a model-restricted
	// query so an upgrade with no model change keeps serving.
	if err := scope.pg.Put(ctx, tenant, "legacy/1", "1.0.0", vec4(0, 0, 1, 0)); err != nil {
		t.Fatalf("plain Put legacy/1: %v", err)
	}
	got, err = mv.QueryModel(ctx, tenant, vec4(0, 0, 1, 0), 10, "model-B")
	if err != nil {
		t.Fatalf("QueryModel legacy: %v", err)
	}
	if !ids(got)["legacy/1"] {
		t.Error("legacy untagged row not served under a model-restricted query")
	}
}

// TestPgVector_Depth_PurgeModelExceptTenantScoped asserts PurgeModelExcept is
// scoped per tenant: purging one tenant's stale model never deletes another
// tenant's rows. The pgvector SQL is WHERE tenant_id = $1 AND model_id <> $2
// (pgvector.go), so this is the vec_artifacts cross-tenant isolation invariant
// for the destructive path (pgvector portion).
//
// Spec: §4.7.1 — the org is the tenant boundary; cross-org rows do not mix.
func TestPgVector_Depth_PurgeModelExceptTenantScoped(t *testing.T) {
	const dim = 4
	scope := openPgSchema(t, dim)
	t.Cleanup(scope.close)
	mv, ok := vector.ModelVersionedOf(scope.pg)
	if !ok {
		t.Fatalf("pgvector does not implement ModelVersioned")
	}
	ctx := context.Background()

	// acme holds a stale model-A row; globex holds only model-A rows.
	if err := mv.PutModel(ctx, "acme", "a/stale", "1.0.0", vec4(1, 0, 0, 0), "model-A"); err != nil {
		t.Fatalf("PutModel acme: %v", err)
	}
	if err := mv.PutModel(ctx, "globex", "g/1", "1.0.0", vec4(1, 0, 0, 0), "model-A"); err != nil {
		t.Fatalf("PutModel globex: %v", err)
	}
	// Purge acme's non-model-B rows; this removes acme/a/stale.
	if _, err := mv.PurgeModelExcept(ctx, "acme", "model-B"); err != nil {
		t.Fatalf("PurgeModelExcept acme: %v", err)
	}
	// globex's model-A row must survive: the purge was scoped to acme.
	got, err := mv.QueryModel(ctx, "globex", vec4(1, 0, 0, 0), 10, "model-A")
	if err != nil {
		t.Fatalf("QueryModel globex: %v", err)
	}
	if !ids(got)["g/1"] {
		t.Error("globex row deleted by an acme-scoped purge: tenant isolation breached")
	}
}

// TestPgVector_Depth_TenantIsolation writes the same (artifact_id, version)
// under two tenants with different vectors and asserts a query scoped to one
// tenant never returns the other's row, and a delete in one tenant leaves the
// other intact (pgvector portion). The composite primary key
// (tenant_id, artifact_id, version) lets the identical id coexist across
// tenants, and every read filters WHERE tenant_id = $2 (pgvector.go).
//
// Spec: §4.7.1 — the org is the tenant boundary.
func TestPgVector_Depth_TenantIsolation(t *testing.T) {
	const dim = 8
	scope := openPgSchema(t, dim)
	t.Cleanup(scope.close)
	p := scope.pg
	ctx := context.Background()

	// Same key, different vector per tenant.
	vA := axis(dim, 0)
	vB := axis(dim, 1)
	if err := p.Put(ctx, "acme", "shared/x", "1.0.0", vA); err != nil {
		t.Fatalf("Put acme: %v", err)
	}
	if err := p.Put(ctx, "globex", "shared/x", "1.0.0", vB); err != nil {
		t.Fatalf("Put globex: %v", err)
	}

	// A query in acme returns acme's row at near-zero distance; globex's
	// identical key never crosses the boundary.
	gotA, err := p.Query(ctx, "acme", vA, 10)
	if err != nil {
		t.Fatalf("Query acme: %v", err)
	}
	if len(gotA) != 1 || gotA[0].ArtifactID != "shared/x" {
		t.Fatalf("acme query = %+v, want exactly its own shared/x", gotA)
	}
	if gotA[0].Distance > 1e-3 {
		t.Errorf("acme self-distance = %v, want ~0 (globex's row must not be the match)", gotA[0].Distance)
	}
	gotB, err := p.Query(ctx, "globex", vB, 10)
	if err != nil {
		t.Fatalf("Query globex: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Distance > 1e-3 {
		t.Fatalf("globex query = %+v, want exactly its own shared/x at ~0", gotB)
	}

	// Deleting acme's row leaves globex's identical key intact.
	if err := p.Delete(ctx, "acme", "shared/x", "1.0.0"); err != nil {
		t.Fatalf("Delete acme: %v", err)
	}
	gotA, _ = p.Query(ctx, "acme", vA, 10)
	if len(gotA) != 0 {
		t.Errorf("acme row survived delete: %+v", gotA)
	}
	gotB, _ = p.Query(ctx, "globex", vB, 10)
	if len(gotB) != 1 || gotB[0].ArtifactID != "shared/x" {
		t.Errorf("globex row removed by an acme delete: tenant isolation breached: %+v", gotB)
	}
}

// ramp builds a deterministic non-axis-aligned vector: v[i] = start + i*step.
// Distinct (start, step) pairs give vectors at non-trivial cosine angles.
func ramp(dim int, start, step float32) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = start + float32(i)*step
	}
	return v
}

// axis returns a unit vector aligned with one coordinate, for the isolation
// test where near-zero self-distance is the signal.
func axis(dim, i int) []float32 {
	v := make([]float32, dim)
	if i < dim {
		v[i] = 1
	} else {
		v[0] = 1
	}
	return v
}
