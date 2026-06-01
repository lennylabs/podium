package e2e

// End-to-end tests for docs/authoring/rule-modes.md (D-rule-modes).
// Covers the four rule modes (always, glob, auto, explicit), scaffold field
// emission and required-companion validation, lint acceptance, the per-adapter
// rule materialization layouts (claude-code, cursor, hermes, opencode, pi), and
// MCP materialization through the claude-code harness. Tests drive the podium
// CLI, the standalone server, and the podium-mcp bridge. Behaviors blocked by a
// known BUILD-GAPS finding are recorded as skips; doc claims the implementation
// does not honor (with no finding filed, or filed but unobservable) are asserted
// against actual behavior with a note so a future change is detected.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rmRuleArtifact builds a type:rule ARTIFACT.md with the given name, an optional
// rule_mode, extra frontmatter lines, and a rule body.
func rmRuleArtifact(name, mode string, extra []string, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: rule\n")
	b.WriteString("version: 1.0.0\n")
	b.WriteString("name: " + name + "\n")
	b.WriteString("description: " + name + " rule.\n")
	if mode != "" {
		b.WriteString("rule_mode: " + mode + "\n")
	}
	for _, line := range extra {
		b.WriteString(line + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(body)
	return b.String()
}

// ---- Scaffold: field emission and required-companion validation ------------

// T-D-rule-modes-1 — scaffold always-mode rule writes rule_mode: always to
// ARTIFACT.md and injects no companion fields.
// spec: docs/authoring/rule-modes.md § "When to use each mode", always block.
func TestRuleModes_ScaffoldAlways(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "style/house-style")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule",
		"--description", "Project-wide house style.", "--rule-mode", "always", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if !strings.Contains(sc.Stdout, "Scaffolded rule at") {
		t.Errorf("stdout missing 'Scaffolded rule at':\n%s", sc.Stdout)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "type: rule") || !strings.Contains(art, "rule_mode: always") {
		t.Errorf("ARTIFACT.md missing type/rule_mode:\n%s", art)
	}
	if strings.Contains(art, "rule_globs") || strings.Contains(art, "rule_description") {
		t.Errorf("always-mode scaffold injected a companion field:\n%s", art)
	}
}

// T-D-rule-modes-2 — scaffold with no --rule-mode defaults to rule_mode: always.
// spec: docs/authoring/rule-modes.md § "Default is always if you don't set the field".
func TestRuleModes_ScaffoldDefaultMode(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "style/default-mode")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule",
		"--description", "Default mode rule.", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "rule_mode: always") {
		t.Errorf("default scaffold missing rule_mode: always:\n%s", art)
	}
}

// T-D-rule-modes-3 — scaffold glob-mode rule writes rule_mode: glob and
// rule_globs carrying both patterns.
// spec: docs/authoring/rule-modes.md § "When to use each mode", glob block.
func TestRuleModes_ScaffoldGlob(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "style/react-style")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule",
		"--description", "React style rules.", "--rule-mode", "glob",
		"--rule-globs", "src/**/*.tsx,src/**/*.ts", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "type: rule") || !strings.Contains(art, "rule_mode: glob") {
		t.Errorf("ARTIFACT.md missing type/rule_mode glob:\n%s", art)
	}
	if !strings.Contains(art, "src/**/*.tsx") || !strings.Contains(art, "src/**/*.ts") {
		t.Errorf("ARTIFACT.md missing one of the glob patterns:\n%s", art)
	}
}

// T-D-rule-modes-4 — scaffold glob-mode without --rule-globs is a usage error.
// spec: docs/authoring/rule-modes.md § "Lint behavior"; glob requires rule_globs.
func TestRuleModes_ScaffoldGlobMissingGlobs(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "style/bad-glob")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule",
		"--description", "x", "--rule-mode", "glob", "--yes", out)
	if sc.Exit != 2 {
		t.Fatalf("scaffold exit=%d, want 2\nstderr=%s", sc.Exit, sc.Stderr)
	}
	if !strings.Contains(sc.Stderr, "rule-globs required") {
		t.Errorf("stderr missing 'rule-globs required':\n%s", sc.Stderr)
	}
}

