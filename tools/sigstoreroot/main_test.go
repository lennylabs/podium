package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/internal/testharness/tufharness"
)

// Spec: §4.7.11 — the release check fails while the embedded copy differs
// from the current TUF target, the refresh writes the target, and the
// check then passes.
func TestRun_RefreshAndCheck(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	current := h.TrustedRootJSON(sh.WithCTLogKey())
	repo := tufharness.New(t, current)
	dir := t.TempDir()
	tufRoot := filepath.Join(dir, "root.json")
	if err := os.WriteFile(tufRoot, repo.RootJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	embedded := filepath.Join(dir, "embedded.json")
	if err := os.WriteFile(embedded, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	common := []string{"-mirror", repo.URL(), "-tuf-root", tufRoot, "-file", embedded}

	var out, errOut bytes.Buffer
	if code := run(context.Background(), append([]string{"-check"}, common...), &out, &errOut); code != 1 {
		t.Fatalf("check of a stale copy exited %d, want 1 (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "go run ./tools/sigstoreroot") {
		t.Errorf("stderr = %q, want the refresh command", errOut.String())
	}
	if code := run(context.Background(), common, &out, &errOut); code != 0 {
		t.Fatalf("refresh exited %d (stderr %q)", code, errOut.String())
	}
	got, _ := os.ReadFile(embedded)
	if !bytes.Equal(got, current) {
		t.Fatal("refresh did not write the current target")
	}
	out.Reset()
	if code := run(context.Background(), append([]string{"-check"}, common...), &out, &errOut); code != 0 {
		t.Fatalf("check of the current copy exited %d (stderr %q)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "is current") {
		t.Errorf("stdout = %q, want the current notice", out.String())
	}
}

// Spec: §4.7.11 — a fetch that fails, a TUF root that cannot be read, a
// copy that cannot be read, and an unknown flag each exit non-zero.
func TestRun_Failures(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	repo := tufharness.New(t, h.TrustedRootJSON(sh.WithCTLogKey()))
	dir := t.TempDir()
	tufRoot := filepath.Join(dir, "root.json")
	if err := os.WriteFile(tufRoot, repo.RootJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	for name, args := range map[string][]string{
		"unknown flag":       {"-nope"},
		"missing TUF root":   {"-mirror", repo.URL(), "-tuf-root", filepath.Join(dir, "absent.json")},
		"missing copy":       {"-check", "-mirror", repo.URL(), "-tuf-root", tufRoot, "-file", filepath.Join(dir, "absent.json")},
		"unwritable copy":    {"-mirror", repo.URL(), "-tuf-root", tufRoot, "-file", filepath.Join(dir, "no", "such", "dir.json")},
		"failing repository": {"-mirror", "http://127.0.0.1:1", "-tuf-root", tufRoot, "-timeout", "2s"},
	} {
		if code := run(context.Background(), args, &out, &errOut); code == 0 {
			t.Errorf("%s: exited 0", name)
		}
	}
}
