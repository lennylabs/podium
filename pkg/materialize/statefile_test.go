package materialize

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// Spec: §7.5.3, §7.8 — a state file is written atomically below the target,
// replacing a leftover regular temporary, and read back.
func TestWriteStateFile_WritesAndReadsBack(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "target")
	if err := WriteStateFile(dest, ".podium/sync.lock", []byte("v1"), 0o644); err != nil {
		t.Fatalf("WriteStateFile into a new target: %v", err)
	}
	confinedFixture(t, dest, map[string]string{".podium/sync.lock.tmp": "leftover"})
	if err := WriteStateFile(dest, ".podium/sync.lock", []byte("v2"), 0o644); err != nil {
		t.Fatalf("WriteStateFile over a leftover temporary: %v", err)
	}
	got, err := ReadStateFile(dest, ".podium/sync.lock")
	if err != nil || string(got) != "v2" {
		t.Errorf("ReadStateFile = (%q, %v), want (v2, nil)", got, err)
	}
	wantGone(t, filepath.Join(dest, ".podium", "sync.lock.tmp"))
	if _, err := ReadStateFile(dest, ".podium/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadStateFile of a missing file = %v, want fs.ErrNotExist", err)
	}
	if err := WriteStateFile("", ".podium/x", nil, 0o644); !errors.Is(err, ErrEmptyDestination) {
		t.Errorf("WriteStateFile with an empty destination = %v, want ErrEmptyDestination", err)
	}
	if err := WriteStateFile(dest, "../x", nil, 0o644); !errors.Is(err, ErrOutOfDestination) {
		t.Errorf("WriteStateFile(../x) = %v, want ErrOutOfDestination", err)
	}
}

// Spec: §7.8 — a repository checkout can commit a link at the lock's
// temporary name, at the lock itself, or at .podium. None of them redirects
// the state write or read outside the checkout: the temporary link refuses
// the write, the lock link is replaced by the rename, and the .podium link
// refuses both the write and the read.
func TestStateFile_CommittedLinksStayInsideTarget(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	victim := filepath.Join(base, "victim.txt")
	confinedFixture(t, base, map[string]string{"victim.txt": "victim\n", "podium-out/sync.lock": "outside lock"})

	t.Run("temporary link", func(t *testing.T) {
		dest := filepath.Join(base, "tmp-link")
		mustSymlink(t, victim, filepath.Join(dest, ".podium", "sync.lock.tmp"))
		err := WriteStateFile(dest, ".podium/sync.lock", []byte("lock"), 0o644)
		if !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("WriteStateFile over a temporary link = %v, want ErrUnconfinedPath", err)
		}
		wantContent(t, victim, "victim\n")
	})
	t.Run("lock link", func(t *testing.T) {
		dest := filepath.Join(base, "lock-link")
		mustSymlink(t, victim, filepath.Join(dest, ".podium", "sync.lock"))
		if _, err := ReadStateFile(dest, ".podium/sync.lock"); !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("ReadStateFile of a lock link = %v, want ErrUnconfinedPath", err)
		}
		if err := WriteStateFile(dest, ".podium/sync.lock", []byte("lock"), 0o644); err != nil {
			t.Fatalf("WriteStateFile over a lock link: %v", err)
		}
		wantContent(t, victim, "victim\n")
		wantContent(t, filepath.Join(dest, ".podium", "sync.lock"), "lock")
	})
	t.Run("lock link inside the target", func(t *testing.T) {
		dest := filepath.Join(base, "inside-lock-link")
		confinedFixture(t, dest, map[string]string{"locks/sync.lock": "kept lock"})
		mustSymlink(t, "../locks/sync.lock", filepath.Join(dest, ".podium", "sync.lock"))
		got, err := ReadStateFile(dest, ".podium/sync.lock")
		if err != nil || string(got) != "kept lock" {
			t.Errorf("ReadStateFile of an in-target lock link = (%q, %v), want (kept lock, nil)", got, err)
		}
	})
	t.Run("podium link", func(t *testing.T) {
		dest := filepath.Join(base, "podium-link")
		mustSymlink(t, filepath.Join(base, "podium-out"), filepath.Join(dest, ".podium"))
		if err := WriteStateFile(dest, ".podium/sync.lock", []byte("lock"), 0o644); !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("WriteStateFile under a .podium link = %v, want ErrUnconfinedPath", err)
		}
		if _, err := ReadStateFile(dest, ".podium/sync.lock"); !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("ReadStateFile under a .podium link = %v, want ErrUnconfinedPath", err)
		}
		wantContent(t, filepath.Join(base, "podium-out", "sync.lock"), "outside lock")
	})
}

