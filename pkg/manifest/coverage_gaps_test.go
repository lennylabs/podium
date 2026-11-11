package manifest

import (
	"testing"
)

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
	if grown := append(first, "extra"); len(grown) != n+1 {
		t.Fatalf("append to the returned copy: len = %d, want %d", len(grown), n+1)
	}

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
