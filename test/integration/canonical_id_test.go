package integration

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// TestPodiumSync_AtSegmentRejected covers F-4.2.2.
// Spec: §4.2 — a directory name containing "@" is an invalid canonical-ID
// segment ("@" is the reference version/hash delimiter). podium sync rejects
// it through the real filesystem walk.
func TestPodiumSync_AtSegmentRejected(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "finance/pay@v2/ARTIFACT.md", Content: contextArtifact,
		},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync", "--registry", registry, "--target", t.TempDir(), "--harness", "none",
	)
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for '@' segment, stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "@") {
		t.Errorf("stderr missing '@': %s", res.Stderr)
	}
}

// TestPodiumSync_RootLevelArtifactRejected covers F-4.2.1.
// Spec: §4.2 — a root-level ARTIFACT.md in single-layer mode has no canonical
// ID; podium sync rejects it. A non-multi_layer registry exercises the
// canonical-ID guard rather than the layer-path ambiguity guard.
func TestPodiumSync_RootLevelArtifactRejected(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "ARTIFACT.md", Content: contextArtifact,
		},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync", "--registry", registry, "--target", t.TempDir(), "--harness", "none",
	)
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for root-level ARTIFACT.md, stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "subdirectory") {
		t.Errorf("stderr missing 'subdirectory': %s", res.Stderr)
	}
}

// TestPodiumSync_NestedArtifactsMaterializeSeparately covers F-4.2.3.
// Spec: §4.2/§4.4 — nested artifacts are separate leaf packages. podium sync
// materializes both without error; the nested artifact lands at its own
// canonical path rather than only as the parent's bundled resource.
func TestPodiumSync_NestedArtifactsMaterializeSeparately(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "outer/ARTIFACT.md", Content: contextArtifact,
		},
		testharness.WriteTreeOption{
			Path: "outer/inner/ARTIFACT.md", Content: contextArtifact,
		},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync", "--registry", registry, "--target", target, "--harness", "none",
	)
	if res.ExitCode != 0 {
		t.Fatalf("podium sync exit=%d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	got := testharness.ReadTree(t, target)
	for _, want := range []string{"outer/ARTIFACT.md", "outer/inner/ARTIFACT.md"} {
		if _, ok := got[want]; !ok {
			t.Errorf("target missing %q (got: %v)", want, keys(got))
		}
	}
}
