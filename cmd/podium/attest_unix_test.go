//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Spec: §7.3.5 — an entry of --out with a subject-file name that is a named
// pipe or a symbolic link, here to /dev/zero, is left unread and in place:
// the run neither blocks on the pipe nor reads the device without end, and
// its own file takes a number no entry holds.
func TestAttest_NonRegularOutEntriesStayUnread(t *testing.T) {
	dir := t.TempDir()
	writeAttestLayer(t, dir, map[string]string{"team/a/ARTIFACT.md": attestRuleMD})
	out := t.TempDir()
	fifo := filepath.Join(out, "podium-subjects-1.sha256")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	link := filepath.Join(out, "podium-subjects-2.sha256")
	if err := os.Symlink("/dev/zero", link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	statementLink := filepath.Join(out, "podium-statement-1.json")
	if err := os.Symlink("/dev/zero", statementLink); err != nil {
		t.Fatal(err)
	}

	type result struct {
		code   int
		stdout string
	}
	done := make(chan result, 1)
	go func() {
		code, stdout, _ := runAttestT(t, dir, "--out", out)
		done <- result{code, stdout}
	}()
	var res result
	select {
	case res = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("podium attest blocked on a named pipe or a device link in --out")
	}
	want := filepath.Join(out, "podium-subjects-3.sha256") + "\n"
	if res.code != 0 || res.stdout != want {
		t.Errorf("run = %d, stdout %q, want 0 and %q", res.code, res.stdout, want)
	}
	for _, p := range []string{fifo, link, statementLink} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}

	// The statement format skips the link that holds its first name.
	_, stdout, _ := runAttestT(t, dir, "--out", out, "--format", "statement")
	if !strings.HasSuffix(stdout, "podium-statement-2.json\n") {
		t.Errorf("statement run printed %q, want podium-statement-2.json", stdout)
	}
}
