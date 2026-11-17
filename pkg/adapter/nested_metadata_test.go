package adapter

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// nestedMetadataSkillMD is a SKILL.md that nests a mapping and a sequence
// under metadata keys Podium does not read, and sets metadata.invocation to
// user beside them, in the block style published skills use.
const nestedMetadataSkillMD = "---\nname: ship\ndescription: Ship it.\nmetadata:\n" +
	"  requires:\n    bins: [\"lark-cli\"]\n    env:\n      - LARK_TOKEN # read at run time\n" +
	"  cliHelp: \"lark-cli docs --help\"\n" +
	"  invocation: \"user\"\n" +
	"---\n\nBody.\n"

// nestedBlock is the metadata block of nestedMetadataSkillMD, which every
// materialized copy must carry byte for byte.
const nestedBlock = "metadata:\n  requires:\n    bins: [\"lark-cli\"]\n    env:\n      - LARK_TOKEN # read at run time\n" +
	"  cliHelp: \"lark-cli docs --help\"\n  invocation: \"user\"\n"

// nestedSkillSource builds the skill team/ship with an ARTIFACT.md that
// states only its type and version, so the invocation comes from metadata.
func nestedSkillSource(skillMD string) Source {
	return Source{
		ArtifactID:    "team/ship",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"),
		SkillBytes:    []byte(skillMD),
	}
}

// Spec: §4.3.4, §6.7 — a SKILL.md whose metadata nests values under keys
// Podium does not read resolves, so its metadata.invocation of user reaches
// the claude-code adapter, which injects the user-only control as the first
// line and keeps every other byte, the nested block included. It fails
// against a parser that refuses the nested value, because the skill then
// resolves to any and takes no control.
func TestClaudeCode_NestedMetadataUserSkill(t *testing.T) {
	t.Parallel()
	out, err := ClaudeCode{}.Adapt(context.Background(), nestedSkillSource(nestedMetadataSkillMD))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(out, ".claude/skills/ship/SKILL.md")
	if !ok {
		t.Fatalf("no SKILL.md in %v", filePaths(out))
	}
	want := "---\ndisable-model-invocation: true\n" + strings.TrimPrefix(nestedMetadataSkillMD, "---\n")
	if string(f.Content) != want {
		t.Errorf("got:\n%s\nwant:\n%s", f.Content, want)
	}
}

// Spec: §4.3.4, §6.7 — an any skill with nested metadata materializes on
// claude-code byte for byte, and a derived compatibility line joins it as the
// first line without touching the nested block.
func TestClaudeCode_NestedMetadataAnySkill(t *testing.T) {
	t.Parallel()
	anySkill := strings.Replace(nestedMetadataSkillMD, "  invocation: \"user\"\n", "", 1)
	out, err := ClaudeCode{}.Adapt(context.Background(), nestedSkillSource(anySkill))
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := findFile(out, ".claude/skills/ship/SKILL.md"); !ok || string(f.Content) != anySkill {
		t.Errorf("got %v:\n%s\nwant:\n%s", filePaths(out), f.Content, anySkill)
	}
	src := nestedSkillSource(anySkill)
	src.ArtifactBytes = []byte("---\ntype: skill\nversion: 1.0.0\nsandbox_profile: read-only-fs\n---\n")
	out, err = ClaudeCode{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := findFile(out, ".claude/skills/ship/SKILL.md")
	want := "---\ncompatibility: \"sandbox: read-only-fs\"\n" + strings.TrimPrefix(anySkill, "---\n")
	if string(f.Content) != want {
		t.Errorf("compatibility derivation got:\n%s\nwant:\n%s", f.Content, want)
	}
}

// Spec: §4.3.4, §6.7 — on codex a user-invoked skill with nested metadata
// keeps SKILL.md byte for byte and gains agents/openai.yaml, the Codex
// user-only control.
func TestCodex_NestedMetadataUserSkill(t *testing.T) {
	t.Parallel()
	out, err := Codex{}.Adapt(context.Background(), nestedSkillSource(nestedMetadataSkillMD))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(out, ".agents/skills/ship/SKILL.md")
	if !ok || string(f.Content) != nestedMetadataSkillMD {
		t.Errorf("got %v:\n%s\nwant:\n%s", filePaths(out), f.Content, nestedMetadataSkillMD)
	}
	if !strings.Contains(string(f.Content), nestedBlock) {
		t.Errorf("nested block changed:\n%s", f.Content)
	}
	policy, ok := findFile(out, ".agents/skills/ship/agents/openai.yaml")
	if !ok || string(policy.Content) != minimalOpenAIYAML {
		t.Errorf("openai.yaml got %v:\n%s", filePaths(out), policy.Content)
	}
}

// Spec: §4.3.4, §6.7 — a flow-style frontmatter takes the user-only control
// by re-encoding, which normalizes the layout and keeps the nested mapping
// and sequence intact as values.
func TestSkillMarkdown_FlowRootKeepsNestedMetadata(t *testing.T) {
	t.Parallel()
	in := "---\n{name: ship, description: d, metadata: {requires: {bins: [lark-cli]}, invocation: user}}\n---\n\nBody.\n"
	got := skillMarkdown(nestedSkillSource(in), controlFrontmatter)
	fm := frontmatterOf(t, got)
	if fm[userOnlyKey] != true {
		t.Errorf("control missing:\n%s", got)
	}
	md, _ := fm["metadata"].(map[string]any)
	want := map[string]any{"requires": map[string]any{"bins": []any{"lark-cli"}}, "invocation": "user"}
	if !reflect.DeepEqual(md, want) {
		t.Errorf("metadata = %#v, want %#v", md, want)
	}
	if !strings.HasSuffix(string(got), "---\n\nBody.\n") {
		t.Errorf("body changed:\n%s", got)
	}
}
