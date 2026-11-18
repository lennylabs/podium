package core

import (
	"errors"
	"strings"
	"testing"
	"time"

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
	f.writeOwned("a", "1.0.0", overlapNow, map[string]float64{"b": 0.90, "h": 0.97, "s": 0.86, "old": 0.99})
	return f
}

// writeOwned stores one computation of owner at version, computed at at, with
// a record for each peer at version 1.0.0 and the given similarity.
func (f *overlapFixture) writeOwned(owner, version string, at time.Time, peers map[string]float64) {
	f.t.Helper()
	recs := make([]store.OverlapRecord, 0, len(peers))
	for peer, sim := range peers {
		recs = append(recs, store.OverlapRecord{Owner: owner, OwnerVersion: version, Peer: peer, PeerVersion: "1.0.0", Similarity: sim, ModelID: "m1", ComputedAt: at})
	}
	comp := store.OverlapComputation{ArtifactID: owner, Version: version, ModelID: "m1", ComputedAt: at}
	if err := f.st.ReplaceOverlapRecords(f.ctx, "t", comp, recs); err != nil {
		f.t.Fatalf("ReplaceOverlapRecords: %v", err)
	}
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

// Spec: §7.3.1 — a cycle a source webhook triggers names only peers in the
// ingested layer, because the git host records the response; the same cycle
// without the trigger also names a peer in a public layer. Both store the
// same records.
func TestIngestSemanticOverlap_WebhookNamesOnlyIngestedLayer(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L"), publicLayer("P")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("d", "1.0.0", "L", "delta", "", true)
	f.put("b", "1.0.0", "P", "bravo", "", true)
	key := []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}
	manual := f.reg.IngestSemanticOverlap(f.ctx, "L", key)
	hooked := f.reg.IngestSemanticOverlap(ContextWithWebhookTrigger(f.ctx), "L", key)
	if len(manual) != 1 || peerIDsLint(manual[0]) != "b,d" || len(hooked) != 1 || peerIDsLint(hooked[0]) != "d" {
		t.Errorf("manual = %+v, webhook = %+v; want b,d and d alone", manual, hooked)
	}
	if got := ownedSummary(f.records()); got != "a>b a>d" {
		t.Errorf("records = %q, want a>b a>d whatever the trigger", got)
	}
}

// Spec: §7.3.1 — when both artifacts own a record naming the other, the pair
// is reported once, with the similarity and the computed time of the more
// recently computed record, and a record whose owner's version is no longer
// served is not reported.
func TestCatalogOverlap_BothDirectionsReportOnce(t *testing.T) {
	t.Parallel()
	f := reportFixture(t)
	later := overlapNow.Add(time.Hour)
	f.writeOwned("b", "1.0.0", later, map[string]float64{"a": 0.95})
	f.writeOwned("old", "1.0.0", later, map[string]float64{"b": 0.99})
	r, err := f.reg.CatalogOverlap(f.ctx, layer.Identity{Sub: "alice", IsAuthenticated: true}, OverlapQuery{})
	if err != nil {
		t.Fatalf("CatalogOverlap: %v", err)
	}
	a, b := semanticOf(r, "a"), semanticOf(r, "b")
	if peerIDs(a) != "b,s" || peerIDs(b) != "a" || a.Peers[0].Similarity != 0.95 || !a.Peers[0].ComputedAt.Equal(later) ||
		b.Peers[0].Similarity != 0.95 {
		t.Errorf("a = %+v, b = %+v; want the pair once at 0.95 computed at %s, and old@1.0.0 never named", a, b, later)
	}
}

// Spec: §7.3.1 — two records of one pair computed at the same time report the
// record whose owner sorts first, so the read does not depend on store order.
func TestReportablePairs_EqualTimesPickTheFirstOwner(t *testing.T) {
	t.Parallel()
	served := map[string]store.ManifestRecord{"a": {ArtifactID: "a", Version: "1"}, "b": {ArtifactID: "b", Version: "1"}}
	ab := store.OverlapRecord{Owner: "a", OwnerVersion: "1", Peer: "b", PeerVersion: "1", Similarity: 0.91, ModelID: "m1", ComputedAt: overlapNow}
	ba := store.OverlapRecord{Owner: "b", OwnerVersion: "1", Peer: "a", PeerVersion: "1", Similarity: 0.92, ModelID: "m1", ComputedAt: overlapNow}
	first := reportablePairs([]store.OverlapRecord{ab, ba}, served, "m1")
	second := reportablePairs([]store.OverlapRecord{ba, ab}, served, "m1")
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] || first[0].Similarity != 0.91 || first[0].A != "a" || first[0].B != "b" {
		t.Errorf("pairs = %+v and %+v, want one (a, b) pair at 0.91 in both orders", first, second)
	}
}
