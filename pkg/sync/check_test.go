package sync

import (
	"strings"
	"testing"
)

// Spec: §7.5.2 — `podium sync --check` reports unresolved profile references,
// malformed globs, target collisions, and cross-scope profile-name collisions
// as warnings (not errors).
func TestCheck_ReportsAllWarningClasses(t *testing.T) {
	t.Parallel()
	merged := &MergedConfig{
		Defaults: Defaults{Profile: "ghost"}, // unresolved defaults.profile
		Profiles: map[string]Profile{
			"finance": {Include: []string{"finance/["}}, // malformed glob (bad class)
			"shared":  {Exclude: []string{"a/{b,c"}},    // unbalanced braces
		},
		Targets: []TargetEntry{
			{ID: "dup"},
			{ID: "dup"},                        // duplicate target id
			{ID: "t3", Profile: "nonexistent"}, // unresolved target profile
		},
		Collisions: map[string][]configFileScope{
			"finance": {scopeUserGlobal, scopeProjectShared}, // multi-scope profile
		},
	}
	warns := Check(merged)
	joined := strings.Join(warns, "\n")
	for _, want := range []string{
		"defaults.profile references undefined profile \"ghost\"",
		"malformed glob",
		"unbalanced braces",
		"target id \"dup\" is defined more than once",
		"references undefined profile \"nonexistent\"",
		"defined in multiple scopes",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
}

// Spec: §7.5.2 — a clean merged config produces no warnings.
func TestCheck_CleanConfigNoWarnings(t *testing.T) {
	t.Parallel()
	merged := &MergedConfig{
		Defaults: Defaults{Profile: "finance"},
		Profiles: map[string]Profile{
			"finance": {Include: []string{"finance/**"}, Exclude: []string{"finance/**/legacy/**"}},
		},
		Targets: []TargetEntry{{ID: "claude", Profile: "finance"}},
	}
	if warns := Check(merged); len(warns) != 0 {
		t.Errorf("clean config produced warnings: %v", warns)
	}
}

func TestCheck_NilIsEmpty(t *testing.T) {
	t.Parallel()
	if warns := Check(nil); len(warns) != 0 {
		t.Errorf("Check(nil) = %v, want empty", warns)
	}
}

// Spec: §7.5.1 glob syntax — validateGlob accepts well-formed patterns and
// rejects unbalanced braces and malformed character classes.
func TestValidateGlob(t *testing.T) {
	t.Parallel()
	ok := []string{"finance/**", "shared/policies/*", "a/{b,c}/d", "finance/invoicing/run"}
	for _, g := range ok {
		if err := validateGlob(g); err != nil {
			t.Errorf("validateGlob(%q) = %v, want nil", g, err)
		}
	}
	bad := []string{"a/{b,c", "finance/[", "x}y"}
	for _, g := range bad {
		if err := validateGlob(g); err == nil {
			t.Errorf("validateGlob(%q) = nil, want error", g)
		}
	}
}
