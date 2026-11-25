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

// Spec: §7.5.3 — the rule that admits a prior-lock entry governs what sync
// writes. A bundled resource whose name holds a backslash, which is a legal
// file name on Unix, is neither written nor recorded, so no later run warns
// about the lock's own entry and no file is orphaned when the artifact goes.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_UnrecordablePathNeitherWrittenNorRecorded(t *testing.T) {
	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: "pack/a/ARTIFACT.md", Content: agentArtifactSrc},
		testharness.WriteTreeOption{Path: `pack/a/notes\x.md`, Content: "n\n"},
	)
	target := filepath.Join(t.TempDir(), "target")
	opts := Options{RegistryPath: reg, Target: target, AdapterID: "none"}
	stderr := captureStderr(t, func() { syncChanged(t, opts) })
	if !strings.Contains(stderr, `refusing to write "pack/a/notes\\x.md" for pack/a`) {
		t.Errorf("stderr = %q, want the refused resource named", stderr)
	}
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	for _, la := range lock.Artifacts {
		if strings.Contains(la.MaterializedPath, `\`) {
			t.Errorf("the lock records the refused path %q", la.MaterializedPath)
		}
	}
	if _, err := os.Lstat(filepath.Join(target, "pack", "a", `notes\x.md`)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the refused resource must not be written; lstat err = %v", err)
	}

	if err := os.RemoveAll(filepath.Join(reg, "pack")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	stderr = captureStderr(t, func() { syncChanged(t, opts) })
	if strings.Contains(stderr, "skipping") {
		t.Errorf("removal run stderr = %q, want no skipped lock entry", stderr)
	}
	if _, err := os.Lstat(filepath.Join(target, "pack")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the removed artifact must leave nothing behind; lstat err = %v", err)
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
	if err == nil || !strings.Contains(err.Error(), "refusing to merge") {
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

			if _, err := Render(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "write lock") {
				t.Errorf("Render = %v, want the lock write refused", err)
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
