package sync

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §6.7, §7.5.3 — the cleanup accepts the directory links the writer
// accepts. A target whose pack directory is a link to vendor-pack inside the
// target receives pack/a and pack/b through the link; when pack/b leaves the
// view, the next sync removes vendor-pack/b, keeps the link and the directory
// it names, and names nothing on stderr.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_StaleCleanupFollowsInRootLink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(target, "vendor-pack"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink("vendor-pack", filepath.Join(target, "pack")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	syncChanged(t, Options{RegistryPath: agentRegistry(t, "pack/a", "pack/b"), Target: target, AdapterID: "none"})
	stale := filepath.Join(target, "vendor-pack", "b", "ARTIFACT.md")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("the writer did not write through the in-target link: %v", err)
	}

	onlyA := Options{RegistryPath: agentRegistry(t, "pack/a"), Target: target, AdapterID: "none"}
	var changed bool
	stderr := captureStderr(t, func() { changed = syncChanged(t, onlyA) })
	if !changed {
		t.Errorf("a sync that removed pack/b must report Changed true")
	}
	if strings.Contains(stderr, "skipping") {
		t.Errorf("stderr = %q, want no skipped entry", stderr)
	}
	if _, err := os.Lstat(filepath.Join(target, "vendor-pack", "b")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("vendor-pack/b must be removed with its empty directory; lstat err = %v", err)
	}
	for _, rel := range []string{"pack", "vendor-pack/a/ARTIFACT.md"} {
		if _, err := os.Lstat(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s must survive: %v", rel, err)
		}
	}
}