// T-D-rule-modes-5 — scaffold auto-mode rule writes rule_mode: auto and the
// rule_description text.
// spec: docs/authoring/rule-modes.md § "When to use each mode", auto block.
func TestRuleModes_ScaffoldAuto(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "rules/db-migration-checks")
	desc := "Apply when working with database migrations or schema changes"
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule",
		"--description", "DB migration checks.", "--rule-mode", "auto",
		"--rule-description", desc, "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "rule_mode: auto") || !strings.Contains(art, desc) {
		t.Errorf("ARTIFACT.md missing rule_mode auto / rule_description:\n%s", art)
	}
}

// T-D-rule-modes-6 — scaffold auto-mode without --rule-description is a usage
// error.
// spec: docs/authoring/rule-modes.md § "Lint behavior"; auto requires rule_description.
func TestRuleModes_ScaffoldAutoMissingDescription(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "rules/bad-auto")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule",
		"--description", "x", "--rule-mode", "auto", "--yes", out)
	if sc.Exit != 2 {
		t.Fatalf("scaffold exit=%d, want 2\nstderr=%s", sc.Exit, sc.Stderr)
	}
	if !strings.Contains(sc.Stderr, "rule-description required") {
		t.Errorf("stderr missing 'rule-description required':\n%s", sc.Stderr)
	}
}

// T-D-rule-modes-7 — scaffold explicit-mode rule writes rule_mode: explicit and
// injects no companion fields.
// spec: docs/authoring/rule-modes.md § "When to use each mode", explicit block.
func TestRuleModes_ScaffoldExplicit(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "rules/incident-response")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule",
		"--description", "Incident response procedures.", "--rule-mode", "explicit", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "rule_mode: explicit") {
		t.Errorf("ARTIFACT.md missing rule_mode: explicit:\n%s", art)
	}
	if strings.Contains(art, "rule_globs") || strings.Contains(art, "rule_description") {
		t.Errorf("explicit-mode scaffold injected a companion field:\n%s", art)
	}
}

// T-D-rule-modes-8 — scaffold with an invalid --rule-mode value is a usage error
// naming the accepted values.
// spec: docs/authoring/rule-modes.md § mode table (always, glob, auto, explicit).
func TestRuleModes_ScaffoldInvalidMode(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "rules/bad")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "rule",
		"--description", "x", "--rule-mode", "invalid", "--yes", out)
	if sc.Exit != 2 {
		t.Fatalf("scaffold exit=%d, want 2\nstderr=%s", sc.Exit, sc.Stderr)
	}
	if !strings.Contains(sc.Stderr, "rule-mode") && !strings.Contains(sc.Stderr, "invalid") && !strings.Contains(sc.Stderr, "always") {
		t.Errorf("stderr does not flag the invalid rule-mode:\n%s", sc.Stderr)
	}
}

// ---- Lint -----------------------------------------------------------------

// T-D-rule-modes-9 — an always-mode rule with no companion fields lints clean.
// spec: docs/authoring/rule-modes.md § "Lint behavior".
func TestRuleModes_LintAlwaysClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/house-style/ARTIFACT.md": rmRuleArtifact("house-style", "always", nil, "House style guide.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || strings.Contains(res.Stdout, "[error]") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-rule-modes-10 — lint errors for a glob-mode rule missing rule_globs.
// spec: docs/authoring/rule-modes.md § "Lint behavior"; glob requires rule_globs.
func TestRuleModes_LintGlobMissingGlobs(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/react-style/ARTIFACT.md": rmRuleArtifact("react-style", "glob", nil, "React rules.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1 (error)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[error]") || !strings.Contains(res.Stdout, "rule_globs") {
		t.Errorf("expected an error naming rule_globs:\n%s", res.Stdout)
	}
}

