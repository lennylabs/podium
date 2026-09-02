package source

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

const confinementArtifact = "---\ntype: context\nversion: 1.0.0\n---\nbody\n"

// confinedLayer builds a layer directory under a parent that also holds a
// secret one level above the layer root, and returns the parent and the root.
func confinedLayer(t *testing.T) (parent, root string) {
	t.Helper()
	parent = t.TempDir()
	root = filepath.Join(parent, "layer")
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: "pkg/ARTIFACT.md", Content: confinementArtifact},
		testharness.WriteTreeOption{Path: "shared/inside.txt", Content: "in-root secret\n"},
	)
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("outside secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	return parent, root
}

func snapshotFS(t *testing.T, root string) fs.FS {
	t.Helper()
	snap, err := Local{}.Snapshot(context.Background(), LayerConfig{Path: root})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snap.Files
}

// Spec: §4.6 source types; §7.3.1 local-source ingest confinement — a symbolic
// link inside the layer directory resolving outside it is refused, and the
// refusal does not stop the rest of the tree from being walked.
func TestLocal_ReadRefusesEscapingSymlink(t *testing.T) {
	t.Parallel()
	parent, root := confinedLayer(t)
	if err := os.Symlink(filepath.Join("..", "..", "outside.txt"), filepath.Join(root, "pkg", "leak.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	fsys := snapshotFS(t, root)

	data, err := fs.ReadFile(fsys, "pkg/leak.txt")
	if err == nil {
		t.Fatalf("read escaping link: want error, got %q", data)
	}
	if len(data) != 0 {
		t.Errorf("read escaping link: want zero bytes, got %d", len(data))
	}
	if !errors.Is(err, ErrSourceUnreachable) {
		t.Errorf("read escaping link: want ErrSourceUnreachable, got %v", err)
	}
	if !strings.Contains(err.Error(), "pkg/leak.txt") {
		t.Errorf("error does not name the refused path: %v", err)
	}
	if strings.Contains(err.Error(), parent) || strings.Contains(err.Error(), root) {
		t.Errorf("error leaks a host path: %v", err)
	}

	var reached bool
	if err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == "pkg/ARTIFACT.md" {
			reached = true
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if !reached {
		t.Errorf("WalkDir did not reach pkg/ARTIFACT.md")
	}
}

// Spec: §4.6; §7.3.1 — the control is confinement rather than a symbolic-link
// ban, so a relative link resolving inside the root reads through.
func TestLocal_ReadAdmitsSymlinkInsideRoot(t *testing.T) {
	t.Parallel()
	_, root := confinedLayer(t)
	// The target is written relative explicitly, because the sibling case
	// pins that an absolute one is refused.
	if err := os.Symlink(filepath.Join("..", "shared", "inside.txt"), filepath.Join(root, "pkg", "inside.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	data, err := fs.ReadFile(snapshotFS(t, root), "pkg/inside.txt")
	if err != nil {
		t.Fatalf("read in-root link: %v", err)
	}
	if string(data) != "in-root secret\n" {
		t.Errorf("read in-root link: got %q", data)
	}
}

// Spec: §4.6; §7.3.1 — os.Root refuses a link whose target is absolute whatever
// it resolves to, and the refusal is classified as unreachable.
func TestLocal_ReadRefusesAbsoluteSymlinkInsideRoot(t *testing.T) {
	t.Parallel()
	_, root := confinedLayer(t)
	if err := os.Symlink(filepath.Join(root, "shared", "inside.txt"), filepath.Join(root, "pkg", "abs.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	data, err := fs.ReadFile(snapshotFS(t, root), "pkg/abs.txt")
	if err == nil {
		t.Fatalf("read absolute link: want error, got %q", data)
	}
	if len(data) != 0 {
		t.Errorf("read absolute link: want zero bytes, got %d", len(data))
	}
	if !errors.Is(err, ErrSourceUnreachable) {
		t.Errorf("read absolute link: want ErrSourceUnreachable, got %v", err)
	}
}

// Spec: §4.6; §7.3.1; §6.10 — an absent path keeps fs.ErrNotExist so the
// ingest's required-file reads still report a missing file, and an unreadable
// in-root file is classified as unreachable.
func TestLocal_ReadClassifiesAbsentAndUnreadable(t *testing.T) {
	t.Parallel()
	_, root := confinedLayer(t)
	fsys := snapshotFS(t, root)

	_, err := fs.ReadFile(fsys, "pkg/absent.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("absent read: want fs.ErrNotExist, got %v", err)
	}
	if errors.Is(err, ErrSourceUnreachable) {
		t.Errorf("absent read: must not carry ErrSourceUnreachable, got %v", err)
	}

	if os.Geteuid() == 0 {
		// The mode bits do not bind root, so the unreadable arm cannot run
		// as that caller.
		t.Skip("running as root: mode 0 does not refuse the read")
	}
	unreadable := filepath.Join(root, "pkg", "locked.txt")
	if err := os.WriteFile(unreadable, []byte("locked\n"), 0o000); err != nil {
		t.Fatalf("WriteFile locked: %v", err)
	}
	_, err = fs.ReadFile(fsys, "pkg/locked.txt")
	if !errors.Is(err, ErrSourceUnreachable) {
		t.Errorf("unreadable read: want ErrSourceUnreachable, got %v", err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unreadable read: must not report the file as absent, got %v", err)
	}
}

// Spec: §4.6; §7.3.1 — os.OpenRoot resolves its own root argument normally, so
// a declared path that is itself a symbolic link keeps working.
func TestLocal_SnapshotRootMaySelfBeASymlink(t *testing.T) {
	t.Parallel()
	parent, root := confinedLayer(t)
	link := filepath.Join(parent, "link-to-layer")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	data, err := fs.ReadFile(snapshotFS(t, link), "pkg/ARTIFACT.md")
	if err != nil {
		t.Fatalf("read through symlinked root: %v", err)
	}
	if string(data) != confinementArtifact {
		t.Errorf("read through symlinked root: got %q", data)
	}
}

// Spec: §7.3.1 — the constructor the bootstrap sites call refuses the same
// escaping link the provider refuses. This case asserts the constructor; the
// call sites are pinned in pkg/registry/server and internal/serverboot.
func TestLocal_BootstrapTreeIsConfined(t *testing.T) {
	t.Parallel()
	_, root := confinedLayer(t)
	if err := os.Symlink(filepath.Join("..", "..", "outside.txt"), filepath.Join(root, "pkg", "leak.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	fsys := ConfinedFS(root)
	data, readErr := fs.ReadFile(fsys, "pkg/leak.txt")
	if readErr == nil {
		t.Fatalf("bootstrap tree read escaping link: want error, got %q", data)
	}
	if len(data) != 0 {
		t.Errorf("bootstrap tree read: want zero bytes, got %d", len(data))
	}
	if !errors.Is(readErr, ErrSourceUnreachable) {
		t.Errorf("bootstrap tree read: want ErrSourceUnreachable, got %v", readErr)
	}
	if _, err := fs.ReadFile(fsys, "pkg/ARTIFACT.md"); err != nil {
		t.Errorf("bootstrap tree read in-root artifact: %v", err)
	}
	f, err := fsys.Open("pkg/ARTIFACT.md")
	if err != nil {
		t.Fatalf("bootstrap tree open in-root artifact: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if _, err := fsys.Open("pkg/leak.txt"); !errors.Is(err, ErrSourceUnreachable) {
		t.Errorf("bootstrap tree open escaping link: want ErrSourceUnreachable, got %v", err)
	}
	// fs.FS requires an unrooted, cleaned name. A caller that passes anything
	// else is refused without a resolution attempt, and the refusal carries
	// ErrSourceUnreachable like every other non-ENOENT failure, so the
	// reingest endpoint classifies it as a source refusal.
	if _, err := fsys.Open("../outside.txt"); !errors.Is(err, ErrSourceUnreachable) {
		t.Errorf("bootstrap tree open invalid name: want ErrSourceUnreachable, got %v", err)
	}
	if _, err := fsys.Open("/etc/passwd"); !errors.Is(err, ErrSourceUnreachable) {
		t.Errorf("bootstrap tree open rooted name: want ErrSourceUnreachable, got %v", err)
	}
	// An absent path keeps fs.ErrNotExist, which is the other arm of the
	// classification and what the ingest's SKILL.md read distinguishes.
	if _, err := fsys.Open("pkg/absent.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("bootstrap tree open absent path: want fs.ErrNotExist, got %v", err)
	}
	if _, err := fs.Stat(fsys, "pkg"); err != nil {
		t.Errorf("bootstrap tree stat: %v", err)
	}
	entries, err := fs.ReadDir(fsys, "shared")
	if err != nil {
		t.Fatalf("bootstrap tree readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "inside.txt" {
		t.Errorf("bootstrap tree readdir: got %v", entries)
	}
}
