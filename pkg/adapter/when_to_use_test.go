package adapter

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

const wtuSkillArtifact = "---\ntype: skill\nversion: 1.0.0\n---\n"

// claudeSkillMarkdown returns the SKILL.md the claude-code adapter writes for
// a skill with the given ARTIFACT.md and SKILL.md bytes.
func claudeSkillMarkdown(t *testing.T, artifact, skill string) string {
	t.Helper()
	out, err := ClaudeCode{}.Adapt(context.Background(), Source{
		ArtifactID: "team/standup", ArtifactBytes: []byte(artifact), SkillBytes: []byte(skill),
	})
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	for _, f := range out {
		if f.Path == ".claude/skills/standup/SKILL.md" {
			return string(f.Content)
		}
	}
	t.Fatalf("no SKILL.md among %d files", len(out))
	return ""
}

// topLevelWhenToUse decodes the top-level when_to_use string of a SKILL.md,
// failing the test when the frontmatter does not decode or repeats the key.
func topLevelWhenToUse(t *testing.T, skill string) (string, bool) {
	t.Helper()
	fm, _, err := manifest.SplitFrontmatter([]byte(skill))
	if err != nil {
		t.Fatalf("split frontmatter: %v\n%s", err, skill)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		t.Fatalf("the written frontmatter does not decode: %v\n%s", err, skill)
	}
	value, found := "", false
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "when_to_use" {
			if found {
				t.Fatalf("the frontmatter repeats when_to_use:\n%s", skill)
			}
			value, found = root.Content[i+1].Value, true
		}
	}
	return value, found
}

// Spec: §4.3.4, §6.7 — the claude-code adapter writes the resolved
// when_to_use at the top level: from metadata when ARTIFACT.md states none,
// from ARTIFACT.md over metadata when it states one, with the situations
// joined by line feeds, at the indentation of the frontmatter's keys, and
// with metadata.when_to_use left in place.
func TestClaudeCode_WritesResolvedWhenToUseAtTopLevel(t *testing.T) {
	t.Parallel()
	metadataOnly := "---\nname: standup\ndescription: x\nmetadata:\n  when_to_use: |\n    At standup.\n    When asked about yesterday.\n---\nbody\n"
	got := claudeSkillMarkdown(t, wtuSkillArtifact, metadataOnly)
	if v, ok := topLevelWhenToUse(t, got); !ok || v != "At standup.\nWhen asked about yesterday." {
		t.Errorf("metadata value at the top level = %q (%v):\n%s", v, ok, got)
	}
	if !strings.Contains(got, "  when_to_use: |\n    At standup.\n") {
		t.Errorf("metadata.when_to_use was not kept:\n%s", got)
	}
	fromArtifact := "---\ntype: skill\nversion: 1.0.0\nwhen_to_use:\n  - When the artifact says so.\n---\n"
	if v, ok := topLevelWhenToUse(t, claudeSkillMarkdown(t, fromArtifact, metadataOnly)); !ok || v != "When the artifact says so." {
		t.Errorf("ARTIFACT.md value at the top level = %q (%v), want the ARTIFACT.md value over metadata", v, ok)
	}
	indented := "---\n  name: standup\n  description: x\n  metadata:\n    when_to_use: At standup.\n---\nbody\n"
	got = claudeSkillMarkdown(t, wtuSkillArtifact, indented)
	if v, ok := topLevelWhenToUse(t, got); !ok || v != "At standup." || !strings.HasPrefix(got, "---\n  when_to_use: ") {
		t.Errorf("indented frontmatter: %q (%v):\n%s", v, ok, got)
	}
}

// Spec: §4.3.4, §6.7 — an authored top-level when_to_use is kept byte for
// byte whatever its value, in a block or a flow mapping, and a skill with no
// resolved when_to_use gets no key.
func TestClaudeCode_KeepsAuthoredOrAbsentWhenToUse(t *testing.T) {
	t.Parallel()
	for name, skill := range map[string]string{
		"authored value":      "---\nname: standup\ndescription: x\nwhen_to_use: When I say so.\nmetadata:\n  when_to_use: At standup.\n---\nbody\n",
		"authored empty":      "---\nname: standup\ndescription: x\nwhen_to_use: \"\"\nmetadata:\n  when_to_use: At standup.\n---\nbody\n",
		"no value":            "---\nname: standup\ndescription: x\n---\nbody\n",
		"authored flow value": "---\n{name: standup, description: x, when_to_use: Mine., metadata: {when_to_use: At standup.}}\n---\nbody\n",
		"flow, no value":      "---\n{name: standup, description: x}\n---\nbody\n",
	} {
		if got := claudeSkillMarkdown(t, wtuSkillArtifact, skill); got != skill {
			t.Errorf("%s: SKILL.md changed:\n got %q\nwant %q", name, got, skill)
		}
	}
}