// T-D-rule-modes-11 — lint errors for an auto-mode rule missing
// rule_description.
// spec: docs/authoring/rule-modes.md § "Lint behavior"; auto requires rule_description.
func TestRuleModes_LintAutoMissingDescription(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db-checks/ARTIFACT.md": rmRuleArtifact("db-checks", "auto", nil, "DB checks.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1 (error)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[error]") || !strings.Contains(res.Stdout, "rule_description") {
		t.Errorf("expected an error naming rule_description:\n%s", res.Stdout)
	}
}

// T-D-rule-modes-12 — lint warns for a glob-mode rule that also sets
// rule_description (ignored field).
// spec: docs/authoring/rule-modes.md § "Lint behavior"; glob + rule_description warns.
func TestRuleModes_LintGlobWithDescriptionWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/react-style/ARTIFACT.md": rmRuleArtifact("react-style", "glob",
			[]string{`rule_globs: "src/**/*.tsx"`, `rule_description: "ignored"`}, "React rules.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[warning]") || !strings.Contains(res.Stdout, "rule-description is ignored") {
		t.Errorf("expected a warning that rule-description is ignored:\n%s", res.Stdout)
	}
}

// T-D-rule-modes-13 — lint warns for an auto-mode rule that also sets
// rule_globs (ignored field).
// spec: docs/authoring/rule-modes.md § "Lint behavior"; auto + rule_globs warns.
func TestRuleModes_LintAutoWithGlobsWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db-checks/ARTIFACT.md": rmRuleArtifact("db-checks", "auto",
			[]string{`rule_description: "when migrating"`, `rule_globs: "src/**"`}, "DB checks.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[warning]") || !strings.Contains(res.Stdout, "rule-globs is ignored") {
		t.Errorf("expected a warning that rule-globs is ignored:\n%s", res.Stdout)
	}
}

// T-D-rule-modes-14 — lint warns for a non-rule artifact that sets rule_mode.
// spec: docs/authoring/rule-modes.md § "Lint behavior"; rule_mode on non-rule warns.
func TestRuleModes_LintRuleModeOnNonRuleWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"ctx/note/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\nname: note\ndescription: A note.\nrule_mode: glob\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[warning]") || !strings.Contains(res.Stdout, "only applicable to type: rule") {
		t.Errorf("expected a warning that rule-mode is only applicable to type: rule:\n%s", res.Stdout)
	}
}

// ---- Sync: per-adapter rule layouts ---------------------------------------

// T-D-rule-modes-15 — claude-code materializes an always-mode rule to
// .claude/rules/<name>.md carrying the artifact content, and writes nothing
// under .claude/podium for it.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", claude-code always.
func TestRuleModes_SyncClaudeCodeAlways(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/house-style/ARTIFACT.md": rmRuleArtifact("house-style", "always", nil, "House style: prefer tabs.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/rules/house-style.md"))
	if !strings.Contains(got, "House style: prefer tabs.") {
		t.Errorf("materialized rule missing body content:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(tgt, ".claude/podium/style/house-style/ARTIFACT.md")); err == nil {
		t.Errorf("rule should not appear under .claude/podium")
	}
}

// T-D-rule-modes-16 — claude-code materializes an explicit-mode rule to
// .claude/rules/<name>.md (same path as always).
// spec: docs/authoring/rule-modes.md § "What each adapter writes", claude-code explicit.
func TestRuleModes_SyncClaudeCodeExplicit(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/incident-response/ARTIFACT.md": rmRuleArtifact("incident-response", "explicit", nil, "Incident response steps.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/rules/incident-response.md"))
}

// T-D-rule-modes-17 — claude-code materializes a glob-mode rule to
// .claude/rules/<name>.md and the sync succeeds. The doc promises a glob
// fallback lint warning; that warning is not emitted (F-6.7.1), so no warning is
// asserted here.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", claude-code glob.
func TestRuleModes_SyncClaudeCodeGlob(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/react-style/ARTIFACT.md": rmRuleArtifact("react-style", "glob", []string{`rule_globs: "src/**/*.tsx"`}, "React rules.\n"),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/rules/react-style.md"))
}

