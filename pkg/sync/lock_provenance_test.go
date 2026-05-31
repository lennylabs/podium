package sync

import (
	"context"
	"testing"
	"time"
)

// Spec: §7.5.3 — last_synced_by is one of "full | watch | override". A manual
// one-shot sync stamps "full".
func TestRun_LastSyncedByDefaultsToFull(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	if _, err := Run(Options{RegistryPath: registry, Target: target}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock.LastSyncedBy != "full" {
		t.Errorf("last_synced_by = %q, want full", lock.LastSyncedBy)
	}
}

// Spec: §7.5.3 — an explicit LastSyncedBy (e.g. the override path) is written
// verbatim, and the watch path stamps "watch".
func TestRun_LastSyncedByOverride(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	if _, err := Run(Options{RegistryPath: registry, Target: target, LastSyncedBy: "override"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock, _ := ReadLock(target)
	if lock.LastSyncedBy != "override" {
		t.Errorf("last_synced_by = %q, want override", lock.LastSyncedBy)
	}
}

// Spec: §7.5.3 — the watcher stamps last_synced_by: watch on its reruns. Watch
// sets the field on the underlying Run options for every event.
func TestWatch_StampsWatchProvenance(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := Watch(ctx, WatchOptions{
		Sync: Options{RegistryPath: registry, Target: target},
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// Consume the initial sync event, then cancel.
	ev := <-events
	if ev.Err != nil {
		t.Fatalf("initial watch sync: %v", ev.Err)
	}
	cancel()
	for range events {
		// drain until the channel closes
	}
	lock, _ := ReadLock(target)
	if lock == nil || lock.LastSyncedBy != "watch" {
		t.Fatalf("last_synced_by = %v, want watch", lock)
	}
}

// Spec: §7.5.3 — Run persists the active profile and the resolved scope into
// the lock so override / save-as / profile edit can default to them (F-7.5.8).
// The per-artifact version is recorded too.
func TestRun_PersistsProfileScopeAndVersion(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	_, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		Profile:      "finance-team",
		Scope:        ScopeFilter{Include: []string{"finance/**"}, Types: []string{"context"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock, _ := ReadLock(target)
	if lock.Profile != "finance-team" {
		t.Errorf("lock.Profile = %q, want finance-team", lock.Profile)
	}
	if len(lock.Scope.Include) != 1 || lock.Scope.Include[0] != "finance/**" {
		t.Errorf("lock.Scope.Include = %v", lock.Scope.Include)
	}
	if len(lock.Scope.Type) != 1 || lock.Scope.Type[0] != "context" {
		t.Errorf("lock.Scope.Type = %v", lock.Scope.Type)
	}
	if len(lock.Artifacts) == 0 || lock.Artifacts[0].Version != "1.0.0" {
		t.Errorf("lock artifact version not recorded: %+v", lock.Artifacts)
	}
}

// Spec: §7.5.6 — after a real sync, save-as renders the lock's populated scope
// into the profile. Before F-7.5.8, the scope was empty so save-as emitted an
// empty include block. This drives Run, then SaveAs, and asserts the include
// carries over.
func TestSaveAs_AfterRunCarriesScope(t *testing.T) {
	t.Parallel()
	registry := scopeRegistry(t)
	target := t.TempDir()

	if _, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		Scope:        ScopeFilter{Include: []string{"finance/**"}},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	res, err := SaveAs(SaveAsOptions{Target: target, Profile: "finance-team", DryRun: true})
	if err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	if len(res.Profile.Include) != 1 || res.Profile.Include[0] != "finance/**" {
		t.Errorf("save-as include = %v, want [finance/**] (F-7.5.8)", res.Profile.Include)
	}
}
