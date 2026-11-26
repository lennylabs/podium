package sync

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// Spec: §7.5.3 — the lock reader drops every entry whose path the rule
// refuses, names it on standard error, and keeps the other entries, including
// one that records no path.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestDropUnsafeLockPaths(t *testing.T) {
	if dropUnsafeLockPaths(nil) != nil {
		t.Errorf("dropUnsafeLockPaths(nil) must return nil")
	}
	lock := &LockFile{Harness: "none", Artifacts: []LockArtifact{
		{ID: "good", MaterializedPath: "good/ARTIFACT.md"},
		{ID: "evil", MaterializedPath: "../victim/file.txt"},
		{ID: "nopath"},
		{ID: "state", MaterializedPath: ".podium/sync.lock"},
	}}
	var got *LockFile
	stderr := captureStderr(t, func() { got = dropUnsafeLockPaths(lock) })
	ids := make([]string, 0, len(got.Artifacts))
	for _, a := range got.Artifacts {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, ",") != "good,nopath" {
		t.Errorf("kept entries = %v, want [good nopath]", ids)
	}
	if got.Harness != "none" {
		t.Errorf("Harness = %q, want the prior lock's value", got.Harness)
	}
	if len(lock.Artifacts) != 4 {
		t.Errorf("the input lock must be left unchanged; it has %d entries", len(lock.Artifacts))
	}
	for _, want := range []string{`"../victim/file.txt"`, `".podium/sync.lock"`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to name %s", stderr, want)
		}
	}
}

// Spec: §7.5.3 — the rule that admits a prior-lock entry also governs what
// sync writes: a file whose path fails it is dropped from the output with an
// error naming the path and its owner, and the input is left unchanged.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestDropUnrecordablePaths(t *testing.T) {
	in := []adapter.File{{Path: "pack/a/ARTIFACT.md"}, {Path: `pack/a/notes\x.md`}, {Path: ".podium/resources/pack/a/run.sh"}}
	var got []adapter.File
	stderr := captureStderr(t, func() { got = dropUnrecordablePaths("pack/a", in) })
	if len(got) != 2 || got[0].Path != "pack/a/ARTIFACT.md" || got[1].Path != ".podium/resources/pack/a/run.sh" {
		t.Errorf("kept files = %v, want the two recordable paths", got)
	}
	if len(in) != 3 || in[1].Path != `pack/a/notes\x.md` {
		t.Errorf("the input slice must be left unchanged: %v", in)
	}
	if !strings.Contains(stderr, `refusing to write "pack/a/notes\\x.md" for pack/a`) {
		t.Errorf("stderr = %q, want the refused path and its owner", stderr)
	}
}
