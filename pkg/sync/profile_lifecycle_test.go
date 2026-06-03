package sync_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §7.5.3 — the lock's `profile:` field is "null when no profile was
// used". A lock with no active profile serializes `profile: null` rather than
// omitting the key.
func TestLock_NoProfileSerializesNull(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{Version: 1, Target: target}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	raw, err := readFileShim(sync.LockFilePath(target))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if !strings.Contains(string(raw), "profile: null") {
		t.Errorf("lock with no profile must render `profile: null`, got:\n%s", raw)
	}
}

// Spec: §7.5.3 — a present active profile serializes as the plain name, not as
// null, and round-trips back to the same value.
func TestLock_ProfileSerializesNameAndRoundTrips(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target, Profile: "finance-team",
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	raw, err := readFileShim(sync.LockFilePath(target))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if !strings.Contains(string(raw), "profile: finance-team") {
		t.Errorf("active profile must render verbatim, got:\n%s", raw)
	}
	if strings.Contains(string(raw), "profile: null") {
		t.Errorf("active profile must not render null, got:\n%s", raw)
	}
	lock, err := sync.ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock.Profile != "finance-team" {
		t.Errorf("Profile round-trip = %q, want finance-team", lock.Profile)
	}
}

// Spec: §7.5.3 — `profile: null` reads back as the empty active profile.
func TestLock_NullProfileReadsBackEmpty(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{Version: 1, Target: target}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	lock, err := sync.ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock.Profile != "" {
		t.Errorf("null profile should read back empty, got %q", lock.Profile)
	}
}

// Spec: §7.5.5 — "running --add on something already materialized is a no-op
// with a warning." A redundant --add neither records a toggle nor reports a
// change; it surfaces a warning instead.
func TestOverride_RedundantAddWarnsAndNoOps(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	// Seed a lock whose artifacts list marks finance/a as already materialized.
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target,
		Artifacts: []sync.LockArtifact{{ID: "finance/a", Version: "1.0.0"}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	res, err := sync.Override(sync.OverrideOptions{
		Target: target, Add: []string{"finance/a"}, Clock: newClock(),
	})
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if res.Changed {
		t.Errorf("redundant --add should not report a change")
	}
	if len(res.Lock.Toggles.Add) != 0 {
		t.Errorf("redundant --add must not record a toggle: %+v", res.Lock.Toggles.Add)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "finance/a") {
		t.Errorf("expected one warning naming finance/a, got %v", res.Warnings)
	}
}

// Spec: §7.5.5 — an --add for an artifact not yet materialized is a real
// toggle, recorded without a warning.
func TestOverride_NonRedundantAddRecordsToggle(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target,
		Artifacts: []sync.LockArtifact{{ID: "finance/a"}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	res, err := sync.Override(sync.OverrideOptions{
		Target: target, Add: []string{"finance/new"}, Clock: newClock(),
	})
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if !res.Changed {
		t.Errorf("a new --add should report a change")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("a new --add should not warn, got %v", res.Warnings)
	}
	if len(res.Lock.Toggles.Add) != 1 || res.Lock.Toggles.Add[0].ID != "finance/new" {
		t.Errorf("toggle not recorded: %+v", res.Lock.Toggles.Add)
	}
}

// Spec: §7.5.5 — re-adding an artifact that is pending removal is a real
// operation (it cancels the removal), not a redundant no-op, even though the
// artifact still appears in the lock's artifacts list.
func TestOverride_ReaddPendingRemoveIsRealOp(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target,
		Artifacts: []sync.LockArtifact{{ID: "finance/a"}},
		Toggles:   sync.LockToggles{Remove: []sync.LockToggle{{ID: "finance/a"}}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	res, err := sync.Override(sync.OverrideOptions{
		Target: target, Add: []string{"finance/a"}, Clock: newClock(),
	})
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if !res.Changed {
		t.Errorf("re-adding a pending-removed artifact should report a change")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("re-adding a pending-removed artifact should not warn, got %v", res.Warnings)
	}
	if len(res.Lock.Toggles.Remove) != 0 {
		t.Errorf("re-add should cancel the pending removal: %+v", res.Lock.Toggles.Remove)
	}
	if len(res.Lock.Toggles.Add) != 1 || res.Lock.Toggles.Add[0].ID != "finance/a" {
		t.Errorf("re-add should record a toggle: %+v", res.Lock.Toggles.Add)
	}
}

// Spec: §7.5.6 — when sync.yaml does not exist yet, save-as "creates it with
// the new profile and an empty defaults: block."
func TestSaveAs_FreshFileEmitsDefaultsBlock(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target,
		Scope: sync.LockScope{Include: []string{"finance/**"}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	if _, err := sync.SaveAs(sync.SaveAsOptions{Target: target, Profile: "team"}); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	raw, err := readFileShim(sync.ConfigPath(target))
	if err != nil {
		t.Fatalf("read sync.yaml: %v", err)
	}
	if !strings.Contains(string(raw), "defaults:") {
		t.Errorf("fresh save-as must emit a defaults: block, got:\n%s", raw)
	}
}

// Spec: §7.5.6 — an update of an existing sync.yaml that has no defaults block
// does not gain a spurious one; the empty block is a fresh-file affordance.
func TestSaveAs_ExistingFileNoSpuriousDefaults(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteConfig(target, &sync.SyncConfig{
		Profiles: map[string]sync.Profile{"team": {Include: []string{"x"}}},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if err := sync.WriteLock(target, &sync.LockFile{Version: 1, Target: target}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	if _, err := sync.SaveAs(sync.SaveAsOptions{
		Target: target, Profile: "team", Update: true,
	}); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	raw, err := readFileShim(sync.ConfigPath(target))
	if err != nil {
		t.Fatalf("read sync.yaml: %v", err)
	}
	if strings.Contains(string(raw), "defaults:") {
		t.Errorf("update of an existing file must not add a defaults: block, got:\n%s", raw)
	}
}

// Spec: §11 "Save-as test" / §7.5.3 — after save-as, the saved profile becomes
// the target's active profile (the lock's `profile:` field).
func TestSaveAs_ActivatesProfile(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target,
		Scope:   sync.LockScope{Include: []string{"finance/**"}},
		Toggles: sync.LockToggles{Add: []sync.LockToggle{{ID: "finance/extra"}}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	if _, err := sync.SaveAs(sync.SaveAsOptions{Target: target, Profile: "team-v2"}); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	lock, err := sync.ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock.Profile != "team-v2" {
		t.Errorf("save-as must activate the saved profile, lock.Profile = %q, want team-v2", lock.Profile)
	}
	// The lock also serializes the active profile name (not null).
	raw, _ := readFileShim(sync.LockFilePath(target))
	if !strings.Contains(string(raw), "profile: team-v2") {
		t.Errorf("activated profile not serialized, got:\n%s", raw)
	}
}

// Spec: §7.5.6 — save-as --dry-run neither writes the profile nor activates it.
func TestSaveAs_DryRunDoesNotActivate(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target,
		Scope: sync.LockScope{Include: []string{"finance/**"}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	if _, err := sync.SaveAs(sync.SaveAsOptions{
		Target: target, Profile: "team-v2", DryRun: true,
	}); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	lock, _ := sync.ReadLock(target)
	if lock.Profile != "" {
		t.Errorf("dry-run save-as must not activate, lock.Profile = %q", lock.Profile)
	}
}
