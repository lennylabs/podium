package integration

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// Spec: §4.6 — a same-ID contribution from two registry-side layers is a
// collision. `podium lint` validates the registry, so the duplicate is
// surfaced as an ingest.collision error. Drives the real binary through
// filesystem.Walk.
func TestPodiumLint_CollisionErrors(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: ".registry-config", Content: "multi_layer: true\nlayer_order:\n  - org\n  - team\n",
		},
		testharness.WriteTreeOption{
			Path: "org/shared/note/ARTIFACT.md", Content: contextArtifact,
		},
		testharness.WriteTreeOption{
			Path: "team/shared/note/ARTIFACT.md", Content: contextArtifact,
		},
	)
	res := cmdharness.Run(t, "podium", "", "lint", "--registry", registry)
	if res.ExitCode == 0 {
		t.Fatalf("podium lint exit=0, want non-zero for a same-ID collision\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout+res.Stderr, "collision") {
		t.Errorf("output missing a collision diagnostic:\nstdout=%q\nstderr=%q", res.Stdout, res.Stderr)
	}
}
