package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// These tests drive the compiled binary through the conditions under which
// podium sync keeps an unparseable artifact's previous output. Each starts
// from a keptFixture, whose clean claude-code sync wrote tools/a and tools/b
// before tools/a's SKILL.md stopped parsing. When a condition fails, the run
// removes tools/a's previous output as under --on-unparseable remove, and the
// rejected line carries no kept note.

// assertNoKept asserts that a run over the broken fixture exited 1, named
// tools/a's drop without the kept note, and that the target's lock records no
// entry for tools/a.
func (f keptFixture) assertNoKept(t *testing.T, res cliResult, what string) {
	t.Helper()
	cliWantExit(t, res, 1, what)
	assertRejectedLines(t, res.Stderr, f.rejected)
	if got := lockEntriesOf(t, f.target, "tools/a"); len(got) != 0 {
		t.Errorf("%s: lock still records tools/a: %+v", what, got)
	}
}

// A sync whose scope no longer selects tools/a removes its previous output:
// an include of tools/b alone, and a type filter the type recorded in tools/a's
// lock entry fails. The dry run of the include names the drop without a note
// and writes nothing.
//
// Spec: §13.11.3, §7.5.1
func TestFilesystemSync_UnparseableKeepFollowsScope(t *testing.T) {
	t.Parallel()

	t.Run("include", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		dry := f.sync(t, nil, "--include", "tools/b", "--dry-run")
		cliWantExit(t, dry, 1, "dry run of the narrowed scope")
		assertRejectedLines(t, dry.Stderr, f.rejected)
		mustExist(t, filepath.Join(f.skillDir("a"), "SKILL.md"))

		res := f.sync(t, nil, "--include", "tools/b")
		f.assertNoKept(t, res, "sync --include tools/b")
		mustNotExist(t, f.skillDir("a"))
		f.assertBUpdated(t, "sync --include tools/b")
	})

	t.Run("type", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		for _, la := range f.lockA {
			if la.Type != "skill" {
				t.Fatalf("clean sync recorded type %q for tools/a, want skill", la.Type)
			}
		}
		res := f.sync(t, nil, "--type", "agent")
		f.assertNoKept(t, res, "sync --type agent")
		mustNotExist(t, f.skillDir("a"))
	})
}

// podium sync override --remove tools/a records the toggle and removes the
// previous output of the broken tools/a, which a toggle now removes from the
// target.
//
// Spec: §13.11.3, §7.5.5
func TestSyncOverride_RemoveEndsUnparseableKeep(t *testing.T) {
	t.Parallel()
	f := newKeptFixture(t)
	res := runPodium(t, "", withIsolatedHome(nil, f.home), "sync", "override", "--target", f.target,
		"--remove", "tools/a", "--registry", f.reg, "--harness", "claude-code")
	f.assertNoKept(t, res, "override --remove tools/a")
	mustNotExist(t, f.skillDir("a"))
	f.assertBUpdated(t, "override --remove tools/a")
	lock, err := sync.ReadLock(f.target)
	if err != nil || lock == nil {
		t.Fatalf("read lock: lock=%v err=%v", lock, err)
	}
	if rm := lock.Toggles.Remove; len(rm) != 1 || rm[0].ID != "tools/a" {
		t.Errorf("toggles.remove = %+v, want tools/a", rm)
	}
}

// A sync with another harness adapter keeps nothing the claude-code sync
// wrote: tools/a's .claude/ files are removed, and the lock, now for cursor,
// records no entry for tools/a.
//
// Spec: §13.11.3
func TestFilesystemSync_HarnessSwitchEndsUnparseableKeep(t *testing.T) {
	t.Parallel()
	f := newKeptFixture(t)
	res := runPodium(t, "", withIsolatedHome(nil, f.home),
		"sync", "--registry", f.reg, "--target", f.target, "--harness", "cursor")
	f.assertNoKept(t, res, "sync --harness cursor")
	mustNotExist(t, f.skillDir("a"))
	lock, err := sync.ReadLock(f.target)
	if err != nil || lock == nil {
		t.Fatalf("read lock: lock=%v err=%v", lock, err)
	}
	if lock.Harness != "cursor" {
		t.Errorf("lock harness = %q, want cursor", lock.Harness)
	}
}

// A new skill other/a materializes to tools/a's .claude/skills/a/SKILL.md.
// The sync writes other/a's copy, removes tools/a's resource, and records the
// path once, for other/a. Removing other/a then removes the file, and nothing
// stays attributed to tools/a.
//
// Spec: §13.11.3
func TestFilesystemSync_PathTakeoverEndsUnparseableKeep(t *testing.T) {
	t.Parallel()
	f := newKeptFixture(t)
	other := filepath.Join(f.reg, "other", "a")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTreeFile(t, filepath.Join(other, "SKILL.md"), skillBodyDesc("a", "The other a skill."))

	res := f.sync(t, nil)
	f.assertNoKept(t, res, "sync with other/a")
	skill := filepath.Join(f.skillDir("a"), "SKILL.md")
	if got := readFile(t, skill); !strings.Contains(got, "The other a skill.") {
		t.Errorf("%s = %q, want other/a's copy", skill, got)
	}
	mustNotExist(t, filepath.Join(f.skillDir("a"), "references", "guide.md"))
	lock, err := sync.ReadLock(f.target)
	if err != nil || lock == nil {
		t.Fatalf("read lock: lock=%v err=%v", lock, err)
	}
	var owners []string
	for _, la := range lock.Artifacts {
		if la.MaterializedPath == ".claude/skills/a/SKILL.md" {
			owners = append(owners, la.ID)
		}
	}
	if len(owners) != 1 || owners[0] != "other/a" {
		t.Errorf("lock entries for .claude/skills/a/SKILL.md belong to %v, want other/a alone", owners)
	}

	if err := os.RemoveAll(filepath.Join(f.reg, "other")); err != nil {
		t.Fatal(err)
	}
	res = f.sync(t, nil)
	f.assertNoKept(t, res, "sync after other/a's removal")
	mustNotExist(t, f.skillDir("a"))
}
