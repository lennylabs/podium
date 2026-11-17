package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// keptNote is the suffix podium sync adds to the rejected line of an
// unparseable artifact whose previous output it kept, and dryRunKeptNote the
// suffix under --dry-run, which writes nothing.
const (
	keptNote       = "; the previous sync output was kept"
	dryRunKeptNote = "; the previous sync output would be kept"
)

// brokenA is the SKILL.md of tools/a after an edit that writes its
// description as a mapping, so it no longer parses.
const brokenA = "---\nname: a\ndescription:\n  text: The a skill, half edited.\n---\n\nhalf-saved body.\n"

// keptFixture is a filesystem registry and a claude-code target that one
// clean sync materialized, after which tools/a's SKILL.md stopped parsing and
// tools/b changed.
type keptFixture struct {
	reg, target, home string
	// before is tools/a's materialized files after the clean sync.
	before map[string]string
	// lockA is tools/a's lock entries after the clean sync.
	lockA []sync.LockArtifact
	// rejected is the rejected line of tools/a without the kept note.
	rejected string
}

// newKeptFixture builds a keptFixture. tools/c exists in the registry but the
// clean sync excludes it, so an override can add it.
func newKeptFixture(t *testing.T) keptFixture {
	t.Helper()
	f := keptFixture{target: t.TempDir(), home: t.TempDir()}
	f.reg = writeRegistry(t, map[string]string{
		"tools/a/SKILL.md":            skillBody("a"),
		"tools/a/references/guide.md": "guide v1\n",
		"tools/b/SKILL.md":            skillBody("b"),
		"tools/c/SKILL.md":            skillBody("c"),
	})
	res := f.sync(t, nil, "--exclude", "tools/c")
	cliWantExit(t, res, 0, "clean sync")
	f.before = treeOf(t, f.skillDir("a"))
	if len(f.before) < 2 {
		t.Fatalf("clean sync wrote %v for tools/a, want its SKILL.md and resource", f.before)
	}
	f.lockA = lockEntriesOf(t, f.target, "tools/a")
	if len(f.lockA) == 0 {
		t.Fatal("clean sync recorded no lock entry for tools/a")
	}
	writeTreeFile(t, filepath.Join(f.reg, "tools", "a", "SKILL.md"), brokenA)
	writeTreeFile(t, filepath.Join(f.reg, "tools", "b", "SKILL.md"), skillBodyDesc("b", "The b skill, second revision."))
	f.rejected = unparseableRejected(t, "tools/a", brokenA)
	return f
}

// skillDir returns the claude-code skill directory of name in the target.
func (f keptFixture) skillDir(name string) string {
	return filepath.Join(f.target, ".claude", "skills", name)
}

// sync runs podium sync over the fixture with an isolated HOME and extra env.
func (f keptFixture) sync(t *testing.T, env []string, args ...string) cliResult {
	t.Helper()
	base := []string{"sync", "--registry", f.reg, "--target", f.target, "--harness", "claude-code"}
	return runPodium(t, "", withIsolatedHome(env, f.home), append(base, args...)...)
}

// assertKept asserts the kept outcome of a run over the broken fixture: exit
// 1, the rejected line with the kept note, tools/a's files and lock entries
// unchanged, and tools/b updated.
func (f keptFixture) assertKept(t *testing.T, res cliResult, what string) {
	t.Helper()
	cliWantExit(t, res, 1, what)
	assertRejectedLines(t, res.Stderr, f.rejected+keptNote)
	if got := treeOf(t, f.skillDir("a")); !reflect.DeepEqual(got, f.before) {
		t.Errorf("%s: tools/a output = %v, want the previous output %v", what, got, f.before)
	}
	if got := lockEntriesOf(t, f.target, "tools/a"); !reflect.DeepEqual(got, f.lockA) {
		t.Errorf("%s: tools/a lock entries = %+v, want %+v", what, got, f.lockA)
	}
	f.assertBUpdated(t, what)
}

// assertRemoved asserts the remove outcome: exit 1, the rejected line without
// the kept note, no tools/a files, no tools/a lock entry, and tools/b updated.
func (f keptFixture) assertRemoved(t *testing.T, res cliResult, what string) {
	t.Helper()
	cliWantExit(t, res, 1, what)
	assertRejectedLines(t, res.Stderr, f.rejected)
	mustNotExist(t, f.skillDir("a"))
	if got := lockEntriesOf(t, f.target, "tools/a"); len(got) != 0 {
		t.Errorf("%s: lock still records tools/a: %+v", what, got)
	}
	f.assertBUpdated(t, what)
}