// T-D-rule-modes-18 — claude-code materializes an auto-mode rule to
// .claude/rules/<name>.md carrying its rule_description.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", claude-code auto.
func TestRuleModes_SyncClaudeCodeAuto(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db-migration-checks/ARTIFACT.md": rmRuleArtifact("db-migration-checks", "auto",
			[]string{`rule_description: "Apply when working with database migrations or schema changes"`}, "DB checks.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/rules/db-migration-checks.md"))
	// The auto trigger lands in Claude's native `description:` frontmatter; the
	// Podium-internal field names are not leaked.
	if !strings.Contains(got, "description: Apply when working with database migrations or schema changes") {
		t.Errorf("materialized auto rule missing Claude-native description:\n%s", got)
	}
	if strings.Contains(got, "rule_mode") || strings.Contains(got, "rule_description") {
		t.Errorf("Podium-internal frontmatter leaked into the Claude rule file:\n%s", got)
	}
}

// T-D-rule-modes-19 — cursor materializes an always-mode rule to
// .cursor/rules/<name>.mdc with non-empty content.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", cursor all modes.
func TestRuleModes_SyncCursorAlways(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/house-style/ARTIFACT.md": rmRuleArtifact("house-style", "always", nil, "House style guide.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "cursor"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".cursor/rules/house-style.mdc"))
	if strings.TrimSpace(got) == "" {
		t.Errorf("cursor .mdc is empty")
	}
}

// T-D-rule-modes-20 — cursor materializes a glob-mode rule to
// .cursor/rules/<name>.mdc carrying rule_globs verbatim. The doc promises
// translation into a native cursor globs field; that translation is absent
// (F-6.7.4), so the test asserts the raw rule_globs value carries through.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", cursor all modes.
func TestRuleModes_SyncCursorGlob(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/react-style/ARTIFACT.md": rmRuleArtifact("react-style", "glob",
			[]string{`rule_globs: "src/**/*.tsx,src/**/*.ts"`}, "React rules.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "cursor"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".cursor/rules/react-style.mdc"))
	if !strings.Contains(got, "src/**/*.tsx") {
		t.Errorf("cursor .mdc missing rule_globs value:\n%s", got)
	}
}

// T-D-rule-modes-21 — hermes reuses the Cursor .mdc format and materializes an
// always-mode rule to .cursor/rules/<name>.mdc.
// spec: §6.7 type-target table, hermes rule = .cursor/rules/<n>.mdc.
func TestRuleModes_SyncHermesAlways(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/house-style/ARTIFACT.md": rmRuleArtifact("house-style", "always", nil, "House style guide.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "hermes"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".cursor/rules/house-style.mdc"))
}

// T-D-rule-modes-22 — opencode injects an always-mode rule into root AGENTS.md
// (§6.7 inject mechanism).
// spec: §6.7 type-target table, opencode rule = AGENTS.md (inject).
func TestRuleModes_SyncOpencodeAlways(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/house-style/ARTIFACT.md": rmRuleArtifact("house-style", "always", nil, "House style guide.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "opencode"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if got := readFile(t, filepath.Join(tgt, "AGENTS.md")); !strings.Contains(got, "House style guide.") {
		t.Errorf("AGENTS.md missing the injected rule body:\n%s", got)
	}
}

// T-D-rule-modes-23 — pi injects an explicit-mode rule into root AGENTS.md
// (§6.7 inject mechanism).
// spec: §6.7 type-target table, pi rule = AGENTS.md (inject).
func TestRuleModes_SyncPiExplicit(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/incident-response/ARTIFACT.md": rmRuleArtifact("incident-response", "explicit", nil, "Incident response steps.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "pi"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if got := readFile(t, filepath.Join(tgt, "AGENTS.md")); !strings.Contains(got, "Incident response steps.") {
		t.Errorf("AGENTS.md missing the injected rule body:\n%s", got)
	}
}

// rmCapWarns reports whether the lint output carries a capability fallback
// warning naming the harness, with no error severity. rmCapErrors is its
// error-severity counterpart.
func rmExpectWarn(t *testing.T, reg, harness string) {
	t.Helper()
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning only)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.harness_capability") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("expected a capability fallback warning:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "falls back on adapter \""+harness+"\"") {
		t.Errorf("warning should name adapter %q:\n%s", harness, res.Stdout)
	}
}

