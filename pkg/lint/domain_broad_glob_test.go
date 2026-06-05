package lint_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// broadGlobDiags lints a tree whose finance/DOMAIN.md carries the given
// include patterns and returns only the §12 broad-recursive-glob warnings.
func broadGlobDiags(t *testing.T, patterns ...string) []lint.Diagnostic {
	t.Helper()
	var inc strings.Builder
	for _, p := range patterns {
		// Quote each pattern: a leading `*` is a YAML alias marker, so a
		// root-recursive glob like `**` must be quoted in DOMAIN.md.
		inc.WriteString("  - \"" + p + "\"\n")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"finance/ap/run/ARTIFACT.md": liveArtifact,
		"finance/DOMAIN.md":          "---\nname: finance\ninclude:\n" + inc.String() + "---\n",
	})
	reg, err := filesystem.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	all := (&lint.Linter{}).Lint(context.Background(), reg, records)
	var out []lint.Diagnostic
	for _, d := range all {
		if d.Code == "lint.domain_import_broad_glob" {
			out = append(out, d)
		}
	}
	return out
}

// spec: §12 — "Lint warns on overly broad recursive globs." A bare `**`
// and a root-anchored `**/payments` are unbounded and warn; a literal
// prefix (`finance/**`) or a brace whose every alternative carries a
// literal prefix (`{a,b}/**`) is bounded and draws no warning; a pattern
// with no `**` segment is not recursive and draws no warning.
func TestRuleDomainImportBroadGlob(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern  string
		wantWarn bool
	}{
		{"**", true},
		{"**/payments", true},
		{"*/**", true},
		{"finance/**", false},
		{"{a,b}/**", false},
		{"finance/ap/*", false},
		{"finance/{ap,ar}/**", false},
	}
	for _, c := range cases {
		diags := broadGlobDiags(t, c.pattern)
		got := len(diags) > 0
		if got != c.wantWarn {
			t.Errorf("pattern %q: broad-glob warn = %v, want %v (%+v)", c.pattern, got, c.wantWarn, diags)
		}
	}
}

// A clean tree with only a bounded import draws no broad-glob warning,
// confirming the rule is silent in the common case.
func TestRuleDomainImportBroadGlob_NoFalsePositive(t *testing.T) {
	t.Parallel()
	if diags := broadGlobDiags(t, "finance/ap/*"); len(diags) != 0 {
		t.Errorf("false positive: %+v", diags)
	}
}
