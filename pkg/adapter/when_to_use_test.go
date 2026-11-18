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
// byte whatever its value, a skill with no resolved when_to_use gets no key,
// and a frontmatter the adapter cannot extend by one line is left as it is.
func TestClaudeCode_KeepsAuthoredOrAbsentWhenToUse(t *testing.T) {
	t.Parallel()
	for name, skill := range map[string]string{
		"authored value": "---\nname: standup\ndescription: x\nwhen_to_use: When I say so.\nmetadata:\n  when_to_use: At standup.\n---\nbody\n",
		"authored empty": "---\nname: standup\ndescription: x\nwhen_to_use: \"\"\nmetadata:\n  when_to_use: At standup.\n---\nbody\n",
		"no value":       "---\nname: standup\ndescription: x\n---\nbody\n",
		"flow mapping":   "---\n{name: standup, description: x, metadata: {when_to_use: At standup.}}\n---\nbody\n",
	} {
		if got := claudeSkillMarkdown(t, wtuSkillArtifact, skill); got != skill {
			t.Errorf("%s: SKILL.md changed:\n got %q\nwant %q", name, got, skill)
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
