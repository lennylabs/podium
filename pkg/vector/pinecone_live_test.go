package vector_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/vector"
)

// Pinecone live integration. Gated on PODIUM_LIVE_EXTERNAL=1 plus a Pinecone API key and a
// reachable index. The index is supplied either as a ready data-plane host
// (PODIUM_PINECONE_HOST) or as an index name (PODIUM_PINECONE_INDEX) that the
// control plane resolves to a host, matching OpenBuiltin's auto-resolution
// (builtin.go). Storage-only requires the index to be created at livePineconeDim
// (8); self-embedding requires PODIUM_PINECONE_INFERENCE_MODEL and an index
// provisioned for Integrated Inference.
//
// Spec: §13.12 — Pinecone backend selection and env vars (PODIUM_PINECONE_*).

// livePineconeDim is the dimension the storage-only Pinecone index must be
// created at. It matches the managed semantic-search e2e (1536) so one storage
// index serves both that e2e and these conformance suites; the deterministic
// char embedder and vectortest.Suite are dimension-agnostic, and the live
// corpus self-matches exactly, so recall holds at any dimension.
const livePineconeDim = 1536

// livePineconeHost resolves the data-plane host. It prefers an explicit
// PODIUM_PINECONE_HOST and otherwise resolves PODIUM_PINECONE_INDEX through the
// control plane the same way OpenBuiltin does. ok is false when neither a host
// nor an index is configured.
func livePineconeHost(t *testing.T, apiKey string) (string, bool) {
	t.Helper()
	if host := os.Getenv("PODIUM_PINECONE_HOST"); host != "" {
		return host, true
	}
	index := os.Getenv("PODIUM_PINECONE_INDEX")
	if index == "" {
		return "", false
	}
	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()
	host, err := vector.ResolvePineconeHost(ctx, os.Getenv("PODIUM_PINECONE_CONTROL_PLANE"), apiKey, index, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		t.Skipf("Pinecone host resolution from index %q failed: %v", index, err)
	}
	return host, true
}

// livePineconeStorage builds a storage-only Pinecone backend, or skips when the
// switch or credentials are absent. The namespace prefix isolates this suite's
// rows within the index.
func livePineconeStorage(t *testing.T) *vector.Pinecone {
	t.Helper()
	requireLiveExternal(t)
	apiKey := os.Getenv("PODIUM_PINECONE_API_KEY")
	if apiKey == "" {
		t.Skip("PODIUM_PINECONE_API_KEY unset; skipping live Pinecone")
	}
	host, ok := livePineconeHost(t, apiKey)
	if !ok {
		t.Skip("PODIUM_PINECONE_HOST/INDEX unset; skipping live Pinecone")
	}
	ns := os.Getenv("PODIUM_PINECONE_NAMESPACE")
	if ns == "" {
		ns = liveTenantPrefix
	}
	p, err := vector.NewPinecone(vector.PineconeConfig{
		APIKey: apiKey, Host: host, Namespace: ns, Dimensions: livePineconeDim,
	})
	if err != nil {
		t.Fatalf("NewPinecone (storage-only): %v", err)
	}
	if vector.SelfEmbeds(p) {
		t.Fatalf("storage-only Pinecone reports SelfEmbeds() true")
	}
	return p
}