// Spec: §6.6, §6.7, §7.5.2, §7.5.3 — a workspace that links GEMINI.md and
// CLAUDE.md to AGENTS.md and links .gemini/skills to a directory inside the
// target syncs through the links. The rule block lands in AGENTS.md next to the
// operator's content, the skill lands behind the directory link, every link
// stays a link, a re-sync reports no change, and when both artifacts leave the
// view the block is stripped through the link and the skill is removed, with
// no entry skipped.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_WorkspaceLinksInsideTargetFollowed(t *testing.T) {
	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: "style/house/ARTIFACT.md", Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nHouse rule.\n"},
		testharness.WriteTreeOption{Path: "greet/hello-world/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\n"},
		testharness.WriteTreeOption{Path: "greet/hello-world/SKILL.md", Content: skillBodySrc},
		testharness.WriteTreeOption{Path: "team/glossary/ARTIFACT.md", Content: agentArtifactSrc},
	)
	target := filepath.Join(t.TempDir(), "target")
	writeFile(t, filepath.Join(target, "AGENTS.md"), "operator notes\n")
	if err := os.MkdirAll(filepath.Join(target, "shared", "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(target, ".gemini"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	links := map[string]string{"GEMINI.md": "AGENTS.md", "CLAUDE.md": "AGENTS.md", ".gemini/skills": "../shared/skills"}
	for link, dest := range links {
		if err := os.Symlink(dest, filepath.Join(target, filepath.FromSlash(link))); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	wantLinks := func(when string) {
		t.Helper()
		for link := range links {
			if info, err := os.Lstat(filepath.Join(target, filepath.FromSlash(link))); err != nil || info.Mode()&fs.ModeSymlink == 0 {
				t.Errorf("%s: %s must stay a link; lstat = (%v, %v)", when, link, info, err)
			}
		}
	}
	opts := Options{RegistryPath: reg, Target: target, AdapterID: "gemini"}

	stderr := captureStderr(t, func() { syncChanged(t, opts) })
	if strings.Contains(stderr, "skipping") || strings.Contains(stderr, "refusing") {
		t.Errorf("first sync stderr = %q, want no refused path", stderr)
	}
	agents := string(readTargetFile(t, target, "AGENTS.md"))
	if !strings.Contains(agents, "operator notes") || !strings.Contains(agents, "House rule.") {
		t.Errorf("AGENTS.md = %q, want the operator notes and the rule block", agents)
	}
	if _, err := os.Stat(filepath.Join(target, "shared", "skills", "hello-world", "SKILL.md")); err != nil {
		t.Errorf("the skill must land behind the directory link: %v", err)
	}
	wantLinks("after the first sync")
	wantChanged(t, opts, false, "re-sync through the links")

	for _, dir := range []string{"style", "greet"} {
		if err := os.RemoveAll(filepath.Join(reg, dir)); err != nil {
			t.Fatalf("RemoveAll: %v", err)
		}
	}
	stderr = captureStderr(t, func() { syncChanged(t, opts) })
	if strings.Contains(stderr, "skipping") {
		t.Errorf("removal sync stderr = %q, want no skipped entry", stderr)
	}
	agents = string(readTargetFile(t, target, "AGENTS.md"))
	if !strings.Contains(agents, "operator notes") || strings.Contains(agents, "House rule.") {
		t.Errorf("AGENTS.md after removal = %q, want the operator notes without the rule block", agents)
	}
	if _, err := os.Lstat(filepath.Join(target, "shared", "skills", "hello-world")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the removed skill must be cleaned up behind the link; lstat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "shared", "skills")); err != nil {
		t.Errorf("the directory the link names must stay: %v", err)
	}
	wantLinks("after the removal sync")
}

// Spec: §7.5.3 — a prior-lock entry that names, through a link inside the
// target, a file the run just wrote is not stale. A standalone entry
// alias/a/ARTIFACT.md with alias linked to real does not remove the
// real/a/ARTIFACT.md the run wrote, and an inject entry GEMINI.md with
// GEMINI.md linked to AGENTS.md does not strip the blocks the run injected
// into AGENTS.md.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_StaleEntryAliasingCurrentOutputKept(t *testing.T) {
	t.Run("standalone", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target")
		opts := Options{RegistryPath: agentRegistry(t, "real/a"), Target: target, AdapterID: "none"}
		syncChanged(t, opts)
		if err := os.Symlink("real", filepath.Join(target, "alias")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		appendLockEntry(t, target, LockArtifact{ID: "old/a", MaterializedPath: "alias/a/ARTIFACT.md"})
		captureStderr(t, func() { syncChanged(t, opts) })
		if _, err := os.Stat(filepath.Join(target, "real", "a", "ARTIFACT.md")); err != nil {
			t.Errorf("the stale cleanup removed the run's own output through an alias: %v", err)
		}
	})
	t.Run("inject", func(t *testing.T) {
		reg := t.TempDir()
		testharness.WriteTree(t, reg, testharness.WriteTreeOption{
			Path: "style/house/ARTIFACT.md", Content: "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\n---\n\nHouse rule.\n",
		})
		target := filepath.Join(t.TempDir(), "target")
		opts := Options{RegistryPath: reg, Target: target, AdapterID: "codex"}
		syncChanged(t, opts)
		if err := os.Symlink("AGENTS.md", filepath.Join(target, "GEMINI.md")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		appendLockEntry(t, target, LockArtifact{ID: "old/rule", MaterializedPath: "GEMINI.md", Merge: "inject"})
		captureStderr(t, func() { syncChanged(t, opts) })
		if got := string(readTargetFile(t, target, "AGENTS.md")); !strings.Contains(got, "House rule.") {
			t.Errorf("AGENTS.md = %q: the stale reconciliation stripped the run's own block through an alias", got)
		}
	})
}

// appendLockEntry adds entry to the lock at target.
func appendLockEntry(t *testing.T, target string, entry LockArtifact) {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	lock.Artifacts = append(lock.Artifacts, entry)
	if err := WriteLock(target, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
}

// Spec: §6.6, §7.8 — a checkout that commits a plugin's skills directory as a
// link into .git/hooks does not receive a file in .git: the render fails
// before anything is written, naming the refused path.
func TestRender_CommittedDirectoryLinkIntoGitRefused(t *testing.T) {
	t.Parallel()
	workdir := filepath.Join(t.TempDir(), "checkout")
	opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	skills := filepath.Join(workdir, "claude", "finance-pack", "skills")
	if err := os.RemoveAll(skills); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.Symlink("../../.git/hooks", skills); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Render(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "write render") || !strings.Contains(err.Error(), ".git segment") {
		t.Errorf("Render through a skills link into .git = %v, want the write refused", err)
	}
	entries, rerr := os.ReadDir(filepath.Join(workdir, ".git", "hooks"))
	if rerr != nil || len(entries) != 0 {
		t.Errorf(".git/hooks after the render = (%v, %v), want it empty", entries, rerr)
	}
}

// Spec: §7.5.3, §13.11.3, §4.7.11 (Well-formed requests and records) — a
// bundled resource whose name holds a backslash, which is a legal file name
// on Unix, breaks the well-formed rule, so the filesystem source refuses the
// whole package with materialize.request_mismatch (resource_path): nothing of
// it is written or recorded, the run reports it, and no file is orphaned when
// the artifact goes.
func TestRun_UnrecordablePathNeitherWrittenNorRecorded(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: "pack/a/ARTIFACT.md", Content: agentArtifactSrc},
		testharness.WriteTreeOption{Path: `pack/a/notes\x.md`, Content: "n\n"},
		testharness.WriteTreeOption{Path: "pack/b/ARTIFACT.md", Content: agentArtifactSrc},
	)
	target := filepath.Join(t.TempDir(), "target")
	opts := Options{RegistryPath: reg, Target: target, AdapterID: "none"}
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Dropped) != 1 || res.Dropped[0].ArtifactID != "pack/a" || res.Dropped[0].Code != "materialize.request_mismatch" ||
		res.Dropped[0].Details["constraint"] != "resource_path" {
		t.Fatalf("dropped = %+v, want pack/a refused as resource_path", res.Dropped)
	}
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	for _, la := range lock.Artifacts {
		if la.ID == "pack/a" {
			t.Errorf("the lock records the refused artifact: %+v", la)
		}
	}
	if _, err := os.Lstat(filepath.Join(target, "pack", "a")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("nothing of the refused package may be written; lstat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "pack", "b", "ARTIFACT.md")); err != nil {
		t.Errorf("the other package must be written: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(reg, "pack")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := Run(opts); err != nil {
		t.Fatalf("removal run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "pack")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the removed artifacts must leave nothing behind; lstat err = %v", err)
	}
}

// Spec: §6.7, §7.8 — a marketplace checkout can commit a config-merge path as
// a link to a file outside it. The render does not read through the link, so
// the outside file's content cannot be folded into the rendered manifest the
// publish step commits: the render fails naming the link, the link stays, and
// no rendered file carries the outside content.
func TestRender_CommittedMergeLinkNotFollowed(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	workdir := filepath.Join(base, "checkout")
	opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	secret := filepath.Join(base, "creds.json")
	writeFile(t, secret, `{"token":"SECRET-123"}`)
	manifest := filepath.Join(workdir, ".claude-plugin", "marketplace.json")
	if err := os.Remove(manifest); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.Symlink(secret, manifest); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := Render(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "resolves outside the destination") {
		t.Errorf("Render into a committed merge link = %v, want the link refused", err)
	}
	if info, lerr := os.Lstat(manifest); lerr != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the committed link must stay as it was; lstat = (%v, %v)", info, lerr)
	}
	tree := testharness.ReadTree(t, workdir)
	for p, content := range tree {
		if strings.Contains(content, "SECRET-123") && p != ".claude-plugin/marketplace.json" {
			t.Errorf("%s carries the outside file's content", p)
		}
	}
	if got, _ := os.ReadFile(secret); string(got) != `{"token":"SECRET-123"}` {
		t.Errorf("the outside file changed: %q", got)
	}
}

