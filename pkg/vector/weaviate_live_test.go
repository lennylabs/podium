package vector_test

import (
	"os"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// Weaviate Cloud live integration (G-VEC-1, G-VEC-2, G-VEC-4 managed portion,
// G-VEC-5). Gated on PODIUM_LIVE_EXTERNAL=1 plus PODIUM_WEAVIATE_URL and
// PODIUM_WEAVIATE_COLLECTION (PODIUM_WEAVIATE_API_KEY when the cluster requires
// auth). The collection must already exist with the properties the backend
// writes: tenantId, artifactId, version (and content for the self-embedding
// path). Storage-only requires the class created at liveWeaviateDim (8) with no
// vectorizer; self-embedding requires PODIUM_WEAVIATE_VECTORIZER and a class
// configured with that module.
//
// Spec: §13.12 — Weaviate Cloud backend selection and env vars
// (PODIUM_WEAVIATE_*).

// liveWeaviateDim is the dimension the storage-only Weaviate class must be
// created at. It matches the managed semantic-search e2e (1536) so one storage
// class serves both that e2e and these conformance suites; the deterministic
// char embedder and vectortest.Suite are dimension-agnostic.
const liveWeaviateDim = 1536

// liveWeaviateStorage builds a storage-only Weaviate backend, or skips when the
// switch or credentials are absent.
func liveWeaviateStorage(t *testing.T) *vector.Weaviate {
	t.Helper()
	requireLiveExternal(t)
	url := os.Getenv("PODIUM_WEAVIATE_URL")
	collection := os.Getenv("PODIUM_WEAVIATE_COLLECTION")
	if url == "" || collection == "" {
		t.Skip("PODIUM_WEAVIATE_URL/COLLECTION unset; skipping live Weaviate")
	}
	w, err := vector.NewWeaviate(vector.WeaviateConfig{
		URL:        url,
		APIKey:     os.Getenv("PODIUM_WEAVIATE_API_KEY"),
		Collection: collection,
		Dimensions: liveWeaviateDim,
	})
	if err != nil {
		t.Fatalf("NewWeaviate (storage-only): %v", err)
	}
	if vector.SelfEmbeds(w) {
		t.Fatalf("storage-only Weaviate reports SelfEmbeds() true")
	}
	return w
}

// liveWeaviateSelfEmbed builds a self-embedding Weaviate backend, or skips when
// the vectorizer, credentials, or the dedicated self-embedding collection are
// absent. It uses PODIUM_WEAVIATE_SELFEMBED_COLLECTION, a class configured with
// the vectorizer module, separate from the storage class that has none.
func liveWeaviateSelfEmbed(t *testing.T) *vector.Weaviate {
	t.Helper()
	requireLiveExternal(t)
	url := os.Getenv("PODIUM_WEAVIATE_URL")
	collection := os.Getenv("PODIUM_WEAVIATE_SELFEMBED_COLLECTION")
	vectorizer := os.Getenv("PODIUM_WEAVIATE_VECTORIZER")
	if url == "" || collection == "" || vectorizer == "" {
		t.Skip("PODIUM_WEAVIATE_URL/SELFEMBED_COLLECTION/VECTORIZER unset; skipping self-embedding Weaviate")
	}
	w, err := vector.NewWeaviate(vector.WeaviateConfig{
		URL:        url,
		APIKey:     os.Getenv("PODIUM_WEAVIATE_API_KEY"),
		Collection: collection,
		Vectorizer: vectorizer,
	})
	if err != nil {
		t.Fatalf("NewWeaviate (self-embed): %v", err)
	}
	if !vector.SelfEmbeds(w) {
		t.Fatalf("self-embedding Weaviate reports SelfEmbeds() false")
	}
	return w
}

// TestWeaviate_Live_Conformance runs the shared SPI contract against the live
// collection (G-VEC-2).
//
// Spec: §4.7 — RegistrySearchProvider conformance.
func TestWeaviate_Live_Conformance(t *testing.T) {
	w := liveWeaviateStorage(t)
	t.Cleanup(func() { _ = w.Close() })
	runLiveSuite(t, liveWeaviateDim, w)
}

// TestWeaviate_Live_StorageOnly covers the precomputed-vector path (G-VEC-1,
// G-VEC-5): ingest, nearest-neighbour recall, upsert replace, delete remove.
//
// Spec: §4.7 — storage-only backend (registry computes the vector).
func TestWeaviate_Live_StorageOnly(t *testing.T) {
	w := liveWeaviateStorage(t)
	t.Cleanup(func() { _ = w.Close() })
	ctx := liveBackgroundContext()
	emb := charEmbedder{dim: liveWeaviateDim}
	tenant := liveTenantPrefix + "_alice"
	corpus := liveCorpus()
	t.Cleanup(func() {
		for _, a := range corpus {
			_ = w.Delete(ctx, tenant, a.id, a.version)
		}
	})
	for _, a := range corpus {
		if err := w.Put(ctx, tenant, a.id, a.version, emb.embed(a.text)); err != nil {
			t.Fatalf("Put %s: %v", a.id, err)
		}
	}
	if err := waitUntilQueryable(t, w, tenant, emb.embed(queryFor("alice/payments")), "alice/payments"); err != nil {
		t.Fatalf("ingest never became queryable: %v", err)
	}
	matches, err := w.Query(ctx, tenant, emb.embed(queryFor("alice/payments")), 3)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	top, ok := topMatch(matches)
	if !ok || top.ArtifactID != "alice/payments" {
		t.Fatalf("nearest neighbour = %+v, want alice/payments first", matches)
	}

	// Upsert replaces: re-Put alice/payments with the shipping vector.
	if err := w.Put(ctx, tenant, "alice/payments", "1.0.0", emb.embed(queryFor("alice/shipping"))); err != nil {
		t.Fatalf("upsert Put: %v", err)
	}
	if err := waitUntilQueryable(t, w, tenant, emb.embed(queryFor("alice/shipping")), "alice/payments"); err != nil {
		t.Fatalf("upsert never landed: %v", err)
	}

	// Delete removes alice/weather.
	if err := w.Delete(ctx, tenant, "alice/weather", "1.0.0"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := waitUntilAbsent(t, w, tenant, emb.embed(queryFor("alice/weather")), "alice/weather"); err != nil {
		t.Fatalf("delete never removed the vector: %v", err)
	}
}

// TestWeaviate_Live_TenantIsolation writes two tenants' vectors into the one
// collection and asserts a query scoped to one tenant never returns the other's
// (G-VEC-4). Weaviate isolates per tenant by the tenantId property filter.
//
// Spec: §4.7.1 — the org is the tenant boundary.
func TestWeaviate_Live_TenantIsolation(t *testing.T) {
	w := liveWeaviateStorage(t)
	t.Cleanup(func() { _ = w.Close() })
	ctx := liveBackgroundContext()
	emb := charEmbedder{dim: liveWeaviateDim}
	tenantA := liveTenantPrefix + "_alice"
	tenantB := liveTenantPrefix + "_bob"
	// Distinct ids so the cross-tenant leak is visible by artifact id, not only
	// by version. The vectors are identical so a tenant-A query would surface a
	// leaked tenant-B row if the property filter were absent.
	artA := liveArtifact{id: "alice/onlyA", version: "1.0.0", text: liveCorpus()[0].text}
	artB := liveArtifact{id: "bob/onlyB", version: "1.0.0", text: liveCorpus()[0].text}
	t.Cleanup(func() {
		_ = w.Delete(ctx, tenantA, artA.id, artA.version)
		_ = w.Delete(ctx, tenantB, artB.id, artB.version)
	})
	if err := w.Put(ctx, tenantA, artA.id, artA.version, emb.embed(artA.text)); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if err := w.Put(ctx, tenantB, artB.id, artB.version, emb.embed(artB.text)); err != nil {
		t.Fatalf("Put B: %v", err)
	}
	if err := waitUntilQueryable(t, w, tenantA, emb.embed(artA.text), artA.id); err != nil {
		t.Fatalf("tenant A never became queryable: %v", err)
	}
	if err := waitUntilQueryable(t, w, tenantB, emb.embed(artB.text), artB.id); err != nil {
		t.Fatalf("tenant B never became queryable: %v", err)
	}
	// A tenant-A query must never surface tenant B's row, and vice versa.
	gotA, err := w.Query(ctx, tenantA, emb.embed(artA.text), 20)
	if err != nil {
		t.Fatalf("Query A: %v", err)
	}
	if containsArtifact(gotA, artB.id) {
		t.Fatalf("tenant A query leaked tenant B row %q: %+v", artB.id, gotA)
	}
	gotB, err := w.Query(ctx, tenantB, emb.embed(artB.text), 20)
	if err != nil {
		t.Fatalf("Query B: %v", err)
	}
	if containsArtifact(gotB, artA.id) {
		t.Fatalf("tenant B query leaked tenant A row %q: %+v", artA.id, gotB)
	}
}

// TestWeaviate_Live_SelfEmbedding covers the server-side embedding path
// (G-VEC-5): drive PutText/QueryText against the vectorizer module.
//
// Spec: §13.12 — PODIUM_WEAVIATE_VECTORIZER self-embedding.
func TestWeaviate_Live_SelfEmbedding(t *testing.T) {
	w := liveWeaviateSelfEmbed(t)
	t.Cleanup(func() { _ = w.Close() })
	ctx := liveBackgroundContext()
	tenant := liveTenantPrefix + "_se_alice"
	corpus := liveCorpus()
	t.Cleanup(func() {
		for _, a := range corpus {
			_ = w.Delete(ctx, tenant, a.id, a.version)
		}
	})
	for _, a := range corpus {
		if err := w.PutText(ctx, tenant, a.id, a.version, a.text); err != nil {
			t.Fatalf("PutText %s: %v", a.id, err)
		}
	}
	if err := waitUntilTextQueryable(t, w, tenant, queryFor("alice/shipping"), "alice/shipping"); err != nil {
		t.Fatalf("self-embed ingest never became queryable: %v", err)
	}
	matches, err := w.QueryText(ctx, tenant, queryFor("alice/shipping"), 3)
	if err != nil {
		t.Fatalf("QueryText: %v", err)
	}
	top, ok := topMatch(matches)
	if !ok || top.ArtifactID != "alice/shipping" {
		t.Fatalf("self-embed nearest neighbour = %+v, want alice/shipping first", matches)
	}
}
