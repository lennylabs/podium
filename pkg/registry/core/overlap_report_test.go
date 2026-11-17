package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// reportFixture stores a public layer L (a, b, s, old@1/old@2) and a layer R
// restricted to the eng group (h), with overlap records written directly so
// each case controls the similarities.
func reportFixture(t *testing.T) *overlapFixture {
	t.Helper()
	f := newOverlapFixture(t, []layer.Layer{
		publicLayer("L"),
		{ID: "R", Visibility: layer.Visibility{Groups: []string{"eng"}}},
	})
	f.put("a", "1.0.0", "L", "alpha reconciles vendor invoices at month end", "", false)
	f.put("b", "1.0.0", "L", "bravo reconciles vendor invoices at month end", "", false)
	f.put("s", "1.0.0", "L", "suppressed sibling of alpha", "lint_suppress:\n  - lint.semantic_overlap\n", false)
	f.put("old", "1.0.0", "L", "older release of a thing", "", false)
	f.put("old", "2.0.0", "L", "newer release of a thing", "", false)
	f.put("h", "1.0.0", "R", "hidden twin of alpha", "", false)
	write := func(id string, recs ...store.OverlapRecord) {
		for i := range recs {
			recs[i].ModelID, recs[i].ComputedAt = "m1", overlapNow
		}
		if err := f.st.ReplaceOverlapRecords(f.ctx, "t", id, recs); err != nil {
			t.Fatalf("ReplaceOverlapRecords: %v", err)
		}
	}
	write("a",
		store.OverlapRecord{ArtifactA: "a", VersionA: "1.0.0", ArtifactB: "b", VersionB: "1.0.0", Similarity: 0.90},
		store.OverlapRecord{ArtifactA: "a", VersionA: "1.0.0", ArtifactB: "h", VersionB: "1.0.0", Similarity: 0.97},
		store.OverlapRecord{ArtifactA: "a", VersionA: "1.0.0", ArtifactB: "s", VersionB: "1.0.0", Similarity: 0.86},
		store.OverlapRecord{ArtifactA: "a", VersionA: "1.0.0", ArtifactB: "old", VersionB: "1.0.0", Similarity: 0.99},
	)
	return f
}

func semanticOf(r *OverlapReport, id string) *OverlapDiagnostic {
	for i := range r.Diagnostics {
		if r.Diagnostics[i].ArtifactID == id && r.Diagnostics[i].Code == lint.SemanticOverlapCode {
			return &r.Diagnostics[i]
		}
	}
	return nil
}

func peerIDs(d *OverlapDiagnostic) string {
	if d == nil {
		return "<none>"
	}
	ids := make([]string, 0, len(d.Peers))
	for _, p := range d.Peers {
		ids = append(ids, p.ArtifactID)
	}
	return strings.Join(ids, ",")
}

// Spec: §7.3.1 — the report names only artifacts the caller can see: a record
// with a hidden artifact is neither reported nor counted, and a caller who
// can see both artifacts gets it. A record naming a version that is no longer
// served is not reported, and a suppressed artifact raises no diagnostic
// while its peers still name it.
func TestCatalogOverlap_VisibilityAndServedVersions(t *testing.T) {
	t.Parallel()
	f := reportFixture(t)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	bob := layer.Identity{Sub: "bob", IsAuthenticated: true, Groups: []string{"eng"}}
	forAlice, err := f.reg.CatalogOverlap(f.ctx, alice, OverlapQuery{})
	if err != nil {
		t.Fatalf("CatalogOverlap(alice): %v", err)
	}
	forBob, err := f.reg.CatalogOverlap(f.ctx, bob, OverlapQuery{})
	if err != nil {
		t.Fatalf("CatalogOverlap(bob): %v", err)
	}
	a := semanticOf(forAlice, "a")
	if peerIDs(a) != "b,s" || strings.Contains(a.Message, "h (") || strings.Contains(a.Message, "more") {
		t.Errorf("alice a: peers %s, message %q; want b and s only, h never named or counted", peerIDs(a), a.Message)
	}
	if semanticOf(forAlice, "h") != nil || semanticOf(forAlice, "s") != nil || semanticOf(forAlice, "old") != nil {
		t.Errorf("alice report names h, a suppressed s, or the non-served old@1.0.0: %+v", forAlice.Diagnostics)
	}
	if peerIDs(semanticOf(forBob, "a")) != "h,b,s" || peerIDs(semanticOf(forBob, "h")) != "a" {
		t.Errorf("bob: a peers %s, h peers %s; want h,b,s and a", peerIDs(semanticOf(forBob, "a")), peerIDs(semanticOf(forBob, "h")))
	}
	p := semanticOf(forAlice, "a").Peers[0]
	if p.Similarity != 0.90 || p.ComputedAt == nil || !p.ComputedAt.Equal(overlapNow) {
		t.Errorf("peer = %+v, want similarity 0.9 and the computed time", p)
	}
	if forAlice.SemanticOverlap.State != "on" || *forAlice.SemanticOverlap.Threshold != 0.85 || *forAlice.SemanticOverlap.Model != "m1" {
		t.Errorf("state = %+v", forAlice.SemanticOverlap)
	}
}

