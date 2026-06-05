package e2e

// End-to-end tests for docs/authoring/your-first-skill.md (D-first-skill).
// Each test drives the real podium binary against the tutorial's `greet`
// skill (personal/hello/greet) and asserts the behavior the page
// documents: init/sync/lint, the two-file skill layout, a bundled
// script, runtime requirements, watch mode, and the claude-code /
// none materialization layouts. Doc-accuracy gaps (the doc's
// positional `podium lint <path>` form, the quickstart's
// .claude/agents arrow) are asserted against actual behavior.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fsTodayPy is the bundled script from the doc's "Add a bundled script"
// section. spec: docs/authoring/your-first-skill.md.
const fsTodayPy = "\"\"\"Print today's date in a friendly format.\"\"\"\n" +
	"import datetime\n" +
	"print(datetime.date.today().strftime(\"%A, %B %-d, %Y\"))\n"

// greetSkillBodyFuller is the SKILL.md after the "Add fuller frontmatter"
// section: a longer, more specific description.
const greetSkillBodyFuller = "---\n" +
	"name: greet\n" +
	"description: Greet the user by name and tell them today's date in a friendly format. Use when the user opens a session with a greeting or asks who you are.\n" +
	"license: MIT\n" +
	"---\n\nGreet the user warmly and state today's date.\n"

// greetArtifactFuller is the ARTIFACT.md after "Add fuller frontmatter":
// when_to_use (two entries), three tags, sensitivity, comment body.
const greetArtifactFuller = "---\n" +
	"type: skill\n" +
	"version: 1.0.0\n" +
	"when_to_use:\n" +
	"  - \"When the user opens a session with a greeting like 'hi' or 'hello'.\"\n" +
	"  - \"When the user asks who you are at session start.\"\n" +
	"tags: [demo, hello-world, greeting]\n" +
	"sensitivity: low\n" +
	"---\n\n<!-- Skill body lives in SKILL.md. -->\n"

// greetArtifactRuntime is the final ARTIFACT.md from "Declare runtime
// requirements": the fuller frontmatter plus runtime_requirements.
const greetArtifactRuntime = "---\n" +
	"type: skill\n" +
	"version: 1.0.0\n" +
	"when_to_use:\n" +
	"  - \"When the user opens a session with a greeting like 'hi' or 'hello'.\"\n" +
	"  - \"When the user asks who you are at session start.\"\n" +
	"tags: [demo, hello-world, greeting]\n" +
	"sensitivity: low\n" +
	"runtime_requirements:\n" +
	"  python: \">=3.10\"\n" +
	"---\n\n<!-- Skill body lives in SKILL.md. -->\n"

// greetSkillWithBody returns a SKILL.md with the doc's frontmatter and
// the supplied prose body (used to exercise prose-reference resolution).
func greetSkillWithBody(body string) string {
	return "---\n" +
		"name: greet\n" +
		"description: Greet the user by name and tell them today's date. Use when the user greets you or asks who you are.\n" +
		"license: MIT\n" +
		"---\n\n" + body
}

// podium init writes .podium/sync.yaml with the
// registry path and harness; a second init without --force is refused.
// spec: doc "## Starting point", quickstart step 2.
func TestSkillTutorial_InitWritesSyncYAML(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()},
		"init", "--registry", reg, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	sync := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(sync, "registry: "+reg) {
		t.Errorf("sync.yaml missing registry %q:\n%s", reg, sync)
	}
	if !strings.Contains(sync, "harness: claude-code") {
		t.Errorf("sync.yaml missing harness: claude-code:\n%s", sync)
	}
	gi := readFile(t, filepath.Join(ws, ".gitignore"))
	for _, want := range []string{".podium/sync.local.yaml", ".podium/overlay/"} {
		if !strings.Contains(gi, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, gi)
		}
	}
	// config show exits cleanly. It prints the merged sync.yaml (§7.7), not
	// .gitignore entries, so the .gitignore assertion above reads the file
	// directly.
	if cs := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "config", "show"); cs.Exit != 0 {
		t.Errorf("config show exit=%d stderr=%s", cs.Exit, cs.Stderr)
	}
	again := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg)
	if again.Exit == 0 {
		t.Errorf("second init without --force should fail; exit=0")
	}
}