// T-D-rule-modes-24 — codex/auto falls back (the AGENTS.md inject loses the
// auto-attach trigger), so targeting codex draws a capability warning.
// spec: docs/authoring/rule-modes.md § capability matrix, codex/auto = ⚠.
func TestRuleModes_CodexAutoFallbackWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db/ARTIFACT.md": rmRuleArtifact("db", "auto",
			[]string{`rule_description: "When migrating"`, "target_harnesses: [codex]"}, "DB checks.\n"),
	})
	rmExpectWarn(t, reg, "codex")
}

// T-D-rule-modes-25 — opencode/auto falls back, so targeting opencode warns.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", opencode auto = ⚠.
func TestRuleModes_OpencodeAutoFallbackWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db/ARTIFACT.md": rmRuleArtifact("db", "auto",
			[]string{`rule_description: "When migrating"`, "target_harnesses: [opencode]"}, "DB checks.\n"),
	})
	rmExpectWarn(t, reg, "opencode")
}

// T-D-rule-modes-26 — pi/auto falls back, so targeting pi warns.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", pi auto = ⚠.
func TestRuleModes_PiAutoFallbackWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db/ARTIFACT.md": rmRuleArtifact("db", "auto",
			[]string{`rule_description: "When migrating"`, "target_harnesses: [pi]"}, "DB checks.\n"),
	})
	rmExpectWarn(t, reg, "pi")
}

// T-D-rule-modes-27 — target_harnesses scopes the capability lint and the
// materialization set: a rule targeting only cursor (native for every rule
// mode) lints clean even though claude-desktop is ✗ for rules, and a sync for a
// harness not in the list skips the artifact entirely.
// spec: §4.3.5 / §6.7.1 — target_harnesses is the cross-harness opt-out.
func TestRuleModes_TargetHarnessesOptOut(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/incident/ARTIFACT.md": rmRuleArtifact("incident", "auto",
			[]string{`rule_description: "When responding to incidents"`, "target_harnesses: [cursor]"},
			"Incident steps.\n"),
	})
	// Lint is clean: cursor is native for auto, and the unlisted harnesses
	// (including the ✗ claude-desktop) are not checked.
	if res := runPodium(t, "", nil, "lint", "--registry", reg); res.Exit != 0 || strings.Contains(res.Stdout, "[error]") {
		t.Errorf("rule targeting only cursor should lint clean: exit=%d stdout=%s", res.Exit, res.Stdout)
	}
	// A sync for an excluded harness skips the artifact (nothing injected).
	excluded := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", excluded, "--harness", "codex"); res.Exit != 0 {
		t.Fatalf("sync codex exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(excluded, "AGENTS.md")); err == nil {
		t.Errorf("a rule excluded by target_harnesses must not materialize for codex")
	}
	// A sync for the listed harness materializes it.
	included := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", included, "--harness", "cursor"); res.Exit != 0 {
		t.Fatalf("sync cursor exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(included, ".cursor/rules/incident.mdc"))
}

// T-D-rule-modes-28 — gemini/always maps natively to GEMINI.md, so targeting
// gemini for an always-mode rule lints clean with no fallback warning.
// spec: docs/authoring/rule-modes.md § capability matrix, gemini/always = ✓.
func TestRuleModes_GeminiAlwaysNativeClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/house/ARTIFACT.md": rmRuleArtifact("house", "always",
			[]string{"target_harnesses: [gemini]"}, "House style.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.harness_capability") {
		t.Errorf("gemini/always is native; expected no capability diagnostic:\n%s", res.Stdout)
	}
}

// T-D-rule-modes-29 — gemini/glob falls back (the GEMINI.md inject loses glob
// scoping), so targeting gemini for a glob rule warns rather than errors.
// spec: docs/authoring/rule-modes.md § capability matrix, gemini/glob = ⚠.
func TestRuleModes_GeminiGlobFallbackWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/ts/ARTIFACT.md": rmRuleArtifact("ts", "glob",
			[]string{`rule_globs: "src/**/*.ts"`, "target_harnesses: [gemini]"}, "TS rules.\n"),
	})
	rmExpectWarn(t, reg, "gemini")
}

