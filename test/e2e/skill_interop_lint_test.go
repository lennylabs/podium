package e2e

// `podium lint` over published Agent Skills, end to end.
//
// Published skills commonly declare no version, carry a top-level version
// key, link to a section of a bundled file, link to a sibling skill, show
// link syntax in code, and bundle a license file and scripts. Spec §4.3.4,
// §4.4, and §7.3.1 set how lint treats each, and these tests drive the real
// `podium` binary over registries that hold them.

import (
	"path/filepath"
	"strings"
	"testing"
)

// interopDescriptions gives each fixture skill a distinct description, so no
// fixture draws lint.colliding_descriptions.
var interopDescriptions = map[string]string{
	"greet":    "Greet the user by name when they say hello.",
	"farewell": "Say goodbye politely when the conversation ends.",
	"history":  "Summarize repository history with git and jq for a weekly engineering report.",
}

// interopSkill returns a SKILL.md with the given name, extra frontmatter
// lines, and body.
func interopSkill(name, extraFrontmatter, body string) string {
	return "---\nname: " + name + "\ndescription: " + interopDescriptions[name] + "\n" +
		extraFrontmatter + "---\n\n" + body
}

// interopVersioned is the frontmatter line set that declares a usable
// version, so a fixture draws no lint.skill_version_assigned diagnostic.
const interopVersioned = "metadata:\n  version: \"1.0.0\"\n"

// lintJSON runs `podium lint --json` with extra flags and decodes the
// envelope.
func lintJSON(t *testing.T, reg string, flags ...string) (cliResult, lintEnvelope) {
	t.Helper()
	res := runPodium(t, "", nil, append([]string{"lint", "--registry", reg, "--json"}, flags...)...)
	return res, decodeLintEnvelope(t, res.Stdout)
}

// A plain Agent Skill that declares no version draws the
// lint.skill_version_assigned diagnostic at info severity: it is printed and
// counted under counts.info, and it fails neither --fail-on value.
//
// Spec: §4.3.4, §7.3.1
func TestInteropLint_PlainSkillWithoutVersionIsInfo(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"ops/greet/SKILL.md": interopSkill("greet", "", "Greet the user.\n"),
	})

	strict, env := lintJSON(t, reg, "--fail-on", "warning")
	cliWantExit(t, strict, 0, "lint --json --fail-on warning over a plain skill")
	if len(env.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", env.Diagnostics)
	}
	d := env.Diagnostics[0]
	if d.ArtifactID != "ops/greet" || d.Code != "lint.skill_version_assigned" || d.Severity != "info" {
		t.Errorf("diagnostic = %+v, want ops/greet lint.skill_version_assigned info", d)
	}
	if env.Counts["info"] != 1 || env.Counts["warning"] != 0 || env.Counts["error"] != 0 {
		t.Errorf("counts = %v, want info 1, warning 0, error 0", env.Counts)
	}

	text := runPodium(t, "", nil, "lint", "--registry", reg, "--fail-on", "warning")
	cliWantExit(t, text, 0, "lint --fail-on warning over a plain skill")
	cliContains(t, text.Stdout, "[info] ops/greet:", "the info diagnostic is printed")
	cliContains(t, text.Stdout, "(lint.skill_version_assigned)", "the info diagnostic names its code")
}

// A link to a section of a bundled file resolves once the fragment is
// removed, a link with a query or percent-encoding resolves the same way, and
// link syntax in code is not checked, so the skill lints clean.
//
// Spec: §4.4
func TestInteropLint_FragmentLinkToBundledFileLintsClean(t *testing.T) {
	t.Parallel()
	body := "Read [the error table](references/api.md#errors) and [the notes](references/my%20notes.md?plain=1).\n\n" +
		"Write a link as `[text](url)`.\n\n```markdown\n- [Template](templates/missing.md)\n```\n"
	reg := writeRegistry(t, map[string]string{
		"ops/greet/SKILL.md":               interopSkill("greet", interopVersioned, body),
		"ops/greet/references/api.md":      "# API\n\n## Errors\n",
		"ops/greet/references/my notes.md": "Notes.\n",
	})
	res, env := lintJSON(t, reg, "--fail-on", "warning")
	cliWantExit(t, res, 0, "lint over a fragment link")
	if len(env.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none", env.Diagnostics)
	}
}

