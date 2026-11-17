package ingest_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
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

// ingestLayer ingests one layer holding several ARTIFACT.md files with the
// default linter.
func ingestLayer(t *testing.T, files map[string]string) *ingest.Result {
	t.Helper()
	return ingestLayerWith(t, newStore(t), nil, files)
}

// ingestLayerWith ingests one layer holding several ARTIFACT.md files into
// st with linter as req.Linter. A nil linter leaves ingest's default.
func ingestLayerWith(t *testing.T, st store.Store, linter *lint.Linter, files map[string]string) *ingest.Result {
	t.Helper()
	mfs := fstest.MapFS{}
	for path, src := range files {
		mfs[path] = &fstest.MapFile{Data: []byte(src)}
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L1", Files: mfs, Linter: linter,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return res
}

func ctxArtifact(version, desc string) string {
	return ctxArtifactBody(version, desc, "body")
}

func ctxArtifactBody(version, desc, body string) string {
	return "---\ntype: context\nversion: " + version + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
}

// Spec: §7.3.1 — the registry flags thin descriptions and pairs of
// artifacts with colliding descriptions at ingest time. The flags are
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

// sharedProse is a prose body of more than 50 words, the §7.3.1 bound below
// which a unit is not compared.
const sharedProse = "Reconcile every vendor invoice against its purchase order before the " +
	"monthly close begins. Flag any invoice whose amount differs from the order " +
	"by more than two percent, and route the flagged invoice to the accounts " +
	"payable lead for review. Record the reviewer, the decision, and the " +
	"adjusted amount in the close ledger so the auditors can trace each " +
	"exception back to its source document during the quarterly review."

// Spec: §7.3.1 — ingest runs lint.near_duplicate_content over the ingested
// layer. Two bodies that differ only in their last sentence are reported
// on both artifacts, and the advisory does not gate ingest.
func TestIngest_NearDuplicateAdvisory(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res := ingestLayerWith(t, st, nil, map[string]string{
		"finance/reconcile/ARTIFACT.md": ctxArtifactBody("1.0.0",
			"Reconcile vendor invoices at month-end close.",
			sharedProse+" Archive the ledger afterwards."),
		"ops/invoice-review/ARTIFACT.md": ctxArtifactBody("1.0.0",
			"Operations checklist for reviewing supplier billing.",
			sharedProse+" Notify the controller when done."),
	})

	if res.Accepted != 2 {
		t.Fatalf("accepted=%d, want 2 (advisories must not gate ingest); rejected=%+v", res.Accepted, res.Rejected)
	}
	for _, id := range []string{"finance/reconcile", "ops/invoice-review"} {
		if !hasAdvisory(res, "lint.near_duplicate_content", id) {
			t.Errorf("near-duplicate body not flagged on %s: %+v", id, res.Advisories)
		}
		if _, err := st.GetManifest(context.Background(), "tenant-1", id, "1.0.0"); err != nil {
			t.Errorf("GetManifest(%s): %v; the advisory must not keep the artifact out of the store", id, err)
		}
	}
}

// Spec: §7.3.1, §13.12 — ingest builds the advisories from req.Linter's
// thresholds rather than from the defaults. A paraphrase pair whose word
// sets overlap at 0.875 collides under the default 0.7 threshold and is
// silent when DescriptionSimilarity is 1.0.
func TestIngest_AdvisoriesUseLinterThresholds(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"finance/close/ARTIFACT.md": ctxArtifact("1.0.0", "Close the monthly books and file the report."),
		"ops/close/ARTIFACT.md":     ctxArtifact("1.0.0", "Close the monthly books and file the summary report."),
	}

	def := ingestLayerWith(t, newStore(t), &lint.Linter{}, files)
	if !hasAdvisory(def, "lint.colliding_descriptions", "finance/close") ||
		!hasAdvisory(def, "lint.colliding_descriptions", "ops/close") {
		t.Fatalf("default linter: paraphrase pair not flagged: %+v", def.Advisories)
	}

	strict := ingestLayerWith(t, newStore(t), &lint.Linter{Thresholds: lint.Thresholds{DescriptionSimilarity: 1.0}}, files)
	if strict.Accepted != 2 {
		t.Fatalf("accepted=%d, want 2; rejected=%+v", strict.Accepted, strict.Rejected)
	}
	for _, d := range strict.Advisories {
		if d.Code == "lint.colliding_descriptions" {
			t.Errorf("DescriptionSimilarity 1.0: paraphrase pair flagged: %v", d)
		}
	}
}
