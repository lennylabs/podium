package source

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// commitWorkTree writes files, keyed by slash path, into a new non-bare
// repository at dir, commits them on HEAD, and returns the commit hash.
func commitWorkTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatalf("AddGlob: %v", err)
	}
	sig := &object.Signature{Name: "alice", Email: "alice@acme.com", When: time.Unix(0, 0).UTC()}
	h, err := wt.Commit("c", &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return h.String()
}

// Spec: §4.7.6, §7.3.5 — OpenCommitTree reads the commit's tree below the
// path of dir in the working tree joined with root, from the object database:
// a working-tree edit after the commit is not read, and a dir below the top
// of the working tree selects its own subtree.
func TestOpenCommitTree_ReadsTheCommit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	head := commitWorkTree(t, dir, map[string]string{
		"skills/team/pkg/ARTIFACT.md": "committed\n",
		"README.md":                   "top\n",
	})
	if err := os.WriteFile(filepath.Join(dir, "skills/team/pkg/ARTIFACT.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, c := range []struct{ dir, root, path string }{
		{dir, "skills", "skills"},
		{dir, "/skills/", "skills"},
		{filepath.Join(dir, "skills"), "", "skills"},
		{filepath.Join(dir, "skills"), "team", "skills/team"},
	} {
		tree, err := OpenCommitTree(ctx, c.dir, "HEAD", c.root)
		if err != nil {
			t.Fatalf("OpenCommitTree(%s, %q): %v", c.dir, c.root, err)
		}
		if tree.Commit != head || tree.Path != c.path {
			t.Errorf("OpenCommitTree(%s, %q) = commit %s path %q, want %s %q", c.dir, c.root, tree.Commit, tree.Path, head, c.path)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix("skills/team/pkg/ARTIFACT.md", c.path), "/")
		b, err := fs.ReadFile(tree.Files, rel)
		if err != nil || string(b) != "committed\n" {
			t.Errorf("read %s = %q, %v, want the committed bytes", rel, b, err)
		}
	}
	tree, err := OpenCommitTree(ctx, dir, head[:12], "")
	if err != nil || tree.Path != "" {
		t.Fatalf("OpenCommitTree(short hash) = %+v, %v", tree, err)
	}
	if _, err := fs.ReadFile(tree.Files, "README.md"); err != nil {
		t.Errorf("read README.md at the root: %v", err)
	}
}

// Spec: §7.3.5 — a revision that names no commit, a path the commit holds
// no directory at, a directory in no repository, and a cancelled context
// each fail.
func TestOpenCommitTree_Refusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	commitWorkTree(t, dir, map[string]string{"skills/a/ARTIFACT.md": "x\n"})
	ctx := context.Background()
	if _, err := OpenCommitTree(ctx, dir, "no-such-branch", ""); err == nil || !strings.Contains(err.Error(), "no-such-branch") {
		t.Errorf("unknown revision = %v, want an error naming it", err)
	}
	if _, err := OpenCommitTree(ctx, dir, "HEAD", "missing"); err == nil || !strings.Contains(err.Error(), `"missing"`) {
		t.Errorf("missing root = %v, want an error naming it", err)
	}
	if _, err := OpenCommitTree(ctx, t.TempDir(), "HEAD", ""); err == nil {
		t.Error("a directory in no repository was opened")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := OpenCommitTree(cancelled, dir, "HEAD", ""); err == nil {
		t.Error("a cancelled context was not honored")
	}
}

// Spec: §7.3.5 — a dir whose path passes through a link out of the working
// tree lies outside it, as Git reads it, so no tree path is guessed for it.
func TestOpenCommitTree_LinkOutOfTheWorkTree(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	commitWorkTree(t, repo, map[string]string{"skills/a/ARTIFACT.md": "x\n"})
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "link")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	_, err := OpenCommitTree(context.Background(), filepath.Join(repo, "link", "skills"), "HEAD", "")
	if err == nil || !strings.Contains(err.Error(), "outside the working tree") {
		t.Errorf("a dir reached through a link out of the tree = %v, want refused", err)
	}
}

// Spec: §7.3.5 — a bare repository has no working tree, so its tree is read
// from the root joined with root.
func TestOpenCommitTree_BareRepository(t *testing.T) {
	t.Parallel()
	work := t.TempDir()
	commitWorkTree(t, work, map[string]string{"skills/a/ARTIFACT.md": "x\n"})
	bare := filepath.Join(t.TempDir(), "bare.git")
	if _, err := git.PlainClone(bare, true, &git.CloneOptions{URL: work}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	tree, err := OpenCommitTree(context.Background(), bare, "HEAD", "skills")
	if err != nil || tree.Path != "skills" {
		t.Fatalf("OpenCommitTree(bare) = %+v, %v", tree, err)
	}
	if b, err := fs.ReadFile(tree.Files, "a/ARTIFACT.md"); err != nil || string(b) != "x\n" {
		t.Errorf("read = %q, %v", b, err)
	}
	if IsGitWorkTree(bare) {
		t.Error("IsGitWorkTree(bare) = true")
	}
}

// Spec: §7.3.5 — IsGitWorkTree finds the repository from any directory of
// its working tree and reports false outside every repository.
func TestIsGitWorkTree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	commitWorkTree(t, dir, map[string]string{"skills/a/ARTIFACT.md": "x\n"})
	if !IsGitWorkTree(dir) || !IsGitWorkTree(filepath.Join(dir, "skills", "a")) {
		t.Error("IsGitWorkTree is false inside a working tree")
	}
	if IsGitWorkTree(t.TempDir()) {
		t.Error("IsGitWorkTree is true outside every repository")
	}
}