// A link to a sibling skill through ../ is a warning that recommends the
// canonical ID, so lint exits 0 under the default --fail-on error.
//
// Spec: §4.4, §7.3.1
func TestInteropLint_SiblingLinkIsWarning(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"ops/greet/SKILL.md":    interopSkill("greet", interopVersioned, "See [farewell](../farewell/SKILL.md).\n"),
		"ops/farewell/SKILL.md": interopSkill("farewell", interopVersioned, "Say goodbye.\n"),
	})
	res, env := lintJSON(t, reg)
	cliWantExit(t, res, 0, "lint over a sibling link")
	if len(env.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", env.Diagnostics)
	}
	d := env.Diagnostics[0]
	if d.ArtifactID != "ops/greet" || d.Code != "lint.prose_reference" || d.Severity != "warning" {
		t.Errorf("diagnostic = %+v, want ops/greet lint.prose_reference warning", d)
	}
	for _, want := range []string{`"../farewell/SKILL.md"`, "side by side in one source tree", "canonical ID"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q missing %q", d.Message, want)
		}
	}
	if env.Counts["warning"] != 1 || env.Counts["error"] != 0 {
		t.Errorf("counts = %v, want warning 1, error 0", env.Counts)
	}
}

// A top-level version in SKILL.md is a warning that recommends
// metadata.version, while another top-level Podium-only field stays an
// error.
//
// Spec: §4.3.4
func TestInteropLint_TopLevelVersionIsWarning(t *testing.T) {
	t.Parallel()
	versionOnly := writeRegistry(t, map[string]string{
		"ops/greet/SKILL.md": interopSkill("greet", "version: 1.0.0\n"+interopVersioned, "Greet the user.\n"),
	})
	res, env := lintJSON(t, versionOnly)
	cliWantExit(t, res, 0, "lint over a top-level version")
	if len(env.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", env.Diagnostics)
	}
	d := env.Diagnostics[0]
	if d.Code != "lint.skill_podium_only_field" || d.Severity != "warning" || !strings.Contains(d.Message, "metadata.version") {
		t.Errorf("diagnostic = %+v, want a lint.skill_podium_only_field warning naming metadata.version", d)
	}

	withTags := writeRegistry(t, map[string]string{
		"ops/greet/SKILL.md": interopSkill("greet", "version: 1.0.0\ntags: [demo]\n"+interopVersioned, "Greet the user.\n"),
	})
	res, env = lintJSON(t, withTags)
	cliWantExit(t, res, 1, "lint over a top-level tags field")
	if env.Counts["error"] != 1 || env.Counts["warning"] != 1 {
		t.Errorf("counts = %v, want the tags error and the version warning", env.Counts)
	}
}

// interopSharedText is a passage of more than 50 words that two packages
// bundle unchanged.
const interopSharedText = "Reconcile the ledger against the bank statement before the monthly close begins. " +
	"Collect every outstanding receipt from the shared drive and match each one to a posted entry. " +
	"Flag any unmatched amount above the materiality threshold for review by the controller. " +
	"Record adjustments in the journal with a short note that explains the source document and the approver. " +
	"Archive the signed reconciliation in the audit folder when the review completes.\n"

// Near-duplicate content compares prose resources only: a script and a
// license file bundled unchanged in two skills draw nothing, while a shared
// Markdown reference draws a warning that says the copies can drift.
//
// Spec: §7.3.1
func TestInteropLint_NearDuplicateComparesProseResourcesOnly(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"ops/greet/SKILL.md":            interopSkill("greet", interopVersioned, "Greet the user.\n"),
		"ops/greet/scripts/build.py":    "# " + interopSharedText,
		"ops/greet/LICENSE.txt":         interopSharedText,
		"ops/farewell/SKILL.md":         interopSkill("farewell", interopVersioned, "Say goodbye.\n"),
		"ops/farewell/scripts/build.py": "# " + interopSharedText,
		"ops/farewell/LICENSE.txt":      interopSharedText,
	}
	res, env := lintJSON(t, writeRegistry(t, files), "--fail-on", "warning")
	cliWantExit(t, res, 0, "lint over shared scripts and license files")
	if len(env.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none", env.Diagnostics)
	}

	files["ops/greet/references/guide.md"] = interopSharedText
	files["ops/farewell/references/guide.md"] = interopSharedText
	res, env = lintJSON(t, writeRegistry(t, files))
	cliWantExit(t, res, 0, "lint over a shared Markdown reference")
	if len(env.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %+v, want one near-duplicate warning per skill", env.Diagnostics)
	}
	want := "references/guide.md nearly duplicates ops/greet references/guide.md (100%); the copies can drift"
	if d := env.Diagnostics[0]; d.ArtifactID != "ops/farewell" || d.Code != "lint.near_duplicate_content" || !strings.HasPrefix(d.Message, want) {
		t.Errorf("diagnostic = %+v, want ops/farewell near-duplicate with prefix %q", d, want)
	}
}

