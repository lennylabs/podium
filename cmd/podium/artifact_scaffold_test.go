package main

import (
	"bufio"
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
	diags := linter.Lint(context.Background(), reg, records)
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

// ---------- Interactive prompt coverage ----------

// Interactive path: --description omitted, user types it. Confirms
// promptString round-trips a typed line into the description field
// of the rendered manifest.
func TestScaffold_Interactive_DescriptionPrompt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		target,
	}, "Reference content for the catalog.\n")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("description: Reference content for the catalog.")) {
		t.Errorf("description from prompt not written: %s", body)
	}
}

// Interactive type pick by name (typing the literal type string
// instead of an index). Exercises the contains() branch of
// promptChoice.
func TestScaffold_Interactive_TypePromptByName(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--description", "x",
		target,
	}, "agent\n")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("type: agent")) {
		t.Errorf("typed type-name did not yield agent: %s", body)
	}
}

// Interactive type pick that retries on invalid input. The first
// line ("99") is out of range and matches no type name; the loop
// reprompts and accepts the valid second line. Exercises the
// invalid-input retry branch of promptChoice.
func TestScaffold_Interactive_TypePromptRetriesOnInvalid(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--description", "x",
		target,
	}, "99\n2\n")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	// Position 2 is "agent" in firstClassTypes.
	if !bytes.Contains(body, []byte("type: agent")) {
		t.Errorf("retry-after-invalid did not pick choice 2 (agent): %s", body)
	}
}

// Interactive type pick with EOF (closed stdin, no value entered).
// promptChoice must return "" so the subsequent validateType call
// rejects the empty type and the command exits 2 instead of hanging.
func TestScaffold_Interactive_TypePromptEOFErrors(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--description", "x",
		target,
	}, "") // empty stdin → EOF on the first read
	if exit != 2 {
		t.Fatalf("exit=%d, want 2 (EOF should not loop forever)", exit)
	}
	if !strings.Contains(stderr, "--type is required") {
		t.Errorf("stderr missing required-type hint: %s", stderr)
	}
}

// Interactive hook-event prompt. Exercises the prompt branch of
// collectTypeSpecific for type=hook.
func TestScaffold_Interactive_HookEventPrompt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "h")
	exit, _, stderr := runScaffold([]string{
		"--type", "hook",
		"--description", "x",
		target,
	}, "pre_tool_use\n")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("hook_event: pre_tool_use")) {
		t.Errorf("hook_event from prompt not written: %s", body)
	}
}

// Interactive server-identifier prompt for type=mcp-server.
func TestScaffold_Interactive_ServerIdPrompt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "m")
	exit, _, stderr := runScaffold([]string{
		"--type", "mcp-server",
		"--description", "x",
		target,
	}, "npx:@acme/test-mcp\n")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("server_identifier: npx:@acme/test-mcp")) {
		t.Errorf("server_identifier from prompt not written: %s", body)
	}
}

// Interactive rule-globs prompt when --rule-mode=glob is set
// without --rule-globs. Exercises the prompt branch of
// collectTypeSpecific for the rule/glob path.
func TestScaffold_Interactive_RuleGlobsPrompt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "r")
	exit, _, stderr := runScaffold([]string{
		"--type", "rule",
		"--description", "x",
		"--rule-mode", "glob",
		target,
	}, "internal/**/*.go\n")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte(`rule_globs: "internal/**/*.go"`)) {
		t.Errorf("rule_globs from prompt not written: %s", body)
	}
}

// Interactive rule-description prompt when --rule-mode=auto is set
// without --rule-description.
func TestScaffold_Interactive_RuleDescPrompt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "r")
	exit, _, stderr := runScaffold([]string{
		"--type", "rule",
		"--description", "x",
		"--rule-mode", "auto",
		target,
	}, "Apply on database migrations\n")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte(`rule_description: "Apply on database migrations"`)) {
		t.Errorf("rule_description from prompt not written: %s", body)
	}
}

// Interactive prompt for rule-description that returns empty errors
// cleanly instead of writing a malformed manifest.
func TestScaffold_Interactive_RuleDescEmptyErrors(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "r")
	exit, _, stderr := runScaffold([]string{
		"--type", "rule",
		"--description", "x",
		"--rule-mode", "auto",
		target,
	}, "\n")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "rule-description is required") {
		t.Errorf("stderr missing rule-description required hint: %s", stderr)
	}
}

// Interactive hook-event prompt that returns empty errors cleanly.
func TestScaffold_Interactive_HookEventEmptyErrors(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "h")
	exit, _, stderr := runScaffold([]string{
		"--type", "hook",
		"--description", "x",
		target,
	}, "\n")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "hook-event is required") {
		t.Errorf("stderr missing hook-event required hint: %s", stderr)
	}
}

