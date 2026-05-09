package source_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer/source"
)

// repoFactory creates a synthetic in-memory git repo with the given
// files and returns the storer URL the Git provider will clone.
func repoFactory(t *testing.T, files map[string]string) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	for relPath, body := range files {
		// Use the worktree's billy.Filesystem to create files.
		f, err := wt.Filesystem.Create(relPath)
		if err != nil {
			t.Fatalf("Create %s: %v", relPath, err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatalf("Write %s: %v", relPath, err)
		}
		_ = f.Close()
		if _, err := wt.Add(relPath); err != nil {
			t.Fatalf("Add %s: %v", relPath, err)
		}
	}
	if _, err := wt.Commit("test commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Tester", Email: "test@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	url := "file://" + dir
	cleanup := func() {}
	// Storer reuse not needed; keep cleanup as a no-op.
	_ = memory.NewStorage
	_ = memfs.New
	return url, cleanup
}

// Spec: §4.6 source types — Git.Snapshot clones the repo and exposes
// the tree at cfg.Ref as an fs.FS that the ingest pipeline can walk.
// Phase: 6
func TestGit_SnapshotExposesTree(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()
	url, cleanup := repoFactory(t, map[string]string{
		"company-glossary/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\n---\n\nbody\n",
		"finance/run/ARTIFACT.md":      "---\ntype: skill\nversion: 1.0.0\n---\n\n",
		"finance/run/SKILL.md":         "---\nname: run\ndescription: x\n---\n\nbody\n",
	})
	defer cleanup()

	snap, err := (source.Git{}).Snapshot(context.Background(), source.LayerConfig{
		Repo: url, Ref: "master",
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Reference == "" {
		t.Errorf("Reference is empty")
	}

	// The fs.FS exposes ARTIFACT.md files at their canonical paths.
	bytes, err := fs.ReadFile(snap.Files, "company-glossary/ARTIFACT.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(bytes), "type: context") {
		t.Errorf("body wrong: %s", bytes)
	}
}

// Spec: §4.6 — fs.WalkDir traversal works against the git tree FS.
// Phase: 6
func TestGit_FSWalkDir(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()
	url, cleanup := repoFactory(t, map[string]string{
		"a/ARTIFACT.md":   "---\ntype: context\nversion: 1.0.0\n---\n\n",
		"b/c/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\n---\n\n",
	})
	defer cleanup()
	snap, err := (source.Git{}).Snapshot(context.Background(), source.LayerConfig{
		Repo: url, Ref: "master",
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	found := []string{}
	walkErr := fs.WalkDir(snap.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, "ARTIFACT.md") {
			found = append(found, p)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("WalkDir: %v", walkErr)
	}
	if len(found) != 2 {
		t.Errorf("got %d ARTIFACT.md files, want 2: %v", len(found), found)
	}
}

// Spec: §6.10 — unreachable repo returns ErrSourceUnreachable.
// Phase: 6
// Matrix: §6.10 (ingest.source_unreachable)
func TestGit_UnreachableRepo(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()
	_, err := (source.Git{}).Snapshot(context.Background(), source.LayerConfig{
		Repo: "file:///nonexistent/repo/path", Ref: "main",
	})
	if !errors.Is(err, source.ErrSourceUnreachable) {
		t.Fatalf("got %v, want ErrSourceUnreachable", err)
	}
}

// Spec: §4.6 — cfg.Root narrows the snapshot to a subtree.
// Phase: 6
func TestGit_RootNarrowsToSubtree(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()
	url, cleanup := repoFactory(t, map[string]string{
		"top.txt":               "outside",
		"artifacts/x/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\n---\n\n",
	})
	defer cleanup()

	snap, err := (source.Git{}).Snapshot(context.Background(), source.LayerConfig{
		Repo: url, Ref: "master", Root: "artifacts",
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	bytes, err := fs.ReadFile(snap.Files, "x/ARTIFACT.md")
	if err != nil {
		t.Fatalf("expected x/ARTIFACT.md under subtree: %v", err)
	}
	if len(bytes) == 0 {
		t.Errorf("empty body")
	}
	// Files outside the subtree are not visible.
	if _, err := fs.ReadFile(snap.Files, "top.txt"); err == nil {
		t.Errorf("top.txt should not be visible under root=artifacts")
	}
}