// Prose references follow CommonMark parsing. A fence nested in a list item,
// an indented code block, an escaped bracket, a mailto: link, and a
// reference-style link are not checked, so the missing files they name draw
// nothing. A link whose text holds nested brackets is a link, so the missing
// file it names is the one error, and lint exits 1.
//
// Spec: §4.4
func TestInteropLint_ProseReferencesFollowCommonMark(t *testing.T) {
	t.Parallel()
	body := "1. Write the summary.\n\n    ~~~markdown\n    - [Template](templates/missing.md)\n    ~~~\n\n" +
		"Example:\n\n    [Example](examples/missing.md)\n\n" +
		"Write \\[text](references/escaped.md) for a link, mail [the team](mailto:team@acme.com), " +
		"and read [the guide][guide].\n\n[guide]: references/guide.md\n\n" +
		"See [the [draft] notes](references/notes.md).\n"
	reg := writeRegistry(t, map[string]string{
		"ops/greet/SKILL.md": interopSkill("greet", interopVersioned, body),
	})
	res, env := lintJSON(t, reg)
	cliWantExit(t, res, 1, "lint over CommonMark prose references")
	if len(env.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", env.Diagnostics)
	}
	d := env.Diagnostics[0]
	if d.Code != "lint.prose_reference" || d.Severity != "error" || !strings.Contains(d.Message, `"references/notes.md"`) {
		t.Errorf("diagnostic = %+v, want a lint.prose_reference error naming references/notes.md", d)
	}
}

// A prose file whose name only contains a license word is a prose resource,
// so a copy shared by two skills draws a near-duplicate warning, while a
// NOTICE.md and an OFL.txt font license shared the same way draw nothing.
//
// Spec: §7.3.1
func TestInteropLint_NoticeNamedProseFileIsCompared(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"ops/greet/SKILL.md":         interopSkill("greet", interopVersioned, "Greet the user.\n"),
		"ops/greet/NOTICE.md":        interopSharedText,
		"ops/greet/fonts/OFL.txt":    interopSharedText,
		"ops/farewell/SKILL.md":      interopSkill("farewell", interopVersioned, "Say goodbye.\n"),
		"ops/farewell/NOTICE.md":     interopSharedText,
		"ops/farewell/fonts/OFL.txt": interopSharedText,
	}
	res, env := lintJSON(t, writeRegistry(t, files), "--fail-on", "warning")
	cliWantExit(t, res, 0, "lint over shared license files")
	if len(env.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none", env.Diagnostics)
	}

	files["ops/greet/privacy-notice.md"] = interopSharedText
	files["ops/farewell/privacy-notice.md"] = interopSharedText
	res, env = lintJSON(t, writeRegistry(t, files))
	cliWantExit(t, res, 0, "lint over a shared privacy notice")
	if len(env.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %+v, want one near-duplicate warning per skill", env.Diagnostics)
	}
	for _, d := range env.Diagnostics {
		if d.Code != "lint.near_duplicate_content" || !strings.HasPrefix(d.Message, "privacy-notice.md nearly duplicates") {
			t.Errorf("diagnostic = %+v, want a near-duplicate of privacy-notice.md", d)
		}
	}
}

// A skill whose allowed-tools is the Agent Skills space-separated string
// lints clean and syncs to Claude Code with its SKILL.md unchanged.
//
// Spec: §4.3.4, §6.7
func TestInteropSync_AllowedToolsStringFormIsKept(t *testing.T) {
	t.Parallel()
	skill := interopSkill("history", interopVersioned+"allowed-tools: Bash(git:*) Bash(jq:*) Read\n",
		"Summarize the commits of the past week.\n")
	reg := writeRegistry(t, map[string]string{"ops/history/SKILL.md": skill})

	res, env := lintJSON(t, reg, "--fail-on", "warning")
	cliWantExit(t, res, 0, "lint over an allowed-tools string")
	if len(env.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none", env.Diagnostics)
	}

	tgt := t.TempDir()
	sync := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	cliWantExit(t, sync, 0, "sync of an allowed-tools string")
	if got := readFile(t, filepath.Join(tgt, ".claude/skills/history/SKILL.md")); got != skill {
		t.Errorf("materialized SKILL.md != authored bytes:\nwant %q\n got %q", skill, got)
	}
}