// ---- MCP materialization (claude-code harness) ----------------------------

// T-D-rule-modes-30 — MCP load_artifact for an always-mode rule under the
// claude-code harness materializes .claude/rules/<name>.md.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", claude-code always (MCP path).
func TestRuleModes_McpAlwaysMaterializes(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"style/house-style/ARTIFACT.md": rmRuleArtifact("house-style", "always", nil, "House style guide.\n"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=claude-code", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "style/house-style"}))
	rpcResult(t, res.Stdout, 1)
	mustExist(t, filepath.Join(mat, ".claude/rules/house-style.md"))
}

// T-D-rule-modes-31 — MCP load_artifact for an explicit-mode rule materializes
// .claude/rules/<name>.md and does not inject a CLAUDE.md.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", claude-code explicit (MCP path).
func TestRuleModes_McpExplicitMaterializes(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"rules/incident-response/ARTIFACT.md": rmRuleArtifact("incident-response", "explicit", nil, "Incident response steps.\n"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, append(mcpServerEnv(t, srv.BaseURL), "PODIUM_HARNESS=claude-code", "PODIUM_MATERIALIZE_ROOT="+mat),
		toolCall(1, "load_artifact", map[string]any{"id": "rules/incident-response"}))
	rpcResult(t, res.Stdout, 1)
	mustExist(t, filepath.Join(mat, ".claude/rules/incident-response.md"))
	if _, err := os.Stat(filepath.Join(mat, "CLAUDE.md")); err == nil {
		t.Errorf("explicit-mode rule should not inject a CLAUDE.md")
	}
}

// ---- Lint: well-formed companion fields ------------------------------------

// T-D-rule-modes-32 — a glob-mode rule with rule_globs present lints clean.
// spec: docs/authoring/rule-modes.md § "Lint behavior".
func TestRuleModes_LintGlobClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/react-style/ARTIFACT.md": rmRuleArtifact("react-style", "glob",
			[]string{`rule_globs: "src/**/*.tsx,src/**/*.ts"`}, "React rules.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || strings.Contains(res.Stdout, "[error]") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-rule-modes-33 — an auto-mode rule with rule_description present lints clean.
// spec: docs/authoring/rule-modes.md § "Lint behavior".
func TestRuleModes_LintAutoClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/db-migration-checks/ARTIFACT.md": rmRuleArtifact("db-migration-checks", "auto",
			[]string{`rule_description: "Apply when working with database migrations or schema changes"`}, "DB checks.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || strings.Contains(res.Stdout, "[error]") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-rule-modes-34 — an explicit-mode rule with no companion fields lints clean.
// spec: docs/authoring/rule-modes.md § "Lint behavior"; explicit requires none.
func TestRuleModes_LintExplicitClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/incident-response/ARTIFACT.md": rmRuleArtifact("incident-response", "explicit", nil, "Incident response steps.\n"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || strings.Contains(res.Stdout, "[error]") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// ---- Scaffold + lint round-trip; sync invariants --------------------------

// T-D-rule-modes-35 — scaffolding one rule per mode into a single registry root
// and linting it produces no error-severity diagnostics.
// spec: docs/authoring/rule-modes.md § all four mode examples.
func TestRuleModes_ScaffoldAllModesLintClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	scaffolds := [][]string{
		{"--rule-mode", "always"},
		{"--rule-mode", "glob", "--rule-globs", "src/**/*.ts"},
		{"--rule-mode", "auto", "--rule-description", "Apply on DB work"},
		{"--rule-mode", "explicit"},
	}
	leaves := []string{"a/always-rule", "a/glob-rule", "a/auto-rule", "a/explicit-rule"}
	for i, extra := range scaffolds {
		args := append([]string{"artifact", "scaffold", "--type", "rule", "--description", "A rule."}, extra...)
		args = append(args, "--yes", filepath.Join(root, leaves[i]))
		if sc := runPodium(t, "", nil, args...); sc.Exit != 0 {
			t.Fatalf("scaffold %s exit=%d stderr=%s", leaves[i], sc.Exit, sc.Stderr)
		}
	}
	res := runPodium(t, "", nil, "lint", "--registry", root)
	if res.Exit != 0 || strings.Contains(res.Stdout, "[error]") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-rule-modes-36 — claude-code sync of a registry holding one rule per mode
// writes exactly one file per rule under .claude/rules/.
// spec: docs/authoring/rule-modes.md § "What each adapter writes".
func TestRuleModes_SyncOneFilePerRule(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/always-rule/ARTIFACT.md":   rmRuleArtifact("always-rule", "always", nil, "Always.\n"),
		"rules/glob-rule/ARTIFACT.md":     rmRuleArtifact("glob-rule", "glob", []string{`rule_globs: "src/**/*.ts"`}, "Glob.\n"),
		"rules/auto-rule/ARTIFACT.md":     rmRuleArtifact("auto-rule", "auto", []string{`rule_description: "Apply on DB work"`}, "Auto.\n"),
		"rules/explicit-rule/ARTIFACT.md": rmRuleArtifact("explicit-rule", "explicit", nil, "Explicit.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	entries, err := os.ReadDir(filepath.Join(tgt, ".claude/rules"))
	if err != nil {
		t.Fatalf("read .claude/rules: %v", err)
	}
	if len(entries) != 4 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("got %d files under .claude/rules, want 4: %v", len(entries), names)
	}
}

// T-D-rule-modes-37 — a claude-code-synced rule appears only at
// .claude/rules/<name>.md and never under .claude/podium.
// spec: docs/authoring/rule-modes.md § "What each adapter writes", claude-code rows.
func TestRuleModes_SyncNotUnderPodium(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/house-style/ARTIFACT.md": rmRuleArtifact("house-style", "always", nil, "House style guide.\n"),
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/rules/house-style.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/podium/style/house-style/ARTIFACT.md")); err == nil {
		t.Errorf("rule should not appear under .claude/podium")
	}
}

