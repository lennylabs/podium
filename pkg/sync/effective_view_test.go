package sync

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// fsRegistryTwo stages a two-layer-less filesystem registry with two visible
// artifacts (greetings/hello, company-glossary) and returns its path.
func fsRegistryTwo(t *testing.T) string {
	t.Helper()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team-shared/greetings/hello/ARTIFACT.md", Content: contextArtifactSrc},
		testharness.WriteTreeOption{Path: "team-shared/company-glossary/ARTIFACT.md", Content: contextArtifactSrc},
	)
	return registry
}

// spec: §7.5.5 — ResolveEffectiveView returns every artifact the caller can see
// so the override checklist can render it. With no prior lock, no artifact is
// materialized.
func TestResolveEffectiveView_AllVisibleNoneMaterialized(t *testing.T) {
	registry := fsRegistryTwo(t)
	target := t.TempDir()

	view, err := ResolveEffectiveView(Options{RegistryPath: registry, Target: target})
	if err != nil {
		t.Fatalf("ResolveEffectiveView: %v", err)
	}
	if len(view) != 2 {
		t.Fatalf("view has %d artifacts, want 2: %+v", len(view), view)
	}
	wantIDs := map[string]bool{"greetings/hello": false, "company-glossary": false}
	for _, a := range view {
		if _, ok := wantIDs[a.ID]; !ok {
			t.Errorf("unexpected artifact %q", a.ID)
		}
		if a.Materialized {
			t.Errorf("artifact %q materialized with no prior sync", a.ID)
		}
		if a.Layer == "" {
			t.Errorf("artifact %q has empty layer", a.ID)
		}
	}
}

// spec: §7.5.5 — after a sync, the artifacts the target materialized are flagged
// so the checklist starts them checked.
func TestResolveEffectiveView_MaterializedFlagFromLock(t *testing.T) {
	registry := fsRegistryTwo(t)
	target := t.TempDir()
	if _, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "none"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	view, err := ResolveEffectiveView(Options{RegistryPath: registry, Target: target})
	if err != nil {
		t.Fatalf("ResolveEffectiveView: %v", err)
	}
	if len(view) != 2 {
		t.Fatalf("view has %d artifacts, want 2", len(view))
	}
	for _, a := range view {
		if !a.Materialized {
			t.Errorf("artifact %q not flagged materialized after sync", a.ID)
		}
	}
}

// spec: §7.5.5 — with no registry source and no lock there is nothing to show;
// the view is empty rather than an error so the caller prints guidance.
func TestResolveEffectiveView_EmptyWhenNoSourceNoLock(t *testing.T) {
	view, err := ResolveEffectiveView(Options{RegistryPath: "", Target: t.TempDir()})
	if err != nil {
		t.Fatalf("ResolveEffectiveView: %v", err)
	}
	if len(view) != 0 {
		t.Fatalf("view = %+v, want empty", view)
	}
}

// spec: §7.5.5 — a materialized artifact the source no longer exposes still
// appears (folded in from the lock) so the checklist can remove it.
func TestResolveEffectiveView_FoldsLockOnlyArtifact(t *testing.T) {
	target := t.TempDir()
	lock := &LockFile{
		Version: 1,
		Target:  target,
		Artifacts: []LockArtifact{
			{ID: "finance/legacy", Layer: "acme-finance"},
		},
	}
	if err := WriteLock(target, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	// No registry source: the only row is the lock-only artifact.
	view, err := ResolveEffectiveView(Options{RegistryPath: "", Target: target})
	if err != nil {
		t.Fatalf("ResolveEffectiveView: %v", err)
	}
	if len(view) != 1 || view[0].ID != "finance/legacy" || !view[0].Materialized {
		t.Fatalf("view = %+v, want one materialized finance/legacy", view)
	}
}
