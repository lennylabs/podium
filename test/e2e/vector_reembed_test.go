package e2e

// End-to-end admin-reembed journey after a configured embedding-model switch,
// closing G-VEC-10 (TEST-GAPS.md). pgvector_depth_test.go exercises PutModel /
// QueryModel / PurgeModelExcept directly, and extra_handlers_test.go drives the
// /v1/admin/reembed handler past validation, but no test drives the full flow:
// ingest at model A on pgvector, switch the configured model to B, POST
// /v1/admin/reembed, and assert the vectors re-tag to B, the model-A vectors are
// purged tenant-scoped, semantic search returns the expected top result, and the
// manifest content hashes are unchanged.
//
// The model identity is the embedder's Model() string, set per server boot via
// PODIUM_OPENAI_MODEL. Both boots point at the same isolated pgvector schema so
// the second boot sees the model-A rows the first wrote. The mock embedder
// returns the same topic-routed vector for either model (the vector content is
// irrelevant to the model-tag mechanics), so QueryModel's model restriction is
// what determines recall: model-A rows are invisible under a model-B query until
// the reembed re-tags them.

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// pgVectorNamedSchema creates a uniquely-named Postgres schema and returns its
// search-path DSN, an open admin DB handle for direct row inspection, and the
// schema name. The schema is dropped and the handle closed in cleanup. It skips
// the test when Postgres is unreachable. Unlike pgVectorIsolatedSchema it hands
// back the DB handle and schema so the test can assert on model_id rows
// directly, which the reembed purge assertions need.
func pgVectorNamedSchema(t *testing.T, dsn string) (scopedDSN string, db *sql.DB, schema string) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("pgvector DSN unusable (%v); skipping pgvector reembed e2e", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Skipf("Postgres unreachable (%v); skipping pgvector reembed e2e", err)
	}
	schema = "podium_e2e_reembed_" + randHex(8)
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = db.Close()
		t.Skipf("create schema (%v); skipping pgvector reembed e2e", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = db.Close()
	})
	return pgWithSearchPath(dsn, schema), db, schema
}

