package manifest

import (
	"errors"
	"testing"
)

// spec: §4.3.4 — TopLevelFrontmatterKeys returns the frontmatter keys in
// source order so a lint rule can see Podium-only fields that the typed
// decoders silently drop.
func TestTopLevelFrontmatterKeys_Order(t *testing.T) {
	t.Parallel()
	src := []byte("---\nname: hello\ntype: skill\nversion: 1.0.0\n---\n\nbody\n")
	keys, err := TopLevelFrontmatterKeys(src)
	if err != nil {
		t.Fatalf("TopLevelFrontmatterKeys: %v", err)
	}
	want := []string{"name", "type", "version"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], want[i])
		}
	}
}

// spec: §4.3.4 — a manifest with no frontmatter is an error the caller can
// detect (ErrNoFrontmatter), not a silent empty result.
func TestTopLevelFrontmatterKeys_NoFrontmatter(t *testing.T) {
	t.Parallel()
	if _, err := TopLevelFrontmatterKeys([]byte("no frontmatter here")); !errors.Is(err, ErrNoFrontmatter) {
		t.Errorf("want ErrNoFrontmatter, got %v", err)
	}
}

// spec: §4.3.4 — a frontmatter block with no keys yields an empty slice
// without erroring.
func TestTopLevelFrontmatterKeys_EmptyFrontmatter(t *testing.T) {
	t.Parallel()
	keys, err := TopLevelFrontmatterKeys([]byte("---\n\n---\n\nbody\n"))
	if err != nil {
		t.Fatalf("TopLevelFrontmatterKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("keys = %v, want empty", keys)
	}
}

// spec: §4.3.4 field-allocation table — type/version/when_to_use/etc. are
// Podium-only and must not appear in SKILL.md; the agentskills.io subset
// (name/description/license/compatibility/metadata/allowed-tools) is allowed.
func TestPodiumOnlySkillFieldClassification(t *testing.T) {
	t.Parallel()
	podiumOnly := []string{
		"type", "version", "when_to_use", "tags", "sensitivity",
		"search_visibility", "deprecated", "replaced_by", "release_notes",
		"runtime_requirements", "sandbox_profile", "effort_hint",
		"model_class_hint", "sbom", "extends", "target_harnesses",
		"external_resources", "hook_event", "rule_mode", "lint_suppress",
	}
	for _, f := range podiumOnly {
		if !IsPodiumOnlySkillField(f) {
			t.Errorf("IsPodiumOnlySkillField(%q) = false, want true", f)
		}
		if IsAgentSkillsField(f) {
			t.Errorf("IsAgentSkillsField(%q) = true, want false", f)
		}
	}
	allowed := []string{"name", "description", "license", "compatibility", "metadata", "allowed-tools"}
	for _, f := range allowed {
		if IsPodiumOnlySkillField(f) {
			t.Errorf("IsPodiumOnlySkillField(%q) = true, want false", f)
		}
		if !IsAgentSkillsField(f) {
			t.Errorf("IsAgentSkillsField(%q) = false, want true", f)
		}
	}
}

// spec: §4.3.4 — the lint_suppress flag silences a named lint code; a nil
// artifact suppresses nothing.
func TestArtifactSuppresses(t *testing.T) {
	t.Parallel()
	a := &Artifact{LintSuppress: []string{"lint.skill_ref_validate"}}
	if !a.Suppresses("lint.skill_ref_validate") {
		t.Errorf("Suppresses should report the listed code")
	}
	if a.Suppresses("lint.other") {
		t.Errorf("Suppresses should not report an unlisted code")
	}
	var nilArt *Artifact
	if nilArt.Suppresses("lint.skill_ref_validate") {
		t.Errorf("nil artifact must suppress nothing")
	}
}
