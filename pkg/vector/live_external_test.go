package vector_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/vector"
	"github.com/lennylabs/podium/pkg/vector/vectortest"
)

// This file holds the shared gate and fixtures for the managed-backend live
// integration suite (Pinecone, Weaviate Cloud, Qdrant Cloud). Each backend's
// live test lives in its own file (pinecone_live_test.go, weaviate_live_test.go,
// qdrant_live_test.go) and reuses these helpers.
//
// Gating contract (TEST-GAPS.md "Lane gating contract"): the whole suite is
// off unless PODIUM_LIVE_EXTERNAL == "1", and within that each backend skips
// with a reason when its own credentials are absent. This mirrors the
// skip-with-reason idiom in pkg/objectstore/s3_live_test.go and
// pkg/sign/sigstore_live_test.go: a plain `go test ./...` with no credentials
// runs every case as a clean Skip and never fails.

// liveExternalEnabled reports whether the managed-backend live suite is armed.
// The single switch is PODIUM_LIVE_EXTERNAL == "1"; any other value (including
// unset) keeps the suite off. A backend test calls this first and skips before
// touching its own credentials.
func liveExternalEnabled() bool {
	return os.Getenv("PODIUM_LIVE_EXTERNAL") == "1"
}

// requireLiveExternal skips the calling test unless the live external switch is
// set. Backend tests pair this with their own credential check.
func requireLiveExternal(t *testing.T) {
	t.Helper()
	if !liveExternalEnabled() {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping managed vector backend live suite")
	}
}

// liveTenantPrefix is a stable per-run namespace seed so the live suite's rows
// do not collide with other tenants in a shared cloud index or collection. The
// per-sub-test tenant ids derive from it.
const liveTenantPrefix = "podium_live"

// runLiveSuite runs the shared §4.7 conformance contract against a live managed
// backend (G-VEC-2). A remote store has no TRUNCATE affordance, so each of the
// suite's sub-tests gets a fresh tenant prefix (open instructs the factory to
// bind a unique tenant) — the suite already scopes every Put/Query to a tenant,
// so unique tenants give each sub-test an isolated key space inside the one
// shared index. The factory returns the same Provider per sub-test; isolation
// comes from the tenant boundary, not from a fresh store.
//
// Spec: §4.7 — RegistrySearchProvider conformance (the package's one contract).
func runLiveSuite(t *testing.T, dim int, p vector.Provider) {
	t.Helper()
	vectortest.Suite(t, dim, func(t *testing.T) vector.Provider { return p })
}

// charEmbedder is a deterministic storage-only embedder for the live
// storage-only path (G-VEC-5). It mirrors the unexported fakeEmbedder bodies
// the in-process registry tests use (pkg/registry/core, hybrid_test.go): for a
// text, v[j] = byte(text[j]) / 256 for j < len(text), else 0. It is copied here
// rather than imported because every fake in the tree is an unexported,
// test-local type (see the VECTOR GUIDE §6). Two identical texts embed to the
// identical vector, which is what the recall and upsert assertions rely on.
type charEmbedder struct{ dim int }

func (e charEmbedder) embed(text string) []float32 {
	v := make([]float32, e.dim)
	for j := 0; j < len(text) && j < e.dim; j++ {
		v[j] = float32(text[j]) / 256.0
	}
	return v
}

// liveArtifact is one entry in the small fixed artifact set the recall and
// isolation tests ingest. Text is what the storage-only path embeds and what
// the self-embedding backend receives verbatim.
type liveArtifact struct {
	id      string
	version string
	text    string
}

// liveCorpus is the fixed artifact set shared by every backend's recall and
// isolation assertions. The texts are deliberately well-separated so cosine
// distance ranks the matching artifact first under both the deterministic
// char embedder and a real hosted model.
func liveCorpus() []liveArtifact {
	return []liveArtifact{
		{id: "alice/payments", version: "1.0.0", text: "process credit card payments and refunds"},
		{id: "alice/shipping", version: "1.0.0", text: "schedule freight and track delivery logistics"},
		{id: "alice/weather", version: "1.0.0", text: "forecast rain temperature and wind for a city"},
	}
}

