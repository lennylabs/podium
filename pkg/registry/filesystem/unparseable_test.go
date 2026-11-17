package filesystem

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
)

// nestedMetadataSkill is a SKILL.md whose metadata holds a mapping, which
// §4.3.4 does not allow, so it does not parse.
const nestedMetadataSkill = "---\nname: nested\ndescription: Nested metadata.\nmetadata:\n  author:\n    name: alice\n---\n\nBody.\n"

// invalidYAMLArtifact is an ARTIFACT.md whose frontmatter is not valid YAML.
const invalidYAMLArtifact = "---\ntype: agent\nweird: [unclosed\n---\n\nBody.\n"

// unparseableDrop returns the drop the walk reports for package id whose
// manifest file src does not parse. skill selects the SKILL.md parser.
func unparseableDrop(t *testing.T, id, file, src string, skill bool) layer.Drop {
	t.Helper()
	var err error
	if skill {
		_, err = manifest.ParseSkill([]byte(src))
	} else {
		_, err = manifest.ParseArtifact([]byte(src))
	}
	if err == nil {
		t.Fatalf("fixture %s parses", file)
	}
	return layer.RemovedFeatureDrop(id, layer.ManifestUnparseableConstraint, layer.UnparseableReason(id+"/"+file, err))
}

// Spec: §4.1, §4.6, §13.11.3 — Refusal applies the unparseable-manifest rule
// before the no-inheritance and removed-type rules, so a record whose
// manifest did not parse reports manifest_unparseable even when its authored
// bytes declare extends: and a removed type. A record that parsed falls
// through to the later rules, and one that no rule refuses is admitted.
func TestArtifactRecord_Refusal(t *testing.T) {
	t.Parallel()
	extendsCommand := []byte("---\ntype: command\nextends: a/base\n---\n\nBody.\n")
	unparseable := UnparseableRecord("a/b", Layer{ID: "l1"}, "SKILL.md", manifest.ErrNoFrontmatter)
	unparseable.AuthoredBytes = extendsCommand
	unparseable.Artifact = &manifest.Artifact{Type: "command"}
	cases := []struct {
		name                   string
		rec                    ArtifactRecord
		wantConstraint, reason string
		wantOK                 bool
	}{
		{"unparseable first", unparseable, layer.ManifestUnparseableConstraint,
			"a/b/SKILL.md: " + manifest.ErrNoFrontmatter.Error(), true},
		{"extends next", ArtifactRecord{AuthoredBytes: extendsCommand, Artifact: &manifest.Artifact{Type: "command"}},
			layer.ExtendsRemovedConstraint, layer.ExtendsRemovedReason, true},
		{"removed type last", ArtifactRecord{Artifact: &manifest.Artifact{Type: "context"}},
			layer.TypeRemovedConstraint, layer.TypeRemovedReason("context"), true},
		{"admitted", ArtifactRecord{Artifact: &manifest.Artifact{Type: manifest.TypeAgent}}, "", "", false},
	}
	for _, tc := range cases {
		constraint, reason, ok := tc.rec.Refusal()
		if constraint != tc.wantConstraint || reason != tc.reason || ok != tc.wantOK {
			t.Errorf("%s: Refusal() = %q, %q, %v, want %q, %q, %v",
				tc.name, constraint, reason, ok, tc.wantConstraint, tc.reason, tc.wantOK)
		}
	}
	if strings.Contains(unparseable.Unparseable, "l1") {
		t.Errorf("reason %q names the layer", unparseable.Unparseable)
	}
}

