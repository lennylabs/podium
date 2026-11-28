package sign_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/tuf"

	"github.com/lennylabs/podium/pkg/sign"
)

// dirFiles lists the files under dir, relative to it.
func dirFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(dir, p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

// Spec: §4.7.11 (User sources of the MCP server, Root copy) — an in-memory
// source starts from the embedded roots, refreshes through TUF, and reads
// and writes no directory; it names no directory.
func TestTUFRoot_MemoryWritesNothing(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	cfg := f.config(time.Time{})
	cfg.Dir, cfg.Memory = "", true
	src := f.source(t, cfg)
	if src.Status().Origin != sign.RootEmbedded {
		t.Fatalf("origin = %s, want embedded", src.Status().Origin)
	}
	cwd, _ := os.Getwd()
	before := dirFiles(t, cwd)
	if err := src.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := verifiesUnder(t, src, f.served); err != nil {
		t.Fatalf("bundle under the refreshed root: %v", err)
	}
	if len(dirFiles(t, cwd)) != len(before) {
		t.Error("an in-memory refresh wrote into the working directory")
	}
	cfg.Dir = f.dir
	if _, err := sign.NewTUFAttestationRoot(cfg); err == nil {
		t.Error("an in-memory source with a directory was accepted")
	}
}

// Spec: §4.7.11 (User sources of the MCP server, Repository-source roots) —
// a read-only source starts from the user's copy, refreshes, and leaves the
// copy and the TUF metadata as they were.
func TestTUFRoot_ReadOnlyLeavesTheUserCopy(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	writer := f.source(t, f.config(time.Time{}))
	if err := writer.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	before := dirFiles(t, f.dir)
	f.clk.Advance(time.Hour)
	cfg := f.config(time.Time{})
	cfg.ReadOnly = true
	ro := f.source(t, cfg)
	if ro.Status().Origin != sign.RootFromDisk {
		t.Fatalf("origin = %s, want the user's copy", ro.Status().Origin)
	}
	if err := ro.Refresh(context.Background()); err != nil {
		t.Fatalf("read-only Refresh: %v", err)
	}
	if ro.Status().Origin != sign.RootRefreshed {
		t.Errorf("origin = %s, want refreshed", ro.Status().Origin)
	}
	after := dirFiles(t, f.dir)
	if len(after) != len(before) {
		t.Fatalf("files %d, want %d", len(after), len(before))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("a read-only refresh changed %s", k)
		}
	}
}

// Spec: §4.7.11 (User sources of the MCP server, Keys) — a view under its
// own maximum age shares the refreshed root and applies its own limit.
func TestTUFRoot_WithMaxAge(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	src := f.source(t, f.config(f.clk.Now()))
	f.clk.Advance(2 * time.Hour)
	short := src.WithMaxAge(time.Hour)
	if _, err := short.AttestationRoot(context.Background()); !errors.Is(err, sign.ErrAttestationRootStale) || !strings.Contains(err.Error(), "its maximum age") {
		t.Fatalf("short view: %v", err)
	}
	if _, err := src.WithMaxAge(0).AttestationRoot(context.Background()); err != nil {
		t.Errorf("a view with no limit: %v", err)
	}
	if _, err := src.AttestationRoot(context.Background()); err != nil {
		t.Errorf("the source under its own limit: %v", err)
	}
	if st, ok := short.(sign.AttestationRootStatusSource); !ok || st.Status().Digest != src.Status().Digest {
		t.Error("the view reports another root")
	}
}

// Spec: §4.7.11 (Root copy of user-source rules) — a refresh whose write of
// the TUF cache fails leaves the earlier files and refreshes in memory.
func TestTUFRoot_CacheWriteFailureRefreshesInMemory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not stop this user from writing")
	}
	f := newTUFFixture(t)
	meta := filepath.Join(f.dir, tuf.URLToPath(f.repo.URL()))
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(meta, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(meta, 0o700) })
	src := f.source(t, f.config(time.Time{}))
	if err := src.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !f.logs.contains("refreshing in memory") || src.Status().Origin != sign.RootRefreshed {
		t.Fatalf("logs = %q, origin %s", f.logs.lines, src.Status().Origin)
	}
	if entries, _ := os.ReadDir(meta); len(entries) != 0 {
		t.Errorf("the unwritable TUF cache holds %d entries", len(entries))
	}
}

// Spec: §4.7.11 (Trusted root refresh) — a copy whose cached TUF root is
// the next version of the embedded one, signed by its keys, verifies; one
// that rotated two versions past the embedded root counts as no copy until a
// binary that embeds a later root reads it.
func TestTUFRoot_CopyAcrossARootRotation(t *testing.T) {
	t.Parallel()
	for rotations, want := range map[int]sign.RootOrigin{1: sign.RootFromDisk, 2: sign.RootEmbedded} {
		f := newTUFFixture(t)
		for range rotations {
			f.repo.RotateRoot()
		}
		if err := f.source(t, f.config(time.Time{})).Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if got := f.source(t, f.config(time.Time{})).Status().Origin; got != want {
			t.Errorf("%d rotations: origin %s, want %s", rotations, got, want)
		}
	}
}

// Spec: §4.7.11 (Trusted root refresh) — cached metadata that does not
// parse counts as no copy.
func TestTUFRoot_CopyBesideMetadataThatDoesNotParse(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"root.json", "timestamp.json", "snapshot.json", "targets.json"} {
		f := newTUFFixture(t)
		if err := f.source(t, f.config(time.Time{})).Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if err := os.WriteFile(filepath.Join(f.dir, tuf.URLToPath(f.repo.URL()), name), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := f.source(t, f.config(time.Time{})).Status().Origin; got != sign.RootEmbedded {
			t.Errorf("%s: origin %s, want embedded", name, got)
		}
	}
}
