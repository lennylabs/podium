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

// Spec: §4.3 target_harnesses — sync skips an artifact whose
// target_harnesses excludes the active adapter and materializes one that
// includes it (or omits the field). The skipped ID is recorded in
// Result.Skipped so the omission is reported rather than silent.
func TestRun_TargetHarnessesFiltersAdapter(t *testing.T) {
	t.Parallel()
	ccOnly := `---
type: context
version: 1.0.0
description: claude-code only
target_harnesses: [claude-code]
---

body
`
	noneTargeted := `---
type: context
version: 1.0.0
description: targets none adapter
target_harnesses: [none]
---

body
`
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: "team/cc-only/ARTIFACT.md", Content: ccOnly},
		testharness.WriteTreeOption{Path: "team/none-targeted/ARTIFACT.md", Content: noneTargeted},
		testharness.WriteTreeOption{Path: "team/everywhere/ARTIFACT.md", Content: contextArtifactSrc},
	)
	res, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "none"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(res.Skipped) != 1 || res.Skipped[0] != "team/cc-only" {
		t.Errorf("Skipped = %v, want [team/cc-only]", res.Skipped)
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got["team/cc-only/ARTIFACT.md"]; ok {
		t.Errorf("claude-code-only artifact must not materialize under the none adapter: %v", keys(got))
	}
	for _, want := range []string{"team/none-targeted/ARTIFACT.md", "team/everywhere/ARTIFACT.md"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected %q to materialize, got files: %v", want, keys(got))
		}
	}
}

// Spec: §7.5 — DryRun resolves the artifact set without writing.
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

// Spec: §6.9 / §6.7 — Run enforces the §6.7.1 ✗-cell contract before
// adapting, so a plugin-layout type on claude-cowork fails with
// materialize.untranslatable instead of silently producing no output. This
// matches the MCP server load_artifact guard (cmd/podium-mcp/main.go), keeping
// the two canonical-Adapt consumers at parity (§2.2).
func TestRun_CoworkPluginLayoutTypeFailsUntranslatable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		artifact string
	}{
		{
			name:     "skill",
			artifact: skillArtifactSrc,
		},
		{
			// An unset rule_mode defaults to always (§4.3), which is a ✗
			// cell for cowork; the guard must fire for the default mode too.
			name:     "rule-default-mode",
			artifact: "---\ntype: rule\nversion: 1.0.0\n---\n\nRule body.\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			registry := t.TempDir()
			target := t.TempDir()
			opts := []testharness.WriteTreeOption{
				{Path: "team/plugin-type/ARTIFACT.md", Content: tc.artifact},
			}
			if tc.name == "skill" {
				opts = append(opts, testharness.WriteTreeOption{
					Path: "team/plugin-type/SKILL.md", Content: skillBodySrc,
				})
			}
			testharness.WriteTree(t, registry, opts...)
			_, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "claude-cowork"})
			if err == nil {
				t.Fatalf("Run on claude-cowork over a %s artifact: want error, got nil", tc.name)
			}
			if !contains(err.Error(), "materialize.untranslatable") {
				t.Errorf("error = %v, want materialize.untranslatable", err)
			}
			// The guard fails before writing, so the target stays empty.
			if got := testharness.ReadTree(t, target); len(got) != 0 {
				t.Errorf("target had %d files after a failed sync, want 0: %v", len(got), keys(got))
			}
		})
	}
}

// Spec: §6.7 / §6.7.1 — the cowork context cell stays ✓, so a type: context
// artifact still materializes onto claude-cowork through the §6.9 guard.
func TestRun_CoworkContextStillMaterializes(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: "team/glossary/ARTIFACT.md", Content: contextArtifactSrc},
	)
	res, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "claude-cowork"})
	if err != nil {
		t.Fatalf("Run on claude-cowork over a context artifact: %v", err)
	}
	if len(res.Artifacts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(res.Artifacts))
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got[".podium/context/team/glossary/ARTIFACT.md"]; !ok {
		t.Errorf("cowork context did not materialize to .podium/context/, got: %v", keys(got))
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
