package e2e

// `podium lint` over published Agent Skills, end to end.
//
// Published skills commonly declare no version, carry a top-level version
// key, link to a section of a bundled file, link to a sibling skill, show
// link syntax in code, and bundle a license file and scripts. Spec §4.3.4,
// §4.4, and §7.3.1 set how lint treats each, and these tests drive the real
// `podium` binary over registries that hold them.

import (
	"strings"
	"testing"
)

// interopDescriptions gives each fixture skill a distinct description, so no
// fixture draws lint.colliding_descriptions.
var interopDescriptions = map[string]string{
	"greet":    "Greet the user by name when they say hello.",
	"farewell": "Say goodbye politely when the conversation ends.",
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
