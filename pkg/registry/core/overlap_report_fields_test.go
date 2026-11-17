package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// putTyped stores a manifest of type typ whose stored description is desc and
// whose frontmatter states artifactDesc as its description when it is set.
func (f *overlapFixture) putTyped(id, typ, lay, desc, artifactDesc string) {
	f.t.Helper()
	fm := "---\ntype: " + typ + "\nversion: 1.0.0\n"
	if artifactDesc != "" {
		fm += "description: " + artifactDesc + "\n"
	}
	m := store.ManifestRecord{
		TenantID: "t", ArtifactID: id, Version: "1.0.0", ContentHash: "sha256:" + id,
		Type: typ, Description: desc, Layer: lay, IngestedAt: overlapNow, Frontmatter: []byte(fm + "---\n"),
	}
	if err := f.st.PutManifest(f.ctx, m); err != nil {
		f.t.Fatalf("PutManifest %s: %v", id, err)
	}
}

func diagnosticOf(r *OverlapReport, id, code string) *OverlapDiagnostic {
	for i := range r.Diagnostics {
		if r.Diagnostics[i].ArtifactID == id && r.Diagnostics[i].Code == code {
			return &r.Diagnostics[i]
		}
	}
	return nil
}

// Spec: §7.3.1 — each diagnostic of the catalog overlap report and each of
// its peers carries the artifact's description as the catalog advisories
// define it, its type, and the identifier of the layer serving its current
// version, on lint.thin_description, lint.colliding_descriptions, and
// lint.semantic_overlap alike. A skill's description is its ARTIFACT.md
// description when ARTIFACT.md states one, and its SKILL.md description
// otherwise.
func TestCatalogOverlap_ArtifactFieldsOnEveryCode(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L"), publicLayer("M")})
	f.putTyped("thin", "agent", "L", "tiny", "tiny")
	f.putTyped("sk/one", "skill", "L", "routes vendor invoices to the approver queue", "")
	f.putTyped("sk/two", "skill", "M", "skill md text that differs", "routes vendor invoices to the approver queue")
	f.putTyped("ag/three", "agent", "M", "summarizes quarterly revenue for the board", "summarizes quarterly revenue for the board")
	f.writeOwned("ag/three", "1.0.0", overlapNow, map[string]float64{"sk/one": 0.95})
	r, err := f.reg.CatalogOverlap(f.ctx, layer.Identity{IsPublic: true}, OverlapQuery{})
	if err != nil {
		t.Fatalf("CatalogOverlap: %v", err)
	}
	thin := diagnosticOf(r, "thin", "lint.thin_description")
	colliding := diagnosticOf(r, "sk/one", "lint.colliding_descriptions")
	semantic := diagnosticOf(r, "ag/three", "lint.semantic_overlap")
	if thin == nil || colliding == nil || semantic == nil || len(colliding.Peers) != 1 || len(semantic.Peers) != 1 {
		t.Fatalf("report diagnostics = %+v, want thin, colliding, and semantic entries with one peer each", r.Diagnostics)
	}
	invoices := "routes vendor invoices to the approver queue"
	got := []OverlapArtifact{thin.OverlapArtifact, colliding.OverlapArtifact, colliding.Peers[0].OverlapArtifact,
		semantic.OverlapArtifact, semantic.Peers[0].OverlapArtifact}
	want := []OverlapArtifact{
		{ArtifactID: "thin", Description: "tiny", Type: "agent", Layer: "L"},
		{ArtifactID: "sk/one", Description: invoices, Type: "skill", Layer: "L"},
		{ArtifactID: "sk/two", Description: invoices, Type: "skill", Layer: "M"},
		{ArtifactID: "ag/three", Description: "summarizes quarterly revenue for the board", Type: "agent", Layer: "M"},
		{ArtifactID: "sk/one", Description: invoices, Type: "skill", Layer: "L"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	body, err := json.Marshal(semantic)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, key := range []string{`"artifact_id":"ag/three","description":`, `"type":"agent","layer":"M","code":`, `"peers":[{"artifact_id":"sk/one","description":`, `"layer":"L","similarity":0.95,"computed_at":`} {
		if !strings.Contains(string(body), key) {
			t.Errorf("JSON %s lacks %s", body, key)
		}
	}
}

// Spec: §7.3.1, §4.6 — the report gives fields only to artifacts the caller
// can see. A hidden artifact whose description collides with a visible one,
// and whose record names a visible one, contributes no description, type, or
// layer to the caller's report, while a caller who can see it gets them.
func TestCatalogOverlap_HiddenArtifactFieldsNeverAppear(t *testing.T) {
	t.Parallel()
	f := newOverlapFixture(t, []layer.Layer{
		publicLayer("L"),
		{ID: "secret-layer", Visibility: layer.Visibility{Groups: []string{"eng"}}},
	})
	f.putTyped("a", "agent", "L", "reconciles vendor invoices at month end", "reconciles vendor invoices at month end")
	f.putTyped("h", "workflow", "secret-layer", "reconciles vendor invoices at month end!", "reconciles vendor invoices at month end!")
	f.writeOwned("h", "1.0.0", overlapNow, map[string]float64{"a": 0.99})
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
	aliceJSON, _ := json.Marshal(forAlice)
	bobJSON, _ := json.Marshal(forBob)
	for _, leak := range []string{`"h"`, "secret-layer", "workflow", "month end!"} {
		if strings.Contains(string(aliceJSON), leak) {
			t.Errorf("alice's report contains %s: %s", leak, aliceJSON)
		}
		if !strings.Contains(string(bobJSON), leak) {
			t.Errorf("bob's report lacks %s: %s", leak, bobJSON)
		}
	}
}

// Spec: §7.3.1 — a peer or diagnostic identifier missing from the caller's
// served set, which the report never produces, carries its identifier alone.
func TestOverlapArtifactOf_MissingIdentifier(t *testing.T) {
	t.Parallel()
	if got := overlapArtifactOf(map[string]OverlapArtifact{}, "x"); got != (OverlapArtifact{ArtifactID: "x"}) {
		t.Errorf("overlapArtifactOf = %+v, want the identifier alone", got)
	}
}