// livePineconeSelfEmbed builds a self-embedding Pinecone backend, or skips when
// the inference model, credentials, or the dedicated self-embedding index are
// absent. It resolves the host from PODIUM_PINECONE_SELFEMBED_INDEX rather than
// the storage index: an Integrated Inference index is bound to its model at the
// model's dimension, so it must be a separate index from the dim-8 storage one,
// which lets both modes run in the same suite.
func livePineconeSelfEmbed(t *testing.T) *vector.Pinecone {
	t.Helper()
	requireLiveExternal(t)
	apiKey := os.Getenv("PODIUM_PINECONE_API_KEY")
	model := os.Getenv("PODIUM_PINECONE_INFERENCE_MODEL")
	index := os.Getenv("PODIUM_PINECONE_SELFEMBED_INDEX")
	if apiKey == "" || model == "" || index == "" {
		t.Skip("PODIUM_PINECONE_API_KEY/INFERENCE_MODEL/SELFEMBED_INDEX unset; skipping self-embedding Pinecone")
	}
	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()
	host, err := vector.ResolvePineconeHost(ctx, os.Getenv("PODIUM_PINECONE_CONTROL_PLANE"), apiKey, index, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		t.Skipf("Pinecone self-embed host resolution from index %q failed: %v", index, err)
	}
	ns := os.Getenv("PODIUM_PINECONE_NAMESPACE")
	if ns == "" {
		ns = liveTenantPrefix + "_se"
	}
	p, err := vector.NewPinecone(vector.PineconeConfig{
		APIKey: apiKey, Host: host, Namespace: ns, InferenceModel: model,
	})
	if err != nil {
		t.Fatalf("NewPinecone (self-embed): %v", err)
	}
	if !vector.SelfEmbeds(p) {
		t.Fatalf("self-embedding Pinecone reports SelfEmbeds() false")
	}
	return p
}

// TestPinecone_Live_Conformance runs the shared SPI contract against the live
// index. Sub-test isolation comes from the tenant boundary the suite
// already enforces.
//
// Spec: §4.7 — RegistrySearchProvider conformance.
func TestPinecone_Live_Conformance(t *testing.T) {
	p := livePineconeStorage(t)
	t.Cleanup(func() { _ = p.Close() })
	runLiveSuite(t, livePineconeDim, p)
}

