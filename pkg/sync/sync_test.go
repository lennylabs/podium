package sync

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

const (
	skillArtifactSrc = `---
type: skill
version: 1.0.0
---

`
	skillBodySrc = `---
name: hello-world
description: Say hello.
---

Body.
`
	contextArtifactSrc = `---
type: context
version: 1.0.0
description: glossary
---

Glossary body.
`
)

// Spec: §7.5 podium sync — Run materializes every visible artifact through
// the configured HarnessAdapter to the target directory.
// Phase: 0
func TestRun_SyncsArtifactsToTarget(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: ".registry-config", Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path: "team-shared/greetings/hello/ARTIFACT.md", Content: skillArtifactSrc,
		},
		testharness.WriteTreeOption{
			Path: "team-shared/greetings/hello/SKILL.md", Content: skillBodySrc,
		},
		testharness.WriteTreeOption{
			Path: "team-shared/company-glossary/ARTIFACT.md", Content: contextArtifactSrc,
		},
	)
	res, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		AdapterID:    "none",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Artifacts) != 2 {
		t.Fatalf("got %d artifacts in result, want 2", len(res.Artifacts))
	}

	got := testharness.ReadTree(t, target)
	for _, want := range []string{
		"company-glossary/ARTIFACT.md",
		"greetings/hello/ARTIFACT.md",
		"greetings/hello/SKILL.md",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected file %q in target, got files: %v", want, keys(got))
		}
	}
}

// Spec: §7.5 — DryRun resolves the artifact set without writing.
// Phase: 0
func TestRun_DryRunWritesNothing(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "company-glossary/ARTIFACT.md", Content: contextArtifactSrc,
		},
	)
	res, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Artifacts) != 1 {
		t.Errorf("got %d artifacts, want 1", len(res.Artifacts))
	}
	got := testharness.ReadTree(t, target)
	if len(got) != 0 {
		t.Errorf("dry-run target had %d files, want 0", len(got))
	}
}

// Spec: §6.7 — Run uses the "none" adapter by default when AdapterID is
// empty.
// Phase: 0
func TestRun_DefaultAdapterIsNone(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md", Content: contextArtifactSrc,
		},
	)
	res, err := Run(Options{RegistryPath: registry, Target: target})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Adapter != "none" {
		t.Errorf("Adapter = %q, want none", res.Adapter)
	}
}

// Spec: §6.10 namespace — an unknown adapter returns config.unknown_harness.
// Phase: 0
// Matrix: §6.10 (config.unknown_harness)
func TestRun_UnknownAdapterFails(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md", Content: contextArtifactSrc,
		},
	)
	_, err := Run(Options{
		RegistryPath: registry,
		Target:       t.TempDir(),
		AdapterID:    "not-an-adapter",
	})
	if err == nil {
		t.Fatalf("expected error for unknown adapter")
	}
}

// Spec: §13.11.3 — when no target is set and DryRun is false, Run errors.
// Phase: 0
func TestRun_RequiresTarget(t *testing.T) {
	t.Parallel()
	_, err := Run(Options{RegistryPath: t.TempDir(), Target: ""})
	if !errors.Is(err, ErrNoTarget) {
		t.Fatalf("got %v, want ErrNoTarget", err)
	}
}

// Spec: §4.6 — when two layers both contribute the same canonical ID,
// sync uses highest-precedence wins (the user's effective view), not the
// raw ingest behavior that errors on collision.
// Phase: 0
func TestRun_HigherLayerWinsOnCollision(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: ".registry-config",
			Content: `multi_layer: true
layer_order:
  - team-shared
  - personal
`,
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: shared\n---\n\nshared body\n",
		},
		testharness.WriteTreeOption{
			Path:    "personal/x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 2.0.0\ndescription: personal\n---\n\npersonal body\n",
		},
	)
	_, err := Run(Options{RegistryPath: registry, Target: target})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := testharness.ReadTree(t, target)
	if !contains(got["x/ARTIFACT.md"], "personal") {
		t.Errorf("expected personal layer's content, got: %s", got["x/ARTIFACT.md"])
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