// assertBUpdated asserts that tools/b carries its second revision.
func (f keptFixture) assertBUpdated(t *testing.T, what string) {
	t.Helper()
	if b := readFile(t, filepath.Join(f.skillDir("b"), "SKILL.md")); !strings.Contains(b, "second revision") {
		t.Errorf("%s: tools/b SKILL.md = %q, want the second revision", what, b)
	}
}

// treeOf returns the files under root keyed by slash path, or an empty map
// when root does not exist.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return filepath.SkipDir
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// lockEntriesOf returns the entries the target's lock records for id.
func lockEntriesOf(t *testing.T, target, id string) []sync.LockArtifact {
	t.Helper()
	lock, err := sync.ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("read lock of %s: lock=%v err=%v", target, lock, err)
	}
	var out []sync.LockArtifact
	for _, a := range lock.Artifacts {
		if a.ID == id {
			out = append(out, a)
		}
	}
	return out
}

// writeTreeFile overwrites path with content.
func writeTreeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// A filesystem-source podium sync that finds tools/a's SKILL.md unparseable
// keeps the files and lock entry an earlier sync wrote for tools/a under the
// keep default, names the drop with the kept note, updates tools/b, and exits
// 1; --dry-run says the output would be kept and writes nothing. --on-unparseable
// remove, PODIUM_SYNC_ON_UNPARSEABLE, and defaults.on_unparseable select
// removal by the §7.5.2 precedence, and a removal drops the files and the lock
// entry.
//
// Spec: §13.11.3, §7.5.2
func TestFilesystemSync_UnparseableManifestKeepsPreviousOutput(t *testing.T) {
	t.Parallel()

	t.Run("keep default", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		dry := f.sync(t, nil, "--dry-run")
		cliWantExit(t, dry, 1, "dry run over the broken SKILL.md")
		assertRejectedLines(t, dry.Stderr, f.rejected+dryRunKeptNote)
		if b := readFile(t, filepath.Join(f.skillDir("b"), "SKILL.md")); strings.Contains(b, "second revision") {
			t.Errorf("dry run wrote tools/b: %q", b)
		}
		f.assertKept(t, f.sync(t, nil), "sync under the keep default")
		f.assertKept(t, f.sync(t, nil), "repeated sync under the keep default")
	})

	t.Run("flag remove", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		f.assertRemoved(t, f.sync(t, nil, "--on-unparseable", "remove"), "sync --on-unparseable remove")
	})

	t.Run("env remove", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		f.assertRemoved(t, f.sync(t, []string{"PODIUM_SYNC_ON_UNPARSEABLE=remove"}), "sync under PODIUM_SYNC_ON_UNPARSEABLE=remove")
	})

	t.Run("flag keep over env remove", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		f.assertKept(t, f.sync(t, []string{"PODIUM_SYNC_ON_UNPARSEABLE=remove"}, "--on-unparseable", "keep"), "flag keep over env remove")
	})

	t.Run("sync.yaml remove and env keep over it", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		ws := t.TempDir()
		writeWorkspaceConfig(t, ws, "defaults:\n  on_unparseable: remove\n")
		args := []string{"sync", "--registry", f.reg, "--target", f.target, "--harness", "claude-code"}
		env := withIsolatedHome(nil, f.home)
		f.assertKept(t, runPodium(t, ws, append(env, "PODIUM_SYNC_ON_UNPARSEABLE=keep"), args...), "env keep over sync.yaml remove")
		f.assertRemoved(t, runPodium(t, ws, env, args...), "sync.yaml remove")
	})

	t.Run("config workspace target", func(t *testing.T) {
		t.Parallel()
		reg := writeRegistry(t, map[string]string{
			"tools/a/SKILL.md": skillBody("a"),
			"tools/b/SKILL.md": skillBody("b"),
		})
		ws := t.TempDir()
		target := filepath.Join(ws, "out")
		cfg := writeCollisionWorkspaceConfig(t, ws, reg, target)
		env := withIsolatedHome(nil, t.TempDir())
		cliWantExit(t, runPodium(t, "", env, "sync", "--config", cfg), 0, "clean --config sync")
		before := readFile(t, filepath.Join(target, "tools", "a", "SKILL.md"))
		writeTreeFile(t, filepath.Join(reg, "tools", "a", "SKILL.md"), brokenA)
		res := runPodium(t, "", env, "sync", "--config", cfg)
		cliWantExit(t, res, 1, "--config sync over the broken SKILL.md")
		assertRejectedLines(t, res.Stderr, unparseableRejected(t, "tools/a", brokenA)+keptNote)
		cliContains(t, res.Stderr, "target collide-ws:", "the target failure line")
		if got := readFile(t, filepath.Join(target, "tools", "a", "SKILL.md")); got != before {
			t.Errorf("--config target rewrote tools/a: %q, want %q", got, before)
		}
		res = runPodium(t, "", env, "sync", "--config", cfg, "--on-unparseable", "remove")
		cliWantExit(t, res, 1, "--config sync --on-unparseable remove")
		mustNotExist(t, filepath.Join(target, "tools", "a"))
	})
}

