package embedding_test

import (
	"errors"
	"os"
	"testing"

	"github.com/lennylabs/podium/pkg/embedding"
)

// liveVoyage builds the Voyage provider from VOYAGE_API_KEY (and the
// optional PODIUM_VOYAGE_MODEL override), or skips when the suite is
// disabled or the key is absent.
func liveVoyage(t *testing.T) embedding.Voyage {
	t.Helper()
	if !liveExternal() {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live Voyage embedding smoke")
	}
	key := os.Getenv("VOYAGE_API_KEY")
	if key == "" {
		t.Skip("VOYAGE_API_KEY unset; skipping live Voyage embedding smoke")
	}
	return embedding.Voyage{
		APIKey: key,
		Model_: os.Getenv("PODIUM_VOYAGE_MODEL"),
	}
}

// Spec: §4.7 — a real Voyage embedding returns a vector whose dimension
// matches the configured model's default (voyage-3 → 1024), and the same
// phrase embeds deterministically across two calls.
func TestVoyage_Live_DimensionAndDeterminism(t *testing.T) {
	p := liveVoyage(t)
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

// Spec: §4.7 — Voyage shares OpenAI's {data:[{embedding,index}]} wire
// format; a real batched response must preserve per-item alignment so the
// index-scatter parser is exercised against the live vendor response.
func TestVoyage_Live_BatchAlignment(t *testing.T) {
	p := liveVoyage(t)
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
		t.Errorf("identical batch inputs produced different vectors; index alignment broken")
	}
	if withinTolerance(batch[0], batch[1], 1e-4) {
		t.Errorf("distinct batch inputs produced the same vector; index alignment suspect")
	}
}

// Spec: §6.10 — a live Voyage call with an invalid key maps to ErrAuth.
func TestVoyage_Live_AuthFailure(t *testing.T) {
	if !liveExternal() {
		t.Skip("PODIUM_LIVE_EXTERNAL != 1; skipping live Voyage auth smoke")
	}
	if os.Getenv("VOYAGE_API_KEY") == "" {
		t.Skip("VOYAGE_API_KEY unset; skipping live Voyage auth smoke")
	}
	ctx, cancel := liveContext(t)
	defer cancel()
	bad := embedding.Voyage{
		APIKey: "pa-podium-live-invalid-key",
		Model_: os.Getenv("PODIUM_VOYAGE_MODEL"),
	}
	_, err := bad.Embed(ctx, []string{liveEmbedPhrase})
	if !errors.Is(err, embedding.ErrAuth) {
		t.Fatalf("bad-key Embed err = %v, want ErrAuth", err)
	}
}