// ---- Interactive scaffold prompts -----------------------------------------

// T-D-rule-modes-38 — interactive glob scaffold (no --rule-globs) prompts and
// writes the typed patterns into rule_globs.
// spec: docs/authoring/rule-modes.md § "When to use each mode", glob block (interactive).
func TestRuleModes_InteractiveGlobPrompt(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "style/react-style")
	sc := runPodiumStdin(t, "", nil, "src/**/*.tsx,src/**/*.ts\n",
		"artifact", "scaffold", "--type", "rule", "--description", "React style rules.",
		"--rule-mode", "glob", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "rule_globs") || !strings.Contains(art, "src/**/*.tsx") {
		t.Errorf("ARTIFACT.md missing rule_globs / typed pattern:\n%s", art)
	}
}

// T-D-rule-modes-39 — interactive auto scaffold (no --rule-description) prompts
// and writes the typed text into rule_description.
// spec: docs/authoring/rule-modes.md § "When to use each mode", auto block (interactive).
func TestRuleModes_InteractiveAutoPrompt(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "rules/db-migration-checks")
	sc := runPodiumStdin(t, "", nil, "Apply when working with database migrations or schema changes\n",
		"artifact", "scaffold", "--type", "rule", "--description", "DB migration checks.",
		"--rule-mode", "auto", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "rule_description") || !strings.Contains(art, "Apply when working with database migrations") {
		t.Errorf("ARTIFACT.md missing rule_description / typed text:\n%s", art)
	}
}

// T-D-rule-modes-40 — a rule with no rule_mode field lints clean and
// materializes under claude-code at .claude/rules/<name>.md (the absent field
// defaults to always behavior).
// spec: docs/authoring/rule-modes.md § "Default is always if you don't set the field".
func TestRuleModes_AbsentModeDefaultsAlways(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/implicit-always/ARTIFACT.md": rmRuleArtifact("implicit-always", "", nil, "Implicit always rule.\n"),
	})
	if res := runPodium(t, "", nil, "lint", "--registry", reg); res.Exit != 0 || strings.Contains(res.Stdout, "[error]") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/rules/implicit-always.md"))
}
