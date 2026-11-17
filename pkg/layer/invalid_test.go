package layer

import (
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// Spec: §4.1, §4.6, §13.11.3 — a filesystem-source composition reports an
// ARTIFACT.md the no-inheritance rule or the removed-type rule refuses as an
// ingest.invalid_artifact drop whose details carry exactly the constraint.
func TestRemovedFeatureDrop(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, constraint, reason string
	}{
		{"extends", "extends_removed", ExtendsRemovedReason},
		{"type removed", "type_removed", TypeRemovedReason("command")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := Drop{
				ArtifactID: "a/b",
				Code:       "ingest.invalid_artifact",
				Reason:     tc.reason,
				Details:    map[string]any{"constraint": tc.constraint},
			}
			if got := RemovedFeatureDrop("a/b", tc.constraint, tc.reason); !reflect.DeepEqual(got, want) {
				t.Errorf("RemovedFeatureDrop(a/b) = %+v, want %+v", got, want)
			}
		})
	}
	const wantReason = "extends: is not supported; remove the extends: key and declare every field this artifact needs in its own frontmatter"
	if ExtendsRemovedReason != wantReason {
		t.Errorf("ExtendsRemovedReason = %q, want %q", ExtendsRemovedReason, wantReason)
	}
	if ExtendsRemovedConstraint != "extends_removed" {
		t.Errorf("ExtendsRemovedConstraint = %q, want extends_removed", ExtendsRemovedConstraint)
	}
	if TypeRemovedConstraint != "type_removed" {
		t.Errorf("TypeRemovedConstraint = %q, want type_removed", TypeRemovedConstraint)
	}
	if InvalidArtifactCode != "ingest.invalid_artifact" {
		t.Errorf("InvalidArtifactCode = %q, want ingest.invalid_artifact", InvalidArtifactCode)
	}
}

// Spec: §4.1 — the removed-type reason names the type, its replacement, and
// the upgrade notes, and it names no layer.
func TestTypeRemovedReason(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"command": `type: command is not supported; a command is a skill whose metadata.invocation is "user"; the 0.7.0 upgrade notes describe how to convert an existing package`,
		"context": "type: context is not supported; reference material is a skill's references/ resources or a skill that carries reference material; the 0.7.0 upgrade notes describe how to convert an existing package",
	}
	for typ, want := range cases {
		if got := TypeRemovedReason(typ); got != want {
			t.Errorf("TypeRemovedReason(%q) = %q, want %q", typ, got, want)
		}
	}
}

// Spec: §4.1, §4.6 — the classifier checks extends: before the removed type,
// so an artifact that trips both reports extends_removed, an artifact that
// trips one reports that rule, and an artifact that trips neither is
// admitted.
func TestRemovedFeature_Order(t *testing.T) {
	t.Parallel()
	extends := []byte("---\ntype: command\nversion: 1.0.0\nextends: a/b@1.0.0\n---\n\nbody\n")
	extendsOnly := []byte("---\ntype: agent\nversion: 1.0.0\nextends: a/b@1.0.0\n---\n\nbody\n")
	plain := []byte("---\ntype: agent\nversion: 1.0.0\n---\n\nbody\n")
	cases := []struct {
		name           string
		authored       []byte
		typ            manifest.ArtifactType
		wantConstraint string
		wantReason     string
		wantOK         bool
	}{
		{"extends wins over a removed type", extends, "command", ExtendsRemovedConstraint, ExtendsRemovedReason, true},
		{"extends only", extendsOnly, manifest.TypeAgent, ExtendsRemovedConstraint, ExtendsRemovedReason, true},
		{"command", plain, "command", TypeRemovedConstraint, TypeRemovedReason("command"), true},
		{"context", plain, "context", TypeRemovedConstraint, TypeRemovedReason("context"), true},
		{"remaining type", plain, manifest.TypeAgent, "", "", false},
		{"plain skill with no ARTIFACT.md", nil, manifest.TypeSkill, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			constraint, reason, ok := RemovedFeature(tc.authored, tc.typ)
			if constraint != tc.wantConstraint || reason != tc.wantReason || ok != tc.wantOK {
				t.Errorf("RemovedFeature = (%q, %q, %v), want (%q, %q, %v)",
					constraint, reason, ok, tc.wantConstraint, tc.wantReason, tc.wantOK)
			}
		})
	}
}
