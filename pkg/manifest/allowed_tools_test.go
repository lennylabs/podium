package manifest

import (
	"errors"
	"reflect"
	"testing"
)

// Spec: §4.3.4 — allowed-tools takes the Agent Skills space-separated string
// and a YAML sequence. The standard's own example must parse.
func TestParseSkill_AllowedToolsForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want AllowedTools
	}{
		{"standard string", "allowed-tools: Bash(git:*) Bash(jq:*) Read", AllowedTools{"Bash(git:*)", "Bash(jq:*)", "Read"}},
		{"pattern with spaces", `allowed-tools: "Bash(git log:*) Read"`, AllowedTools{"Bash(git log:*)", "Read"}},
		{"comma separated", "allowed-tools: Bash, Read, Grep", AllowedTools{"Bash", "Read", "Grep"}},
		{"single tool", "allowed-tools: Bash", AllowedTools{"Bash"}},
		{"flow sequence", "allowed-tools: [Read, Bash]", AllowedTools{"Read", "Bash"}},
		{"block sequence", "allowed-tools:\n  - Read\n  - Bash(npm run *)", AllowedTools{"Read", "Bash(npm run *)"}},
		{"null", "allowed-tools:", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "---\nname: x\ndescription: Summarizes repository history for a weekly report.\n" + tc.line + "\n---\n\nBody.\n"
			s, err := ParseSkill([]byte(src))
			if err != nil {
				t.Fatalf("ParseSkill: %v", err)
			}
			if !reflect.DeepEqual(s.AllowedTools, tc.want) {
				t.Errorf("AllowedTools = %#v, want %#v", s.AllowedTools, tc.want)
			}
		})
	}
}

// Spec: §4.3.4 — a mapping, or a sequence holding a mapping, is not a tool
// list, so the frontmatter does not parse.
func TestParseSkill_AllowedToolsRejectsMapping(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"allowed-tools:\n  bash: true",
		"allowed-tools:\n  - bash: true",
	} {
		src := "---\nname: x\ndescription: Summarizes repository history for a weekly report.\n" + line + "\n---\n\nBody.\n"
		if _, err := ParseSkill([]byte(src)); !errors.Is(err, ErrInvalidYAML) {
			t.Errorf("ParseSkill(%q) err = %v, want ErrInvalidYAML", line, err)
		}
	}
}

func TestSplitToolList_UnbalancedParentheses(t *testing.T) {
	t.Parallel()
	got := splitToolList("Read) Bash(x y")
	want := []string{"Read)", "Bash(x y"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitToolList = %#v, want %#v", got, want)
	}
}
