package ingest_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// hasAdvisory reports whether res carries an advisory with the given code
// for artifactID.
func hasAdvisory(res *ingest.Result, code, artifactID string) bool {
	for _, d := range res.Advisories {
		if d.Code == code && d.ArtifactID == artifactID {
			return true
		}
	}
	return false
}

// ingestLayer ingests one layer holding several ARTIFACT.md files.
func ingestLayer(t *testing.T, files map[string]string) *ingest.Result {
	t.Helper()
	mfs := fstest.MapFS{}
	for path, src := range files {
		mfs[path] = &fstest.MapFile{Data: []byte(src)}
	}
	res, err := ingest.Ingest(context.Background(), newStore(t), ingest.Request{
		TenantID: "tenant-1", LayerID: "L1", Files: mfs,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return res
}

func ctxArtifact(version, desc string) string {
	return "---\ntype: context\nversion: " + version + "\ndescription: " + desc + "\n---\n\nbody\n"
}

// Spec: §3.3 / §12 — the registry flags thin descriptions and clusters of
// artifacts with colliding summaries at ingest time. The flags are
// advisory: ingest still accepts the artifacts.
func TestIngest_DescriptionAdvisories(t *testing.T) {
	t.Parallel()
	res := ingestLayer(t, map[string]string{
		"finance/close-books/ARTIFACT.md": ctxArtifact("1.0.0", "Close the books."),
		"ops/close-the-books/ARTIFACT.md": ctxArtifact("1.0.0", "close the books"),
		"finance/thin/ARTIFACT.md":        ctxArtifact("1.0.0", "demo"),
		"finance/rich/ARTIFACT.md":        ctxArtifact("1.0.0", "Reconcile vendor invoices at month-end close."),
	})

	// Advisory, not gating: every artifact ingested.
	if res.Accepted != 4 {
		t.Fatalf("accepted=%d, want 4 (advisories must not gate ingest); rejected=%+v", res.Accepted, res.Rejected)
	}
	// Colliding summaries flagged on both members of the cluster.
	if !hasAdvisory(res, "lint.colliding_descriptions", "finance/close-books") ||
		!hasAdvisory(res, "lint.colliding_descriptions", "ops/close-the-books") {
		t.Errorf("colliding summaries not flagged: %+v", res.Advisories)
	}
	// Thin description flagged.
	if !hasAdvisory(res, "lint.thin_description", "finance/thin") {
		t.Errorf("thin description not flagged: %+v", res.Advisories)
	}
	// The rich, unique description draws neither flag.
	for _, d := range res.Advisories {
		if d.ArtifactID == "finance/rich" {
			t.Errorf("rich/unique description flagged: %v", d)
		}
	}
}

// Spec: §3.3 — a layer of distinct, well-formed descriptions produces no
// advisories, so the signal stays meaningful.
func TestIngest_CleanLayerNoAdvisories(t *testing.T) {
	t.Parallel()
	res := ingestLayer(t, map[string]string{
		"finance/close/ARTIFACT.md": ctxArtifact("1.0.0", "Close the monthly books and file the report."),
		"finance/pay/ARTIFACT.md":   ctxArtifact("1.0.0", "Submit an approved vendor payment for release."),
	})
	if res.Accepted != 2 {
		t.Fatalf("accepted=%d, want 2", res.Accepted)
	}
	if len(res.Advisories) != 0 {
		t.Errorf("clean layer produced advisories: %+v", res.Advisories)
	}
}

// Spec: §12 — the thin-description message names the rule so an operator
// reading the ingest report can act on it.
func TestIngest_AdvisoryMessageNamesRule(t *testing.T) {
	t.Parallel()
	res := ingestLayer(t, map[string]string{
		"x/ARTIFACT.md": ctxArtifact("1.0.0", "demo"),
	})
	var msg string
	for _, d := range res.Advisories {
		if d.Code == "lint.thin_description" {
			msg = d.Message
		}
	}
	if !strings.Contains(msg, "thin") {
		t.Errorf("advisory message = %q, want it to name the thin description", msg)
	}
}
