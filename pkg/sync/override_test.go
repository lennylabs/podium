package sync_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sync"
)

func newClock() clock.Clock {
	return clock.NewFrozen(time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC))
}

// Spec: §7.5.4 / §7.5.5 — `podium sync override --add <id>` lands
// the entry in toggles.add. The lock file is written when changes
// occur, demonstrating the §7.5.4 toggle-persistence contract.
// Phase: 14
func TestOverride_AddInsertsToggle(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()

	res, err := sync.Override(sync.OverrideOptions{
		Target: target, Add: []string{"finance/x"}, Clock: newClock(),
	})
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if !res.Changed {
		t.Errorf("Changed = false, want true")
	}
	if len(res.Lock.Toggles.Add) != 1 || res.Lock.Toggles.Add[0].ID != "finance/x" {
		t.Errorf("Toggles.Add = %+v", res.Lock.Toggles.Add)
	}
	// Persisted to disk.
	read, err := sync.ReadLock(target)
	if err != nil || read == nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if len(read.Toggles.Add) != 1 {
		t.Errorf("persisted toggles wrong: %+v", read.Toggles)
	}
}

// Spec: §7.5.5 — --remove inserts into toggles.remove and clears any
// matching toggles.add.
// Phase: 14
func TestOverride_RemoveInvalidatesAdd(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()
	if _, err := sync.Override(sync.OverrideOptions{
		Target: target, Add: []string{"x"}, Clock: newClock(),
	}); err != nil {
		t.Fatalf("first Override: %v", err)
	}
	res, err := sync.Override(sync.OverrideOptions{
		Target: target, Remove: []string{"x"}, Clock: newClock(),
	})
	if err != nil {
		t.Fatalf("second Override: %v", err)
	}
	if len(res.Lock.Toggles.Add) != 0 {
		t.Errorf("Add should be empty: %+v", res.Lock.Toggles.Add)
	}
	if len(res.Lock.Toggles.Remove) != 1 {
		t.Errorf("Remove len = %d", len(res.Lock.Toggles.Remove))
	}
}

