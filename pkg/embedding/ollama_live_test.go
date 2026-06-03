package embedding_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/embedding"
)

// ollamaBaseURL resolves the Ollama endpoint from PODIUM_OLLAMA_URL,
// falling back to the documented default. Ollama needs no account, so the
// live gate is the suite opt-in plus reachability of this endpoint rather
// than a credential.
func ollamaBaseURL() string {
	if v := os.Getenv("PODIUM_OLLAMA_URL"); v != "" {
		return v
	}
	return "http://localhost:11434"
}

// liveOllama builds the Ollama provider, or skips when the suite is
// disabled or the endpoint is unreachable. Reachability is probed with a
// short GET against the base URL; any transport error skips the test
// rather than failing it, so a machine without Ollama running stays green.
func liveOllama(t *testing.T) embedding.Ollama {
	t.Helper()
	if !liveExternal() {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live Ollama embedding smoke")
	}
	base := ollamaBaseURL()
	probe, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probe, http.MethodGet, base, nil)
	if err != nil {
		t.Skipf("Ollama probe request: %v", err)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Skipf("PODIUM_OLLAMA_URL (%s) unreachable: %v", base, err)
	}
	_ = resp.Body.Close()
	return embedding.Ollama{
		BaseURL: base,
		Model_:  os.Getenv("PODIUM_OLLAMA_MODEL"),
	}
}

// Spec: §4.7 — a real Ollama embedding returns a vector whose dimension
// matches the configured model's default (nomic-embed-text → 768), and the
// same phrase embeds deterministically across two calls.
func TestOllama_Live_DimensionAndDeterminism(t *testing.T) {
	p := liveOllama(t)
	ctx, cancel := liveContext(t)
	defer cancel()

	first, err := p.Embed(ctx, []string{liveEmbedPhrase})
	if err != nil {
		t.Fatalf("Embed (first): %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("got %d vectors, want 1", len(first))
	}
	if len(first[0]) != p.Dimensions() {
		t.Fatalf("dimension = %d, want %d (model %q)", len(first[0]), p.Dimensions(), p.Model())
	}

	second, err := p.Embed(ctx, []string{liveEmbedPhrase})
	if err != nil {
		t.Fatalf("Embed (second): %v", err)
	}
	if !withinTolerance(first[0], second[0], 1e-4) {
		t.Errorf("two embeddings of the same phrase diverged beyond tolerance")
	}
}

// Spec: §4.7 — Ollama issues one HTTP call per text and stitches the
// results back in input order. A real batched call must preserve per-item
// alignment: a repeated phrase embeds to the same vector as its neighbors,
// and a distinct phrase differs.
func TestOllama_Live_BatchAlignment(t *testing.T) {
	p := liveOllama(t)
	ctx, cancel := liveContext(t)
	defer cancel()

	phrases := []string{
		"alice published a deployment skill for acme",
		"bob deprecated the legacy onboarding context",
		"alice published a deployment skill for acme",
	}
	batch, err := p.Embed(ctx, phrases)
	if err != nil {
		t.Fatalf("Embed (batch): %v", err)
	}
	if len(batch) != len(phrases) {
		t.Fatalf("got %d vectors, want %d", len(batch), len(phrases))
	}
	for i, v := range batch {
		if len(v) != p.Dimensions() {
			t.Fatalf("[%d] dimension = %d, want %d", i, len(v), p.Dimensions())
		}
	}
	if !withinTolerance(batch[0], batch[2], 1e-4) {
		t.Errorf("identical batch inputs produced different vectors; per-call stitching broken")
	}
	if withinTolerance(batch[0], batch[1], 1e-4) {
		t.Errorf("distinct batch inputs produced the same vector; per-call stitching suspect")
	}
}

// Spec: §4.7 — Ollama has no credential, so the analogue of the auth path
// is a request for a model the server does not serve. Ollama answers with a
// non-2xx status, which the provider surfaces as a non-nil error rather
// than a vector. The error must not be classified as an auth failure,
// since Ollama performs no authentication.
func TestOllama_Live_UnknownModelErrors(t *testing.T) {
	p := liveOllama(t)
	ctx, cancel := liveContext(t)
	defer cancel()
	bad := embedding.Ollama{
		BaseURL: p.BaseURL,
		Model_:  "podium-live-nonexistent-model",
	}
	_, err := bad.Embed(ctx, []string{liveEmbedPhrase})
	if err == nil {
		t.Fatalf("unknown-model Embed returned no error")
	}
}
