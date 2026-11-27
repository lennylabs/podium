package materialize

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// podiumAliasLink lays out a destination holding a directory real/ with
// real/resources/x, and a symbolic link named PODIUM~1, the NTFS short name
// of .podium, that points at real/. The link resolves inside the destination,
// so only the .podium link rule refuses a path through it.
func podiumAliasLink(t *testing.T) (dest, victim string) {
	t.Helper()
	dest = t.TempDir()
	victim = filepath.Join(dest, "real", "resources", "x")
	if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(victim, []byte("operator\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(dest, "PODIUM~1")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return dest, victim
}

// wantVictimUnchanged fails when the file behind the PODIUM~1 link changed.
func wantVictimUnchanged(t *testing.T, victim string) {
	t.Helper()
	if got, err := os.ReadFile(victim); err != nil || string(got) != "operator\n" {
		t.Errorf("%s = (%q, %v), want it unchanged", victim, got, err)
	}
}

// Spec: §6.6, §7.5.3 — a written path whose first segment names .podium under
// the aliasing rule is refused when that segment is a symbolic link, as a path
// under a .podium link is, although PODIUM~1/resources/x passes the
// recorded-path rule.
func TestWrite_RefusesPathThroughAliasedPodiumLink(t *testing.T) {
	t.Parallel()
	dest, victim := podiumAliasLink(t)
	err := Write(dest, []adapter.File{{Path: "PODIUM~1/resources/x", Content: []byte("podium\n")}})
	if !errors.Is(err, ErrPodiumNotDirectory) {
		t.Errorf("Write through PODIUM~1 = %v, want ErrPodiumNotDirectory", err)
	}
	wantVictimUnchanged(t, victim)
}

// Spec: §7.5.3 — the stale-file cleanup refuses a recorded path whose
// directory names .podium under the aliasing rule and is a symbolic link, and
// removes nothing behind it. The entry carries the hash of the file behind
// the link, so the link alone is what keeps the file in place.
func TestRemoveStale_RefusesAliasedPodiumLink(t *testing.T) {
	t.Parallel()
	dest, victim := podiumAliasLink(t)
	if err := RemoveStale(dest, "PODIUM~1/resources/x", FileHash([]byte("operator\n"))); !errors.Is(err, ErrPodiumNotDirectory) {
		t.Errorf("RemoveStale through PODIUM~1 = %v, want ErrPodiumNotDirectory", err)
	}
	wantVictimUnchanged(t, victim)
}

// Spec: §7.5.3 — a state file under a first segment that names .podium is
// written only when that segment is a directory rather than a symbolic link.
func TestWriteStateFile_RefusesAliasedPodiumLink(t *testing.T) {
	t.Parallel()
	dest, victim := podiumAliasLink(t)
	if err := WriteStateFile(dest, "PODIUM~1/sync.lock", []byte("version: 1\n"), 0o644); !errors.Is(err, ErrPodiumNotDirectory) {
		t.Errorf("WriteStateFile through PODIUM~1 = %v, want ErrPodiumNotDirectory", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "real", "sync.lock")); !os.IsNotExist(err) {
		t.Errorf("WriteStateFile wrote through the link: lstat err = %v", err)
	}
	wantVictimUnchanged(t, victim)
}

// Spec: §7.5.3 — a state file that is a symbolic link is read when it
// resolves to another regular file inside .podium/, and a resolved first
// segment that names .podium under the aliasing rule counts as .podium. A
// link that resolves to the sync state reached through such a segment's own
// .podium/ subtree is still checked as a path of its own.
func TestReadStateFile_AliasedPodiumLinkTarget(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(dest, ".podium", "keep"):          "",
		filepath.Join(dest, "PODIUM~1", "real.lock"):    "version: 1\n",
		filepath.Join(dest, "PODIUM~1", ".git", "HEAD"): "ref: refs/heads/main\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	for name, target := range map[string]string{
		"sync.lock": "../PODIUM~1/real.lock",
		"git.lock":  "../PODIUM~1/.git/HEAD",
	} {
		if err := os.Symlink(target, filepath.Join(dest, ".podium", name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if got, err := ReadStateFile(dest, ".podium/sync.lock"); err != nil || string(got) != "version: 1\n" {
		t.Errorf("ReadStateFile through a link into PODIUM~1 = (%q, %v), want the linked lock", got, err)
	}
	if _, err := ReadStateFile(dest, ".podium/git.lock"); !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("ReadStateFile through a link into PODIUM~1/.git = %v, want ErrUnconfinedPath", err)
	}
}