// Spec: §4.3.4, §6.7 — a frontmatter that no single injected line can extend,
// a flow mapping, a mapping whose first key is explicit, and one that opens
// with a tag line, still gets the resolved top-level when_to_use: the key
// joins the mapping and the frontmatter is re-encoded, keeping the opening
// delimiter, the closing delimiter, and the body. A user-invoked flow-mapping
// skill carries both the user-only control and the key.
func TestClaudeCode_WritesWhenToUseIntoNonBlockFrontmatter(t *testing.T) {
	t.Parallel()
	for name, skill := range map[string]string{
		"flow mapping": "---\n{name: standup, description: x, metadata: {version: \"1.0.0\", when_to_use: \"At standup.\\nWhen asked about yesterday.\"}}\n---\n\nbody\n",
		"explicit key": "---\n? name\n: standup\ndescription: x\nmetadata:\n  when_to_use: |\n    At standup.\n    When asked about yesterday.\n---\n\nbody\n",
		"tag line":     "---\n!!map\nname: standup\ndescription: x\nmetadata:\n  when_to_use: |\n    At standup.\n    When asked about yesterday.\n---\n\nbody\n",
		"flow, user":   "---\n{name: standup, description: x, metadata: {invocation: user, when_to_use: \"At standup.\\nWhen asked about yesterday.\"}}\n---\n\nbody\n",
	} {
		got := claudeSkillMarkdown(t, wtuSkillArtifact, skill)
		if v, ok := topLevelWhenToUse(t, got); !ok || v != "At standup.\nWhen asked about yesterday." {
			t.Errorf("%s: top-level when_to_use = %q (%v):\n%s", name, v, ok, got)
		}
		if !strings.HasPrefix(got, "---\n") || !strings.HasSuffix(got, "\n---\n\nbody\n") {
			t.Errorf("%s: delimiters or body changed:\n%s", name, got)
		}
		a, err := manifest.ResolveArtifact([]byte(wtuSkillArtifact), []byte(got))
		if err != nil {
			t.Fatalf("%s: the written SKILL.md does not resolve: %v\n%s", name, err, got)
		}
		if strings.Join(a.WhenToUse, "|") != "At standup.|When asked about yesterday." {
			t.Errorf("%s: resolved when_to_use = %q", name, a.WhenToUse)
		}
		if name == "flow, user" && !strings.Contains(got, "disable-model-invocation: true") {
			t.Errorf("%s: the user-only control is missing:\n%s", name, got)
		}
	}
}

// Spec: §4.3.4, §6.7, §7.8 — the Claude marketplace emitter writes the same
// top-level key, and an adapter for another harness writes none.
func TestWhenToUse_ClaudeEmitterWritesItAndOtherAdaptersDoNot(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "team/standup",
		ArtifactBytes: []byte(wtuSkillArtifact),
		SkillBytes:    []byte("---\nname: standup\ndescription: x\nmetadata:\n  when_to_use: At standup.\n---\nbody\n"),
	}
	files := claudePluginSkill("plugins/team", src)
	if len(files) == 0 {
		t.Fatal("the emitter wrote no files")
	}
	if v, ok := topLevelWhenToUse(t, string(files[0].Content)); !ok || v != "At standup." {
		t.Errorf("emitter SKILL.md top-level when_to_use = %q (%v):\n%s", v, ok, files[0].Content)
	}
	out, err := Cursor{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range out {
		if strings.HasSuffix(f.Path, "SKILL.md") {
			if _, ok := topLevelWhenToUse(t, string(f.Content)); ok {
				t.Errorf("the cursor adapter wrote a top-level when_to_use:\n%s", f.Content)
			}
		}
	}
}

// Spec: §4.3.4, §6.7 — a SKILL.md the adapter cannot read as a frontmatter
// mapping is written as it is: one with no frontmatter, one whose frontmatter
// does not decode, one whose frontmatter decodes to something other than a
// mapping, and an empty one.
func TestWithClaudeWhenToUse_LeavesUnreadableFrontmatterAlone(t *testing.T) {
	t.Parallel()
	for name, skill := range map[string]string{
		"no frontmatter":  "name: standup\nbody\n",
		"does not decode": "---\nname: [standup\nmetadata:\n  when_to_use: At standup.\n---\nbody\n",
		"scalar":          "---\njust a sentence\n---\nbody\n",
		"empty":           "---\n---\nbody\n",
	} {
		src := Source{ArtifactID: "team/standup", ArtifactBytes: []byte(wtuSkillArtifact), SkillBytes: []byte(skill)}
		if got := withClaudeWhenToUse([]byte(skill), src); string(got) != skill {
			t.Errorf("%s: changed to %q", name, got)
		}
	}
}