// Spec: §4.1, §13.11.3 — the walk drops a plain SKILL.md with nested
// metadata, an ARTIFACT.md that is not valid YAML, an ARTIFACT.md with no
// frontmatter, and a skill package whose SKILL.md does not parse beside an
// ARTIFACT.md that declares extends:, and returns the valid siblings. Each
// drop carries manifest_unparseable, never extends_removed. The refusing
// policies (lint's default with OnDrop, and sync's drop policy) report the
// same drops; with OnDrop nil the walk fails with the reason, and under
// CollisionPolicyHighestWins the workspace overlay fails because it has no
// parsed artifact to serve.
func TestWalk_UnparseableManifestDropped(t *testing.T) {
	t.Parallel()
	extendsSkill := "---\ntype: skill\nversion: 1.0.0\nextends: l1/keep\n---\n\n"
	files := map[string]string{
		"l1/plain/SKILL.md":     nestedMetadataSkill,
		"l1/yaml/ARTIFACT.md":   invalidYAMLArtifact,
		"l1/nofm/ARTIFACT.md":   "no frontmatter\n",
		"l1/nofm/refs/SKILL.md": plainSkill("refs", "Refs skill.", ""),
		"l1/order/ARTIFACT.md":  extendsSkill,
		"l1/order/SKILL.md":     nestedMetadataSkill,
		"l1/keep/ARTIFACT.md":   dropFixtureArtifact("plain", ""),
		"l1/good/SKILL.md":      plainSkill("good", "Good skill.", ""),
	}
	want := []layer.Drop{
		unparseableDrop(t, "nofm", "ARTIFACT.md", "no frontmatter\n", false),
		unparseableDrop(t, "order", "SKILL.md", nestedMetadataSkill, true),
		unparseableDrop(t, "plain", "SKILL.md", nestedMetadataSkill, true),
		unparseableDrop(t, "yaml", "ARTIFACT.md", invalidYAMLArtifact, false),
	}
	reg := openLayeredTree(t, []string{"l1"}, files)
	for _, policy := range []CollisionPolicy{CollisionPolicyDefault, CollisionPolicyDrop} {
		got, drops, err := walkCollecting(reg, policy, true)
		if err != nil {
			t.Fatalf("policy %d: Walk: %v", policy, err)
		}
		if !reflect.DeepEqual(drops, want) {
			t.Errorf("policy %d: OnDrop calls = %+v, want %+v", policy, drops, want)
		}
		// nofm/refs stays a bundled file of the refused package under §4.2,
		// so it is neither kept nor refused as a plain skill.
		if ids := idsOf(got); !reflect.DeepEqual(ids, []string{"good", "keep"}) {
			t.Errorf("policy %d: kept = %v, want [good keep]", policy, ids)
		}
	}

	single := openLayeredTree(t, []string{"l1"}, map[string]string{"l1/plain/SKILL.md": nestedMetadataSkill})
	_, _, err := walkCollecting(single, CollisionPolicyDefault, false)
	if err == nil || !strings.Contains(err.Error(), layer.InvalidArtifactCode) ||
		!strings.Contains(err.Error(), "plain/SKILL.md: manifest: invalid YAML frontmatter") {
		t.Errorf("Walk without OnDrop err = %v, want the unparseable reason under %s", err, layer.InvalidArtifactCode)
	}
	for _, collect := range []bool{false, true} {
		_, drops, err := walkCollecting(single, CollisionPolicyHighestWins, collect)
		if err == nil || !strings.Contains(err.Error(), "plain/SKILL.md: manifest: invalid YAML frontmatter") || len(drops) != 0 {
			t.Errorf("HighestWins collect=%v: err %v, drops %+v; want the parse failure and no drop", collect, err, drops)
		}
	}
}

// Spec: §4.1, §4.6, §13.11.3 — an unparseable artifact is dropped before the
// collision rule, so it collides with no artifact of the same ID in another
// layer. Under the default policy, which fails on a collision, the walk keeps
// the parsed copy and reports only the refusal, whichever layer holds the
// unparseable copy.
func TestWalk_UnparseableManifestCollidesWithNothing(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"lower", "upper"} {
		good := "upper"
		if bad == "upper" {
			good = "lower"
		}
		reg := openLayeredTree(t, []string{"lower", "upper"}, map[string]string{
			bad + "/x/SKILL.md":  nestedMetadataSkill,
			good + "/x/SKILL.md": plainSkill("x", "Parsed skill.", ""),
		})
		got, drops, err := walkCollecting(reg, CollisionPolicyDefault, true)
		if err != nil {
			t.Fatalf("unparseable in %s: Walk: %v", bad, err)
		}
		if want := []layer.Drop{unparseableDrop(t, "x", "SKILL.md", nestedMetadataSkill, true)}; !reflect.DeepEqual(drops, want) {
			t.Errorf("unparseable in %s: OnDrop calls = %+v, want %+v", bad, drops, want)
		}
		if len(got) != 1 || got[0].Layer.ID != good {
			t.Errorf("unparseable in %s: kept %v, want x from %s", bad, idsOf(got), good)
		}
	}
}

// Spec: §4.1, §4.2 — only a manifest that does not parse is refused per
// artifact. A root-level manifest has no canonical ID, so the directory
// cannot hold an artifact at all, and it fails the walk even with OnDrop set
// and an unparseable sibling the walk would otherwise drop.
func TestWalk_StructuralFailureStillFailsBesideUnparseable(t *testing.T) {
	t.Parallel()
	reg := openLayeredTree(t, []string{"l1"}, map[string]string{
		"l1/SKILL.md":       plainSkill("root", "Root skill.", ""),
		"l1/plain/SKILL.md": nestedMetadataSkill,
	})
	_, drops, err := walkCollecting(reg, CollisionPolicyDrop, true)
	if err == nil || !strings.Contains(err.Error(), "subdirectory") {
		t.Fatalf("Walk err = %v, want the root-level manifest to fail the walk", err)
	}
	if len(drops) != 0 {
		t.Errorf("OnDrop calls = %+v, want none from a failed walk", drops)
	}
	if errors.Is(err, manifest.ErrInvalidYAML) {
		t.Errorf("Walk err = %v reports the sibling's parse failure", err)
	}
}
