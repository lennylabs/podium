package sync_test

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sync"
)

const ctxArtifact = `---
type: context
version: 1.0.0
description: a context
---

body
`

// overrideRegistry writes finance/a, finance/b, shared/c into a filesystem
// registry and returns the registry directory.
func overrideRegistry(t *testing.T) string {
	t.Helper()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team/finance/a/ARTIFACT.md", Content: ctxArtifact},
		testharness.WriteTreeOption{Path: "team/finance/b/ARTIFACT.md", Content: ctxArtifact},
		testharness.WriteTreeOption{Path: "team/shared/c/ARTIFACT.md", Content: ctxArtifact},
	)
	return registry
}

// Spec: §7.5.5 — `podium sync override --add <id>` fetches and writes the
// artifact through the active harness adapter, just like a full sync would.
// The added artifact is out of the baseline scope but visible, so it lands on
// disk.
func TestOverride_AddMaterializesFile(t *testing.T) {
	t.Parallel()
	registry := overrideRegistry(t)
	target := t.TempDir()

	// Baseline sync with a scope that excludes shared/c.
	if _, err := sync.Run(sync.Options{
		RegistryPath: registry,
		Target:       target,
		Scope:        sync.ScopeFilter{Include: []string{"finance/**"}},
	}); err != nil {
		t.Fatalf("baseline Run: %v", err)
	}
	if _, ok := testharness.ReadTree(t, target)["shared/c/ARTIFACT.md"]; ok {
		t.Fatalf("precondition: shared/c should not be materialized by the baseline scope")
	}

	// Override adds shared/c with a registry, so it must materialize.
	if _, err := sync.Override(sync.OverrideOptions{
		Target:       target,
		Add:          []string{"shared/c"},
		RegistryPath: registry,
		Clock:        newClock(),
	}); err != nil {
		t.Fatalf("Override add: %v", err)
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got["shared/c/ARTIFACT.md"]; !ok {
		t.Errorf("override --add must write shared/c to disk, got: %v", treeKeys(got))
	}
	// The toggle persists in the lock.
	lock, _ := sync.ReadLock(target)
	if len(lock.Toggles.Add) != 1 || lock.Toggles.Add[0].ID != "shared/c" {
		t.Errorf("toggles.add not recorded: %+v", lock.Toggles)
	}
}

// Spec: §7.5.5 — `--remove <id>` deletes the artifact's materialized files
// from the target.
func TestOverride_RemoveDeletesFiles(t *testing.T) {
	t.Parallel()
	registry := overrideRegistry(t)
	target := t.TempDir()

	if _, err := sync.Run(sync.Options{
		RegistryPath: registry,
		Target:       target,
		Scope:        sync.ScopeFilter{Include: []string{"finance/**"}},
	}); err != nil {
		t.Fatalf("baseline Run: %v", err)
	}
	if _, ok := testharness.ReadTree(t, target)["finance/a/ARTIFACT.md"]; !ok {
		t.Fatalf("precondition: finance/a should be materialized")
	}

	if _, err := sync.Override(sync.OverrideOptions{
		Target:       target,
		Remove:       []string{"finance/a"},
		RegistryPath: registry,
		Clock:        newClock(),
	}); err != nil {
		t.Fatalf("Override remove: %v", err)
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got["finance/a/ARTIFACT.md"]; ok {
		t.Errorf("override --remove must delete finance/a, still present: %v", treeKeys(got))
	}
	if _, ok := got["finance/b/ARTIFACT.md"]; !ok {
		t.Errorf("override --remove must not touch finance/b, got: %v", treeKeys(got))
	}
}

// Spec: §7.5.5 — --dry-run records nothing and writes no files.
func TestOverride_AddDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	registry := overrideRegistry(t)
	target := t.TempDir()
	if _, err := sync.Run(sync.Options{
		RegistryPath: registry, Target: target,
		Scope: sync.ScopeFilter{Include: []string{"finance/**"}},
	}); err != nil {
		t.Fatalf("baseline Run: %v", err)
	}
	if _, err := sync.Override(sync.OverrideOptions{
		Target: target, Add: []string{"shared/c"},
		RegistryPath: registry, DryRun: true, Clock: newClock(),
	}); err != nil {
		t.Fatalf("Override dry-run: %v", err)
	}
	if _, ok := testharness.ReadTree(t, target)["shared/c/ARTIFACT.md"]; ok {
		t.Errorf("dry-run override must not write shared/c")
	}
}

func treeKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
