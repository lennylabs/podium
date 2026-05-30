package sync

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// scopeRegistry writes a three-artifact filesystem registry: two skills under
// finance/ and one context under shared/. Returns the registry dir.
func scopeRegistry(t *testing.T) string {
	t.Helper()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team/finance/a/ARTIFACT.md", Content: contextArtifactSrc},
		testharness.WriteTreeOption{Path: "team/finance/b/ARTIFACT.md", Content: contextArtifactSrc},
		testharness.WriteTreeOption{Path: "team/shared/c/ARTIFACT.md", Content: contextArtifactSrc},
	)
	return registry
}

// Spec: §7.5.1 — --include narrows the materialized set to canonical IDs
// matching at least one include glob, and the resolved scope is persisted into
// the lock (§7.5.3).
func TestRun_ScopeIncludeNarrows(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	res, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		Scope:        ScopeFilter{Include: []string{"finance/**"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2 (finance/a, finance/b)", len(res.Artifacts))
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got["shared/c/ARTIFACT.md"]; ok {
		t.Errorf("shared/c must be excluded by include scope, got files: %v", keys(got))
	}
	for _, want := range []string{"finance/a/ARTIFACT.md", "finance/b/ARTIFACT.md"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected %q, got files: %v", want, keys(got))
		}
	}
	// §7.5.3: the resolved scope is recorded in the lock.
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if len(lock.Scope.Include) != 1 || lock.Scope.Include[0] != "finance/**" {
		t.Errorf("lock.Scope.Include = %v, want [finance/**]", lock.Scope.Include)
	}
}

// Spec: §7.5.1 — --exclude removes matching IDs after the include set.
func TestRun_ScopeExcludeRemoves(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	_, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		Scope: ScopeFilter{
			Include: []string{"finance/**"},
			Exclude: []string{"finance/b"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got["finance/b/ARTIFACT.md"]; ok {
		t.Errorf("finance/b must be excluded, got files: %v", keys(got))
	}
	if _, ok := got["finance/a/ARTIFACT.md"]; !ok {
		t.Errorf("finance/a must remain, got files: %v", keys(got))
	}
}

// Spec: §7.5.1 — --type restricts to the listed artifact types. A type that
// matches nothing yields an empty materialization.
func TestRun_ScopeTypeFilters(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	res, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		Scope:        ScopeFilter{Types: []string{"skill"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Artifacts) != 0 {
		t.Errorf("no context artifact should pass a skill-only type filter, got %d", len(res.Artifacts))
	}
}

// Spec: §7.5.4 / §7.5.5 — with PreserveToggles set (watch / override mode) Run
// materializes profile + toggles: toggles.add brings an out-of-scope artifact
// in, toggles.remove drops an in-scope one, and the toggles survive into the
// rewritten lock.
func TestRun_PreserveTogglesAppliesAddAndRemove(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	// Seed a lock: scope includes finance/**, but toggles add shared/c and
	// remove finance/a.
	if err := WriteLock(target, &LockFile{
		Version: 1, Target: target,
		Scope: LockScope{Include: []string{"finance/**"}},
		Toggles: LockToggles{
			Add:    []LockToggle{{ID: "shared/c"}},
			Remove: []LockToggle{{ID: "finance/a"}},
		},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	_, err := Run(Options{
		RegistryPath:    registry,
		Target:          target,
		Scope:           ScopeFilter{Include: []string{"finance/**"}},
		PreserveToggles: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got["shared/c/ARTIFACT.md"]; !ok {
		t.Errorf("toggles.add shared/c must materialize, got: %v", keys(got))
	}
	if _, ok := got["finance/a/ARTIFACT.md"]; ok {
		t.Errorf("toggles.remove finance/a must not materialize, got: %v", keys(got))
	}
	if _, ok := got["finance/b/ARTIFACT.md"]; !ok {
		t.Errorf("in-scope finance/b must materialize, got: %v", keys(got))
	}
	lock, _ := ReadLock(target)
	if len(lock.Toggles.Add) != 1 || len(lock.Toggles.Remove) != 1 {
		t.Errorf("toggles not preserved into rewritten lock: %+v", lock.Toggles)
	}
}

// Spec: §7.5.4 — a manual sync (PreserveToggles false) ignores and clears the
// lock's toggles, the "reset to baseline" gesture.
func TestRun_ManualSyncClearsToggles(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	if err := WriteLock(target, &LockFile{
		Version: 1, Target: target,
		Toggles: LockToggles{Add: []LockToggle{{ID: "shared/c"}}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	_, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		Scope:        ScopeFilter{Include: []string{"finance/**"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock, _ := ReadLock(target)
	if len(lock.Toggles.Add) != 0 || len(lock.Toggles.Remove) != 0 {
		t.Errorf("manual sync must clear toggles, got: %+v", lock.Toggles)
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got["shared/c/ARTIFACT.md"]; ok {
		t.Errorf("cleared toggle add must not materialize, got: %v", keys(got))
	}
}
