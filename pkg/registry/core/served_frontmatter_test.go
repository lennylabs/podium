package core_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// plainSkillRow is a sealed row of a skill with no ARTIFACT.md at ver, with
// the columns ingest stores for it.
func plainSkillRow(t *testing.T, id, ver string) store.ManifestRecord {
	t.Helper()
	return storetest.Seal(t, store.ManifestRecord{
		TenantID: admTenant, ArtifactID: id, Version: ver, Layer: "L", Type: "skill",
		Description: "The admitted description.", Body: []byte("Admitted skill prose.\n"),
		SkillRaw: []byte(admPlainSkillMD),
	}, nil, nil)
}

// searchDescriptor returns the single search descriptor for id.
func searchDescriptor(t *testing.T, reg *core.Registry, id string) core.ArtifactDescriptor {
	t.Helper()
	res, err := reg.SearchArtifacts(context.Background(), layer.Identity{IsPublic: true}, core.SearchArtifactsOptions{TopK: 10})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	for _, d := range res.Results {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("search results %+v carry no %s", res.Results, id)
	return core.ArtifactDescriptor{}
}

// Spec: §4.3.4, §4.7.10, §7.6.1 — a plain-skill row loads with the
// synthesized ARTIFACT.md in frontmatter, and its search descriptor carries
// that document's frontmatter block without the body, as every descriptor
// carries its served document's block.
func TestPlainSkill_LoadAndDescriptorServeTheSynthesizedDocument(t *testing.T) {
	t.Parallel()
	f := newAdmFixture(t)
	f.put(t, plainSkillRow(t, "ops/plain", "0.0.0-auto.0"))
	reg := f.registry(nil)
	res, err := admLoad(reg, "ops/plain")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	want, err := manifest.SynthesizedSkillDocument("0.0.0-auto.0")
	if err != nil {
		t.Fatalf("SynthesizedSkillDocument: %v", err)
	}
	if !bytes.Equal(res.Frontmatter, want) || res.Type != "skill" || res.Version != "0.0.0-auto.0" {
		t.Errorf("load = type %q version %q frontmatter %q, want skill 0.0.0-auto.0 %q", res.Type, res.Version, res.Frontmatter, want)
	}
	if got := searchDescriptor(t, reg, "ops/plain").Frontmatter; got != "---\ntype: skill\nversion: 0.0.0-auto.0\n---\n" {
		t.Errorf("descriptor frontmatter = %q, want the synthesized block", got)
	}
}

// Spec: §4.3.4 — a plain-skill row whose stored version the synthesized
// document cannot carry fails the read rather than serving a document with
// an injected value, and its search descriptor carries an empty block.
func TestPlainSkill_SynthesisFailureFailsTheRead(t *testing.T) {
	t.Parallel()
	f := newAdmFixture(t)
	f.put(t, plainSkillRow(t, "ops/plain", "1.0.0-rc.1"))
	reg := f.registry(nil)
	if res, err := admLoadVersion(reg, "ops/plain", "1.0.0-rc.1"); err == nil {
		t.Fatalf("LoadArtifact = %+v, want an error for an unsynthesizable version", res)
	}
	if got := searchDescriptor(t, reg, "ops/plain").Frontmatter; got != "" {
		t.Errorf("descriptor frontmatter = %q, want the empty block", got)
	}
}