// init refuses to overwrite an existing sync.yaml
// without --force, leaving the first registry intact.
func TestSkillTutorial_InitRefusesOverwrite(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	home := t.TempDir()
	runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "/first/registry")
	res := runPodium(t, ws, []string{"HOME=" + home}, "init", "--registry", "/second/registry")
	if res.Exit != 2 {
		t.Fatalf("second init exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "already exists") || !strings.Contains(res.Stderr, "--force") {
		t.Errorf("stderr missing 'already exists'/'--force': %q", res.Stderr)
	}
	if got := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(got, "registry: /first/registry") {
		t.Errorf("sync.yaml was overwritten:\n%s", got)
	}
}

// the minimal two-file greet skill lints cleanly.
// spec: doc "## Starting point".
func TestSkillTutorial_MinimalTwoFileLints(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	mustExist(t, filepath.Join(reg, "personal/hello/greet/SKILL.md"))
	mustExist(t, filepath.Join(reg, "personal/hello/greet/ARTIFACT.md"))
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Errorf("stdout=%q, want 'lint: no issues.'", res.Stdout)
	}
}

// the fuller SKILL.md/ARTIFACT.md frontmatter is
// accepted by lint. spec: doc "## Add fuller frontmatter".
func TestSkillTutorial_FullerFrontmatterLints(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetArtifactFuller,
		"personal/hello/greet/SKILL.md":    greetSkillBodyFuller,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
	for _, code := range []string{"required_field_missing", "skill_md_compliance"} {
		if strings.Contains(res.Stdout, code) {
			t.Errorf("unexpected %s diagnostic:\n%s", code, res.Stdout)
		}
	}
}

// a Podium-only field in SKILL.md is an ingest error;
// SKILL.md stays within the agentskills.io subset (spec §4.3.4).
func TestSkillTutorial_SkillMDPodiumFieldErrors(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    "---\nname: greet\ndescription: Greet the user.\nversion: 1.0.0\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1 (error)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[error]") || !strings.Contains(res.Stdout, "version") {
		t.Errorf("expected an error flagging the Podium-only field version:\n%s", res.Stdout)
	}
}

// name/description/license in a skill's ARTIFACT.md that
// disagree with SKILL.md are an ingest error (spec §4.3.4).
func TestSkillTutorial_ArtifactFieldMismatchErrors(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\ndescription: A different description than SKILL.md.\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1 (error)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "skill_artifact_field_mismatch") {
		t.Errorf("expected a skill_artifact_field_mismatch error:\n%s", res.Stdout)
	}
}

// a non-comment ARTIFACT.md body for a skill warns
// (lint.skill_artifact_body) but does not fail. spec: §4.3.4.
func TestSkillTutorial_ArtifactBodyMustBeComment(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\ntags: [demo]\nsensitivity: low\n---\n\nThis is plain prose, not the required HTML comment.\n",
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning only)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.skill_artifact_body") || !strings.Contains(res.Stdout, "[warning]") {
		t.Errorf("missing skill_artifact_body warning:\n%s", res.Stdout)
	}
}

// SKILL.md name must match the parent directory.
// spec: lint.skill_md_compliance.
func TestSkillTutorial_NameMustMatchDir(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"personal/hello/greet/SKILL.md":    "---\nname: wrongname\ndescription: A skill whose name does not match its directory for testing.\n---\n\nbody\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.skill_md_compliance") ||
		!strings.Contains(res.Stdout, "wrongname") || !strings.Contains(res.Stdout, "greet") {
		t.Errorf("missing name-mismatch diagnostic:\n%s", res.Stdout)
	}
}