// Spec: §7.5.5 — --reset clears all toggles, like a manual sync would.
// Phase: 14
func TestOverride_ResetClearsToggles(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()
	if _, err := sync.Override(sync.OverrideOptions{
		Target: target, Add: []string{"x", "y"}, Clock: newClock(),
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	res, err := sync.Override(sync.OverrideOptions{
		Target: target, Reset: true, Clock: newClock(),
	})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if len(res.Lock.Toggles.Add) != 0 || len(res.Lock.Toggles.Remove) != 0 {
		t.Errorf("toggles not cleared: %+v", res.Lock.Toggles)
	}
}

// Spec: §7.5.5 — --dry-run resolves and reports without writing.
// Phase: 14
func TestOverride_DryRunDoesNotWrite(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()
	res, err := sync.Override(sync.OverrideOptions{
		Target: target, Add: []string{"x"}, DryRun: true, Clock: newClock(),
	})
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if !res.Changed {
		t.Errorf("Changed = false, want true (dry run still reports changes)")
	}
	read, err := sync.ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if read != nil {
		t.Errorf("dry run wrote: %+v", read)
	}
}

// Spec: §7.5.5 — empty IDs are rejected.
// Phase: 14
func TestOverride_EmptyIDRejected(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	_, err := sync.Override(sync.OverrideOptions{
		Target: t.TempDir(), Add: []string{""},
	})
	if !errors.Is(err, sync.ErrInvalidArtifactID) {
		t.Fatalf("got %v, want ErrInvalidArtifactID", err)
	}
}

// Spec: §7.5.6 — `podium sync save-as` renders toggles + scope as a
// profile in sync.yaml and clears the lock toggles on success.
// Phase: 14
func TestSaveAs_RendersProfileAndClearsToggles(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteLock(target, &sync.LockFile{
		Version: 1, Target: target,
		Scope: sync.LockScope{
			Include: []string{"finance/**"},
			Exclude: []string{"finance/legacy/**"},
		},
		Toggles: sync.LockToggles{
			Add:    []sync.LockToggle{{ID: "finance/extra"}},
			Remove: []sync.LockToggle{{ID: "finance/old"}},
		},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	res, err := sync.SaveAs(sync.SaveAsOptions{
		Target: target, Profile: "team-finance",
	})
	if err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	if !res.Wrote {
		t.Errorf("Wrote = false")
	}
	cfg, err := sync.ReadConfig(target)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	prof, ok := cfg.Profiles["team-finance"]
	if !ok {
		t.Fatalf("profile missing")
	}
	if !contains(prof.Include, "finance/extra") {
		t.Errorf("toggles.add not folded into include: %+v", prof.Include)
	}
	if !contains(prof.Exclude, "finance/old") {
		t.Errorf("toggles.remove not folded into exclude: %+v", prof.Exclude)
	}
	// Lock toggles cleared.
	lock, _ := sync.ReadLock(target)
	if len(lock.Toggles.Add) != 0 || len(lock.Toggles.Remove) != 0 {
		t.Errorf("toggles not cleared after save-as: %+v", lock.Toggles)
	}
}

// Spec: §7.5.6 — `--update` is required to overwrite an existing profile.
// Phase: 14
func TestSaveAs_RequiresUpdateForExistingProfile(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteConfig(target, &sync.SyncConfig{
		Profiles: map[string]sync.Profile{
			"team": {Include: []string{"existing"}},
		},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if err := sync.WriteLock(target, &sync.LockFile{Version: 1}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	_, err := sync.SaveAs(sync.SaveAsOptions{Target: target, Profile: "team"})
	if err == nil {
		t.Errorf("expected error without --update")
	}
	if _, err := sync.SaveAs(sync.SaveAsOptions{
		Target: target, Profile: "team", Update: true,
	}); err != nil {
		t.Errorf("with --update: %v", err)
	}
}

// Spec: §7.5.7 — `podium profile edit` adds/removes patterns to a
// profile in sync.yaml.
// Phase: 14
func TestProfileEdit_AddInclude(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()
	if err := sync.WriteConfig(target, &sync.SyncConfig{
		Profiles: map[string]sync.Profile{
			"team": {Include: []string{"finance/**"}},
		},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	res, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target: target, Profile: "team",
		AddInclude: []string{"shared/policies/*"},
	})
	if err != nil {
		t.Fatalf("ProfileEdit: %v", err)
	}
	if !contains(res.Profile.Include, "shared/policies/*") {
		t.Errorf("Include did not pick up new pattern: %+v", res.Profile.Include)
	}
	if !contains(res.Profile.Include, "finance/**") {
		t.Errorf("Include lost existing pattern: %+v", res.Profile.Include)
	}
}

// Spec: §7.5.7 — profile edit creates the file when it does not exist.
// Phase: 14
func TestProfileEdit_CreatesFileWhenMissing(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()
	if _, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target: target, Profile: "new-team",
		AddInclude: []string{"finance/**"},
	}); err != nil {
		t.Fatalf("ProfileEdit: %v", err)
	}
	cfg, err := sync.ReadConfig(target)
	if err != nil || cfg == nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if _, ok := cfg.Profiles["new-team"]; !ok {
		t.Errorf("new-team profile not written")
	}
	// File exists at the expected path.
	if _, err := readFile(filepath.Join(target, ".podium", "sync.yaml")); err != nil {
		t.Errorf("sync.yaml not created: %v", err)
	}
}

// Spec: §7.5.7 — --dry-run prints the change without writing.
// Phase: 14
func TestProfileEdit_DryRun(t *testing.T) {
	testharness.RequirePhase(t, 14)
	t.Parallel()
	target := t.TempDir()
	res, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target: target, Profile: "x",
		AddInclude: []string{"y"},
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("ProfileEdit: %v", err)
	}
	if res.Wrote {
		t.Errorf("dry run wrote")
	}
	if cfg, err := sync.ReadConfig(target); err == nil && cfg != nil {
		t.Errorf("dry run created file: %+v", cfg)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// readFile is a tiny helper to avoid importing os in this test.
func readFile(path string) ([]byte, error) {
	return readFileShim(path)
}
