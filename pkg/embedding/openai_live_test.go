package embedding_test

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/embedding"
)

// liveExternal reports whether the live external suite is enabled. The
// embedding live tests reach real vendor APIs, so they run only when an
// operator opts in via PODIUM_LIVE_EXTERNAL=1 and supplies the relevant
// credential. A plain `go test ./...` with no environment leaves every
// provider's live test skipped.
func liveExternal() bool { return os.Getenv("PODIUM_LIVE_EXTERNAL") == "1" }

// liveEmbedPhrase is the fixed phrase embedded for the dimension and
// determinism assertions. A stable input keeps the determinism check
// meaningful across two calls.
const liveEmbedPhrase = "alice published a deployment skill for acme"

// liveContext bounds each live call so a hung provider fails the test
// instead of blocking the suite.
func liveContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// withinTolerance reports whether two vectors of equal length agree on
// every component within tol. It is the determinism predicate: a provider
// asked to embed the same text twice must return the same vector up to
// floating-point and server-side numerical noise.
func withinTolerance(a, b []float32, tol float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > tol {
			return false
		}
	}
	return true
}

// liveOpenAI builds the OpenAI provider from OPENAI_API_KEY (and the
// optional PODIUM_OPENAI_MODEL / PODIUM_OPENAI_BASE_URL / PODIUM_OPENAI_ORG
// overrides), or skips when the suite is disabled or the key is absent.
func liveOpenAI(t *testing.T) embedding.OpenAI {
	t.Helper()
	if !liveExternal() {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live OpenAI embedding smoke")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY unset; skipping live OpenAI embedding smoke")
	}
	return embedding.OpenAI{
		APIKey:  key,
		Model_:  os.Getenv("PODIUM_OPENAI_MODEL"),
		BaseURL: os.Getenv("PODIUM_OPENAI_BASE_URL"),
		Org:     os.Getenv("PODIUM_OPENAI_ORG"),
	}
}

// Spec: §4.7 — a real OpenAI embedding returns a vector whose dimension
// matches the configured model's default (text-embedding-3-small → 1536),
// and the same phrase embeds deterministically across two calls.
func TestOpenAI_Live_DimensionAndDeterminism(t *testing.T) {
	p := liveOpenAI(t)
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

// Spec: §4.7 — a real batched OpenAI response preserves per-item
// alignment. OpenAI returns one {embedding,index} per input and the
// provider scatters by index; a repeated phrase must embed to the same
// vector as when sent alone, and a distinct phrase must differ
// (G-EMB-2: exercises index reordering against a real response).
func TestOpenAI_Live_BatchAlignment(t *testing.T) {
	p := liveOpenAI(t)
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
	// Identical inputs at positions 0 and 2 must align to the same vector.
	if !withinTolerance(batch[0], batch[2], 1e-4) {
		t.Errorf("identical batch inputs produced different vectors; index alignment broken")
	}
	// A distinct input must not collide with the repeated one.
	if withinTolerance(batch[0], batch[1], 1e-4) {
		t.Errorf("distinct batch inputs produced the same vector; index alignment suspect")
	}
}

// Spec: §6.10 — a live OpenAI call with an invalid key maps to ErrAuth so
// the registry can distinguish bad credentials from an unreachable provider.
func TestOpenAI_Live_AuthFailure(t *testing.T) {
	if !liveExternal() {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live OpenAI auth smoke")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY unset; skipping live OpenAI auth smoke")
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	// A syntactically plausible but invalid key against the real endpoint.
	bad := embedding.OpenAI{
		APIKey:  "sk-podium-live-invalid-key",
		Model_:  os.Getenv("PODIUM_OPENAI_MODEL"),
		BaseURL: os.Getenv("PODIUM_OPENAI_BASE_URL"),
	}
	_, err := bad.Embed(ctx, []string{liveEmbedPhrase})
	if !errors.Is(err, embedding.ErrAuth) {
		t.Fatalf("bad-key Embed err = %v, want ErrAuth", err)
	}
}