// modelCounts returns how many vec_artifacts rows in the schema carry each
// model_id, scoped to the registry's own tenant (excluding the seeded globex
// tenant) and excluding the reserved domain-projection version so only the
// default tenant's artifact vectors are counted. The default standalone tenant
// id is a UUID (§4.7.1), so the count excludes globex by name rather than
// filtering to a known id.
func modelCounts(t *testing.T, db *sql.DB, schema string) map[string]int {
	t.Helper()
	rows, err := db.Query(`SELECT model_id, count(*) FROM ` + schema + `.vec_artifacts WHERE version <> '@domain' AND tenant_id <> 'globex' GROUP BY model_id`)
	if err != nil {
		t.Fatalf("count vec_artifacts by model: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var model string
		var n int
		if err := rows.Scan(&model, &n); err != nil {
			t.Fatalf("scan model count: %v", err)
		}
		out[model] = n
	}
	return out
}

// loadContentHash fetches the content_hash for an artifact id through the HTTP
// load_artifact surface.
func loadContentHash(t *testing.T, baseURL, id string) string {
	t.Helper()
	var resp struct {
		ID          string `json:"id"`
		ContentHash string `json:"content_hash"`
	}
	getJSON(t, baseURL+"/v1/load_artifact?id="+queryEscape(id), &resp)
	if resp.ContentHash == "" {
		t.Fatalf("load_artifact(%q) returned empty content_hash", id)
	}
	return resp.ContentHash
}

// TestVectorReembed_ModelSwitchRetagsAndPurges closes G-VEC-10. It ingests a
// corpus on pgvector with embedder model A, asserts the rows are model-A-tagged,
// reboots the server with model B over the same schema, POSTs
// /v1/admin/reembed, and asserts every artifact vector now carries model B, no
// model-A vector survives, semantic search returns the expected artifact at rank
// 1 under the model-B restriction, and the manifest content hashes are unchanged
// across the re-embed.
//
// Gating: PODIUM_POSTGRES_DSN[_VECTOR]. With no DSN the test skips cleanly.
//
// Spec: §4.7 (model versioning and re-embedding: the vector store records the
// model per artifact, a model switch triggers a background re-embed via podium
// admin reembed, query-time results restrict to the current model, and stale
// rows are purged once re-embedding completes).
func TestVectorReembed_ModelSwitchRetagsAndPurges(t *testing.T) {
	t.Parallel()
	dsn := firstEnv("PODIUM_POSTGRES_DSN_VECTOR", "PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN[_VECTOR] unset; skipping pgvector reembed e2e")
	}
	scopedDSN, db, schema := pgVectorNamedSchema(t, dsn)
	reg := paraphraseRegistry(t)
	const modelA = "podium-test-model-a"
	const modelB = "podium-test-model-b"

	// Both boots share one HOME so the standalone SQLite metadata store persists
	// across the restart. With a persistent store, boot B's bootstrap re-ingest
	// is idempotent (same content hashes), so it does not re-embed and the rows
	// stay model-A until the admin reembed runs. A fresh HOME per boot would make
	// boot B a full re-ingest that re-embeds at model B and overwrites the rows,
	// hiding the stale-model state the reembed is meant to resolve.
	sharedHome := t.TempDir()

	// Boot 1: model A. The collocated pgvector path embeds each artifact at
	// ingest and tags the rows with model A.
	embA := newControllableEmbedder(t)
	srvA := startServerArgs(t, []string{
		"HOME=" + sharedHome,
		"PODIUM_VECTOR_BACKEND=pgvector",
		"PODIUM_PGVECTOR_DSN=" + scopedDSN,
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"PODIUM_OPENAI_MODEL=" + modelA,
		"OPENAI_API_KEY=sk-test",
		"PODIUM_OPENAI_BASE_URL=" + embA.url(),
	}, "serve", "--standalone", "--layer-path", reg)
	if log := srvA.log(); !strings.Contains(log, "vector=pgvector") || strings.Contains(log, "vector search disabled") {
		t.Fatalf("boot A did not wire pgvector:\n%s", log)
	}

	countsA := modelCounts(t, db, schema)
	if countsA[modelA] == 0 {
		t.Fatalf("after boot A no rows carry model %q; collocated ingest did not embed at the configured model\ncounts: %v", modelA, countsA)
	}
	if n := countsA[modelB]; n != 0 {
		t.Fatalf("model %q rows present before the switch: %d", modelB, n)
	}
	// Capture content hashes before the re-embed so the unchanged assertion has a
	// baseline. Content hash is independent of the embedding model.
	hashBefore := map[string]string{}
	for _, id := range []string{hybridTarget, hybridLexicalAnchor, "hr/onboarding", "infra/deploy"} {
		hashBefore[id] = loadContentHash(t, srvA.BaseURL, id)
	}

	// Stop boot A so its drain/probe goroutines and the (idempotent) layer stay
	// out of the way; the schema persists for boot B.
	stopProc(srvA.cmd)

	// Boot 2: model B over the same schema. Bootstrap re-ingest is idempotent
	// (same content hashes), so it does not re-embed; the rows are still model-A
	// until the admin reembed runs.
	embB := newControllableEmbedder(t)
	srvB := startServerArgs(t, []string{
		"HOME=" + sharedHome,
		"PODIUM_VECTOR_BACKEND=pgvector",
		"PODIUM_PGVECTOR_DSN=" + scopedDSN,
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"PODIUM_OPENAI_MODEL=" + modelB,
		"OPENAI_API_KEY=sk-test",
		"PODIUM_OPENAI_BASE_URL=" + embB.url(),
	}, "serve", "--standalone", "--layer-path", reg)
	if log := srvB.log(); !strings.Contains(log, "vector=pgvector") || strings.Contains(log, "vector search disabled") {
		t.Fatalf("boot B did not wire pgvector:\n%s", log)
	}

	// Before reembed: the rows are still model-A, so a model-B-restricted search
	// cannot see them. This is the stale state the reembed resolves.
	if c := modelCounts(t, db, schema); c[modelB] != 0 {
		t.Fatalf("model %q rows present before reembed: %v (idempotent re-ingest must not re-embed)", modelB, c)
	}

	// Seed a second tenant's stale-model row directly so the purge's tenant
	// scoping is observable: the reembed runs in the default tenant and must not
	// delete globex's rows.
	if _, err := db.Exec(`INSERT INTO `+schema+`.vec_artifacts (tenant_id, artifact_id, version, embedding, model_id)
		VALUES ('globex', 'g/keep', '1.0.0', $1::vector, $2)`, zeroVectorLiteral(semanticDim), modelA); err != nil {
		t.Fatalf("seed globex row: %v", err)
	}

	// Run the admin reembed against the model-B server. A full pass re-embeds and
	// re-tags every visible artifact at model B, then purges the stale model-A
	// rows in this tenant.
	res := runPodium(t, "", nil, "admin", "reembed", "--registry", srvB.BaseURL)
	if res.Exit != 0 {
		t.Fatalf("admin reembed exit=%d, want 0\nstdout:%s\nstderr:%s", res.Exit, res.Stdout, res.Stderr)
	}

	// After reembed: every artifact vector carries model B and no model-A
	// artifact row survives in the default tenant.
	countsB := modelCounts(t, db, schema)
	if countsB[modelB] == 0 {
		t.Fatalf("after reembed no rows carry model %q\ncounts: %v", modelB, countsB)
	}
	if countsB[modelA] != 0 {
		t.Errorf("model %q artifact rows survived the reembed purge: %d\ncounts: %v", modelA, countsB[modelA], countsB)
	}

	// Tenant scoping: globex's stale model-A row is untouched by the default
	// tenant's purge.
	var globexRows int
	if err := db.QueryRow(`SELECT count(*) FROM ` + schema + `.vec_artifacts WHERE tenant_id = 'globex'`).Scan(&globexRows); err != nil {
		t.Fatalf("count globex rows: %v", err)
	}
	if globexRows != 1 {
		t.Errorf("globex rows = %d, want 1 (the default-tenant purge must be tenant-scoped)", globexRows)
	}

	// Semantic search under the model-B server returns the expected artifact at
	// rank 1: the model-B rows are now present and the query routes to the
	// concept-A target.
	ids, resp := searchIDs(t, srvB.BaseURL, hybridQuery)
	if len(ids) == 0 || ids[0] != hybridTarget {
		t.Fatalf("after reembed search top result = %v, want %q\nresults: %+v", ids, hybridTarget, resp.Results)
	}

	// Content hashes are unchanged across the re-embed: re-embedding re-tags
	// vectors and never rewrites the manifest content store (§4.7 immutability).
	for id, before := range hashBefore {
		after := loadContentHash(t, srvB.BaseURL, id)
		if after != before {
			t.Errorf("content_hash for %q changed across reembed: before=%s after=%s", id, before, after)
		}
	}
}

// zeroVectorLiteral renders a pgvector text literal of n zeros, for seeding a
// raw vec_artifacts row whose vector content does not matter to the assertion.
func zeroVectorLiteral(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "0"
	}
	return "[" + strings.Join(parts, ",") + "]"
}