// Spec: §6.6, §6.7, §7.8 — a pre-existing "<path>.tmp" link, which a repository
// checkout can commit, does not redirect the writer's staging write outside
// the destination: the write is refused and the outside file is unchanged. A
// leftover regular temporary is replaced.
func TestWrite_StagingTemporaryLinkRefused(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "checkout")
	victim := filepath.Join(base, "victim.txt")
	confinedFixture(t, base, map[string]string{"victim.txt": "victim\n"})
	mustSymlink(t, victim, filepath.Join(dest, ".claude-plugin", "marketplace.json.tmp"))

	err := Write(dest, []adapter.File{{Path: ".claude-plugin/marketplace.json", Content: []byte("RENDERED\n")}})
	if !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("Write over a staging link = %v, want ErrUnconfinedPath", err)
	}
	wantContent(t, victim, "victim\n")
	wantGone(t, filepath.Join(dest, ".claude-plugin", "marketplace.json"))

	confinedFixture(t, dest, map[string]string{"a.md.tmp": "leftover"})
	if err := Write(dest, []adapter.File{{Path: "a.md", Content: []byte("A")}}); err != nil {
		t.Fatalf("Write over a leftover regular temporary: %v", err)
	}
	wantContent(t, filepath.Join(dest, "a.md"), "A")
	wantGone(t, filepath.Join(dest, "a.md.tmp"))
}

// Spec: §6.6, §6.7, §7.8 — a merge or inject target that is a link resolving
// outside the destination, into .git, or into the sync state, or a dangling
// link, is not read through: a committed link to an outside JSON file cannot
// fold that file's content into the rendered file the publish step commits.
func TestWrite_MergeTargetLinkRefused(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "checkout")
	confinedFixture(t, base, map[string]string{"creds.json": `{"token":"SECRET-123"}`})
	confinedFixture(t, dest, map[string]string{".git/config": "git", ".podium/sync.lock": "lock"})
	mustSymlink(t, filepath.Join(base, "creds.json"), filepath.Join(dest, ".claude-plugin", "marketplace.json"))
	mustSymlink(t, filepath.Join(base, "creds.json"), filepath.Join(dest, "AGENTS.md"))
	mustSymlink(t, ".git/config", filepath.Join(dest, "GEMINI.md"))
	mustSymlink(t, ".podium/sync.lock", filepath.Join(dest, "CLAUDE.md"))
	mustSymlink(t, "absent.json", filepath.Join(dest, "settings.json"))

	for _, f := range []adapter.File{
		{Path: ".claude-plugin/marketplace.json", Op: adapter.OpMergeJSON, Content: []byte(`{"name":"acme"}`)},
		{Path: "AGENTS.md", Op: adapter.OpInject, Key: "a", Content: []byte("block")},
		{Path: "GEMINI.md", Op: adapter.OpInject, Key: "a", Content: []byte("block")},
		{Path: "CLAUDE.md", Op: adapter.OpInject, Key: "a", Content: []byte("block")},
		{Path: "settings.json", Op: adapter.OpMergeJSON, Content: []byte(`{"a":1}`)},
	} {
		err := Write(dest, []adapter.File{f})
		if !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("Write(%s) into a link = %v, want ErrUnconfinedPath", f.Path, err)
		}
		info, lerr := os.Lstat(filepath.Join(dest, filepath.FromSlash(f.Path)))
		if lerr != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("%s must stay the committed link; lstat = (%v, %v)", f.Path, info, lerr)
		}
	}
	wantContent(t, filepath.Join(base, "creds.json"), `{"token":"SECRET-123"}`)
	wantContent(t, filepath.Join(dest, ".git", "config"), "git")
	wantContent(t, filepath.Join(dest, ".podium", "sync.lock"), "lock")
	wantGone(t, filepath.Join(dest, "absent.json"))
}

// Spec: §6.6, §6.7 — a merge or inject target that is a link to a regular
// file inside the destination, such as GEMINI.md linked to AGENTS.md, is
// merged into the file the link names: the operator's content stays, the
// block lands in AGENTS.md, and GEMINI.md stays a link. A directory link
// inside the destination is written through as well.
func TestWrite_MergeTargetLinkInsideFollowed(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "target")
	confinedFixture(t, dest, map[string]string{
		"AGENTS.md":                   "operator\n",
		"shared/gemini/settings.json": `{"theme":"dark"}`,
	})
	mustSymlink(t, "AGENTS.md", filepath.Join(dest, "GEMINI.md"))
	mustSymlink(t, "shared/gemini", filepath.Join(dest, ".gemini"))

	err := Write(dest, []adapter.File{
		{Path: "GEMINI.md", Op: adapter.OpInject, Key: "style/house", Content: []byte("house rules")},
		{Path: ".gemini/settings.json", Op: adapter.OpMergeJSON, Content: []byte(`{"mcpServers":{"x":{"command":"y"}}}`)},
	})
	if err != nil {
		t.Fatalf("Write through in-destination links: %v", err)
	}
	agents, _ := os.ReadFile(filepath.Join(dest, "AGENTS.md"))
	if !strings.Contains(string(agents), "operator") || !strings.Contains(string(agents), "house rules") {
		t.Errorf("AGENTS.md = %q, want the operator content and the injected block", agents)
	}
	settings, _ := os.ReadFile(filepath.Join(dest, "shared", "gemini", "settings.json"))
	if !strings.Contains(string(settings), "theme") || !strings.Contains(string(settings), "mcpServers") {
		t.Errorf("settings.json = %q, want both the operator key and the merged entry", settings)
	}
	for _, rel := range []string{"GEMINI.md", ".gemini"} {
		if info, err := os.Lstat(filepath.Join(dest, rel)); err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("%s must stay a link; lstat = (%v, %v)", rel, info, err)
		}
	}
}

