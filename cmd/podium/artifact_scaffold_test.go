package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// runScaffold invokes the testable core with stdin/stdout/stderr
// buffers and returns the exit code plus captured output. Args are
// passed verbatim; per the codebase convention (mirroring
// `artifact show` and `lint`), the positional <path> goes last so
// stdlib flag.Parse picks up every preceding flag.
func runScaffold(args []string, stdin string) (exit int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	exit = artifactScaffoldWithIO(args, strings.NewReader(stdin), &out, &errOut)
	return exit, out.String(), errOut.String()
}

// assertLintClean opens the directory at root as a filesystem registry
// and runs the full lint rule set. Any error-severity diagnostic
// fails the test. Warnings and info diagnostics are tolerated so the
// scaffolder is checked against the same gate ingest uses.
func assertLintClean(t *testing.T, root string) {
	t.Helper()
	reg, err := filesystem.Open(root)
	if err != nil {
		t.Fatalf("filesystem.Open(%s): %v", root, err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(records) == 0 {
		t.Fatalf("no records discovered under %s", root)
	}
	linter := &lint.Linter{}
	diags := linter.Lint(reg, records)
	for _, d := range diags {
		if d.Severity == lint.SeverityError {
			t.Errorf("lint error: %s", d)
		}
	}
}

func TestScaffold_Skill_LintClean(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "greet")
	exit, _, stderr := runScaffold([]string{
		"--type", "skill",
		"--description", "Greet the user by name.",
		"--tags", "demo,greeting",
		"--license", "MIT",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	for _, f := range []string{"ARTIFACT.md", "SKILL.md"} {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Errorf("expected %s: %v", f, err)
		}
	}
	// SKILL.md carries the agentskills.io fields; ARTIFACT.md must
	// not duplicate them (lint rule §4.3.4).
	skill, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if !bytes.Contains(skill, []byte("name: greet")) {
		t.Errorf("SKILL.md missing name: %s", skill)
	}
	if !bytes.Contains(skill, []byte("description: Greet the user by name.")) {
		t.Errorf("SKILL.md missing description: %s", skill)
	}
	if !bytes.Contains(skill, []byte("license: MIT")) {
		t.Errorf("SKILL.md missing license: %s", skill)
	}
	artifact, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if bytes.Contains(artifact, []byte("name:")) {
		t.Errorf("ARTIFACT.md must not carry name: for skills (§4.3.4): %s", artifact)
	}
	if bytes.Contains(artifact, []byte("description:")) {
		t.Errorf("ARTIFACT.md must not carry description: for skills (§4.3.4): %s", artifact)
	}
	assertLintClean(t, root)
}

func TestScaffold_Context_LintClean(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "company-glossary")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "Company glossary of acronyms.",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err == nil {
		t.Errorf("context type should not produce SKILL.md")
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("type: context")) {
		t.Errorf("ARTIFACT.md missing type: %s", body)
	}
	if !bytes.Contains(body, []byte("name: company-glossary")) {
		t.Errorf("ARTIFACT.md missing name: %s", body)
	}
	assertLintClean(t, root)
}

func TestScaffold_Command_LintClean(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "code-change-pr")
	exit, _, stderr := runScaffold([]string{
		"--type", "command",
		"--description", "Open a PR from the working tree.",
		"--expose-as-mcp-prompt",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("expose_as_mcp_prompt: true")) {
		t.Errorf("expected expose_as_mcp_prompt: true, got: %s", body)
	}
	assertLintClean(t, root)
}

func TestScaffold_Agent_LintClean(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "release-orchestrator")
	exit, _, stderr := runScaffold([]string{
		"--type", "agent",
		"--description", "Coordinate the release across services.",
		"--input-schema", "./schemas/input.json",
		"--output-schema", "./schemas/output.json",
		"--delegates-to", "finance/ap/pay-invoice@1.x,finance/close-reporting/run-variance-analysis@1.x",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("input: ./schemas/input.json")) {
		t.Errorf("missing input ref: %s", body)
	}
	if !bytes.Contains(body, []byte("- finance/ap/pay-invoice@1.x")) {
		t.Errorf("missing delegates_to entry: %s", body)
	}
	assertLintClean(t, root)
}

func TestScaffold_Rule_Glob_LintClean(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "ts-style")
	exit, _, stderr := runScaffold([]string{
		"--type", "rule",
		"--description", "TypeScript house style rules.",
		"--rule-mode", "glob",
		"--rule-globs", "src/**/*.ts,src/**/*.tsx",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("rule_mode: glob")) {
		t.Errorf("missing rule_mode: %s", body)
	}
	if !bytes.Contains(body, []byte("rule_globs:")) {
		t.Errorf("missing rule_globs: %s", body)
	}
	assertLintClean(t, root)
}

func TestScaffold_Rule_GlobRequiresGlobs(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "ts-style")
	exit, _, stderr := runScaffold([]string{
		"--type", "rule",
		"--description", "x",
		"--rule-mode", "glob",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2 (missing --rule-globs)", exit)
	}
	if !strings.Contains(stderr, "rule-globs required") {
		t.Errorf("stderr missing required-globs hint: %s", stderr)
	}
}

func TestScaffold_Rule_AutoRequiresDescription(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "db-migration-rule")
	exit, _, stderr := runScaffold([]string{
		"--type", "rule",
		"--description", "x",
		"--rule-mode", "auto",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2 (missing --rule-description)", exit)
	}
	if !strings.Contains(stderr, "rule-description required") {
		t.Errorf("stderr missing required-description hint: %s", stderr)
	}
}

func TestScaffold_Hook_LintClean(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "audit-stop")
	exit, _, stderr := runScaffold([]string{
		"--type", "hook",
		"--description", "Log session stops.",
		"--hook-event", "session_end",
		"--hook-action", "echo session ended",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("hook_event: session_end")) {
		t.Errorf("missing hook_event: %s", body)
	}
	if !bytes.Contains(body, []byte("hook_action: |")) {
		t.Errorf("missing hook_action block: %s", body)
	}
	assertLintClean(t, root)
}

func TestScaffold_Hook_RequiresEvent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "audit-stop")
	exit, _, stderr := runScaffold([]string{
		"--type", "hook",
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "hook-event required") {
		t.Errorf("stderr missing required-event hint: %s", stderr)
	}
}

