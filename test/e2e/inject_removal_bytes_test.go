package e2e

// End-to-end tests for the removal of Podium's inject block from a shared
// instruction file. A rule that leaves the sync scope has its block removed
// from GEMINI.md, and the bytes the operator wrote around the block come back
// exactly: the final newline or its absence, blank lines, and CRLF line
// endings.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// injectRemovalRegistry is a registry with two always-mode rules that the
// gemini adapter injects into GEMINI.md.
func injectRemovalRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		"rules/alpha/ARTIFACT.md": rmRuleArtifact("alpha", "always", nil, "Alpha rule.\n"),
		"rules/bravo/ARTIFACT.md": rmRuleArtifact("bravo", "always", nil, "Bravo rule.\n"),
	})
}

// injectRemovalSync runs podium sync for the gemini harness with the given
// scope flags and fails the test on a non-zero exit.
func injectRemovalSync(t *testing.T, reg, target string, scope ...string) {
	t.Helper()
	args := append([]string{"sync", "--registry", reg, "--target", target, "--harness", "gemini"}, scope...)
	if res := runPodium(t, "", nil, args...); res.Exit != 0 {
		t.Fatalf("sync %v exit=%d stderr=%s", scope, res.Exit, res.Stderr)
	}
}

// Spec: §6.7, §7.5.3 — a sync that narrows the scope so no rule injects into
// GEMINI.md removes Podium's blocks and restores the operator's file byte for
// byte, and a file Podium created holds no bytes afterwards.
func TestInjectRemoval_NarrowedScopeRestoresOperatorBytes(t *testing.T) {
	t.Parallel()
	reg := injectRemovalRegistry(t)
	// An absent operator file is the case where Podium creates GEMINI.md, so
	// the file holds no bytes once the blocks are gone.
	cases := []struct {
		name     string
		operator string
		present  bool
	}{
		{"trailing newline", "# House rules\n\nKeep PRs small.\n", true},
		{"no trailing newline", "# House rules\n\nKeep PRs small.", true},
		{"trailing blank line", "# House rules\n\nKeep PRs small.\n\n", true},
		{"crlf", "# House rules\r\n\r\nKeep PRs small.\r\n", true},
		{"absent", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			target := t.TempDir()
			gemini := filepath.Join(target, "GEMINI.md")
			if c.present {
				if err := os.WriteFile(gemini, []byte(c.operator), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			injectRemovalSync(t, reg, target)
			if got := readFile(t, gemini); !strings.Contains(got, "Alpha rule.") || !strings.Contains(got, "Bravo rule.") {
				t.Fatalf("first sync did not inject both rules:\n%q", got)
			}
			injectRemovalSync(t, reg, target, "--exclude", "rules/*")
			want := c.operator
			if got := readFile(t, gemini); got != want {
				t.Errorf("after the rules left scope GEMINI.md = %q (%d bytes), want %q (%d bytes)", got, len(got), want, len(want))
			}
		})
	}
}

// Spec: §6.7, §7.5.3 — a sync that narrows the scope from two rules to one
// leaves GEMINI.md byte-identical to a sync that selected only that rule from
// the start, so removing a block disturbs neither the operator's bytes nor the
// remaining block.
func TestInjectRemoval_NarrowedScopeMatchesSingleRuleSync(t *testing.T) {
	t.Parallel()
	reg := injectRemovalRegistry(t)
	const operator = "# House rules\n\nKeep PRs small.\n"
	for _, remaining := range []string{"rules/alpha", "rules/bravo"} {
		narrowed, direct := t.TempDir(), t.TempDir()
		for _, dir := range []string{narrowed, direct} {
			if err := os.WriteFile(filepath.Join(dir, "GEMINI.md"), []byte(operator), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		injectRemovalSync(t, reg, narrowed)
		injectRemovalSync(t, reg, narrowed, "--include", remaining)
		injectRemovalSync(t, reg, direct, "--include", remaining)
		got := readFile(t, filepath.Join(narrowed, "GEMINI.md"))
		want := readFile(t, filepath.Join(direct, "GEMINI.md"))
		if got != want {
			t.Errorf("narrowed to %s: GEMINI.md = %q, want %q", remaining, got, want)
		}
	}
}