// Spec: §7.3.1 — the threshold parameter replaces the default for one read
// with no recomputation, an out-of-range value is registry.invalid_argument,
// and a record of another model is not reported.
func TestCatalogOverlap_ThresholdAndModel(t *testing.T) {
	t.Parallel()
	f := reportFixture(t)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	high := 0.88
	r, err := f.reg.CatalogOverlap(f.ctx, alice, OverlapQuery{Threshold: &high})
	if err != nil || peerIDs(semanticOf(r, "a")) != "b" || *r.SemanticOverlap.Threshold != 0.88 {
		t.Errorf("at 0.88: peers %s (%v), want b", peerIDs(semanticOf(r, "a")), err)
	}
	bad := 0.3
	if _, err := f.reg.CatalogOverlap(f.ctx, alice, OverlapQuery{Threshold: &bad}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("threshold 0.3 = %v, want ErrInvalidArgument", err)
	}
	f.reg.WithVectorSearch(f.idx, overlapEmbedder{model: "m2"})
	r, _ = f.reg.CatalogOverlap(f.ctx, alice, OverlapQuery{})
	if semanticOf(r, "a") != nil {
		t.Errorf("records of model m1 reported under m2: %+v", semanticOf(r, "a"))
	}
}

// Spec: §7.3.1 — the artifact parameter restricts the response to the
// diagnostics of one artifact and the counts to those diagnostics; the
// description checks carry their peers too.
func TestCatalogOverlap_ArtifactFilterAndDescriptionPeers(t *testing.T) {
	t.Parallel()
	f := reportFixture(t)
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	full, _ := f.reg.CatalogOverlap(f.ctx, alice, OverlapQuery{})
	only, err := f.reg.CatalogOverlap(f.ctx, alice, OverlapQuery{ArtifactID: "b"})
	if err != nil {
		t.Fatalf("CatalogOverlap(artifact=b): %v", err)
	}
	var colliding *OverlapDiagnostic
	for i, d := range only.Diagnostics {
		if d.ArtifactID != "b" {
			t.Errorf("artifact=b returned a diagnostic of %s", d.ArtifactID)
		}
		if d.Code == "lint.colliding_descriptions" {
			colliding = &only.Diagnostics[i]
		}
	}
	if only.Counts.Warning != len(only.Diagnostics) || only.Counts.Warning >= full.Counts.Warning {
		t.Errorf("counts = %+v of %d diagnostics; full report counts %+v", only.Counts, len(only.Diagnostics), full.Counts)
	}
	if colliding == nil || peerIDs(colliding) != "a" || colliding.Peers[0].ComputedAt != nil || colliding.Peers[0].Similarity <= 0.5 {
		t.Errorf("b colliding diagnostic = %+v, want peer a with a similarity and no computed time", colliding)
	}
}

// Spec: §7.3.1 — with the check off the report carries the description
// checks alone, and threshold and model are null.
func TestCatalogOverlap_Off(t *testing.T) {
	t.Parallel()
	f := reportFixture(t)
	f.reg.WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{})
	r, err := f.reg.CatalogOverlap(f.ctx, layer.Identity{IsPublic: true}, OverlapQuery{})
	if err != nil || r.SemanticOverlap.State != "off" || r.SemanticOverlap.Threshold != nil || r.SemanticOverlap.Model != nil || semanticOf(r, "a") != nil {
		t.Errorf("off report = %+v, %v", r, err)
	}
}

// Spec: §7.3.1 — an ingest reports lint.semantic_overlap for each artifact it
// computed, names a peer only in the ingested layer or in a public or
// organization layer, and reports nothing for an artifact whose computation
// failed.
func TestIngestSemanticOverlap_NamesOnlyEligibleLayers(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{
		publicLayer("L"),
		{ID: "R", Visibility: layer.Visibility{Groups: []string{"eng"}}},
	})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	f.put("h", "1.0.0", "R", "delta", "", true)
	diags := f.reg.IngestSemanticOverlap(f.ctx, "L", []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}})
	if len(diags) != 1 || diags[0].ArtifactID != "a" || peerIDsLint(diags[0]) != "b" {
		t.Fatalf("ingest diagnostics = %+v, want one for a naming b and not the restricted h", diags)
	}
	if recs := f.records(); len(recs) != 2 {
		t.Errorf("records = %+v, want (a, b) and (a, h) stored whatever the layer", recs)
	}
	f.idx.fail = true
	if diags := f.reg.IngestSemanticOverlap(f.ctx, "L", []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}); len(diags) != 0 {
		t.Errorf("failed computation reported %+v", diags)
	}
	if diags := f.reg.IngestSemanticOverlap(f.ctx, "L", nil); diags != nil {
		t.Errorf("an empty cycle reported %+v", diags)
	}
	f.idx.fail = false
	if diags := f.reg.IngestSemanticOverlap(f.ctx, "L", []vector.RowKey{{ArtifactID: "a", Version: "0.9.0"}}); diags != nil {
		t.Errorf("a version that is not served reported %+v", diags)
	}
	f.reg.WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{})
	if diags := f.reg.IngestSemanticOverlap(f.ctx, "L", []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}); diags != nil {
		t.Errorf("the check off reported %+v", diags)
	}
}

func peerIDsLint(d lint.Diagnostic) string {
	ids := make([]string, 0, len(d.Peers))
	for _, p := range d.Peers {
		ids = append(ids, p.ArtifactID)
	}
	return strings.Join(ids, ",")
}