// podium sync override re-materializes through the same rule: under the keep
// default it keeps tools/a's previous output and names the drop with the kept
// note while it adds tools/c, and under --on-unparseable remove it removes
// tools/a's output.
//
// Spec: §13.11.3, §7.5.5
func TestSyncOverride_UnparseableManifestKeepsPreviousOutput(t *testing.T) {
	t.Parallel()
	override := func(t *testing.T, f keptFixture, extra ...string) cliResult {
		t.Helper()
		args := append([]string{"sync", "override", "--target", f.target, "--add", "tools/c",
			"--registry", f.reg, "--harness", "claude-code"}, extra...)
		return runPodium(t, "", withIsolatedHome(nil, f.home), args...)
	}

	t.Run("keep default", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		f.assertKept(t, override(t, f), "override under the keep default")
		mustExist(t, filepath.Join(f.skillDir("c"), "SKILL.md"))
	})

	t.Run("remove", func(t *testing.T) {
		t.Parallel()
		f := newKeptFixture(t)
		f.assertRemoved(t, override(t, f, "--on-unparseable", "remove"), "override --on-unparseable remove")
		mustExist(t, filepath.Join(f.skillDir("c"), "SKILL.md"))
	})
}

// A value of the setting other than keep or remove is a usage error: podium
// sync, podium sync --config, and podium sync override exit 2 with a message
// that names the source and the accepted values, write nothing, and an
// override records no toggle.
//
// Spec: §13.11.3, §7.5.2
func TestSync_OnUnparseableInvalidValueExits2(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"tools/a/SKILL.md": skillBody("a")})
	home := t.TempDir()
	env := withIsolatedHome(nil, home)

	cases := []struct {
		name, cwd string
		env       []string
		args      []string
		message   string
	}{
		{
			name:    "flag",
			args:    []string{"sync", "--registry", reg, "--on-unparseable", "discard"},
			message: `error: --on-unparseable must be keep | remove, got "discard"`,
		},
		{
			name:    "env",
			env:     []string{"PODIUM_SYNC_ON_UNPARSEABLE=Keep"},
			args:    []string{"sync", "--registry", reg},
			message: `error: PODIUM_SYNC_ON_UNPARSEABLE must be keep | remove, got "Keep"`,
		},
		{
			name:    "override flag",
			args:    []string{"sync", "override", "--add", "tools/a", "--registry", reg, "--on-unparseable", "discard"},
			message: `error: --on-unparseable must be keep | remove, got "discard"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := t.TempDir()
			args := append(append([]string{}, tc.args...), "--target", target)
			res := runPodium(t, "", append(append([]string{}, env...), tc.env...), args...)
			cliWantExit(t, res, 2, tc.name)
			cliContains(t, res.Stderr, tc.message, "the usage error")
			mustNotExist(t, filepath.Join(target, ".podium"))
			mustNotExist(t, filepath.Join(target, "tools"))
		})
	}

	t.Run("sync.yaml", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		writeWorkspaceConfig(t, ws, "defaults:\n  on_unparseable: discard\n")
		target := t.TempDir()
		res := runPodium(t, ws, env, "sync", "--registry", reg, "--target", target)
		cliWantExit(t, res, 2, "sync.yaml")
		cliContains(t, res.Stderr, `error: defaults.on_unparseable must be keep | remove, got "discard"`, "the usage error")
		mustNotExist(t, filepath.Join(target, "tools"))
	})

	t.Run("config", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		target := filepath.Join(ws, "out")
		cfg := writeCollisionWorkspaceConfig(t, ws, reg, target)
		res := runPodium(t, "", env, "sync", "--config", cfg, "--on-unparseable", "discard")
		cliWantExit(t, res, 2, "--config")
		cliContains(t, res.Stderr, `error: --on-unparseable must be keep | remove, got "discard"`, "the usage error")
		mustNotExist(t, target)
	})
}