func TestScaffold_MCPServer_LintClean(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "finance-warehouse")
	exit, _, stderr := runScaffold([]string{
		"--type", "mcp-server",
		"--description", "Finance data warehouse MCP server.",
		"--server-identifier", "npx:@company/finance-warehouse-mcp",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("server_identifier: npx:@company/finance-warehouse-mcp")) {
		t.Errorf("missing server_identifier: %s", body)
	}
	assertLintClean(t, root)
}

func TestScaffold_MCPServer_RequiresIdentifier(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "finance-warehouse")
	exit, _, stderr := runScaffold([]string{
		"--type", "mcp-server",
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "server-identifier required") {
		t.Errorf("stderr missing required-identifier hint: %s", stderr)
	}
}

func TestScaffold_DomainHierarchyInPath(t *testing.T) {
	root := t.TempDir()
	// <root>/team-shared/finance/ap/pay-invoice/ — the scaffolder
	// mkdir -p all intermediate domains.
	target := filepath.Join(root, "team-shared", "finance", "ap", "pay-invoice")
	exit, _, stderr := runScaffold([]string{
		"--type", "skill",
		"--description", "Pay an invoice via the AP system.",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	for _, f := range []string{"ARTIFACT.md", "SKILL.md"} {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Errorf("expected %s: %v", f, err)
		}
	}
	assertLintClean(t, root)
}

func TestScaffold_RejectsMissingType(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "--type required") {
		t.Errorf("stderr missing required-type hint: %s", stderr)
	}
}

func TestScaffold_RejectsMissingDescription(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "--description required") {
		t.Errorf("stderr missing required-description hint: %s", stderr)
	}
}

func TestScaffold_RejectsMissingPath(t *testing.T) {
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--yes",
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "missing positional") {
		t.Errorf("stderr missing positional-path hint: %s", stderr)
	}
}

func TestScaffold_RejectsBadName(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "Bad_Name") // underscore + uppercase
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "kebab-case") {
		t.Errorf("stderr missing kebab-case hint: %s", stderr)
	}
}

func TestScaffold_SkillNameRejectsConsecutiveHyphens(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "two--dashes")
	exit, _, stderr := runScaffold([]string{
		"--type", "skill",
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "consecutive hyphens") {
		t.Errorf("stderr missing consecutive-hyphens hint: %s", stderr)
	}
}

func TestScaffold_RejectsBadSensitivity(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--sensitivity", "ultra",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "low|medium|high") {
		t.Errorf("stderr missing sensitivity-hint: %s", stderr)
	}
}

func TestScaffold_RefusesExistingDir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 1 {
		t.Fatalf("exit=%d, want 1", exit)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("stderr missing already-exists hint: %s", stderr)
	}
}

func TestScaffold_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "stale"), []byte("old"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "fresh",
		"--force",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("description: fresh")) {
		t.Errorf("ARTIFACT.md not overwritten: %s", body)
	}
}

func TestScaffold_Interactive_TypePrompt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	// Pick option 3 (context).
	exit, _, stderr := runScaffold([]string{
		"--description", "x",
		target,
	}, "3\n")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("type: context")) {
		t.Errorf("interactive type pick did not yield context: %s", body)
	}
}

func TestScaffold_ExtensionTypeWarns(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "company-extension",
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0 (extension types are allowed), stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "not a first-class type") {
		t.Errorf("expected warning about extension type, got: %s", stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("type: company-extension")) {
		t.Errorf("expected type: company-extension, got: %s", body)
	}
}