// a type:skill artifact without SKILL.md is a hard
// error. The registry walk (filesystem.Walk) rejects it before the lint
// rules run, so lint exits 1 with "<id>: type: skill missing SKILL.md" on
// stderr rather than a lint.skill_md_compliance diagnostic on stdout.
func TestSkillTutorial_MissingSkillMD(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	out := res.Stdout + res.Stderr
	if !strings.Contains(out, "missing SKILL.md") || !strings.Contains(out, "personal/hello/greet") {
		t.Errorf("missing 'missing SKILL.md' diagnostic:\nstdout=%s\nstderr=%s", res.Stdout, res.Stderr)
	}
}

// a markdown-link prose reference to an existing
// bundled script resolves; lint passes. spec: doc "## Add a bundled script".
func TestSkillTutorial_ProseRefResolves(t *testing.T) {
	t.Parallel()
	body := "Tell them today's date by reading [scripts/today.py](scripts/today.py).\n"
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":      greetSkillArtifact,
		"personal/hello/greet/SKILL.md":         greetSkillWithBody(body),
		"personal/hello/greet/scripts/today.py": fsTodayPy,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.prose_reference") {
		t.Errorf("unexpected prose_reference diagnostic:\n%s", res.Stdout)
	}
}

// a markdown-link prose reference to a missing
// bundled file fails lint. spec: doc "## Add a bundled script".
func TestSkillTutorial_ProseRefBrokenFails(t *testing.T) {
	t.Parallel()
	body := "Run [scripts/today.py](scripts/today.py) to print the date.\n"
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillWithBody(body),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.prose_reference") || !strings.Contains(res.Stdout, "[error]") {
		t.Errorf("missing prose_reference error:\n%s", res.Stdout)
	}
}

// a prose reference escaping the artifact package
// fails lint. spec: ruleProseReferenceResolution.checkBundled.
func TestSkillTutorial_ProseRefEscapesPackage(t *testing.T) {
	t.Parallel()
	body := "See [secret](../../../etc/passwd) for details.\n"
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillWithBody(body),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.prose_reference") || !strings.Contains(res.Stdout, "escapes the artifact package") {
		t.Errorf("missing escapes-package diagnostic:\n%s", res.Stdout)
	}
}

