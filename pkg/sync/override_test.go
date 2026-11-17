package sync_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sync"
)

func newClock() clock.Clock {
	return clock.NewFrozen(time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC))
}

// Spec: §7.5.4 / §7.5.5 — `podium sync override --add <id>` lands
// the entry in toggles.add. The lock file is written when changes
// occur, demonstrating the §7.5.4 toggle-persistence contract.
func TestOverride_AddInsertsToggle(t *testing.T) {
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
func TestOverride_RemoveInvalidatesAdd(t *testing.T) {
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
func TestOverride_ResetClearsToggles(t *testing.T) {
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
func TestOverride_DryRunDoesNotWrite(t *testing.T) {
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
func TestOverride_EmptyIDRejected(t *testing.T) {
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
func TestSaveAs_RendersProfileAndClearsToggles(t *testing.T) {
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
func TestSaveAs_RequiresUpdateForExistingProfile(t *testing.T) {
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
func TestProfileEdit_AddInclude(t *testing.T) {
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
func TestProfileEdit_CreatesFileWhenMissing(t *testing.T) {
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
func TestProfileEdit_DryRun(t *testing.T) {
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

// saveAsWorkspace writes doc as the workspace's sync.yaml beside an empty
// lock, so SaveAs runs with the workspace as its target.
func saveAsWorkspace(t *testing.T, doc string) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sync.ConfigPath(ws), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sync.WriteLock(ws, &sync.LockFile{Version: 1}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	return ws
}

// claudeCodeBudget loads the merged configuration of ws with no home scope
// and resolves the claude-code listing budget.
func claudeCodeBudget(t *testing.T, ws string) sync.EffectiveListingBudget {
	t.Helper()
	merged, _, err := sync.LoadMergedConfig(ws, "")
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	b, err := merged.ListingBudgetFor("claude-code", adapter.DefaultRegistry())
	if err != nil {
		t.Fatalf("ListingBudgetFor: %v", err)
	}
	return b
}

// Spec: §7.5.6, §7.5.2 — `podium sync save-as` writes the listing_budgets
// block back with the keys and values it read, writes each alias as a copy of
// the value it names, and fails without writing on an alias to a node that
// encloses it.
func TestSaveAs_PreservesListingBudgets(t *testing.T) {
	t.Parallel()

	t.Run("block kept as read", func(t *testing.T) {
		t.Parallel()
		ws := saveAsWorkspace(t, "listing_budgets: {claude-code: {max_chars: 12000}, codex: {types: []}}\n")
		if _, err := sync.SaveAs(sync.SaveAsOptions{Target: ws, Profile: "team"}); err != nil {
			t.Fatalf("SaveAs: %v", err)
		}
		cfg, err := sync.ReadConfig(ws)
		if err != nil || cfg == nil {
			t.Fatalf("ReadConfig: %v", err)
		}
		var block map[string]map[string]any
		if err := cfg.ListingBudgets.Decode(&block); err != nil {
			t.Fatalf("decode block: %v", err)
		}
		want := map[string]map[string]any{
			"claude-code": {"max_chars": 12000},
			"codex":       {"types": []any{}},
		}
		if !reflect.DeepEqual(block, want) {
			t.Errorf("block after save-as = %v, want %v", block, want)
		}
		if got := claudeCodeBudget(t, ws); got.MaxChars != 12000 {
			t.Errorf("MaxChars = %d, want 12000", got.MaxChars)
		}
	})

	aliased := map[string]string{
		"merge key":   "x-ok: &ok {max_chars: 12000}\nlisting_budgets:\n  claude-code:\n    <<: *ok\n    check: error\n",
		"block alias": "x-all: &all {claude-code: {max_chars: 12000, check: error}}\nlisting_budgets: *all\n",
	}
	for name, doc := range aliased {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ws := saveAsWorkspace(t, doc)
			if _, err := sync.SaveAs(sync.SaveAsOptions{Target: ws, Profile: "team"}); err != nil {
				t.Fatalf("SaveAs: %v", err)
			}
			raw, err := os.ReadFile(sync.ConfigPath(ws))
			if err != nil {
				t.Fatal(err)
			}
			if strings.ContainsAny(string(raw), "*&") {
				t.Errorf("rewritten sync.yaml holds an alias or anchor:\n%s", raw)
			}
			if _, err := sync.ReadConfig(ws); err != nil {
				t.Fatalf("ReadConfig: %v", err)
			}
			got := claudeCodeBudget(t, ws)
			if got.MaxChars != 12000 || got.Check != "error" {
				t.Errorf("budget = %+v, want MaxChars 12000 and Check error", got)
			}
		})
	}

	t.Run("alias to an enclosing node", func(t *testing.T) {
		t.Parallel()
		ws := saveAsWorkspace(t, "listing_budgets: &a {claude-code: {x: *a}}\n")
		beforeCfg, _ := os.ReadFile(sync.ConfigPath(ws))
		lockPath := filepath.Join(ws, ".podium", "sync.lock")
		beforeLock, _ := os.ReadFile(lockPath)
		_, err := sync.SaveAs(sync.SaveAsOptions{Target: ws, Profile: "team"})
		if err == nil || !strings.Contains(err.Error(), "listing_budgets") {
			t.Fatalf("SaveAs error = %v, want one naming listing_budgets", err)
		}
		afterCfg, _ := os.ReadFile(sync.ConfigPath(ws))
		afterLock, _ := os.ReadFile(lockPath)
		if !bytes.Equal(beforeCfg, afterCfg) || !bytes.Equal(beforeLock, afterLock) {
			t.Errorf("a failed save-as changed sync.yaml or the lock")
		}
	})
}

// Spec: §7.5.5, §7.5.8 — Override returns the re-materialization's Result for
// the listing report, and nil under DryRun and without a registry; the toggle
// is recorded in each case.
func TestOverride_ReturnsMaterializedResult(t *testing.T) {
	t.Parallel()
	registry := overrideRegistry(t)
	cases := []struct {
		name     string
		registry string
		dryRun   bool
		wantRes  bool
	}{
		{"re-materializes", registry, false, true},
		{"dry run", registry, true, false},
		{"no registry", "", false, false},
	}
	for _, tc := range cases {
		target := t.TempDir()
		res, err := sync.Override(sync.OverrideOptions{
			Target: target, Add: []string{"shared/c"}, RegistryPath: tc.registry,
			DryRun: tc.dryRun, Clock: newClock(),
		})
		if err != nil {
			t.Fatalf("%s: Override: %v", tc.name, err)
		}
		if (res.Materialized != nil) != tc.wantRes {
			t.Errorf("%s: Materialized = %+v, want non-nil=%v", tc.name, res.Materialized, tc.wantRes)
		}
		if res.Materialized != nil && !hasArtifact(res.Materialized, "shared/c") {
			t.Errorf("%s: Materialized lacks the added artifact: %+v", tc.name, res.Materialized.Artifacts)
		}
		if len(res.Lock.Toggles.Add) != 1 || res.Lock.Toggles.Add[0].ID != "shared/c" {
			t.Errorf("%s: toggles = %+v, want shared/c added", tc.name, res.Lock.Toggles)
		}
		if tc.dryRun {
			continue
		}
		lock, err := sync.ReadLock(target)
		if err != nil || lock == nil || len(lock.Toggles.Add) != 1 {
			t.Errorf("%s: lock on disk = %+v, %v; want the toggle recorded", tc.name, lock, err)
		}
	}
}

// hasArtifact reports whether res materialized id.
func hasArtifact(res *sync.Result, id string) bool {
	for _, a := range res.Artifacts {
		if a.ID == id {
			return true
		}
	}
	return false
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