// queryFor returns the text whose nearest neighbour must be the corpus entry
// with the given id. It reuses the artifact's own text so the storage-only
// deterministic embedder produces a zero-distance self-match and a real model
// ranks it first.
func queryFor(id string) string {
	for _, a := range liveCorpus() {
		if a.id == id {
			return a.text
		}
	}
	return ""
}

// topMatch returns the lowest-distance match, or false when ms is empty.
func topMatch(ms []vector.Match) (vector.Match, bool) {
	if len(ms) == 0 {
		return vector.Match{}, false
	}
	best := ms[0]
	for _, m := range ms[1:] {
		if m.Distance < best.Distance {
			best = m
		}
	}
	return best, true
}

// containsArtifact reports whether any match carries the artifact id.
func containsArtifact(ms []vector.Match, id string) bool {
	for _, m := range ms {
		if m.ArtifactID == id {
			return true
		}
	}
	return false
}

// liveBackgroundContext is the context every live call uses. Kept as a helper
// so a future timeout wrapper lands in one place.
func liveBackgroundContext() context.Context { return context.Background() }

// contextWithTimeout is a thin wrapper used by the host-resolution paths so the
// bounded-context idiom matches OpenBuiltin (builtin.go).
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// liveConsistencyDeadline bounds how long the eventual-consistency poll helpers
// wait for a managed backend to reflect a write. Pinecone, Weaviate, and Qdrant
// index asynchronously, so a write is not always visible on the next read; the
// poll retries until the deadline before failing. Generous because cloud index
// refresh latency varies.
const liveConsistencyDeadline = 30 * time.Second

// liveConsistencyInterval is the gap between consistency-poll attempts.
const liveConsistencyInterval = 500 * time.Millisecond

// waitUntilQueryable polls Query until id appears among the matches for vec, or
// the deadline elapses. Managed backends index asynchronously, so a freshly
// written vector is not guaranteed to be on the next read. Returns nil once the
// id is present, or the last error / a timeout error otherwise.
func waitUntilQueryable(t *testing.T, p vector.Provider, tenant string, vec []float32, id string) error {
	t.Helper()
	ctx := liveBackgroundContext()
	deadline := time.Now().Add(liveConsistencyDeadline)
	var last error
	for {
		matches, err := p.Query(ctx, tenant, vec, 20)
		if err != nil {
			last = err
		} else if containsArtifact(matches, id) {
			return nil
		} else {
			last = fmt.Errorf("id %q not yet present in %+v", id, matches)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %q: %v", liveConsistencyDeadline, id, last)
		}
		time.Sleep(liveConsistencyInterval)
	}
}

// waitUntilAbsent polls Query until id is gone from the matches for vec, or the
// deadline elapses. Used after Delete, which a managed backend may also reflect
// asynchronously.
func waitUntilAbsent(t *testing.T, p vector.Provider, tenant string, vec []float32, id string) error {
	t.Helper()
	ctx := liveBackgroundContext()
	deadline := time.Now().Add(liveConsistencyDeadline)
	for {
		matches, err := p.Query(ctx, tenant, vec, 20)
		if err == nil && !containsArtifact(matches, id) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %q to disappear", liveConsistencyDeadline, id)
		}
		time.Sleep(liveConsistencyInterval)
	}
}

// waitUntilTextQueryable is the self-embedding counterpart of
// waitUntilQueryable: it polls QueryText (the server-side embedding path) until
// id appears. The provider must be a TextVectorizer with self-embedding active.
func waitUntilTextQueryable(t *testing.T, tv vector.TextVectorizer, tenant, text, id string) error {
	t.Helper()
	ctx := liveBackgroundContext()
	deadline := time.Now().Add(liveConsistencyDeadline)
	var last error
	for {
		matches, err := tv.QueryText(ctx, tenant, text, 20)
		if err != nil {
			last = err
		} else if containsArtifact(matches, id) {
			return nil
		} else {
			last = fmt.Errorf("id %q not yet present in %+v", id, matches)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %q: %v", liveConsistencyDeadline, id, last)
		}
		time.Sleep(liveConsistencyInterval)
	}
}
