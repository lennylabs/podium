package source_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/lennylabs/podium/pkg/layer/source"
)

func TestGit_IDAndTrigger(t *testing.T) {
	t.Parallel()
	g := source.Git{}
	if g.ID() != "git" {
		t.Errorf("Git.ID = %q", g.ID())
	}
	if g.Trigger() != source.TriggerWebhook {
		t.Errorf("Git.Trigger = %v", g.Trigger())
	}
}

func TestLocal_IDAndTrigger(t *testing.T) {
	t.Parallel()
	l := source.Local{}
	if l.ID() != "local" {
		t.Errorf("Local.ID = %q", l.ID())
	}
	if l.Trigger() != source.TriggerManual {
		t.Errorf("Local.Trigger = %v", l.Trigger())
	}
}

func TestLocal_SnapshotRequiresPath(t *testing.T) {
	t.Parallel()
	_, err := source.Local{}.Snapshot(context.Background(), source.LayerConfig{})
	if !errors.Is(err, source.ErrInvalidConfig) {
		t.Errorf("got %v, want ErrInvalidConfig", err)
	}
}

func TestLocal_SnapshotMissingPathReturnsUnreachable(t *testing.T) {
	t.Parallel()
	_, err := source.Local{}.Snapshot(context.Background(),
		source.LayerConfig{Path: filepath.Join(t.TempDir(), "absent")})
	if !errors.Is(err, source.ErrSourceUnreachable) {
		t.Errorf("got %v, want ErrSourceUnreachable", err)
	}
}

func TestLocal_SnapshotOK(t *testing.T) {
	t.Parallel()
	snap, err := source.Local{}.Snapshot(context.Background(),
		source.LayerConfig{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Files == nil {
		t.Errorf("Files nil")
	}
}

// gitTreeRepo builds a tiny git repo with a known directory layout
// and returns the file:// URL the Git provider will clone.
func gitTreeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, _ := repo.Worktree()
	write := func(p, body string) {
		f, _ := wt.Filesystem.Create(p)
		_, _ = f.Write([]byte(body))
		_ = f.Close()
		_, _ = wt.Add(p)
	}
	write("readme.md", "# top")
	write("nested/inner.txt", "hi")
	if _, err := wt.Commit("init", &git.CommitOptions{
		Author: &object.Signature{Name: "T", Email: "t@x", When: time.Now()},
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return "file://" + dir
}

// TestGit_FSExerciseEntryMethods ensures the fs.FS implementation's
// methods (DirEntry.Type/Info, FileInfo.Name/Mode/ModTime/Sys, gitDir.Read)
// are exercised end-to-end.
func TestGit_FSExerciseEntryMethods(t *testing.T) {
	t.Parallel()
	url := gitTreeRepo(t)
	snap, err := source.Git{}.Snapshot(context.Background(), source.LayerConfig{
		Repo: url, Ref: "master",
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Read a directory and inspect entries; this exercises ReadDir,
	// DirEntry.Type, DirEntry.Info, FileInfo getters.
	dir, err := snap.Files.Open(".")
	if err != nil {
		t.Fatalf("Open(.): %v", err)
	}
	defer dir.Close()
	if _, err := dir.(fs.ReadDirFile).ReadDir(-1); err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	// Attempting to Read a directory must error per gitDir.Read.
	if _, err := dir.Read(make([]byte, 8)); err == nil {
		t.Errorf("gitDir.Read = nil error, want error")
	}

	// Walk and call DirEntry.Info on every entry to cover the
	// gitFileInfo getters (Name, Mode, ModTime, Sys, IsDir).
	err = fs.WalkDir(snap.Files, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		_ = info.Name()
		_ = info.Mode()
		_ = info.ModTime()
		_ = info.Sys()
		_ = info.IsDir()
		_ = d.Type()
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}

	// Open a regular file and consume it to exercise gitFile.Read.
	f, err := snap.Files.Open("readme.md")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	_ = f.Close()
}
