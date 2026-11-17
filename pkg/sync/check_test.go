package sync

import (
	"os"
	"path/filepath"
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
	warns := Check(merged, nil)
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
	if warns := Check(merged, nil); len(warns) != 0 {
		t.Errorf("clean config produced warnings: %v", warns)
	}
}

// Spec: §7.5.2 — `podium sync --check` reports every invalid listing_budgets
// entry as a warning: an unregistered adapter, the none adapter, and an
// invalid key value. A valid entry draws nothing.
func TestCheck_ReportsListingBudgetProblems(t *testing.T) {
	t.Parallel()
	max, maybe := 100, "maybe"
	merged := &MergedConfig{ListingBudgets: map[string]ListingBudget{
		"acme-harness": {MaxChars: &max},
		"none":         {MaxChars: &max},
		"claude-code":  {Check: &maybe},
		"codex":        {MaxChars: &max},
	}}
	warns := Check(merged, nil)
	if len(warns) != 3 {
		t.Fatalf("Check = %q, want three warnings", warns)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{
		"listing_budgets.acme-harness: no adapter of that name is registered",
		"listing_budgets.none: the none adapter feeds no harness listing",
		"listing_budgets.claude-code.check",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}

	// An adapter key that resolves to an integer still loads as an entry,
	// for an adapter no binary registers, and an entry key that is not a
	// string is ignored as any unknown key is.
	ws, home := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "listing_budgets:\n  claude-code: {max_chars: 12000, 1: x, true: y}\n  1: {max_chars: 5}\n"
	if err := os.WriteFile(filepath.Join(ws, ".podium", "sync.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadMergedConfig(ws, home)
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	warns = Check(loaded, nil)
	if len(warns) != 1 || !strings.Contains(warns[0], "listing_budgets.1: no adapter of that name is registered") {
		t.Errorf("Check = %q, want one warning for the unregistered adapter 1", warns)
	}
}

func TestCheck_NilIsEmpty(t *testing.T) {
	t.Parallel()
	if warns := Check(nil, nil); len(warns) != 0 {
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