// Spec: §7.5.3, §7.8 — a marketplace checkout can commit a link at the lock's
// temporary name or a .podium directory link. Neither redirects the render's
// lock write outside the checkout: the render fails naming the refused path
// and the outside file is unchanged.
func TestRender_CommittedStateLinksStayInsideCheckout(t *testing.T) {
	t.Parallel()
	for name, plant := range map[string]func(t *testing.T, workdir, victimDir string){
		"lock temporary link": func(t *testing.T, workdir, victimDir string) {
			if err := os.Symlink(filepath.Join(victimDir, "sync.lock.tmp"), filepath.Join(workdir, ".podium", "sync.lock.tmp")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		},
		"podium directory link": func(t *testing.T, workdir, victimDir string) {
			if err := os.RemoveAll(filepath.Join(workdir, ".podium")); err != nil {
				t.Fatalf("RemoveAll: %v", err)
			}
			if err := os.Symlink(victimDir, filepath.Join(workdir, ".podium")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			workdir := filepath.Join(base, "checkout")
			opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
			if _, err := Render(context.Background(), opts); err != nil {
				t.Fatalf("first Render: %v", err)
			}
			victimDir := filepath.Join(base, "victim")
			writeFile(t, filepath.Join(victimDir, "sync.lock.tmp"), "victim\n")
			writeFile(t, filepath.Join(victimDir, "sync.lock"), "victim\n")
			plant(t, workdir, victimDir)

			_, err := Render(context.Background(), opts)
			refused := err != nil && (strings.Contains(err.Error(), "write lock") || errors.Is(err, ErrStateDirUnusable))
			if !refused {
				t.Errorf("Render = %v, want the lock write or the .podium link refused", err)
			}
			for _, f := range []string{"sync.lock.tmp", "sync.lock"} {
				if got, _ := os.ReadFile(filepath.Join(victimDir, f)); string(got) != "victim\n" {
					t.Errorf("%s outside the checkout was overwritten: %q", f, got)
				}
			}
		})
	}
}

// Spec: §7.8 — the change summary is written under the checkout's .podium
// directory through the same confinement. A committed link at the summary's
// path is replaced by a regular file rather than written through, and a
// committed link at its temporary name refuses the write; the outside file is
// unchanged in both cases.
func TestWriteChangeSummary_CommittedLinksStayInsideCheckout(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	workdir := filepath.Join(base, "checkout")
	victim := filepath.Join(base, "victim.txt")
	writeFile(t, victim, "victim\n")
	summary := filepath.Join(workdir, ".podium", "publish-change-summary.json")
	if err := os.MkdirAll(filepath.Dir(summary), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(victim, summary); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path, err := writeChangeSummary(workdir, &RenderResult{OutputID: "acme-agents"})
	if err != nil || path != summary {
		t.Errorf("writeChangeSummary over a committed summary link = (%q, %v), want (%q, nil)", path, err, summary)
	}
	if info, lerr := os.Lstat(summary); lerr != nil || !info.Mode().IsRegular() {
		t.Errorf("the summary must now be a regular file; lstat = (%v, %v)", info, lerr)
	}

	if err := os.Symlink(victim, summary+".tmp"); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := writeChangeSummary(workdir, &RenderResult{OutputID: "acme-agents"}); err == nil {
		t.Errorf("writeChangeSummary over a committed temporary link must fail")
	}
	if got, _ := os.ReadFile(victim); string(got) != "victim\n" {
		t.Errorf("the outside file was overwritten: %q", got)
	}
}