// runtime_requirements is parsed and lints clean.
// spec: doc "## Declare runtime requirements".
func TestSkillTutorial_RuntimeRequirementsLints(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":      greetArtifactRuntime,
		"personal/hello/greet/SKILL.md":         greetSkillBodyFuller,
		"personal/hello/greet/scripts/today.py": fsTodayPy,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// the complete greet skill materializes under the
// claude-code skill layout (SKILL.md + bundled script, no ARTIFACT.md).
// spec: doc "## Declare runtime requirements", quickstart step 4.
func TestSkillTutorial_ClaudeCodeMaterializes(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":      greetArtifactRuntime,
		"personal/hello/greet/SKILL.md":         greetSkillBodyFuller,
		"personal/hello/greet/scripts/today.py": fsTodayPy,
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "personal/hello/greet") {
		t.Errorf("stdout missing artifact id:\n%s", res.Stdout)
	}
	if got := readFile(t, filepath.Join(tgt, ".claude/skills/greet/SKILL.md")); !strings.Contains(got, "name: greet") {
		t.Errorf("materialized SKILL.md missing name: greet:\n%s", got)
	}
	if got := readFile(t, filepath.Join(tgt, ".claude/skills/greet/scripts/today.py")); got != fsTodayPy {
		t.Errorf("materialized script differs from source:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(tgt, ".claude/skills/greet/ARTIFACT.md")); err == nil {
		t.Errorf(".claude/skills/greet/ARTIFACT.md should not exist for a skill")
	}
}

// when SKILL.md omits compatibility, the claude-code
// adapter derives it from runtime_requirements/sandbox_profile and injects it
// into the materialized SKILL.md (spec §4.3.4). The none adapter,
// which materializes the canonical layout verbatim, does not.
func TestSkillTutorial_ClaudeCodeDerivesCompatibility(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetArtifactRuntime,
		"personal/hello/greet/SKILL.md":    greetSkillBodyFuller,
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/skills/greet/SKILL.md"))
	if !strings.Contains(got, "compatibility:") || !strings.Contains(got, "Python >=3.10") {
		t.Errorf("claude-code SKILL.md missing derived compatibility:\n%s", got)
	}

	tgt2 := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt2, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync none exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if got := readFile(t, filepath.Join(tgt2, "personal/hello/greet/SKILL.md")); strings.Contains(got, "compatibility:") {
		t.Errorf("none adapter must materialize SKILL.md verbatim, no derived compatibility:\n%s", got)
	}
}

// the none harness materializes the canonical layout
// including the bundled script. spec: doc "## Declare runtime requirements".
func TestSkillTutorial_NoneCanonical(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":      greetArtifactRuntime,
		"personal/hello/greet/SKILL.md":         greetSkillBodyFuller,
		"personal/hello/greet/scripts/today.py": fsTodayPy,
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.HasPrefix(res.Stdout, "adapter: none") || !strings.Contains(res.Stdout, "personal/hello/greet") {
		t.Errorf("stdout missing adapter:none / id:\n%s", res.Stdout)
	}
	mustExist(t, filepath.Join(tgt, "personal/hello/greet/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "personal/hello/greet/SKILL.md"))
	if got := readFile(t, filepath.Join(tgt, "personal/hello/greet/scripts/today.py")); got != fsTodayPy {
		t.Errorf("bundled script not preserved through none adapter:\n%s", got)
	}
}

// sync stdout reports the materialized artifact ID
// and the destination path under .claude/skills/. spec: quickstart step 4.
func TestSkillTutorial_SyncReportsPath(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "personal/hello/greet") {
		t.Errorf("stdout missing artifact id:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, ".claude/skills/greet/") {
		t.Errorf("stdout missing .claude/skills/greet/ path:\n%s", res.Stdout)
	}
}

// sync reads defaults.registry from sync.yaml when
// --registry is absent. spec: quickstart step 4.
func TestSkillTutorial_SyncReadsConfigRegistry(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "init", "--registry", reg)
	tgt := t.TempDir()
	res := runPodium(t, ws, []string{"HOME=" + t.TempDir()}, "sync", "--target", tgt)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "personal/hello/greet/ARTIFACT.md"))
}

// sync with no registry configured exits 2.
// spec: quickstart troubleshooting (config.no_registry).
func TestSkillTutorial_SyncNoRegistryExits2(t *testing.T) {
	t.Parallel()
	res := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}, "sync", "--target", t.TempDir())
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "config.no_registry") {
		t.Errorf("stderr missing 'registry is required': %q", res.Stderr)
	}
}

// sync --watch performs the initial sync and exits 0
// on SIGINT. spec: doc "## Iterate with watch mode".
func TestSkillTutorial_WatchExitsCleanOnSIGINT(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	tgt := t.TempDir()
	w := startWatch(t, reg, tgt, "none")
	if !pollFile(filepath.Join(tgt, "personal/hello/greet/ARTIFACT.md"), 5*time.Second) {
		t.Fatalf("initial sync did not materialize within 5s\nlog:\n%s", w.log())
	}
	if code := w.stop(t); code != 0 {
		t.Errorf("watch exit after SIGINT = %d, want 0\nlog:\n%s", code, w.log())
	}
}

