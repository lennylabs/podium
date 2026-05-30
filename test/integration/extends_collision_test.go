package integration

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// Spec: §4.6 — "A collision is rejected at ingest unless the
// higher-precedence artifact declares extends:." `podium lint` validates
// the registry, so a same-canonical-ID cross-layer duplicate without
// extends is surfaced as an ingest.collision error (silent shadowing is
// never permitted). Drives the real binary through filesystem.Walk.
func TestPodiumLint_CollisionWithoutExtendsErrors(t *testing.T) {
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

// Spec: §4.6 — the collision is permitted when the higher-precedence
// record declares extends: pointing at the colliding canonical ID; the
// overlay extends the lower-precedence artifact rather than shadowing it.
// `podium lint` must accept it cleanly.
func TestPodiumLint_CollisionWithExtendsAccepted(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	overlay := "---\ntype: context\nversion: 2.0.0\ndescription: overlay\nextends: shared/note\n---\n\noverlay body\n"
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: ".registry-config", Content: "multi_layer: true\nlayer_order:\n  - org\n  - team\n",
		},
		testharness.WriteTreeOption{
			Path: "org/shared/note/ARTIFACT.md", Content: contextArtifact,
		},
		testharness.WriteTreeOption{
			Path: "team/shared/note/ARTIFACT.md", Content: overlay,
		},
	)
	res := cmdharness.Run(t, "podium", "", "lint", "--registry", registry)
	if res.ExitCode != 0 {
		t.Fatalf("podium lint exit=%d, want 0 for an extends overlay\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if strings.Contains(res.Stdout+res.Stderr, "collision") {
		t.Errorf("extends overlay should not raise a collision diagnostic:\nstdout=%q", res.Stdout)
	}
}
