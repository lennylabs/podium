package e2e

import (
	"strings"
	"testing"
)

// End-to-end coverage for the §4.2 canonical-ID invariants enforced by the
// filesystem-source walk, driven through the real `podium lint` binary.

// TestCanonicalID_LintRejectsAtSegment covers F-4.2.2.
// Spec: §4.2 — "@" is reserved for the @version/@sha256 reference suffix, so a
// directory name containing "@" is an invalid canonical-ID segment.
func TestCanonicalID_LintRejectsAtSegment(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/pay@v2/ARTIFACT.md": contextArtifact("Pay"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit == 0 {
		t.Fatalf("podium lint accepted '@' segment, stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "@") {
		t.Errorf("stderr missing '@': %s", res.Stderr)
	}
}

// TestCanonicalID_LintRejectsRootLevel covers F-4.2.1.
// Spec: §4.2 — a root-level ARTIFACT.md has no canonical ID and is rejected.
func TestCanonicalID_LintRejectsRootLevel(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"ARTIFACT.md": contextArtifact("Root"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit == 0 {
		t.Fatalf("podium lint accepted a root-level ARTIFACT.md, stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "subdirectory") {
		t.Errorf("stderr missing 'subdirectory': %s", res.Stderr)
	}
}

// TestCanonicalID_LintNestedArtifactsClean covers F-4.2.3.
// Spec: §4.2/§4.4 — nested artifacts are separate leaf packages; podium lint
// treats them as two artifacts and reports no error (no resource-drift or
// duplicated-content failure from the nested package).
func TestCanonicalID_LintNestedArtifactsClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"outer/ARTIFACT.md":       contextArtifact("Outer"),
		"outer/inner/ARTIFACT.md": contextArtifact("Inner"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("podium lint on nested artifacts exit=%d\nstderr:\n%s\nstdout:\n%s", res.Exit, res.Stderr, res.Stdout)
	}
}
