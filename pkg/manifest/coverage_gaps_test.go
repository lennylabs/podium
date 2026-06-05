package manifest

import (
	"reflect"
	"testing"
)

// Spec: §4.6 mcpServers deep-merge by name — mergeMCPServer overlays one
// child entry onto the parent of the same name. Each non-empty child field
// (transport, command, args) wins, and env maps merge with the child's
// value winning on a key collision while parent-only keys survive.
func TestMergeMCPServer_ChildFieldsWinEnvMerges(t *testing.T) {
	t.Parallel()
	parent := MCPServerRef{
		Name:      "github",
		Transport: "stdio",
		Command:   "old-cmd",
		Args:      []string{"--old"},
		Env:       map[string]string{"SHARED": "parent", "PARENT_ONLY": "p"},
	}
	child := MCPServerRef{
		Name:      "github",
		Transport: "http",
		Command:   "new-cmd",
		Args:      []string{"--new", "--verbose"},
		Env:       map[string]string{"SHARED": "child", "CHILD_ONLY": "c"},
	}
	got := mergeMCPServer(parent, child)

	if got.Name != "github" {
		t.Errorf("name = %q, want github", got.Name)
	}
	if got.Transport != "http" {
		t.Errorf("transport = %q, want http (child wins)", got.Transport)
	}
	if got.Command != "new-cmd" {
		t.Errorf("command = %q, want new-cmd (child wins)", got.Command)
	}
	if !reflect.DeepEqual(got.Args, []string{"--new", "--verbose"}) {
		t.Errorf("args = %v, want child args", got.Args)
	}
	wantEnv := map[string]string{"SHARED": "child", "PARENT_ONLY": "p", "CHILD_ONLY": "c"}
	if !reflect.DeepEqual(got.Env, wantEnv) {
		t.Errorf("env = %v, want %v", got.Env, wantEnv)
	}
	// The merge does not mutate the parent's env map in place.
	if parent.Env["SHARED"] != "parent" {
		t.Errorf("parent env mutated: SHARED = %q", parent.Env["SHARED"])
	}
}

// Spec: §4.6 — when the child leaves a field unset, the parent's value is
// inherited unchanged. An empty child env leaves the parent's env map as
// the merged result without allocating a new one.
func TestMergeMCPServer_EmptyChildInheritsParent(t *testing.T) {
	t.Parallel()
	parent := MCPServerRef{
		Name:      "jira",
		Transport: "stdio",
		Command:   "jira-mcp",
		Args:      []string{"--root"},
		Env:       map[string]string{"TOKEN": "p"},
	}
	got := mergeMCPServer(parent, MCPServerRef{Name: "jira"})
	if !reflect.DeepEqual(got, parent) {
		t.Errorf("empty child should inherit parent verbatim:\ngot  %+v\nwant %+v", got, parent)
	}
}

// Spec: §4.6 — a parent with no env plus a child that sets env produces the
// child's env, exercising the merge branch when the parent map is nil.
func TestMergeMCPServer_ParentNilEnv(t *testing.T) {
	t.Parallel()
	parent := MCPServerRef{Name: "slack", Command: "slack-mcp"}
	child := MCPServerRef{Name: "slack", Env: map[string]string{"WEBHOOK": "x"}}
	got := mergeMCPServer(parent, child)
	if got.Command != "slack-mcp" {
		t.Errorf("command = %q, want inherited slack-mcp", got.Command)
	}
	wantEnv := map[string]string{"WEBHOOK": "x"}
	if !reflect.DeepEqual(got.Env, wantEnv) {
		t.Errorf("env = %v, want %v", got.Env, wantEnv)
	}
}

// Spec: §4.3.4 — PodiumOnlySkillFields returns the ARTIFACT.md-only field
// names. Every entry classifies as Podium-only and none as an
// agentskills.io field, and the result includes the type-specific fields so
// a stray hook_event or rule_mode in SKILL.md is flagged.
func TestPodiumOnlySkillFields_ContentsMatchClassifier(t *testing.T) {
	t.Parallel()
	fields := PodiumOnlySkillFields()
	if len(fields) == 0 {
		t.Fatal("PodiumOnlySkillFields returned no fields")
	}
	for _, f := range fields {
		if !IsPodiumOnlySkillField(f) {
			t.Errorf("PodiumOnlySkillFields lists %q but IsPodiumOnlySkillField(%q) = false", f, f)
		}
		if IsAgentSkillsField(f) {
			t.Errorf("PodiumOnlySkillFields lists %q which is also an agentskills.io field", f)
		}
	}
	for _, want := range []string{"type", "version", "when_to_use", "tags", "hook_event", "rule_mode"} {
		if !contains(fields, want) {
			t.Errorf("PodiumOnlySkillFields missing %q", want)
		}
	}
}

// Spec: §4.3.4 — the returned slice is a copy the caller may modify without
// affecting the package-level list or a later call.
func TestPodiumOnlySkillFields_ReturnsIndependentCopy(t *testing.T) {
	t.Parallel()
	first := PodiumOnlySkillFields()
	n := len(first)
	first[0] = "mutated"
	first = append(first, "extra")

	second := PodiumOnlySkillFields()
	if len(second) != n {
		t.Errorf("second call length = %d, want %d (caller mutation leaked)", len(second), n)
	}
	if second[0] == "mutated" {
		t.Errorf("second call sees caller's element mutation: %q", second[0])
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
