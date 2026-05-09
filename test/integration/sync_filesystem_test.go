// Package integration holds tests that exercise multiple Podium components
// together, running the real binaries via cmdharness against fixtures
// staged on disk. Each test in this package owns its temp directory tree.
package integration

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

const (
	skillArtifact = `---
type: skill
version: 1.0.0
---

`
	skillBody = `---
name: hello-world
description: Say hello.
---

Body.
`
	contextArtifact = `---
type: context
version: 1.0.0
description: Glossary.
---

Glossary body.
`
)

// Spec: §13.11.3 What's Available — `podium sync` against a filesystem
// source materializes every visible artifact through the harness adapter
// to the target directory. End-to-end via the real binary.
// Phase: 0
func TestPodiumSync_FilesystemSourceWritesTarget(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: ".registry-config", Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path: "team-shared/greetings/hello/ARTIFACT.md", Content: skillArtifact,
		},
		testharness.WriteTreeOption{
			Path: "team-shared/greetings/hello/SKILL.md", Content: skillBody,
		},
		testharness.WriteTreeOption{
			Path: "team-shared/company-glossary/ARTIFACT.md", Content: contextArtifact,
		},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync",
		"--registry", registry,
		"--target", target,
		"--harness", "none",
	)
	if res.ExitCode != 0 {
		t.Fatalf("podium sync exit=%d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	got := testharness.ReadTree(t, target)
	wantPaths := []string{
		"company-glossary/ARTIFACT.md",
		"greetings/hello/ARTIFACT.md",
		"greetings/hello/SKILL.md",
	}
	for _, want := range wantPaths {
		if _, ok := got[want]; !ok {
			t.Errorf("target missing %q (got: %v)", want, keys(got))
		}
	}
}

// Spec: §7.5 — --dry-run resolves the artifact set and writes nothing.
// Phase: 0
func TestPodiumSync_DryRunWritesNothing(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "company-glossary/ARTIFACT.md", Content: contextArtifact,
		},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync",
		"--registry", registry,
		"--target", target,
		"--dry-run",
	)
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "dry-run") {
		t.Errorf("stdout missing dry-run notice: %s", res.Stdout)
	}
	got := testharness.ReadTree(t, target)
	if len(got) != 0 {
		t.Errorf("dry-run target had %d files, want 0", len(got))
	}
}

// Spec: §13.10 --layer-path modes — multi_layer: true with manifest at top
// level fails with config.layer_path_ambiguous; the CLI exits non-zero
// and the operator sees the structured error.
// Phase: 0
func TestPodiumSync_LayerPathAmbiguousIsRejected(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: ".registry-config", Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path: "ARTIFACT.md", Content: contextArtifact,
		},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync",
		"--registry", registry,
		"--target", t.TempDir(),
	)
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "ambiguous") {
		t.Errorf("stderr missing 'ambiguous': %s", res.Stderr)
	}
}

// Spec: §6.10 namespace — unknown harness selection fails with a
// config.unknown_harness error.
// Phase: 0
func TestPodiumSync_UnknownHarnessFails(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md", Content: contextArtifact,
		},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync",
		"--registry", registry,
		"--target", t.TempDir(),
		"--harness", "not-an-adapter",
	)
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("stderr missing config.unknown_harness: %s", res.Stderr)
	}
}

// Spec: §7.5.3 Lock file (idempotency precursor) — running `podium sync`
// twice against the same target produces the same on-disk state and does
// not duplicate or corrupt files. The full lock-file behavior lands in
// Phase 3; Phase 0 verifies the on-disk equivalence only.
// Phase: 0
func TestPodiumSync_IsIdempotent(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md", Content: contextArtifact,
		},
		testharness.WriteTreeOption{
			Path: "y/ARTIFACT.md", Content: contextArtifact,
		},
	)
	for i := 0; i < 2; i++ {
		res := cmdharness.Run(t, "podium", "",
			"sync",
			"--registry", registry,
			"--target", target,
		)
		if res.ExitCode != 0 {
			t.Fatalf("run %d: exit=%d stderr:\n%s", i, res.ExitCode, res.Stderr)
		}
	}
	got := testharness.ReadTree(t, target)
	wantPaths := []string{"x/ARTIFACT.md", "y/ARTIFACT.md"}
	if len(got) != len(wantPaths) {
		t.Fatalf("after 2 runs got %d files, want %d (%v)", len(got), len(wantPaths), keys(got))
	}
	for _, want := range wantPaths {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q after second run", want)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