// Interactive server-identifier prompt that returns empty errors cleanly.
func TestScaffold_Interactive_ServerIdEmptyErrors(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "m")
	exit, _, stderr := runScaffold([]string{
		"--type", "mcp-server",
		"--description", "x",
		target,
	}, "\n")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "server-identifier is required") {
		t.Errorf("stderr missing server-identifier required hint: %s", stderr)
	}
}

// Interactive description prompt that returns empty fails the
// post-prompt required-field check.
func TestScaffold_Interactive_DescriptionEmptyErrors(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		target,
	}, "\n")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "description is required") {
		t.Errorf("stderr missing description-required hint: %s", stderr)
	}
}

// Skill name >64 chars is rejected (agentskills.io constraint).
func TestScaffold_SkillNameRejectsOver64Chars(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("a", 65)
	target := filepath.Join(root, "personal", long)
	exit, _, stderr := runScaffold([]string{
		"--type", "skill",
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "64 characters") {
		t.Errorf("stderr missing 64-char hint: %s", stderr)
	}
}

// Skill name with trailing hyphen rejected (agentskills.io constraint).
func TestScaffold_SkillNameRejectsTrailingHyphen(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "bad-")
	exit, _, stderr := runScaffold([]string{
		"--type", "skill",
		"--description", "x",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "cannot end with a hyphen") {
		t.Errorf("stderr missing trailing-hyphen hint: %s", stderr)
	}
}

// Unknown flag triggers stdlib flag.Parse error and the command
// exits with parseExit's code (2 for non-help errors).
func TestScaffold_UnknownFlagErrors(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, _ := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--yes",
		"--no-such-flag",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2 (flag.Parse error)", exit)
	}
}

// --help on the scaffold subcommand exits 0 (the stdlib flag
// package treats it as a non-error usage request).
func TestScaffold_HelpExits0(t *testing.T) {
	exit, _, _ := runScaffold([]string{"--help"}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0", exit)
	}
}

// Interactive rule-globs prompt that returns empty (EOF after the
// prompt). The command must reject the missing value instead of
// writing a malformed manifest.
func TestScaffold_Interactive_RuleGlobsEmptyErrors(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "r")
	exit, _, stderr := runScaffold([]string{
		"--type", "rule",
		"--description", "x",
		"--rule-mode", "glob",
		target,
	}, "\n") // user just hits enter; the resulting empty value is rejected
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "rule-globs is required") {
		t.Errorf("stderr missing rule-globs required hint: %s", stderr)
	}
}

// --rule-mode value validation runs even in interactive mode.
func TestScaffold_RejectsBadRuleMode(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "r")
	exit, _, stderr := runScaffold([]string{
		"--type", "rule",
		"--description", "x",
		"--rule-mode", "bogus",
		"--yes",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "must be one of") {
		t.Errorf("stderr missing rule-mode validity hint: %s", stderr)
	}
}

// ---------- Flag pass-through coverage ----------

// --when-to-use populates the YAML list. Covers the populated
// branch of writeWhenToUse (the empty branch is hit by every other
// test).
func TestScaffold_WhenToUsePopulated(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--when-to-use", "first situation,second situation",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("when_to_use:\n")) {
		t.Errorf("ARTIFACT.md missing when_to_use header: %s", body)
	}
	if !bytes.Contains(body, []byte(`- "first situation"`)) {
		t.Errorf("first when_to_use entry missing: %s", body)
	}
	if !bytes.Contains(body, []byte(`- "second situation"`)) {
		t.Errorf("second when_to_use entry missing: %s", body)
	}
}

// --extends populates the extends field for a non-skill type.
func TestScaffold_ExtendsOnContext(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--extends", "finance/ap/pay-invoice@1.x",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("extends: finance/ap/pay-invoice@1.x")) {
		t.Errorf("extends not written: %s", body)
	}
}

