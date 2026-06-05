package embedding_test

import (
	"errors"
	"os"
	"testing"

	"github.com/lennylabs/podium/pkg/embedding"
)

// liveCohere builds the Cohere provider from COHERE_API_KEY (and the
// optional PODIUM_COHERE_MODEL override), or skips when the suite is
// disabled or the key is absent.
func liveCohere(t *testing.T) embedding.Cohere {
	t.Helper()
	if !liveExternal() {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live Cohere embedding smoke")
	}
	key := os.Getenv("COHERE_API_KEY")
	if key == "" {
		t.Skip("COHERE_API_KEY unset; skipping live Cohere embedding smoke")
	}
	return embedding.Cohere{
		APIKey: key,
		Model_: os.Getenv("PODIUM_COHERE_MODEL"),
	}
}

// Spec: §4.7 — a real Cohere embedding returns a vector whose dimension
// matches the configured model's default (embed-v4 → 1024), and the same
// phrase embeds deterministically across two calls.
func TestCohere_Live_DimensionAndDeterminism(t *testing.T) {
	p := liveCohere(t)
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

// Spec: §4.7 — Cohere returns embeddings under one of two forms: a
// top-level array (legacy models) or the nested embeddings.float object
// (embed-v4 and newer). The provider type-switches over both. This test
// drives the configured model's real response through that switch and
// asserts per-item alignment, so a vendor wire-format change in either
// form is caught. To cover the second form explicitly, set
// PODIUM_COHERE_MODEL to a model that emits it.
func TestCohere_Live_BatchAlignmentBothForms(t *testing.T) {
	p := liveCohere(t)
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
	// Cohere returns rows positionally; identical inputs at 0 and 2 align.
	if !withinTolerance(batch[0], batch[2], 1e-4) {
		t.Errorf("identical batch inputs produced different vectors; row alignment broken")
	}
	if withinTolerance(batch[0], batch[1], 1e-4) {
		t.Errorf("distinct batch inputs produced the same vector; row alignment suspect")
	}
}

// Spec: §6.10 — a live Cohere call with an invalid key maps to ErrAuth.
func TestCohere_Live_AuthFailure(t *testing.T) {
	if !liveExternal() {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live Cohere auth smoke")
	}
	if os.Getenv("COHERE_API_KEY") == "" {
		t.Skip("COHERE_API_KEY unset; skipping live Cohere auth smoke")
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	bad := embedding.Cohere{
		APIKey: "podium-live-invalid-key",
		Model_: os.Getenv("PODIUM_COHERE_MODEL"),
	}
	_, err := bad.Embed(ctx, []string{liveEmbedPhrase})
	if !errors.Is(err, embedding.ErrAuth) {
		t.Fatalf("bad-key Embed err = %v, want ErrAuth", err)
	}
}
