package materialize

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/adapter"
)

// Spec: §6.6 step 4 / §9.1 MaterializationHook — Materialize runs
// every hook in order; the second hook sees the first hook's output.
// Phase: 13
func TestMaterialize_HookChainOrder(t *testing.T) {
	testharness.RequirePhase(t, 13)
	t.Parallel()
	dir := t.TempDir()
	uppercaser := func(f adapter.File) (adapter.File, bool, error) {
		f.Content = []byte(strings.ToUpper(string(f.Content)))
		return f, false, nil
	}
	suffixer := func(f adapter.File) (adapter.File, bool, error) {
		f.Content = append(f.Content, []byte("-suffixed")...)
		return f, false, nil
	}
	if err := Materialize(dir, []adapter.File{
		{Path: "x.txt", Content: []byte("hello")},
	}, []HookFunc{uppercaser, suffixer}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got := testharness.ReadTree(t, dir)
	if got["x.txt"] != "HELLO-suffixed" {
		t.Errorf("x.txt = %q, want HELLO-suffixed", got["x.txt"])
	}
}

// Spec: §6.6 — a hook can drop a file entirely.
// Phase: 13
func TestMaterialize_HookDropsFile(t *testing.T) {
	testharness.RequirePhase(t, 13)
	t.Parallel()
	dir := t.TempDir()
	dropper := func(f adapter.File) (adapter.File, bool, error) {
		if strings.HasSuffix(f.Path, ".tmp") {
			return f, true, nil
		}
		return f, false, nil
	}
	if err := Materialize(dir, []adapter.File{
		{Path: "x.txt", Content: []byte("keep")},
		{Path: "y.tmp", Content: []byte("drop")},
	}, []HookFunc{dropper}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got := testharness.ReadTree(t, dir)
	if _, ok := got["x.txt"]; !ok {
		t.Errorf("x.txt missing")
	}
	if _, ok := got["y.tmp"]; ok {
		t.Errorf("y.tmp should be dropped")
	}
}

// Spec: §6.6 — a hook returning an error aborts the pipeline before
// any write.
// Phase: 13
func TestMaterialize_HookErrorAborts(t *testing.T) {
	testharness.RequirePhase(t, 13)
	t.Parallel()
	dir := t.TempDir()
	failing := func(adapter.File) (adapter.File, bool, error) {
		return adapter.File{}, false, errors.New("nope")
	}
	err := Materialize(dir, []adapter.File{
		{Path: "x.txt", Content: []byte("hi")},
	}, []HookFunc{failing})
	if err == nil {
		t.Errorf("expected error from failing hook")
	}
	if files := testharness.ReadTree(t, dir); len(files) != 0 {
		t.Errorf("hook error wrote %d files: %v", len(files), files)
	}
}

// Spec: §6.6 — Materialize without hooks behaves like Write.
// Phase: 13
func TestMaterialize_NoHooksMatchesWrite(t *testing.T) {
	testharness.RequirePhase(t, 13)
	t.Parallel()
	dir := t.TempDir()
	if err := Materialize(dir, []adapter.File{
		{Path: "x.txt", Content: []byte("ok")},
	}, nil); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := testharness.ReadTree(t, dir); got["x.txt"] != "ok" {
		t.Errorf("x.txt = %q, want ok", got["x.txt"])
	}
}