// --extends populates the extends field for skill (ARTIFACT.md,
// not SKILL.md — extends is Podium-specific per §4.3.4).
func TestScaffold_ExtendsOnSkill(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "child-skill")
	exit, _, stderr := runScaffold([]string{
		"--type", "skill",
		"--description", "Child skill that extends a parent.",
		"--extends", "finance/ap/parent@1.x",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	artifact, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(artifact, []byte("extends: finance/ap/parent@1.x")) {
		t.Errorf("extends not written to skill ARTIFACT.md: %s", artifact)
	}
	skill, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if bytes.Contains(skill, []byte("extends:")) {
		t.Errorf("extends must live in ARTIFACT.md only (§4.3.4): %s", skill)
	}
}

// Hook with no --hook-action emits a default placeholder action so
// the manifest is lint-clean and the author has something to edit.
func TestScaffold_Hook_DefaultActionPlaceholder(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "h")
	exit, _, stderr := runScaffold([]string{
		"--type", "hook",
		"--description", "x",
		"--hook-event", "session_start",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("hook_action: |")) {
		t.Errorf("default hook_action block missing: %s", body)
	}
	if !bytes.Contains(body, []byte("echo \"hook fired\"")) {
		t.Errorf("placeholder hook action body missing: %s", body)
	}
}

// ---------- Render helpers ----------

func TestPlaceholderBody_AllTypes(t *testing.T) {
	cases := []string{"context", "command", "agent", "rule", "hook", "mcp-server", "unknown-extension"}
	for _, typ := range cases {
		t.Run(typ, func(t *testing.T) {
			got := placeholderBody(typ)
			if got == "" {
				t.Errorf("placeholderBody(%q) returned empty string", typ)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("placeholderBody(%q) missing trailing newline", typ)
			}
		})
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"a,,b", []string{"a", "b"}},
		{",,,", []string{}},
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// --license on a non-skill type populates ARTIFACT.md (license
// lives in SKILL.md for skills, so this branch only fires for
// other types).
func TestScaffold_LicenseOnContext(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--license", "Apache-2.0",
		"--yes",
		target,
	}, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	body, _ := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if !bytes.Contains(body, []byte("license: Apache-2.0")) {
		t.Errorf("license not written to ARTIFACT.md: %s", body)
	}
}

// Interactive description prompt with a fully-empty stdin (no
// trailing newline) exercises promptString's EOF branch.
func TestScaffold_Interactive_DescriptionEOF(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		target,
	}, "")
	if exit != 2 {
		t.Fatalf("exit=%d, want 2", exit)
	}
	if !strings.Contains(stderr, "description is required") {
		t.Errorf("stderr missing description-required hint: %s", stderr)
	}
}

// Public-API wrapper exercise: artifactScaffold delegates to the
// IO-injected core. A --yes invocation with all required values
// runs end-to-end against os.Stdin/Stdout/Stderr.
func TestArtifactScaffoldWrapper(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	exit := artifactScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--yes",
		target,
	})
	if exit != 0 {
		t.Fatalf("exit=%d, want 0", exit)
	}
	if _, err := os.Stat(filepath.Join(target, "ARTIFACT.md")); err != nil {
		t.Errorf("expected ARTIFACT.md: %v", err)
	}
}

// promptChoice with a default returns the default when the user
// hits enter; covers the def != "" hint and the empty-input return.
func TestPromptChoice_DefaultOnEmptyInput(t *testing.T) {
	var out bytes.Buffer
	got := promptChoice(bufio.NewReader(strings.NewReader("\n")), &out, "Pick", []string{"a", "b", "c"}, "b")
	if got != "b" {
		t.Errorf("got %q, want default %q", got, "b")
	}
	if !strings.Contains(out.String(), "[b]") {
		t.Errorf("output missing default hint: %s", out.String())
	}
}

// promptString with a default returns the default on empty input.
func TestPromptString_DefaultOnEmptyInput(t *testing.T) {
	var out bytes.Buffer
	got := promptString(bufio.NewReader(strings.NewReader("\n")), &out, "Name", "alice")
	if got != "alice" {
		t.Errorf("got %q, want default %q", got, "alice")
	}
	if !strings.Contains(out.String(), "[alice]") {
		t.Errorf("output missing default hint: %s", out.String())
	}
}

// renderAndWrite's WriteFile error path: pre-create ARTIFACT.md
// as a directory so os.WriteFile fails with a write error and the
// command returns non-zero with a clear message.
func TestScaffold_WriteFileFailsCleanly(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "x")
	if err := os.MkdirAll(filepath.Join(target, "ARTIFACT.md"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	exit, _, stderr := runScaffold([]string{
		"--type", "context",
		"--description", "x",
		"--force",
		"--yes",
		target,
	}, "")
	if exit != 1 {
		t.Fatalf("exit=%d, want 1 (WriteFile error)", exit)
	}
	if !strings.Contains(stderr, "error:") {
		t.Errorf("stderr missing error line: %s", stderr)
	}
}

// Same as above but for the skill branch: pre-create SKILL.md as a
// directory so the second WriteFile in renderAndWrite fails (the
// ARTIFACT.md write succeeds first). Covers the skill-specific
// SKILL.md write-error branch.
func TestScaffold_Skill_SkillMDWriteFailsCleanly(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "greet")
	if err := os.MkdirAll(filepath.Join(target, "SKILL.md"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	exit, _, stderr := runScaffold([]string{
		"--type", "skill",
		"--description", "Greet the user.",
		"--force",
		"--yes",
		target,
	}, "")
	if exit != 1 {
		t.Fatalf("exit=%d, want 1 (SKILL.md WriteFile error)", exit)
	}
	if !strings.Contains(stderr, "error:") {
		t.Errorf("stderr missing error line: %s", stderr)
	}
}

func TestContains(t *testing.T) {
	if !contains([]string{"a", "b", "c"}, "b") {
		t.Error("contains failed for present item")
	}
	if contains([]string{"a", "b", "c"}, "z") {
		t.Error("contains returned true for missing item")
	}
	if contains([]string{}, "anything") {
		t.Error("contains returned true for empty haystack")
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
