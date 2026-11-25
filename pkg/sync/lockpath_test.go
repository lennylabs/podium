package sync

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// Spec: §7.5.3 — a materialized_path read from a prior lock is used only when
// it is a non-empty slash-separated relative path with no empty, ".", "..",
// or ".git" segment, no backslash, no control character, and no volume name,
// and it does not name the sync's own state under .podium/.
func TestCheckLockPath(t *testing.T) {
	t.Parallel()
	refused := []string{
		"",
		"../x",
		"../victim/file.txt",
		"a/../../x",
		"a/..",
		"/abs",
		"/etc/passwd",
		`a\b`,
		`..\x`,
		".git/HEAD",
		"sub/.git/config",
		".GIT/HEAD",
		".podium/sync.lock",
		".podium/sync.lock.tmp",
		".podium/sync.yaml",
		".podium/sync.local.yaml",
		".podium/overlay/x/ARTIFACT.md",
		".PODIUM/sync.lock",
		".podium/resources",
		"a//b",
		"a/",
		"./a",
		"a/./b",
		"a\x00b",
		"a\nb",
		"a\x7fb",
	}
	if runtime.GOOS == "windows" {
		refused = append(refused, "C:/x", "C:x")
	}
	for _, p := range refused {
		if err := checkLockPath(p); !errors.Is(err, errUnsafeLockPath) {
			t.Errorf("checkLockPath(%q) = %v, want errUnsafeLockPath", p, err)
		}
	}
	accepted := []string{
		"agents/pay-invoice.md",
		".claude/skills/hello/SKILL.md",
		".claude/settings.json",
		".podium/resources/finance/ap/pay-invoice/run.sh",
		"claude/finance-pack/skills/pay-invoice/SKILL.md",
		".gitignore",
		"a/.github/workflows/x.yml",
		"..foo/bar",
		"AGENTS.md",
	}
	for _, p := range accepted {
		if err := checkLockPath(p); err != nil {
			t.Errorf("checkLockPath(%q) = %v, want nil", p, err)
		}
	}
}

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
