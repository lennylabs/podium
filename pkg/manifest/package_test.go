package manifest

import (
	"errors"
	"testing"
)

// Spec: §4.7.6, §4.1, §4.3.4 — ResolvePackage resolves a package from its
// root documents, and every outcome that is not a parsed ARTIFACT.md of
// another type, a parsed skill, or a parsed plain skill is unresolved. The
// failing document and the role of the root SKILL.md are reported for the
// walk's refusal reason and for the package digest's entry rule.
func TestResolvePackage_Outcomes(t *testing.T) {
	t.Parallel()
	skillMD := []byte("---\nname: s\ndescription: d\nmetadata:\n  sensitivity: high\n  deprecated: \"true\"\n  replaced_by: a/b\n---\nBody.\n")
	badYAML := []byte("---\nname: [s\n---\n")
	cases := []struct {
		name         string
		docs         PackageDocuments
		wantResolved bool
		wantFields   ResolvedFields
		wantFile     string
		wantErr      error
		wantSkillDoc bool
	}{
		{
			name:         "agent",
			docs:         PackageDocuments{ArtifactMD: []byte("---\ntype: agent\nsensitivity: medium\nreplaced_by: x/y\n---\nBody.\n"), HasArtifactMD: true, SkillMD: skillMD, HasSkillMD: true},
			wantResolved: true,
			wantFields:   ResolvedFields{Type: TypeAgent, Sensitivity: SensitivityMedium, ReplacedBy: "x/y"},
		},
		{
			name:         "skill with an overlay that states an empty sensitivity",
			docs:         PackageDocuments{ArtifactMD: []byte("---\ntype: skill\nsensitivity: \"\"\n---\n"), HasArtifactMD: true, SkillMD: skillMD, HasSkillMD: true},
			wantResolved: true,
			wantFields:   ResolvedFields{Type: TypeSkill, Deprecated: true, ReplacedBy: "a/b"},
			wantSkillDoc: true,
		},
		{
			name:         "plain skill from metadata",
			docs:         PackageDocuments{SkillMD: skillMD, HasSkillMD: true},
			wantResolved: true,
			wantFields:   ResolvedFields{Type: TypeSkill, Sensitivity: SensitivityHigh, Deprecated: true, ReplacedBy: "a/b"},
			wantSkillDoc: true,
		},
		{
			name:     "ARTIFACT.md does not parse",
			docs:     PackageDocuments{ArtifactMD: badYAML, HasArtifactMD: true, SkillMD: skillMD, HasSkillMD: true},
			wantFile: ArtifactFile,
			wantErr:  ErrInvalidYAML,
		},
		{
			name:     "zero-length ARTIFACT.md",
			docs:     PackageDocuments{ArtifactMD: []byte{}, HasArtifactMD: true, SkillMD: skillMD, HasSkillMD: true},
			wantFile: ArtifactFile,
			wantErr:  ErrNoFrontmatter,
		},
		{
			name:         "type: skill with no SKILL.md",
			docs:         PackageDocuments{ArtifactMD: []byte("---\ntype: skill\n---\n"), HasArtifactMD: true},
			wantFile:     SkillFile,
			wantErr:      ErrSkillDocumentMissing,
			wantSkillDoc: true,
		},
		{
			name:         "type: skill whose SKILL.md does not parse",
			docs:         PackageDocuments{ArtifactMD: []byte("---\ntype: skill\n---\n"), HasArtifactMD: true, SkillMD: badYAML, HasSkillMD: true},
			wantFile:     SkillFile,
			wantErr:      ErrInvalidYAML,
			wantSkillDoc: true,
		},
		{
			name:         "type: skill whose SKILL.md is zero-length",
			docs:         PackageDocuments{ArtifactMD: []byte("---\ntype: skill\n---\n"), HasArtifactMD: true, SkillMD: []byte{}, HasSkillMD: true},
			wantFile:     SkillFile,
			wantErr:      ErrNoFrontmatter,
			wantSkillDoc: true,
		},
		{
			name:         "plain skill whose SKILL.md does not parse",
			docs:         PackageDocuments{SkillMD: badYAML, HasSkillMD: true},
			wantFile:     SkillFile,
			wantErr:      ErrInvalidYAML,
			wantSkillDoc: true,
		},
		{
			name:         "no document at all",
			docs:         PackageDocuments{},
			wantFile:     SkillFile,
			wantErr:      ErrSkillDocumentMissing,
			wantSkillDoc: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := ResolvePackage(tc.docs)
			if res.Resolved != tc.wantResolved {
				t.Fatalf("Resolved = %v, want %v (err %v)", res.Resolved, tc.wantResolved, res.Err)
			}
			if res.SkillDocument() != tc.wantSkillDoc {
				t.Errorf("SkillDocument() = %v, want %v", res.SkillDocument(), tc.wantSkillDoc)
			}
			if !tc.wantResolved {
				if res.FailedFile != tc.wantFile || !errors.Is(res.Err, tc.wantErr) {
					t.Errorf("failure = %s %v, want %s %v", res.FailedFile, res.Err, tc.wantFile, tc.wantErr)
				}
				if res.Artifact != nil || res.Fields != (ResolvedFields{}) {
					t.Errorf("unresolved package carries values: %+v %+v", res.Artifact, res.Fields)
				}
				return
			}
			if res.Fields != tc.wantFields {
				t.Errorf("Fields = %+v, want %+v", res.Fields, tc.wantFields)
			}
		})
	}
}

// Spec: §4.7.6 — a resolved value that is not valid UTF-8 leaves the package
// unresolved. The YAML decoder yields no such value, so the branch is driven
// through the constructor that every resolution passes.
func TestResolvePackage_InvalidUTF8IsUnresolved(t *testing.T) {
	t.Parallel()
	for _, a := range []*Artifact{
		{Type: ArtifactType("ru\xffle")},
		{Type: TypeRule, Sensitivity: Sensitivity("\xc3")},
		{Type: TypeRule, ReplacedBy: "a/\xfe"},
	} {
		res := resolved(a, nil, nil, false)
		if res.Resolved || !errors.Is(res.Err, ErrInvalidUTF8) {
			t.Errorf("resolved(%+v) = %+v, want unresolved with ErrInvalidUTF8", a, res)
		}
	}
}

// Spec: §4.7.6, §13.4 — a stored row's empty manifest and empty SKILL.md read
// as absent documents, and a plain-skill row resolves through the same
// function as a walked package.
func TestStoredDocuments(t *testing.T) {
	t.Parallel()
	docs := StoredDocuments(nil, []byte("---\nname: s\ndescription: d\n---\n"))
	if docs.HasArtifactMD || !docs.HasSkillMD {
		t.Fatalf("StoredDocuments = %+v, want no ARTIFACT.md and a SKILL.md", docs)
	}
	res := ResolvePackage(docs)
	if !res.Resolved || res.Artifact.Type != TypeSkill || res.Artifact.Description != "d" {
		t.Errorf("plain-skill row = %+v, want a resolved skill with the SKILL.md description", res)
	}
	if got := StoredDocuments([]byte("---\ntype: rule\n---\nBody.\n"), []byte{}); !got.HasArtifactMD || got.HasSkillMD {
		t.Errorf("StoredDocuments of a rule row = %+v", got)
	}
}