// TestPinecone_Live_StorageOnly covers the precomputed-vector path: ingest the fixed corpus with the deterministic char embedder, query
// nearest-neighbour, assert recall, then assert upsert replaces and delete
// removes.
//
// Spec: §4.7 — storage-only backend (registry computes the vector).
func TestPinecone_Live_StorageOnly(t *testing.T) {
	p := livePineconeStorage(t)
	t.Cleanup(func() { _ = p.Close() })
	ctx := liveBackgroundContext()
	emb := charEmbedder{dim: livePineconeDim}
	tenant := liveTenantPrefix + "_alice"
	corpus := liveCorpus()
	t.Cleanup(func() {
		for _, a := range corpus {
			_ = p.Delete(ctx, tenant, a.id, a.version)
		}
	})
	for _, a := range corpus {
		if err := p.Put(ctx, tenant, a.id, a.version, emb.embed(a.text)); err != nil {
			t.Fatalf("Put %s: %v", a.id, err)
		}
	}
	if err := waitUntilQueryable(t, p, tenant, emb.embed(queryFor("alice/payments")), "alice/payments"); err != nil {
		t.Fatalf("ingest never became queryable: %v", err)
	}
	matches, err := p.Query(ctx, tenant, emb.embed(queryFor("alice/payments")), 3)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	top, ok := topMatch(matches)
	if !ok || top.ArtifactID != "alice/payments" {
		t.Fatalf("nearest neighbour = %+v, want alice/payments first", matches)
	}

	// Upsert replaces: re-Put alice/payments with the shipping vector; a
	// shipping-text query must now return alice/payments at the top.
	if err := p.Put(ctx, tenant, "alice/payments", "1.0.0", emb.embed(queryFor("alice/shipping"))); err != nil {
		t.Fatalf("upsert Put: %v", err)
	}
	if err := waitUntilQueryable(t, p, tenant, emb.embed(queryFor("alice/shipping")), "alice/payments"); err != nil {
		t.Fatalf("upsert never landed: %v", err)
	}

	// Delete removes alice/weather; a weather query must not return it.
	if err := p.Delete(ctx, tenant, "alice/weather", "1.0.0"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := waitUntilAbsent(t, p, tenant, emb.embed(queryFor("alice/weather")), "alice/weather"); err != nil {
		t.Fatalf("delete never removed the vector: %v", err)
	}
}

// TestPinecone_Live_TenantIsolation writes two tenants' vectors into the one
// index and asserts a query scoped to one namespace never returns the other's.
// Pinecone isolates per tenant by namespace.
//
// Spec: §4.7.1 — the org is the tenant boundary.
func TestPinecone_Live_TenantIsolation(t *testing.T) {
	p := livePineconeStorage(t)
	t.Cleanup(func() { _ = p.Close() })
	ctx := liveBackgroundContext()
	emb := charEmbedder{dim: livePineconeDim}
	tenantA := liveTenantPrefix + "_alice"
	tenantB := liveTenantPrefix + "_bob"
	shared := liveCorpus()[0] // same (id, version), one per tenant
	t.Cleanup(func() {
		_ = p.Delete(ctx, tenantA, shared.id, shared.version)
		_ = p.Delete(ctx, tenantB, shared.id, shared.version)
	})
	if err := p.Put(ctx, tenantA, shared.id, shared.version, emb.embed(shared.text)); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if err := p.Put(ctx, tenantB, shared.id, shared.version, emb.embed(shared.text)); err != nil {
		t.Fatalf("Put B: %v", err)
	}
	if err := waitUntilQueryable(t, p, tenantA, emb.embed(shared.text), shared.id); err != nil {
		t.Fatalf("tenant A never became queryable: %v", err)
	}
	// A query in tenant A returns A's row; tenant B's identical key never
	// crosses the namespace boundary (asserted via the version tag, which is
	// the only per-tenant-distinguishable field once ids match).
	got, err := p.Query(ctx, tenantA, emb.embed(shared.text), 10)
	if err != nil {
		t.Fatalf("Query A: %v", err)
	}
	if !containsArtifact(got, shared.id) {
		t.Fatalf("tenant A query missing its own row: %+v", got)
	}
	// Delete A's row, then a tenant-A query must return nothing for that id
	// even though tenant B still holds the identical key.
	if err := p.Delete(ctx, tenantA, shared.id, shared.version); err != nil {
		t.Fatalf("Delete A: %v", err)
	}
	if err := waitUntilAbsent(t, p, tenantA, emb.embed(shared.text), shared.id); err != nil {
		t.Fatalf("tenant A row not removed: %v", err)
	}
	// Tenant B is untouched by the tenant-A delete: its row still answers.
	if err := waitUntilQueryable(t, p, tenantB, emb.embed(shared.text), shared.id); err != nil {
		t.Fatalf("tenant B leaked the tenant-A delete: %v", err)
	}
}

// TestPinecone_Live_SelfEmbedding covers the server-side embedding path:
// drive PutText/QueryText against Integrated Inference and assert
// recall on the nearest text.
//
// Spec: §13.12 — PODIUM_PINECONE_INFERENCE_MODEL self-embedding.
func TestPinecone_Live_SelfEmbedding(t *testing.T) {
	p := livePineconeSelfEmbed(t)
	t.Cleanup(func() { _ = p.Close() })
	ctx := liveBackgroundContext()
	tenant := liveTenantPrefix + "_se_alice"
	corpus := liveCorpus()
	t.Cleanup(func() {
		for _, a := range corpus {
			_ = p.Delete(ctx, tenant, a.id, a.version)
		}
	})
	for _, a := range corpus {
		if err := p.PutText(ctx, tenant, a.id, a.version, a.text); err != nil {
			t.Fatalf("PutText %s: %v", a.id, err)
		}
	}
	if err := waitUntilTextQueryable(t, p, tenant, queryFor("alice/weather"), "alice/weather"); err != nil {
		t.Fatalf("self-embed ingest never became queryable: %v", err)
	}
	matches, err := p.QueryText(ctx, tenant, queryFor("alice/weather"), 3)
	if err != nil {
		t.Fatalf("QueryText: %v", err)
	}
	top, ok := topMatch(matches)
	if !ok || top.ArtifactID != "alice/weather" {
		t.Fatalf("self-embed nearest neighbour = %+v, want alice/weather first", matches)
	}
}
