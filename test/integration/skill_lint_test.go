package integration

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// TestPodiumLint_HookMissingEventErrors covers
// Spec: §4.3 hook schema — hook_event is a required field of a type: hook
// artifact; the real binary's lint pipeline rejects a hook that omits it.
func TestPodiumLint_HookMissingEventErrors(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path:    "hooks/no-event/ARTIFACT.md",
		Content: "---\ntype: hook\nversion: 1.0.0\ndescription: a hook\nhook_action: |\n  echo hi\n---\n\nbody\n",
	})
	res := cmdharness.Run(t, "podium", "", "lint", "--registry", registry)
	if res.ExitCode != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[error]") || !strings.Contains(res.Stdout, "hook_event") {
		t.Errorf("expected an error naming hook_event:\n%s", res.Stdout)
	}
}

// TestPodiumLint_RuleGlobMissingGlobsErrors covers
// Spec: §4.3 rule_mode table — rule_globs is required when rule_mode: glob.
func TestPodiumLint_RuleGlobMissingGlobsErrors(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path:    "style/react/ARTIFACT.md",
		Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: glob\n---\n\nrule body\n",
	})
	res := cmdharness.Run(t, "podium", "", "lint", "--registry", registry)
	if res.ExitCode != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "rule_globs") {
		t.Errorf("expected an error naming rule_globs:\n%s", res.Stdout)
	}
}

// TestPodiumLint_RuleModeOutOfEnumErrors covers
// Spec: 04-artifact-model.md §4.3 — rule_mode is the closed enumeration
// always | glob | auto | explicit; the real binary's lint pipeline rejects
// an out-of-enum value such as rule_mode: sometimes.
func TestPodiumLint_RuleModeOutOfEnumErrors(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path:    "rules/bad-mode/ARTIFACT.md",
		Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: sometimes\n---\n\nrule body\n",
	})
	res := cmdharness.Run(t, "podium", "", "lint", "--registry", registry)
	if res.ExitCode != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.unknown_rule_mode") || !strings.Contains(res.Stdout, "sometimes") {
		t.Errorf("expected an unknown_rule_mode error naming the bad value:\n%s", res.Stdout)
	}
}

// TestPodiumLint_MCPServersUntranslatableErrors covers
// Spec: §6.7.1 — mcpServers is graded ✗ for codex (the TOML agent translation
// drops it). An agent that declares mcpServers and targets codex is an ingest
// error. Before the fix the lint never evaluated the mcpServers row, so the
// combination passed silently and the field was lost.
func TestPodiumLint_MCPServersUntranslatableErrors(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{
		Path: "agents/warehouse/ARTIFACT.md",
		Content: "---\ntype: agent\nversion: 1.0.0\ndescription: a.\n" +
			"mcpServers:\n  - name: finance-warehouse\n    command: npx\n" +
			"target_harnesses: [codex]\n---\n\nbody\n",
	})
	res := cmdharness.Run(t, "podium", "", "lint", "--registry", registry)
	if res.ExitCode != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[error]") || !strings.Contains(res.Stdout, "mcpServers") {
		t.Errorf("expected an error naming mcpServers:\n%s", res.Stdout)
	}
}

// TestPodiumLint_SkillPodiumFieldErrors covers
// Spec: §4.3.4 — a Podium-only field in SKILL.md is an ingest error; the
// field belongs in ARTIFACT.md. ParseSkill drops the key, so the lint
// pipeline must scan the raw frontmatter to catch it.
func TestPodiumLint_SkillPodiumFieldErrors(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path:    "greetings/hello/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\n---\n\n",
		},
		testharness.WriteTreeOption{
			Path:    "greetings/hello/SKILL.md",
			Content: "---\nname: hello\ndescription: Greet the user.\nwhen_to_use:\n  - greeting\n---\n\nbody\n",
		},
	)
	res := cmdharness.Run(t, "podium", "", "lint", "--registry", registry)
	if res.ExitCode != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "when_to_use") {
		t.Errorf("expected an error naming the Podium-only field when_to_use:\n%s", res.Stdout)
	}
}

// TestPodiumSync_DerivesSkillCompatibility covers
// Spec: §4.3.4 — when SKILL.md omits compatibility, the claude-code adapter
// (which consumes only the agentskills.io subset) derives it from
// runtime_requirements/sandbox_profile and injects it into the materialized
// SKILL.md. End-to-end through the real binary's sync path.
func TestPodiumSync_DerivesSkillCompatibility(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path:    "greetings/hello/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.10\"\nsandbox_profile: read-only-fs\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		},
		testharness.WriteTreeOption{
			Path:    "greetings/hello/SKILL.md",
			Content: "---\nname: hello\ndescription: Greet the user.\n---\n\nSkill body.\n",
		},
	)
	res := cmdharness.Run(t, "podium", "", "sync", "--registry", registry, "--target", target, "--harness", "claude-code")
	if res.ExitCode != 0 {
		t.Fatalf("sync exit=%d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	got := testharness.ReadTree(t, target)
	skill, ok := got[".claude/skills/hello/SKILL.md"]
	if !ok {
		t.Fatalf("materialized SKILL.md missing (got: %v)", keys(got))
	}
	if !strings.Contains(skill, "compatibility:") || !strings.Contains(skill, "Python >=3.10") {
		t.Errorf("derived compatibility missing from SKILL.md:\n%s", skill)
	}
	if !strings.Contains(skill, "sandbox: read-only-fs") {
		t.Errorf("derived compatibility missing sandbox detail:\n%s", skill)
	}
}
