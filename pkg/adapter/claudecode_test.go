package adapter

import (
	"context"
	"testing"
)

// Spec: §6.7 — claude-code adapter places skills under .claude/skills/<name>/
// per the agentskills.io standard.
func TestClaudeCode_SkillLayout(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID: "finance/run-variance",
		ArtifactBytes: []byte(`---
type: skill
version: 1.0.0
---

`),
		SkillBytes: []byte("---\nname: run-variance\ndescription: x\n---\nbody\n"),
		Resources: map[string][]byte{
			"scripts/x.py": []byte("print('x')\n"),
		},
	}
	out, err := ClaudeCode{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	want := map[string]bool{
		".claude/skills/run-variance/SKILL.md":     true,
		".claude/skills/run-variance/scripts/x.py": true,
	}
	for _, f := range out {
		want[f.Path] = false
	}
	for path, missing := range want {
		if missing {
			t.Errorf("missing path: %s", path)
		}
	}
}

// Spec: §6.7 — claude-code adapter writes type: rule into .claude/rules/.
func TestClaudeCode_RuleLayout(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID: "ts-style",
		ArtifactBytes: []byte(`---
type: rule
version: 1.0.0
---

ts style rules
`),
	}
	out, err := ClaudeCode{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if len(out) != 1 || out[0].Path != ".claude/rules/ts-style.md" {
		t.Errorf("got %+v, want .claude/rules/ts-style.md", out)
	}
}

// Spec: §6.7 — claude-code adapter writes type: agent into
// .claude/agents/<name>.md.
func TestClaudeCode_AgentLayout(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "finance/pay-invoice",
		ArtifactBytes: []byte("---\ntype: agent\nversion: 1.0.0\n---\nbody\n"),
	}
	out, err := ClaudeCode{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if len(out) != 1 || out[0].Path != ".claude/agents/pay-invoice.md" {
		t.Errorf("got %+v, want .claude/agents/pay-invoice.md", out)
	}
}

// Spec: §6.7 — non-skill / non-rule / non-agent types land under
// .claude/podium/<artifact-id>/ with the canonical layout.
func TestClaudeCode_FallbackPathForOtherTypes(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "company-glossary",
		ArtifactBytes: []byte("---\ntype: context\nversion: 1.0.0\n---\nbody\n"),
	}
	out, err := ClaudeCode{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	// type: context materializes to the harness-neutral .podium/context/ bucket.
	if len(out) != 1 || out[0].Path != ".podium/context/company-glossary/ARTIFACT.md" {
		t.Errorf("got %+v", out)
	}
}

// Spec: §6.7 — the codex adapter injects rules into AGENTS.md between
// Podium-managed markers.
func TestCodex_RulePlacement(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "ts-style",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\n---\nrules\n"),
	}
	out, err := Codex{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if len(out) != 1 || out[0].Path != "AGENTS.md" || out[0].Op != OpInject || out[0].Key != "ts-style" {
		t.Errorf("got %+v, want a single AGENTS.md OpInject keyed by ts-style", out)
	}
}

// Spec: §6.7 — DefaultRegistry contains claude-code and codex once
// Phase 3 is active.
func TestDefaultRegistry_ContainsClaudeCodeAndCodex(t *testing.T) {
	t.Parallel()
	r := DefaultRegistry()
	for _, id := range []string{"claude-code", "codex"} {
		a, err := r.Get(id)
		if err != nil {
			t.Errorf("Get(%q): %v", id, err)
			continue
		}
		if a.ID() != id {
			t.Errorf("Get(%q).ID() = %q", id, a.ID())
		}
	}
}