// editing SKILL.md during watch re-materializes.
// spec: doc "## Iterate with watch mode".
func TestSkillTutorial_WatchRematerializesOnEdit(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	tgt := t.TempDir()
	skillPath := filepath.Join(tgt, "personal/hello/greet/SKILL.md")
	w := startWatch(t, reg, tgt, "none")
	if !pollFile(skillPath, 5*time.Second) {
		t.Fatalf("initial sync missing\nlog:\n%s", w.log())
	}
	appendLine(t, filepath.Join(reg, "personal/hello/greet/SKILL.md"), "\nSENTINEL-EDIT\n")
	deadline := time.Now().Add(6 * time.Second)
	updated := false
	for time.Now().Before(deadline) {
		if strings.Contains(readFile(t, skillPath), "SENTINEL-EDIT") {
			updated = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	w.stop(t)
	if !updated {
		t.Errorf("watcher did not re-materialize the edit within 6s\nlog:\n%s", w.log())
	}
}

// `podium lint` with no flags exits 2. The doc's
// positional `podium lint <path>` form is not accepted; --registry is
// required. spec: doc "## Lint before you commit" (doc-accuracy).
func TestSkillTutorial_LintNoRegistryExits2(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint")
	if res.Exit != 2 {
		t.Fatalf("exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required': %q", res.Stderr)
	}
}

// the end-state greet artifact (fuller frontmatter,
// runtime_requirements, resolved prose reference) lints with no issues.
func TestSkillTutorial_EndStateLintsClean(t *testing.T) {
	t.Parallel()
	body := "Tell them today's date by reading [scripts/today.py](scripts/today.py).\n"
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":      greetArtifactRuntime,
		"personal/hello/greet/SKILL.md":         greetSkillWithBody(body),
		"personal/hello/greet/scripts/today.py": fsTodayPy,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Errorf("stdout=%q, want 'lint: no issues.'", res.Stdout)
	}
}

// missing type field fails lint. spec: ruleRequiredFields.
func TestSkillTutorial_MissingType(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": "---\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "type is required") {
		t.Errorf("missing 'type is required' diagnostic:\n%s", res.Stdout)
	}
}

// missing version field fails lint. spec: ruleRequiredFields.
func TestSkillTutorial_MissingVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": "---\ntype: skill\n---\n\n<!-- body -->\n",
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "version is required") {
		t.Errorf("missing 'version is required' diagnostic:\n%s", res.Stdout)
	}
}

// an invalid semver version fails lint. spec: ruleVersionSemver.
func TestSkillTutorial_InvalidVersion(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": "---\ntype: skill\nversion: not-semver\n---\n\n<!-- body -->\n",
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_version") {
		t.Errorf("missing lint.invalid_version diagnostic:\n%s", res.Stdout)
	}
}

// lint on a non-existent registry path exits 1.
func TestSkillTutorial_LintBadRegistryPath(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist-xyz")
	res := runPodium(t, "", nil, "lint", "--registry", missing)
	if res.Exit != 1 {
		t.Fatalf("exit=%d, want 1\nstderr=%s", res.Exit, res.Stderr)
	}
	if res.Stderr == "" {
		t.Errorf("expected an error message on stderr")
	}
}

// the claude-code adapter writes a skill under
// .claude/skills/, not .claude/agents/. spec: doc-accuracy gap vs the
// quickstart output example.
func TestSkillTutorial_ClaudeCodeNotAgents(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/skills/greet/SKILL.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/agents/greet.md")); err == nil {
		t.Errorf(".claude/agents/greet.md exists; a skill must land under .claude/skills/")
	}
}

// sync is idempotent across two runs. spec: quickstart step 4.
func TestSkillTutorial_SyncIdempotent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":      greetArtifactRuntime,
		"personal/hello/greet/SKILL.md":         greetSkillBodyFuller,
		"personal/hello/greet/scripts/today.py": fsTodayPy,
	})
	tgt := t.TempDir()
	first := syncAndSnapshot(t, reg, tgt)
	second := syncAndSnapshot(t, reg, tgt)
	if len(first) != len(second) {
		t.Fatalf("file count changed: %d -> %d", len(first), len(second))
	}
	for path, content := range first {
		if second[path] != content {
			t.Errorf("content changed for %q across runs", path)
		}
	}
}

// sync --dry-run prints a plan and writes nothing.
func TestSkillTutorial_SyncDryRun(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none", "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "dry-run") {
		t.Fatalf("dry-run exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "personal/hello/greet") {
		t.Errorf("dry-run stdout missing the artifact that would be materialized:\n%s", res.Stdout)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("dry-run wrote %d files, want 0: %v", len(files), files)
	}
}

