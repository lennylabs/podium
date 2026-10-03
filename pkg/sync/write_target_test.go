package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// Spec: §7.5.2 — writeTarget compares the bytes on disk of the materialized
// paths and the prior lock's paths before and after materialization: an
// identical rewrite is unchanged, differing bytes and a removed stale path are
// changed, and an unreadable path counts as changed without failing the run.
func TestWriteTarget_ComparesBytesOnDisk(t *testing.T) {
	t.Parallel()
	files := []adapter.File{{Path: "a.md", Content: []byte("alpha\n"), Mode: 0o644}}
	current := map[string]bool{"a.md": true}

	t.Run("identical rewrite", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		writeFile(t, filepath.Join(target, "a.md"), "alpha\n")
		changed, err := writeTarget(target, files, current, map[string]string{"a.md": ""})
		if err != nil || changed {
			t.Fatalf("writeTarget = (%v, %v), want (false, nil)", changed, err)
		}
	})

	t.Run("differing bytes", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		writeFile(t, filepath.Join(target, "a.md"), "edited by hand\n")
		changed, err := writeTarget(target, files, current, map[string]string{"a.md": ""})
		if err != nil || !changed {
			t.Fatalf("writeTarget = (%v, %v), want (true, nil)", changed, err)
		}
	})

	t.Run("stale path removed", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		writeFile(t, filepath.Join(target, "a.md"), "alpha\n")
		writeFile(t, filepath.Join(target, "old.md"), "stale\n")
		changed, err := writeTarget(target, files, current, map[string]string{"a.md": "", "old.md": ""})
		if err != nil || !changed {
			t.Fatalf("writeTarget = (%v, %v), want (true, nil)", changed, err)
		}
		if _, err := os.Stat(filepath.Join(target, "old.md")); !os.IsNotExist(err) {
			t.Errorf("stale path survived the cleanup: %v", err)
		}
	})

	t.Run("prior-only path absent before and after", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		writeFile(t, filepath.Join(target, "a.md"), "alpha\n")
		changed, err := writeTarget(target, files, current, map[string]string{"a.md": "", "gone.md": ""})
		if err != nil || changed {
			t.Fatalf("writeTarget = (%v, %v), want (false, nil)", changed, err)
		}
	})

	t.Run("unreadable prior-only path", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		writeFile(t, filepath.Join(target, "a.md"), "alpha\n")
		// A non-empty directory at a stale path survives the best-effort
		// cleanup and cannot be read as a file.
		writeFile(t, filepath.Join(target, "old.md", "keep"), "operator\n")
		changed, err := writeTarget(target, files, current, map[string]string{"a.md": "", "old.md": ""})
		if err != nil || !changed {
			t.Fatalf("writeTarget = (%v, %v), want (true, nil)", changed, err)
		}
	})

	t.Run("write failure returned unchanged", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		// A non-empty directory at a standalone path makes materialize.Write
		// fail; the before-read only marks the path unobservable.
		writeFile(t, filepath.Join(target, "a.md", "keep"), "operator\n")
		_, err := writeTarget(target, files, current, nil)
		if err == nil {
			t.Fatal("writeTarget returned no error over a directory at a materialized path")
		}
		if strings.Contains(err.Error(), "sync: read target") {
			t.Errorf("error = %v, want the materialize.Write error", err)
		}
	})

	t.Run("unreadable current path before the write", func(t *testing.T) {
		t.Parallel()
		if os.Geteuid() == 0 {
			t.Skip("root reads a mode-000 file")
		}
		target := t.TempDir()
		path := filepath.Join(target, "a.md")
		writeFile(t, path, "alpha\n")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		changed, err := writeTarget(target, files, current, map[string]string{"a.md": ""})
		if err != nil || !changed {
			t.Fatalf("writeTarget = (%v, %v), want (true, nil)", changed, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "alpha\n" {
			t.Errorf("ReadFile after write = (%q, %v), want alpha", got, err)
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
