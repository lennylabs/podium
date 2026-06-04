package e2e

// Description-quality ingest advisory, end to end (gap G-DOC-8).
//
// Spec §3.3 ("The registry lints for thin descriptions and flags clusters of
// artifacts whose summaries collide") and §12 ("Ingest-time lint flags thin
// descriptions and clusters of artifacts with colliding summaries") attribute
// the description-quality checks to the registry ingest path. The checks are
// advisory: §3.3 states the flags inform the author, and ingest still accepts
// the artifacts. The observable §7.3.1 / §3.3 surface for these flags is the
// per-artifact `advisories` array the ingest pipeline returns, which `podium
// layer reingest` prints as `advisory: <id> [warning] <message> (<code>)` and
// which the boot path logs.
//
// These tests drive the real `podium` CLI against a standalone server: register
// a local-source layer (the G-INFRA-7 runtime-republish foundation), stage a
// vague, a precise, and a borderline-acceptable artifact, reingest, and assert
// the thin-description advisory fires on the vague artifact only with no false
// positive on the borderline one that sits exactly at the §3.3 bound. Ingest
// accepts all three (every artifact is loadable afterward), proving the advisory
// does not gate ingest.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reingestStdout writes the given path->ARTIFACT.md-content map into the layer
// directory and triggers one `podium layer reingest`, returning the CLI result.
// It mirrors the republish foundation's writeArtifact + reingest but stages
// several artifacts under one reingest so the cross-artifact colliding-summary
// check and the per-artifact thin-description check are both visible in one
// pass. The reingest must exit 0: a description-quality advisory is non-gating.
func reingestArtifacts(t testing.TB, l *republishLayer, files map[string]string) cliResult {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(l.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	ri := runPodium(t, "", nil, "layer", "reingest", "--registry", l.srv.BaseURL, l.layerID)
	if ri.Exit != 0 {
		t.Fatalf("layer reingest %q exit=%d (a description-quality advisory must not gate ingest)\nstdout=%s\nstderr=%s",
			l.layerID, ri.Exit, ri.Stdout, ri.Stderr)
	}
	return ri
}

// G-DOC-8 — the §3.3 / §12 thin-description advisory fires through the real
// ingest path on a vague description only, draws no false positive on a
// borderline-acceptable description at the §3.3 bound, and never gates ingest:
// the standalone server accepts and serves all three artifacts.
func TestDescriptionQuality_ThinAdvisoryEndToEnd(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	layer := newRepublishLayer(t, srv, "doc-quality-thin")

	ri := reingestArtifacts(t, layer, map[string]string{
		// Vague: 1 word / 4 chars, below both §3.3 bounds. Flagged.
		"finance/vague/ARTIFACT.md": contextArtifact("demo"),
		// Precise: a rich summary that answers "when should I use this?".
		"finance/precise/ARTIFACT.md": contextArtifact("Reconcile vendor invoices at month-end close."),
		// Borderline-acceptable: exactly 3 words and 15 chars, at the §3.3
		// bound (MinDescriptionChars=15, MinDescriptionWords=3). The
		// false-positive guard: this must NOT be flagged.
		"finance/borderline/ARTIFACT.md": contextArtifact("Greet the user."),
	})

	// The advisory fires on the vague artifact only.
	cliContains(t, ri.Stdout, "lint.thin_description", "thin-description advisory surfaced")
	cliContains(t, ri.Stdout, "advisory: finance/vague", "advisory fires on the vague artifact")
	if strings.Contains(ri.Stdout, "advisory: finance/borderline") {
		t.Errorf("false positive: borderline-acceptable description flagged thin\nstdout=%s", ri.Stdout)
	}
	if strings.Contains(ri.Stdout, "advisory: finance/precise") {
		t.Errorf("false positive: precise description flagged\nstdout=%s", ri.Stdout)
	}

	// Ingest accepted all three despite the advisory: each loads.
	for _, id := range []string{"finance/vague", "finance/precise", "finance/borderline"} {
		var resp map[string]any
		getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &resp)
		if resp["id"] != id {
			t.Errorf("load %s: id=%v, want %q (advisory must not gate ingest)", id, resp["id"], id)
		}
	}
}

// G-DOC-8 (colliding-summary half) — the §3.3 cross-artifact colliding-summary
// advisory fires through the real ingest path on every member of a normalized
// collision, naming each colliding peer, while a distinct summary draws no flag
// and ingest accepts all three.
func TestDescriptionQuality_CollidingAdvisoryEndToEnd(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	layer := newRepublishLayer(t, srv, "doc-quality-collide")

	ri := reingestArtifacts(t, layer, map[string]string{
		// Two summaries that collide after §3.3 normalization
		// (case, punctuation, and whitespace folded).
		"finance/close-a/ARTIFACT.md": contextArtifact("Close the monthly books."),
		"ops/close-b/ARTIFACT.md":     contextArtifact("close   the   monthly   books"),
		// A distinct summary that must not be flagged as colliding.
		"finance/pay/ARTIFACT.md": contextArtifact("Submit an approved vendor payment for release."),
	})

	cliContains(t, ri.Stdout, "lint.colliding_descriptions", "colliding-summary advisory surfaced")
	// Every member of the cluster is flagged and names its peer.
	cliContains(t, ri.Stdout, "advisory: finance/close-a", "close-a flagged as colliding")
	cliContains(t, ri.Stdout, "advisory: ops/close-b", "close-b flagged as colliding")
	if !strings.Contains(ri.Stdout, "collides with ops/close-b") {
		t.Errorf("collision advisory on close-a does not name its peer\nstdout=%s", ri.Stdout)
	}
	// The distinct payment summary draws no colliding-summary advisory.
	for _, line := range strings.Split(ri.Stdout, "\n") {
		if strings.Contains(line, "finance/pay") && strings.Contains(line, "colliding_descriptions") {
			t.Errorf("false positive: distinct summary flagged as colliding: %q", line)
		}
	}

	// Ingest accepted all three despite the advisory.
	for _, id := range []string{"finance/close-a", "ops/close-b", "finance/pay"} {
		var resp map[string]any
		getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &resp)
		if resp["id"] != id {
			t.Errorf("load %s: id=%v, want %q (advisory must not gate ingest)", id, resp["id"], id)
		}
	}
}
