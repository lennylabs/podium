package e2e

import "testing"

// Spec: §4.4 — a prose reference whose target begins with / names a path on
// a website and is not checked, so a skill whose only links are root-relative
// lints clean.
func TestLint_RootRelativeProseReferenceClean(t *testing.T) {
	t.Parallel()
	skill := "---\nname: analytics\ndescription: Explain the analytics dashboard when the user asks how a metric is computed.\n" +
		"metadata:\n  version: \"1.0.0\"\n---\n\n" +
		"See [analytics](/features/analytics) and [the setup guide](/docs/setup.md#install).\n"
	reg := writeRegistry(t, map[string]string{"product/analytics/SKILL.md": skill})
	res := runPodium(t, "", []string{"HOME=" + t.TempDir()}, "lint", "--registry", reg)
	cliWantExit(t, res, 0, "lint over a root-relative prose reference")
	cliNotContains(t, res.Stdout, "lint.prose_reference", "a root-relative target is not checked")
	cliContains(t, res.Stdout, "lint: no issues.", "the clean-run line")
}
