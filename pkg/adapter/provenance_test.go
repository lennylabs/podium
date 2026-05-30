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
	out, err := adapter.ClaudeCode{}.Adapt(context.Background(), adapter.Source{
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
	out, err := adapter.None{}.Adapt(context.Background(), adapter.Source{
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
	out, _ := adapter.ClaudeCode{}.Adapt(context.Background(), adapter.Source{
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

// nonSkillProvenanceArtifact builds an ARTIFACT.md (frontmatter + body)
// for a non-skill type whose body carries one imported provenance block.
func nonSkillProvenanceArtifact(ty string) []byte {
	return []byte("---\ntype: " + ty + "\nversion: 1.0.0\ndescription: aggregated knowledge\n---\n\n" +
		"Authored intro.\n\n" +
		"<!-- begin imported source=\"https://wiki.example/policy\" -->\n" +
		"Imported policy text.\n" +
		"<!-- end imported -->\n")
}

// Spec: §4.4.2 (F-4.4.3) — provenance rewriting must cover every type the
// Claude Code adapter materializes, not just skills. A context, agent, or
// rule body that aggregates external content carries imported blocks that
// must become <untrusted-data> regions so the host can apply differential
// trust. Before the fix only the skill path was rewritten.
func TestClaudeCode_RewritesProvenanceForNonSkillTypes(t *testing.T) {
	t.Parallel()
	for _, ty := range []string{"context", "agent", "rule", "command", "hook"} {
		ty := ty
		t.Run(ty, func(t *testing.T) {
			t.Parallel()
			out, err := adapter.ClaudeCode{}.Adapt(context.Background(), adapter.Source{
				ArtifactID:    "team/aggregate",
				ArtifactBytes: nonSkillProvenanceArtifact(ty),
			})
			if err != nil {
				t.Fatalf("Adapt(%s): %v", ty, err)
			}
			// Find the materialized manifest file (the one carrying the body).
			var body string
			for _, f := range out {
				if strings.Contains(string(f.Content), "Authored intro.") {
					body = string(f.Content)
				}
			}
			if body == "" {
				t.Fatalf("%s: no materialized body in output: %+v", ty, out)
			}
			if !strings.Contains(body, "<untrusted-data source=\"https://wiki.example/policy\">") {
				t.Errorf("%s: imported block not rewritten to <untrusted-data>:\n%s", ty, body)
			}
			if !strings.Contains(body, "</untrusted-data>") {
				t.Errorf("%s: <untrusted-data> not closed:\n%s", ty, body)
			}
			if strings.Contains(body, "begin imported") {
				t.Errorf("%s: raw begin-imported marker survived:\n%s", ty, body)
			}
			// Authored prose and the frontmatter are preserved.
			if !strings.Contains(body, "Authored intro.") || !strings.Contains(body, "type: "+ty) {
				t.Errorf("%s: authored prose or frontmatter dropped:\n%s", ty, body)
			}
		})
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
	out, _ := adapter.ClaudeCode{}.Adapt(context.Background(), adapter.Source{
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
