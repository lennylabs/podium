// Package integration holds tests that exercise multiple Podium components
// together, running the real binaries via cmdharness against fixtures
// staged on disk. Each test in this package owns its temp directory tree.
package integration

import (
	"path/filepath"
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

// Spec: §13.11.3 / §4.6 — `podium sync` against a filesystem source resolves
// extends: through the same merge the registry applies at load time, so the
// materialized output carries the merged, extends-stripped frontmatter rather
// than the child's authored bytes. This is the §13.11.6 equivalent-output
// guarantee on the filesystem side.
func TestPodiumSync_FilesystemSourceResolvesExtends(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	parent := "---\ntype: context\nversion: 1.0.0\nname: Base\ndescription: parent desc\nsensitivity: low\ntags:\n  - shared\n---\n\nParent body.\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child desc\nsensitivity: high\nextends: x\ntags:\n  - team\n---\n\nChild body.\n"
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: ".registry-config", Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
		},
		testharness.WriteTreeOption{Path: "team-shared/x/ARTIFACT.md", Content: parent},
		testharness.WriteTreeOption{Path: "personal/x/ARTIFACT.md", Content: child},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync", "--registry", registry, "--target", target, "--harness", "none",
	)
	if res.ExitCode != 0 {
		t.Fatalf("podium sync exit=%d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	got := testharness.ReadTree(t, target)
	merged, ok := got["x/ARTIFACT.md"]
	if !ok {
		t.Fatalf("target missing x/ARTIFACT.md (got: %v)", keys(got))
	}
	// Child wins on description; parent name is inherited; sensitivity is
	// most-restrictive; tags union; extends is stripped.
	for _, want := range []string{"description: child desc", "name: Base", "sensitivity: high"} {
		if !strings.Contains(merged, want) {
			t.Errorf("merged frontmatter missing %q:\n%s", want, merged)
		}
	}
	if !strings.Contains(merged, "shared") || !strings.Contains(merged, "team") {
		t.Errorf("merged tags missing the union of shared+team:\n%s", merged)
	}
	if strings.Contains(merged, "extends:") {
		t.Errorf("merged frontmatter must strip extends (§4.6 hidden parent):\n%s", merged)
	}
}

// Spec: §4.6 — "The child's type: must match the parent's; ingest
// rejects an extends: chain that crosses types." `podium sync` over a
// filesystem source resolves extends through the same merge; a cross-type
// chain must be rejected rather than silently materialized with the parent's
// fields folded into a differently-typed child. End-to-end via the real binary.
func TestPodiumSync_FilesystemSourceRejectsCrossTypeExtends(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: base agent\n---\n\nagent body\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: overlay context\nextends: x\n---\n\ncontext body\n"
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path: ".registry-config", Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
		},
		testharness.WriteTreeOption{Path: "team-shared/x/ARTIFACT.md", Content: parent},
		testharness.WriteTreeOption{Path: "personal/x/ARTIFACT.md", Content: child},
	)
	res := cmdharness.Run(t, "podium", "",
		"sync", "--registry", registry, "--target", target, "--harness", "none",
	)
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a cross-type extends chain, stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "extends.type_mismatch") {
		t.Errorf("stderr missing extends.type_mismatch: %s", res.Stderr)
	}
}

// Spec: §13.11.2 — when defaults.registry is unset across all scopes and no
// --registry / PODIUM_REGISTRY is given, the CLI errors with config.no_registry
// and points the user at podium init.
func TestPodiumSync_UnsetRegistryEmitsNoRegistryCode(t *testing.T) {
	t.Parallel()
	// A workspace with a .podium/ dir but no defaults.registry in any scope.
	ws := t.TempDir()
	testharness.WriteTree(t, ws,
		testharness.WriteTreeOption{Path: ".podium/sync.yaml", Content: "defaults:\n  harness: none\n"},
	)
	res := cmdharness.Run(t, "podium", ws, "sync", "--target", t.TempDir())
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "config.no_registry") {
		t.Errorf("stderr missing config.no_registry: %s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "podium init") {
		t.Errorf("stderr missing the podium init pointer: %s", res.Stderr)
	}
}

// Spec: §7.5 — --dry-run resolves the artifact set and writes nothing.
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
	// spec: §13.10 — the operator sees the documented
	// config.layer_path_ambiguous code, not only an English phrase.
	if !strings.Contains(res.Stderr, "config.layer_path_ambiguous") {
		t.Errorf("stderr missing 'config.layer_path_ambiguous': %s", res.Stderr)
	}
}

// Spec: §6.10 namespace — unknown harness selection fails with a
// config.unknown_harness error.
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
	all := testharness.ReadTree(t, target)
	// §7.5.3 sync always writes a `.podium/sync.lock`; filter it
	// from the artifact comparison since it's deployment metadata,
	// not artifact output.
	got := map[string]string{}
	for k, v := range all {
		if !strings.HasPrefix(k, ".podium/") {
			got[k] = v
		}
	}
	wantPaths := []string{"x/ARTIFACT.md", "y/ARTIFACT.md"}
	if len(got) != len(wantPaths) {
		t.Fatalf("after 2 runs got %d artifact files, want %d (%v)", len(got), len(wantPaths), keys(got))
	}
	for _, want := range wantPaths {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q after second run", want)
		}
	}
}

// Spec: §7.5.3 / §14.11 — when --target already names the harness
// config directory (./build/.claude/, as in the §14.11 pipeline step), the
// claude-code adapter's .claude/ prefix must not be doubled on disk, and the
// committed lock records each materialized_path relative to the target
// (agents/pay-invoice.md), matching the §7.5.3 lock example. End-to-end via the
// real binary against a single-layer filesystem source.
func TestPodiumSync_TargetNamesHarnessConfigDir_NoDoubledClaude(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	agent := "---\ntype: agent\nversion: 1.2.0\ndescription: Pay an invoice.\n---\n\nPay-invoice body.\n"
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: "finance/ap/pay-invoice/ARTIFACT.md", Content: agent},
	)
	// The target's final segment is the claude-code config dir.
	target := filepath.Join(t.TempDir(), "build", ".claude")
	res := cmdharness.Run(t, "podium", "",
		"sync", "--registry", registry, "--target", target, "--harness", "claude-code",
	)
	if res.ExitCode != 0 {
		t.Fatalf("podium sync exit=%d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	got := testharness.ReadTree(t, target)
	// Single .claude/ deep: the agent lands directly under the target.
	if _, ok := got["agents/pay-invoice.md"]; !ok {
		t.Errorf("target missing agents/pay-invoice.md (got: %v)", keys(got))
	}
	// No doubled .claude/.claude/ segment.
	for p := range got {
		if strings.HasPrefix(p, ".claude/") {
			t.Errorf("doubled .claude/ segment under target: %q", p)
		}
	}
	// The committed lock records the path relative to the target.
	lock, ok := got[".podium/sync.lock"]
	if !ok {
		t.Fatalf("target missing .podium/sync.lock (got: %v)", keys(got))
	}
	if !strings.Contains(lock, "materialized_path: agents/pay-invoice.md") {
		t.Errorf("lock missing relative materialized_path:\n%s", lock)
	}
	if strings.Contains(lock, "materialized_path: .claude/") {
		t.Errorf("lock recorded a doubled .claude/ materialized_path:\n%s", lock)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
