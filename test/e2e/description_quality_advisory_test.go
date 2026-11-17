package e2e

// Description-quality ingest advisory, end to end.
//
// Spec §7.3.1 defines the catalog advisories, lint.thin_description and
// lint.colliding_descriptions among them, and states that registry ingest runs
// them over the artifacts of the layer it ingests and reports their diagnostics
// as non-blocking advisories. The observable surface is the per-artifact
// `advisories` array the ingest pipeline returns, which `podium layer reingest`
// prints as `advisory: <id> [warning] <message> (<code>)` and which the boot
// path logs.
//
// These tests drive the real `podium` CLI against a standalone server: register
// a local-source layer (the runtime-republish foundation), stage a
// vague, a precise, and a borderline-acceptable artifact, reingest, and assert
// the thin-description advisory fires on the vague artifact only with no false
// positive on the borderline one that sits exactly at the default bound. Ingest
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

// Spec: §7.3.1 — the thin-description advisory fires through the real
// ingest path on a vague description only, draws no false positive on a
// borderline-acceptable description at the §7.3.1 bound, and never gates ingest:
// the standalone server accepts and serves all three artifacts.
func TestDescriptionQuality_ThinAdvisoryEndToEnd(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	layer := newRepublishLayer(t, srv, "doc-quality-thin")

	ri := reingestArtifacts(t, layer, map[string]string{
		// Vague: 1 word / 4 chars, below both §7.3.1 bounds. Flagged.
		"finance/vague/ARTIFACT.md": agentArtifact("demo"),
		// Precise: a rich summary that answers "when should I use this?".
		"finance/precise/ARTIFACT.md": agentArtifact("Reconcile vendor invoices at month-end close."),
		// Borderline-acceptable: exactly 3 words and 15 chars, at the §7.3.1
		// bound (MinDescriptionChars=15, MinDescriptionWords=3). The
		// false-positive guard: this must NOT be flagged.
		"finance/borderline/ARTIFACT.md": agentArtifact("Greet the user."),
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

// Spec: §7.3.1 — lint.colliding_descriptions reports two artifacts whose
// normalized descriptions have word sets with a similarity of 0.7 or higher.
// Through the real ingest path, each artifact of an identical-after-normalization
// pair is flagged and names its peer at 100%, while a distinct summary draws no
// flag and ingest accepts all three.
func TestDescriptionQuality_CollidingAdvisoryEndToEnd(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	layer := newRepublishLayer(t, srv, "doc-quality-collide")

	ri := reingestArtifacts(t, layer, map[string]string{
		// Two summaries with equal word sets after §7.3.1 normalization
		// (case, punctuation, and whitespace folded).
		"finance/close-a/ARTIFACT.md": agentArtifact("Close the monthly books."),
		"ops/close-b/ARTIFACT.md":     agentArtifact("close   the   monthly   books"),
		// A distinct summary that must not be flagged as colliding.
		"finance/pay/ARTIFACT.md": agentArtifact("Submit an approved vendor payment for release."),
	})

	cliContains(t, ri.Stdout, "lint.colliding_descriptions", "colliding-summary advisory surfaced")
	// Each artifact of the pair is flagged and names its peer.
	cliContains(t, ri.Stdout, "advisory: finance/close-a", "close-a flagged as colliding")
	cliContains(t, ri.Stdout, "advisory: ops/close-b", "close-b flagged as colliding")
	if !closeAOverlap(ri.Stdout) {
		t.Errorf("overlap advisory on close-a does not name its peer at 100%%\nstdout=%s", ri.Stdout)
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

// closeAOverlap reports whether the finance/close-a advisory line names
// ops/close-b at 100% similarity.
func closeAOverlap(stdout string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "advisory: finance/close-a") && strings.Contains(line, "overlaps ops/close-b (100%)") {
			return true
		}
	}
	return false
}

// Spec: §7.3.1 — registry ingest runs lint.near_duplicate_content over the
// ingested layer. Two agent artifacts whose prose bodies share all but one
// sentence each draw the advisory naming the other, and the reingest exits 0
// because the advisory does not gate ingest.
func TestDescriptionQuality_NearDuplicateAdvisoryEndToEnd(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	layer := newRepublishLayer(t, srv, "doc-quality-near-dup")

	body := func(desc, own string) string {
		return "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\n---\n\n" + advisorySharedRunbook + own + "\n"
	}
	ri := reingestArtifacts(t, layer, map[string]string{
		"ops/rotate-staging/ARTIFACT.md": body("Rotate the staging database credentials and confirm each service reconnects.",
			"Page the on-call database engineer when a read replica lags behind the primary."),
		"ops/rotate-signing/ARTIFACT.md": body("Replace production API signing keys after a suspected compromise is reported.",
			"Notify the security channel whenever a signing key is revoked ahead of schedule."),
	})

	cliContains(t, ri.Stdout, "lint.near_duplicate_content", "near-duplicate advisory surfaced")
	for _, want := range []string{
		"advisory: ops/rotate-signing [warning] ARTIFACT.md body nearly duplicates ops/rotate-staging ARTIFACT.md body",
		"advisory: ops/rotate-staging [warning] ARTIFACT.md body nearly duplicates ops/rotate-signing ARTIFACT.md body",
	} {
		cliContains(t, ri.Stdout, want, "each artifact of the pair names its peer")
	}
	for _, id := range []string{"ops/rotate-staging", "ops/rotate-signing"} {
		var resp map[string]any
		getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &resp)
		if resp["id"] != id {
			t.Errorf("load %s: id=%v, want %q (advisory must not gate ingest)", id, resp["id"], id)
		}
	}
}
