package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// nestedMetadataSkillMD is a SKILL.md that nests a mapping and a sequence
// under metadata keys Podium does not read, the layout published skills such
// as the Lark CLI skills use.
const nestedMetadataSkillMD = "---\nname: lark-docs\ndescription: Read Lark documents with the lark-cli tool when the user asks about a Lark doc.\n" +
	"metadata:\n  version: \"1.0.0\"\n" +
	"  requires:\n    bins: [\"lark-cli\"]\n    env:\n      - LARK_TOKEN # read at run time\n" +
	"  cliHelp: \"lark-cli docs --help\"\n" +
	"---\n\nRun `lark-cli docs --help` first.\n"

// Spec: §4.1, §4.3.4 — a SKILL.md whose metadata nests a mapping under a key
// Podium does not read parses: podium lint refuses nothing, reports
// lint.skill_metadata_nonstring as a warning naming the key and the Agent
// Skills string rule, and exits 0. --fail-on warning turns the warning into
// exit 1, which shows the diagnostic is a warning rather than info.
func TestLint_NestedSkillMetadataWarns(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"tools/lark-docs/SKILL.md": nestedMetadataSkillMD})
	res := runPodium(t, "", []string{"HOME=" + t.TempDir()}, "lint", "--registry", reg)
	cliWantExit(t, res, 0, "lint over nested SKILL.md metadata")
	cliNotContains(t, res.Stderr, "rejected:", "no refusal for nested metadata")
	for _, want := range []string{
		"[warning]",
		"lint.skill_metadata_nonstring",
		"metadata.requires is a mapping",
		"the Agent Skills standard defines metadata values as strings",
	} {
		cliContains(t, res.Stdout, want, "lint names the nested metadata key")
	}
	cliNotContains(t, res.Stdout, "metadata.cliHelp", "a string metadata value draws no warning")

	strict := runPodium(t, "", []string{"HOME=" + t.TempDir()}, "lint", "--registry", reg, "--fail-on", "warning")
	cliWantExit(t, strict, 1, "lint --fail-on warning over nested SKILL.md metadata")
}

// Spec: §4.3.4, §6.7, §13.11.3 — a filesystem-source podium sync for
// claude-code writes a skill with nested metadata byte for byte. A
// user-invoked skill, which takes its invocation from a metadata.invocation
// beside the nested block, gains the disable-model-invocation line as its
// first frontmatter line and keeps every other byte, the nested block
// included.
func TestFilesystemSync_NestedSkillMetadataKept(t *testing.T) {
	t.Parallel()
	userSkill := strings.Replace(nestedMetadataSkillMD, "name: lark-docs\n", "name: lark-send\n", 1)
	userSkill = strings.Replace(userSkill, "  cliHelp:", "  invocation: \"user\"\n  cliHelp:", 1)
	reg := writeRegistry(t, map[string]string{
		"tools/lark-docs/SKILL.md": nestedMetadataSkillMD,
		"tools/lark-send/SKILL.md": userSkill,
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "claude-code")
	cliWantExit(t, res, 0, "sync over nested SKILL.md metadata")
	cliNotContains(t, res.Stderr, "rejected:", "no refusal for nested metadata")

	if got := readFile(t, filepath.Join(target, ".claude", "skills", "lark-docs", "SKILL.md")); got != nestedMetadataSkillMD {
		t.Errorf("lark-docs SKILL.md =\n%s\nwant the authored bytes:\n%s", got, nestedMetadataSkillMD)
	}
	wantUser := "---\ndisable-model-invocation: true\n" + strings.TrimPrefix(userSkill, "---\n")
	if got := readFile(t, filepath.Join(target, ".claude", "skills", "lark-send", "SKILL.md")); got != wantUser {
		t.Errorf("lark-send SKILL.md =\n%s\nwant:\n%s", got, wantUser)
	}
}
