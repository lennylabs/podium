package vector_test

import (
	"os"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// Qdrant Cloud live integration (G-VEC-1, G-VEC-2, G-VEC-4 managed portion,
// G-VEC-5). Gated on PODIUM_LIVE_EXTERNAL=1 plus PODIUM_QDRANT_URL and
// PODIUM_QDRANT_COLLECTION (PODIUM_QDRANT_API_KEY when the cluster requires
// auth). Storage-only requires the collection created sized to liveQdrantDim
// (8) with cosine distance; self-embedding requires PODIUM_QDRANT_INFERENCE_MODEL
// and a collection provisioned for Cloud Inference.
//
// Spec: §13.12 — Qdrant Cloud backend selection and env vars
// (PODIUM_QDRANT_*).

// liveQdrantDim is the dimension the storage-only Qdrant collection must be
// created at. The production-dimension round trip lives in the pgvector depth
// suite (G-PGV-2).
const liveQdrantDim = 8

// liveQdrantStorage builds a storage-only Qdrant backend, or skips when the
// switch or credentials are absent.
func liveQdrantStorage(t *testing.T) *vector.Qdrant {
	t.Helper()
	requireLiveExternal(t)
	url := os.Getenv("PODIUM_QDRANT_URL")
	collection := os.Getenv("PODIUM_QDRANT_COLLECTION")
	if url == "" || collection == "" {
		t.Skip("PODIUM_QDRANT_URL/COLLECTION unset; skipping live Qdrant")
	}
	q, err := vector.NewQdrant(vector.QdrantConfig{
		URL:        url,
		APIKey:     os.Getenv("PODIUM_QDRANT_API_KEY"),
		Collection: collection,
		Dimensions: liveQdrantDim,
	})
	if err != nil {
		t.Fatalf("NewQdrant (storage-only): %v", err)
	}
	if vector.SelfEmbeds(q) {
		t.Fatalf("storage-only Qdrant reports SelfEmbeds() true")
	}
	return q
}

// liveQdrantSelfEmbed builds a self-embedding Qdrant backend, or skips when the
// inference model or credentials are absent.
func liveQdrantSelfEmbed(t *testing.T) *vector.Qdrant {
	t.Helper()
	requireLiveExternal(t)
	url := os.Getenv("PODIUM_QDRANT_URL")
	collection := os.Getenv("PODIUM_QDRANT_COLLECTION")
	model := os.Getenv("PODIUM_QDRANT_INFERENCE_MODEL")
	if url == "" || collection == "" || model == "" {
		t.Skip("PODIUM_QDRANT_URL/COLLECTION/INFERENCE_MODEL unset; skipping self-embedding Qdrant")
	}
	q, err := vector.NewQdrant(vector.QdrantConfig{
		URL:            url,
		APIKey:         os.Getenv("PODIUM_QDRANT_API_KEY"),
		Collection:     collection,
		InferenceModel: model,
	})
	if err != nil {
		t.Fatalf("NewQdrant (self-embed): %v", err)
	}
	if !vector.SelfEmbeds(q) {
		t.Fatalf("self-embedding Qdrant reports SelfEmbeds() false")
	}
	return q
}

// TestQdrant_Live_Conformance runs the shared SPI contract against the live
// collection (G-VEC-2).
//
// Spec: §4.7 — RegistrySearchProvider conformance.
func TestQdrant_Live_Conformance(t *testing.T) {
	q := liveQdrantStorage(t)
	t.Cleanup(func() { _ = q.Close() })
	runLiveSuite(t, liveQdrantDim, q)
}

// TestQdrant_Live_StorageOnly covers the precomputed-vector path (G-VEC-1,
// G-VEC-5): ingest, nearest-neighbour recall, upsert replace, delete remove.
// Qdrant writes carry ?wait=true, so reads are consistent immediately, but the
// poll helpers tolerate that and any future async behaviour.
//
// Spec: §4.7 — storage-only backend (registry computes the vector).
func TestQdrant_Live_StorageOnly(t *testing.T) {
	q := liveQdrantStorage(t)
	t.Cleanup(func() { _ = q.Close() })
	ctx := liveBackgroundContext()
	emb := charEmbedder{dim: liveQdrantDim}
	tenant := liveTenantPrefix + "_alice"
	corpus := liveCorpus()
	t.Cleanup(func() {
		for _, a := range corpus {
			_ = q.Delete(ctx, tenant, a.id, a.version)
		}
	})
	for _, a := range corpus {
		if err := q.Put(ctx, tenant, a.id, a.version, emb.embed(a.text)); err != nil {
			t.Fatalf("Put %s: %v", a.id, err)
		}
	}
	if err := waitUntilQueryable(t, q, tenant, emb.embed(queryFor("alice/payments")), "alice/payments"); err != nil {
		t.Fatalf("ingest never became queryable: %v", err)
	}
	matches, err := q.Query(ctx, tenant, emb.embed(queryFor("alice/payments")), 3)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	top, ok := topMatch(matches)
	if !ok || top.ArtifactID != "alice/payments" {
		t.Fatalf("nearest neighbour = %+v, want alice/payments first", matches)
	}

	// Upsert replaces: re-Put alice/payments with the shipping vector.
	if err := q.Put(ctx, tenant, "alice/payments", "1.0.0", emb.embed(queryFor("alice/shipping"))); err != nil {
		t.Fatalf("upsert Put: %v", err)
	}
	if err := waitUntilQueryable(t, q, tenant, emb.embed(queryFor("alice/shipping")), "alice/payments"); err != nil {
		t.Fatalf("upsert never landed: %v", err)
	}

	// Delete removes alice/weather.
	if err := q.Delete(ctx, tenant, "alice/weather", "1.0.0"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := waitUntilAbsent(t, q, tenant, emb.embed(queryFor("alice/weather")), "alice/weather"); err != nil {
		t.Fatalf("delete never removed the vector: %v", err)
	}
}

// TestQdrant_Live_TenantIsolation writes two tenants' vectors into the one
// collection and asserts a query scoped to one tenant never returns the other's
// (G-VEC-4). Qdrant isolates per tenant by the tenant_id payload filter.
//
// Spec: §4.7.1 — the org is the tenant boundary.
func TestQdrant_Live_TenantIsolation(t *testing.T) {
	q := liveQdrantStorage(t)
	t.Cleanup(func() { _ = q.Close() })
	ctx := liveBackgroundContext()
	emb := charEmbedder{dim: liveQdrantDim}
	tenantA := liveTenantPrefix + "_alice"
	tenantB := liveTenantPrefix + "_bob"
	// Distinct ids and identical vectors: a tenant-A query would surface the
	// leaked tenant-B row if the payload filter were absent. The point id is
	// derived from (tenant, id, version), so distinct tenants never collide on
	// the same Qdrant point even with a shared (id, version).
	artA := liveArtifact{id: "alice/onlyA", version: "1.0.0", text: liveCorpus()[0].text}
	artB := liveArtifact{id: "bob/onlyB", version: "1.0.0", text: liveCorpus()[0].text}
	t.Cleanup(func() {
		_ = q.Delete(ctx, tenantA, artA.id, artA.version)
		_ = q.Delete(ctx, tenantB, artB.id, artB.version)
	})
	if err := q.Put(ctx, tenantA, artA.id, artA.version, emb.embed(artA.text)); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if err := q.Put(ctx, tenantB, artB.id, artB.version, emb.embed(artB.text)); err != nil {
		t.Fatalf("Put B: %v", err)
	}
	if err := waitUntilQueryable(t, q, tenantA, emb.embed(artA.text), artA.id); err != nil {
		t.Fatalf("tenant A never became queryable: %v", err)
	}
	if err := waitUntilQueryable(t, q, tenantB, emb.embed(artB.text), artB.id); err != nil {
		t.Fatalf("tenant B never became queryable: %v", err)
	}
	gotA, err := q.Query(ctx, tenantA, emb.embed(artA.text), 20)
	if err != nil {
		t.Fatalf("Query A: %v", err)
	}
	if containsArtifact(gotA, artB.id) {
		t.Fatalf("tenant A query leaked tenant B row %q: %+v", artB.id, gotA)
	}
	gotB, err := q.Query(ctx, tenantB, emb.embed(artB.text), 20)
	if err != nil {
		t.Fatalf("Query B: %v", err)
	}
	if containsArtifact(gotB, artA.id) {
		t.Fatalf("tenant B query leaked tenant A row %q: %+v", artA.id, gotB)
	}
}

// TestQdrant_Live_SelfEmbedding covers the server-side embedding path
// (G-VEC-5): drive PutText/QueryText against Cloud Inference.
//
// Spec: §13.12 — PODIUM_QDRANT_INFERENCE_MODEL self-embedding.
func TestQdrant_Live_SelfEmbedding(t *testing.T) {
	q := liveQdrantSelfEmbed(t)
	t.Cleanup(func() { _ = q.Close() })
	ctx := liveBackgroundContext()
	tenant := liveTenantPrefix + "_se_alice"
	corpus := liveCorpus()
	t.Cleanup(func() {
		for _, a := range corpus {
			_ = q.Delete(ctx, tenant, a.id, a.version)
		}
	})
	for _, a := range corpus {
		if err := q.PutText(ctx, tenant, a.id, a.version, a.text); err != nil {
			t.Fatalf("PutText %s: %v", a.id, err)
		}
	}
	if err := waitUntilTextQueryable(t, q, tenant, queryFor("alice/payments"), "alice/payments"); err != nil {
		t.Fatalf("self-embed ingest never became queryable: %v", err)
	}
	matches, err := q.QueryText(ctx, tenant, queryFor("alice/payments"), 3)
	if err != nil {
		t.Fatalf("QueryText: %v", err)
	}
	top, ok := topMatch(matches)
	if !ok || top.ArtifactID != "alice/payments" {
		t.Fatalf("self-embed nearest neighbour = %+v, want alice/payments first", matches)
	}
}