// bundled script bytes survive the none adapter
// verbatim. spec: doc "## Add a bundled script".
func TestSkillTutorial_ScriptVerbatimNone(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":      greetSkillArtifact,
		"personal/hello/greet/SKILL.md":         greetSkillBody,
		"personal/hello/greet/scripts/today.py": fsTodayPy,
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if got := readFile(t, filepath.Join(tgt, "personal/hello/greet/scripts/today.py")); got != fsTodayPy {
		t.Errorf("none-adapter script differs from source:\n%q", got)
	}
}

// bundled script bytes survive the claude-code
// adapter verbatim. spec: doc "## Add a bundled script".
func TestSkillTutorial_ScriptVerbatimClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":      greetSkillArtifact,
		"personal/hello/greet/SKILL.md":         greetSkillBody,
		"personal/hello/greet/scripts/today.py": fsTodayPy,
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if got := readFile(t, filepath.Join(tgt, ".claude/skills/greet/scripts/today.py")); got != fsTodayPy {
		t.Errorf("claude-code script differs from source:\n%q", got)
	}
}

// a bundled resource over the 1 MB per-file soft cap
// warns but does not fail lint. spec: §4.1 per-file soft cap.
func TestSkillTutorial_PerFileSoftCapWarns(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a", 1024*1024+1) // 1 MB + 1 byte
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":       greetSkillArtifact,
		"personal/hello/greet/SKILL.md":          greetSkillBody,
		"personal/hello/greet/scripts/large.bin": big,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (warning only)\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.bundled_resource_size") ||
		!strings.Contains(res.Stdout, "[warning]") || !strings.Contains(res.Stdout, "per-file") {
		t.Errorf("missing per-file soft-cap warning:\n%s", res.Stdout)
	}
}

// total bundled resources over the 10 MB per-package
// cap fail lint with an error. spec: §4.1 per-package cap.
func TestSkillTutorial_PerPackageHardCapErrors(t *testing.T) {
	t.Parallel()
	chunk := strings.Repeat("b", 6*1024*1024) // 6 MB each; two -> 12 MB > 10 MB
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md":   greetSkillArtifact,
		"personal/hello/greet/SKILL.md":      greetSkillBody,
		"personal/hello/greet/scripts/a.bin": chunk,
		"personal/hello/greet/scripts/b.bin": chunk,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout(head)=%.200s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.bundled_resource_size") || !strings.Contains(res.Stdout, "per-package") {
		t.Errorf("missing per-package error:\n%.400s", res.Stdout)
	}
}

// podium version prints a version string.
func TestSkillTutorial_Version(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "version")
	if res.Exit != 0 || !strings.HasPrefix(res.Stdout, "podium ") {
		t.Errorf("version exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// an unknown harness fails sync with config.unknown_harness.
func TestSkillTutorial_UnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", t.TempDir(), "--harness", "nonexistent-harness")
	if res.Exit != 1 {
		t.Fatalf("exit=%d, want 1\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("stderr missing config.unknown_harness: %q", res.Stderr)
	}
}

// the tags field round-trips through lint.
func TestSkillTutorial_TagsRoundTrip(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\ntags: [demo, hello-world, greeting]\nsensitivity: low\n---\n\n<!-- body -->\n",
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// sensitivity accepts low, medium, and high.
func TestSkillTutorial_SensitivityValues(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"low", "medium", "high"} {
		reg := writeRegistry(t, map[string]string{
			"personal/hello/greet/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nsensitivity: " + level + "\n---\n\n<!-- body -->\n",
			"personal/hello/greet/SKILL.md":    greetSkillBody,
		})
		res := runPodium(t, "", nil, "lint", "--registry", reg)
		if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
			t.Errorf("sensitivity=%s: lint exit=%d stdout=%q", level, res.Exit, res.Stdout)
		}
	}
}

// the doc's positional `podium lint <path>` form is
// not runnable: the CLI ignores the positional argument and requires
// --registry, exiting 2. spec: doc "## Lint before you commit" (doc-accuracy).
func TestSkillTutorial_LintPositionalPathRejected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "lint", filepath.Join(reg, "personal/hello/greet"))
	if res.Exit != 2 {
		t.Fatalf("positional lint exit=%d, want 2 (doc form unsupported)\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required': %q", res.Stderr)
	}
}