// Spec: §6.6, §7.8 — the writer refuses a path under a .podium directory that is a
// link, even one that resolves inside the destination.
func TestWrite_PodiumDirectoryLinkRefused(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(dest, "elsewhere"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustSymlink(t, "elsewhere", filepath.Join(dest, ".podium"))
	err := Write(dest, []adapter.File{{Path: ".podium/resources/a/run.sh", Content: []byte("x")}})
	if err == nil || !strings.Contains(err.Error(), ".podium is a symbolic link") {
		t.Errorf("Write under a .podium link = %v, want the .podium link refused", err)
	}
	wantGone(t, filepath.Join(dest, "elsewhere", "resources"))
}

// faultFS is a stageFS whose Lstat or Remove fails on demand, and whose
// OpenFile can hand back a read-only file so the staged write fails.
type faultFS struct {
	lstatErr  error
	removeErr error
	readOnly  bool
}

func (f faultFS) Lstat(name string) (fs.FileInfo, error) {
	if f.lstatErr != nil {
		return nil, f.lstatErr
	}
	return os.Lstat(name)
}

func (f faultFS) Remove(name string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	return os.Remove(name)
}

func (f faultFS) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	if f.readOnly {
		if err := os.WriteFile(name, nil, perm); err != nil {
			return nil, err
		}
		return os.Open(name)
	}
	return os.OpenFile(name, flag, perm)
}

// Spec: §6.6 — a staging write that cannot inspect, clear, create, or fill
// its temporary fails with an error naming the step, and a temporary that
// fails part-way is removed.
func TestStage_Failures(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	boom := errors.New("boom")
	tmp := filepath.Join(dir, "a.tmp")

	if err := stage(faultFS{lstatErr: boom}, tmp, nil, 0o644); !errors.Is(err, boom) || !strings.Contains(err.Error(), "inspect") {
		t.Errorf("stage with a failing Lstat = %v, want the inspect error", err)
	}
	confinedFixture(t, dir, map[string]string{"a.tmp": "leftover"})
	if err := stage(faultFS{removeErr: boom}, tmp, nil, 0o644); !errors.Is(err, boom) || !strings.Contains(err.Error(), "remove leftover") {
		t.Errorf("stage with a failing Remove = %v, want the remove error", err)
	}
	if err := stage(hostFS{}, filepath.Join(dir, "missing", "b.tmp"), nil, 0o644); err == nil || !strings.Contains(err.Error(), "create") {
		t.Errorf("stage into a missing directory = %v, want the create error", err)
	}
	ro := filepath.Join(dir, "c.tmp")
	if err := stage(faultFS{readOnly: true}, ro, []byte("x"), 0o644); err == nil || !strings.Contains(err.Error(), "write") {
		t.Errorf("stage into a read-only file = %v, want the write error", err)
	}
	wantGone(t, ro)
}

// Spec: §7.5.3 — a state write refuses a directory component that is a
// regular file, and reports one it cannot inspect.
func TestWriteStateFile_DirectoryComponentFailures(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "target")
	confinedFixture(t, dest, map[string]string{"file": "x"})
	if err := WriteStateFile(dest, "file/sync.lock", nil, 0o644); !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("WriteStateFile below a regular file = %v, want ErrUnconfinedPath", err)
	}
	if err := WriteStateFile(filepath.Join(dest, "file", "target"), ".podium/sync.lock", nil, 0o644); err == nil {
		t.Errorf("WriteStateFile into a destination below a regular file must fail")
	}
	if err := os.MkdirAll(filepath.Join(dest, ".podium", "sync.lock"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := WriteStateFile(dest, ".podium/sync.lock", nil, 0o644); err == nil || !strings.Contains(err.Error(), "rename") {
		t.Errorf("WriteStateFile over a directory = %v, want the rename error", err)
	}
	wantGone(t, filepath.Join(dest, ".podium", "sync.lock.tmp"))
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory, so the inspect error cannot occur")
	}
	locked := filepath.Join(dest, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := WriteStateFile(dest, "locked/sub/sync.lock", nil, 0o644); !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("WriteStateFile below an unreadable directory = %v, want ErrUnconfinedPath", err)
	}
}

// Spec: §6.7 — a merge target the writer cannot read fails the write with an
// error naming the read.
func TestWrite_MergeTargetUnreadable(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file, so the read error cannot occur")
	}
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{"settings.json": "{}"})
	if err := os.Chmod(filepath.Join(dest, "settings.json"), 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	err := Write(dest, []adapter.File{{Path: "settings.json", Op: adapter.OpMergeJSON, Content: []byte(`{"a":1}`)}})
	if err == nil || !strings.Contains(err.Error(), "for merge") {
		t.Errorf("Write into an unreadable merge target = %v, want the read error", err)
	}
}
