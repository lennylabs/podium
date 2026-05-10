package adapter_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

const provenanceSkill = `---
name: aggregate
description: aggregate
---

Authored intro.

<!-- begin imported source="https://wiki.example/policy" -->
Imported policy text.
<!-- end imported -->

Authored conclusion.
`

// Spec: §4.4.2 — Adapters propagate provenance markers to
// harnesses that support trust regions. Claude Code rewrites
// `<!-- begin imported source="X" -->...<!-- end imported -->`
// blocks as `<untrusted-data source="X">...</untrusted-data>` so
// Claude can apply differential trust at read time.
func TestClaudeCode_RewritesProvenanceMarkers(t *testing.T) {
	t.Parallel()
	out, err := adapter.ClaudeCode{}.Adapt(adapter.Source{
		ArtifactID:    "team/aggregate",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nname: aggregate\n---\n"),
		SkillBytes:    []byte(provenanceSkill),
	})
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	body := ""
	for _, f := range out {
		if strings.HasSuffix(f.Path, "SKILL.md") {
			body = string(f.Content)
		}
	}
	if body == "" {
		t.Fatalf("SKILL.md not in output: %+v", out)
	}
	if !strings.Contains(body, "<untrusted-data source=\"https://wiki.example/policy\">") {
		t.Errorf("Claude Code did not rewrite to <untrusted-data>: %s", body)
	}
	if !strings.Contains(body, "</untrusted-data>") {
		t.Errorf("Claude Code did not close <untrusted-data>: %s", body)
	}
	if strings.Contains(body, "begin imported") {
		t.Errorf("Claude Code left the begin-imported marker behind: %s", body)
	}
	// Authored prose around the block should pass through verbatim.
	if !strings.Contains(body, "Authored intro.") || !strings.Contains(body, "Authored conclusion.") {
		t.Errorf("authored prose missing: %s", body)
	}
}

// Spec: §4.4.2 — adapters that don't have a trust-region
// convention preserve the markers unchanged so downstream tooling
// can detect them. The "none" adapter is a passthrough sentinel
// for that case.
func TestNoneAdapter_PreservesProvenanceMarkers(t *testing.T) {
	t.Parallel()
	out, err := adapter.None{}.Adapt(adapter.Source{
		ArtifactID:    "team/aggregate",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nname: aggregate\n---\n"),
		SkillBytes:    []byte(provenanceSkill),
	})
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	body := ""
	for _, f := range out {
		if strings.HasSuffix(f.Path, "SKILL.md") {
			body = string(f.Content)
		}
	}
	if !strings.Contains(body, "<!-- begin imported") {
		t.Errorf("none adapter dropped the marker: %s", body)
	}
}

// Spec: §4.4.2 — multiple imported blocks within one body all
// get rewritten.
func TestClaudeCode_RewritesMultipleImportedBlocks(t *testing.T) {
	t.Parallel()
	body := []byte(`---
name: x
description: x
---

<!-- begin imported source="A" -->
first
<!-- end imported -->

between

<!-- begin imported source="B" -->
second
<!-- end imported -->
`)
	out, _ := adapter.ClaudeCode{}.Adapt(adapter.Source{
		ArtifactID:    "team/x",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nname: x\n---\n"),
		SkillBytes:    body,
	})
	got := ""
	for _, f := range out {
		if strings.HasSuffix(f.Path, "SKILL.md") {
			got = string(f.Content)
		}
	}
	if strings.Count(got, "<untrusted-data") != 2 {
		t.Errorf("want 2 untrusted-data blocks, got %d:\n%s",
			strings.Count(got, "<untrusted-data"), got)
	}
}

// Spec: §4.4.2 — bodies without provenance markers pass
// through unchanged (no-op when there's nothing to rewrite).
func TestClaudeCode_NoMarkersPassesThrough(t *testing.T) {
	t.Parallel()
	plain := []byte(`---
name: x
description: x
---

Plain authored body.
`)
	out, _ := adapter.ClaudeCode{}.Adapt(adapter.Source{
		ArtifactID:    "team/x",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nname: x\n---\n"),
		SkillBytes:    plain,
	})
	for _, f := range out {
		if strings.HasSuffix(f.Path, "SKILL.md") {
			if string(f.Content) != string(plain) {
				t.Errorf("plain body changed:\nin:  %q\nout: %q", plain, f.Content)
			}
		}
	}
}
