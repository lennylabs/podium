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
	t.Run("lock link into git", func(t *testing.T) {
		dest := filepath.Join(base, "git-lock-link")
		confinedFixture(t, dest, map[string]string{".git/config": "[core]\n"})
		mustSymlink(t, "../.git/config", filepath.Join(dest, ".podium", "sync.lock"))
		if _, err := ReadStateFile(dest, ".podium/sync.lock"); !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("ReadStateFile of a lock link into .git = %v, want ErrUnconfinedPath", err)
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
// outside the destination, into .git, or into the sync state is not read
// through: a committed link to an outside JSON file cannot fold that file's
// content into the rendered file the publish step commits. A dangling link
// whose named directory is absent, lies outside the destination, or lies in
// .git, and a dangling link that names another link, are refused as well.
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
	mustSymlink(t, "missing-dir/absent.json", filepath.Join(dest, "settings.json"))
	mustSymlink(t, filepath.Join(base, "absent.json"), filepath.Join(dest, "outside.json"))
	mustSymlink(t, ".git/hooks-new.toml", filepath.Join(dest, "git.toml"))
	mustSymlink(t, "next.json", filepath.Join(dest, "chain.json"))
	mustSymlink(t, "absent.json", filepath.Join(dest, "next.json"))

	for _, f := range []adapter.File{
		{Path: ".claude-plugin/marketplace.json", Op: adapter.OpMergeJSON, Content: []byte(`{"name":"acme"}`)},
		{Path: "AGENTS.md", Op: adapter.OpInject, Key: "a", Content: []byte("block")},
		{Path: "GEMINI.md", Op: adapter.OpInject, Key: "a", Content: []byte("block")},
		{Path: "CLAUDE.md", Op: adapter.OpInject, Key: "a", Content: []byte("block")},
		{Path: "settings.json", Op: adapter.OpMergeJSON, Content: []byte(`{"a":1}`)},
		{Path: "outside.json", Op: adapter.OpMergeJSON, Content: []byte(`{"a":1}`)},
		{Path: "git.toml", Op: adapter.OpInject, Key: "a", Content: []byte("block")},
		{Path: "chain.json", Op: adapter.OpMergeJSON, Content: []byte(`{"a":1}`)},
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
	for _, gone := range []string{filepath.Join(dest, "missing-dir"), filepath.Join(base, "absent.json"), filepath.Join(dest, ".git", "hooks-new.toml"), filepath.Join(dest, "absent.json")} {
		wantGone(t, gone)
	}
}

// Spec: §6.6 — a merge or inject target that is a link naming an absent file
// inside the destination, such as GEMINI.md linked to AGENTS.md before any
// run created AGENTS.md, is followed: the write creates the file the link
// names and the link stays.
func TestWrite_MergeTargetDanglingLinkInsideCreated(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(dest, "shared"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustSymlink(t, "AGENTS.md", filepath.Join(dest, "GEMINI.md"))
	mustSymlink(t, "../shared/settings.json", filepath.Join(dest, ".gemini", "settings.json"))
	err := Write(dest, []adapter.File{
		{Path: "GEMINI.md", Op: adapter.OpInject, Key: "style/house", Content: []byte("house rules")},
		{Path: ".gemini/settings.json", Op: adapter.OpMergeJSON, Content: []byte(`{"a":1}`)},
	})
	if err != nil {
		t.Fatalf("Write through dangling in-destination links: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "AGENTS.md")); !strings.Contains(string(got), "house rules") {
		t.Errorf("AGENTS.md = %q, want the injected block", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "shared", "settings.json")); !strings.Contains(string(got), `"a"`) {
		t.Errorf("shared/settings.json = %q, want the merged entry", got)
	}
	for _, rel := range []string{"GEMINI.md", ".gemini/settings.json"} {
		if info, err := os.Lstat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("%s must stay a link; lstat = (%v, %v)", rel, info, err)
		}
	}
}

// Spec: §6.6 — a dangling link with a relative target names a file relative
// to the directory the link sits in, as the kernel resolves it, also when a
// directory link leads to that directory. The write creates sub/AGENTS.md
// for docs/GEMINI.md -> ../AGENTS.md under docs -> sub/deep, and a second
// write updates the same file through the link.
func TestWrite_DanglingRelativeLinkThroughDirectoryLink(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(filepath.Join(dest, "sub", "deep"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustSymlink(t, "sub/deep", filepath.Join(dest, "docs"))
	mustSymlink(t, "../AGENTS.md", filepath.Join(dest, "sub", "deep", "GEMINI.md"))
	for i, content := range []string{"first rules", "second rules"} {
		err := Write(dest, []adapter.File{
			{Path: "docs/GEMINI.md", Op: adapter.OpInject, Key: "style/house", Content: []byte(content)},
		})
		if err != nil {
			t.Fatalf("Write %d through a relative dangling link: %v", i+1, err)
		}
		got, err := os.ReadFile(filepath.Join(dest, "docs", "GEMINI.md"))
		if err != nil || !strings.Contains(string(got), content) {
			t.Fatalf("Write %d: docs/GEMINI.md = (%q, %v), want the injected block", i+1, got, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dest, "AGENTS.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("AGENTS.md at the destination root: lstat err = %v, want not exist", err)
	}
	if info, err := os.Lstat(filepath.Join(dest, "sub", "deep", "GEMINI.md")); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("sub/deep/GEMINI.md must stay a link; lstat = (%v, %v)", info, err)
	}
}

// Spec: §6.6, §7.8 — a directory link inside the destination that resolves
// into .git or into the sync state fails the write before anything is
// written, so a committed skills -> .git/hooks link cannot plant a file in
// .git. A directory link to an ordinary directory inside the destination is
// written through.
func TestWrite_DirectoryLinkIntoProtectedStateRefused(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "checkout")
	confinedFixture(t, dest, map[string]string{".git/HEAD": "ref", ".podium/sync.yaml": "cfg", "shared/keep": "k"})
	if err := os.MkdirAll(filepath.Join(dest, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustSymlink(t, "../../.git/hooks", filepath.Join(dest, "claude", "pack", "skills"))
	mustSymlink(t, ".podium", filepath.Join(dest, "state"))
	mustSymlink(t, "shared", filepath.Join(dest, "plain"))

	for _, f := range []adapter.File{
		{Path: "claude/pack/skills/pre-commit/SKILL.md", Content: []byte("x"), Mode: 0o755},
		{Path: "state/sync.lock", Content: []byte("x")},
	} {
		err := Write(dest, []adapter.File{{Path: "ok.md", Content: []byte("ok")}, f})
		if !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("Write(%s) = %v, want ErrUnconfinedPath", f.Path, err)
		}
	}
	wantGone(t, filepath.Join(dest, ".git", "hooks", "pre-commit"))
	wantGone(t, filepath.Join(dest, ".podium", "sync.lock"))
	wantGone(t, filepath.Join(dest, "ok.md"))

	if err := Write(dest, []adapter.File{{Path: "plain/a.md", Content: []byte("A")}}); err != nil {
		t.Fatalf("Write through an ordinary directory link: %v", err)
	}
	wantContent(t, filepath.Join(dest, "shared", "a.md"), "A")

	mustSymlink(t, "loop", filepath.Join(dest, "loop"))
	if err := Write(dest, []adapter.File{{Path: "loop/a.md", Content: []byte("A")}}); err == nil {
		t.Errorf("Write below a link loop must fail")
	}
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

// Spec: §6.6, §7.5.3 — every written path passes the recorded-path rule, with
// or without a link: a batch that names .git/hooks/post-checkout or
// .podium/sync.lock fails before anything is written, and the bundled
// resource bucket .podium/resources/ stays writable.
func TestWrite_ProtectedLexicalPathsRefused(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, p := range []string{".git/hooks/post-checkout", ".podium/sync.lock", `notes\x.md`} {
		err := Write(dest, []adapter.File{{Path: "ok.md", Content: []byte("ok")}, {Path: p, Content: []byte("x")}})
		if !errors.Is(err, ErrUnconfinedPath) || !errors.Is(err, ErrUnsafeRecordedPath) {
			t.Errorf("Write(%q) = %v, want ErrUnconfinedPath and ErrUnsafeRecordedPath", p, err)
		}
	}
	wantGone(t, filepath.Join(dest, ".git", "hooks", "post-checkout"))
	wantGone(t, filepath.Join(dest, ".podium", "sync.lock"))
	wantGone(t, filepath.Join(dest, "ok.md"))
	if err := Write(dest, []adapter.File{{Path: ".podium/resources/a/run.sh", Content: []byte("x")}}); err != nil {
		t.Errorf("Write into .podium/resources: %v", err)
	}
}

// Spec: §6.6, §6.7 — one merge file reached under two spellings in one batch,
// AGENTS.md and GEMINI.md linked to it, folds into one item, whether the
// destination is named directly or through a link: both blocks land and the
// write succeeds.
func TestWrite_OneMergeFileUnderTwoSpellingsFolds(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	confinedFixture(t, realDir, map[string]string{"AGENTS.md": "operator\n"})
	mustSymlink(t, "AGENTS.md", filepath.Join(realDir, "GEMINI.md"))
	via := filepath.Join(base, "via")
	mustSymlink(t, realDir, via)
	for i, dest := range []string{realDir, via} {
		files := []adapter.File{
			{Path: "AGENTS.md", Op: adapter.OpInject, Key: "a", Content: []byte("rule A")},
			{Path: "GEMINI.md", Op: adapter.OpInject, Key: "b", Content: []byte("rule B")},
		}
		if err := Write(dest, files); err != nil {
			t.Errorf("Write #%d into %s: %v", i, dest, err)
			continue
		}
		got, _ := os.ReadFile(filepath.Join(realDir, "AGENTS.md"))
		for _, want := range []string{"operator", "rule A", "rule B"} {
			if !strings.Contains(string(got), want) {
				t.Errorf("Write #%d: AGENTS.md = %q, want %q", i, got, want)
			}
		}
	}
	wantGone(t, filepath.Join(realDir, "AGENTS.md.tmp"))
}

// Spec: §4.4.1, §6.6 — the sandbox profile is written under the destination's
// .podium directory with the rules of sync's state: a .podium that is a link
// refuses the write and nothing lands where it points, and a committed link
// at the profile's temporary name refuses it as well.
func TestWriteSandboxProfile_CommittedLinksRefused(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkDest := filepath.Join(base, "link-ws")
	mustSymlink(t, outside, filepath.Join(linkDest, ".podium"))
	if _, _, err := WriteSandboxProfile(linkDest, SandboxProfileSeccompStrict); !errors.Is(err, ErrPodiumNotDirectory) {
		t.Errorf("WriteSandboxProfile under a .podium link = %v, want ErrPodiumNotDirectory", err)
	}
	wantGone(t, filepath.Join(outside, SandboxProfileSeccompStrict+".json"))

	victim := filepath.Join(base, "victim.txt")
	confinedFixture(t, base, map[string]string{"victim.txt": "victim\n"})
	tmpDest := filepath.Join(base, "tmp-ws")
	mustSymlink(t, victim, filepath.Join(tmpDest, ".podium", SandboxProfileSeccompStrict+".json.tmp"))
	if _, _, err := WriteSandboxProfile(tmpDest, SandboxProfileSeccompStrict); !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("WriteSandboxProfile over a temporary link = %v, want ErrUnconfinedPath", err)
	}
	wantContent(t, victim, "victim\n")
}

// Spec: §6.6 step 5, §7.5.3 — CheckStateFile reports the refusals
// WriteStateFile would return without writing anything, and passes a path
// WriteStateFile would create or replace.
func TestCheckStateFile(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkDest := filepath.Join(base, "link-ws")
	mustSymlink(t, outside, filepath.Join(linkDest, ".podium"))
	fileDest := filepath.Join(base, "file-ws")
	confinedFixture(t, fileDest, map[string]string{".podium": "x", "sub/file": "x"})
	tmpDest := filepath.Join(base, "tmp-ws")
	mustSymlink(t, filepath.Join(outside, "victim"), filepath.Join(tmpDest, ".podium", "sync.yaml.tmp"))
	leftover := filepath.Join(base, "leftover-ws")
	confinedFixture(t, leftover, map[string]string{".podium/sync.yaml.tmp": "partial", ".podium/sync.yaml": "old"})
	leafDir := filepath.Join(base, "leafdir-ws")
	if err := os.MkdirAll(filepath.Join(leafDir, ".podium", "sync.yaml"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	leafLink := filepath.Join(base, "leaflink-ws")
	mustSymlink(t, filepath.Join(outside, "elsewhere.yaml"), filepath.Join(leafLink, ".podium", "sync.yaml"))

	for _, tc := range []struct {
		name, dest, rel string
		want            []error
	}{
		{"absent destination", filepath.Join(base, "absent"), ".podium/sync.yaml", nil},
		{"absent .podium", t.TempDir(), ".podium/sync.yaml", nil},
		{"leftover regular temporary and existing file", leftover, ".podium/sync.yaml", nil},
		{"link at the file name", leafLink, ".podium/sync.yaml", nil},
		{"directory at the file name", leafDir, ".podium/sync.yaml", []error{ErrUnconfinedPath}},
		{".podium link", linkDest, ".podium/sync.yaml", []error{ErrUnconfinedPath, ErrPodiumNotDirectory}},
		{".podium regular file", fileDest, ".podium/sync.yaml", []error{ErrUnconfinedPath, ErrPodiumNotDirectory}},
		{"other directory a regular file", fileDest, "sub/file/x", []error{ErrUnconfinedPath}},
		{"temporary link", tmpDest, ".podium/sync.yaml", []error{ErrUnconfinedPath}},
		{"escaping path", leftover, "../x", []error{ErrOutOfDestination}},
		{"empty destination", "", ".podium/sync.yaml", []error{ErrEmptyDestination}},
	} {
		err := CheckStateFile(tc.dest, tc.rel)
		if tc.want == nil && err != nil {
			t.Errorf("%s: CheckStateFile = %v, want nil", tc.name, err)
		}
		if tc.want != nil && err == nil {
			t.Errorf("%s: CheckStateFile = nil, want %v", tc.name, tc.want)
		}
		for _, w := range tc.want {
			if !errors.Is(err, w) {
				t.Errorf("%s: CheckStateFile = %v, want it to match %v", tc.name, err, w)
			}
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("CheckStateFile wrote behind the .podium link: %v", entries)
	}
	wantGone(t, filepath.Join(base, "absent"))
	wantContent(t, filepath.Join(leftover, ".podium", "sync.yaml.tmp"), "partial")
}

// Spec: §7.5.3 — CheckStateFile reports a destination it cannot open, a
// directory it cannot inspect, and a temporary name it cannot inspect.
func TestCheckStateFile_InspectFailures(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{"file": "x"})
	if err := CheckStateFile(filepath.Join(dest, "file"), ".podium/sync.yaml"); err == nil || !strings.Contains(err.Error(), "materialize: open") {
		t.Errorf("CheckStateFile on a regular-file destination = %v, want the open error", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory, so the inspect error cannot occur")
	}
	locked := filepath.Join(dest, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := CheckStateFile(dest, "locked/sub/sync.lock"); !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("CheckStateFile below an unreadable directory = %v, want ErrUnconfinedPath", err)
	}
	if err := CheckStateFile(dest, "locked/sync.lock"); err == nil || !strings.Contains(err.Error(), "materialize: inspect") {
		t.Errorf("CheckStateFile with an unreadable temporary name = %v, want the inspect error", err)
	}
}

// Spec: §6.6 step 5, §4.4.1 — CheckSandboxProfile refuses the profile a
// .podium link would carry outside the destination, passes a profile with no
// shipped baseline, and writes nothing.
func TestCheckSandboxProfile(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkDest := filepath.Join(base, "link-ws")
	mustSymlink(t, outside, filepath.Join(linkDest, ".podium"))
	err := CheckSandboxProfile(linkDest, SandboxProfileSeccompStrict)
	if !errors.Is(err, ErrPodiumNotDirectory) || !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("CheckSandboxProfile under a .podium link = %v, want ErrPodiumNotDirectory and ErrUnconfinedPath", err)
	}
	for _, p := range []string{"", "read-only-fs", "unrestricted"} {
		if err := CheckSandboxProfile(linkDest, p); err != nil {
			t.Errorf("CheckSandboxProfile(%q) with no shipped baseline = %v, want nil", p, err)
		}
	}
	dirDest := filepath.Join(base, "dir-ws")
	if err := os.MkdirAll(filepath.Join(dirDest, ".podium", SandboxProfileSeccompStrict+".json"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := CheckSandboxProfile(dirDest, SandboxProfileSeccompStrict); !errors.Is(err, ErrUnconfinedPath) || !errors.Is(err, ErrStateFileIsDirectory) || !strings.Contains(err.Error(), `".podium/seccomp-strict.json" is a directory`) {
		t.Errorf("CheckSandboxProfile over a directory at the profile name = %v, want ErrUnconfinedPath and ErrStateFileIsDirectory naming the directory", err)
	}
	fresh := filepath.Join(base, "fresh")
	if err := CheckSandboxProfile(fresh, SandboxProfileSeccompStrict); err != nil {
		t.Errorf("CheckSandboxProfile into an absent destination = %v, want nil", err)
	}
	wantGone(t, fresh)
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("CheckSandboxProfile wrote behind the .podium link: %v", entries)
	}
}

// Spec: §7.5.3 — a refusal that matches two sentinels names the package
// once: the .podium refusal matches ErrUnconfinedPath and
// ErrPodiumNotDirectory, a recorded-path refusal matches ErrUnconfinedPath
// and ErrUnsafeRecordedPath, and neither message repeats "materialize:".
func TestRefusalMessages_PrefixOnce(t *testing.T) {
	t.Parallel()
	linkDest := filepath.Join(t.TempDir(), "link-ws")
	mustSymlink(t, t.TempDir(), filepath.Join(linkDest, ".podium"))
	podiumErr := WriteStateFile(linkDest, ".podium/sync.lock", nil, 0o644)
	ruleErr := Write(t.TempDir(), []adapter.File{{Path: ".git/hooks/x", Content: []byte("x")}})
	for _, tc := range []struct {
		name string
		err  error
		want []error
		text string
	}{
		{".podium link", podiumErr, []error{ErrUnconfinedPath, ErrPodiumNotDirectory}, `materialize: .podium is a symbolic link or not a directory: ".podium/sync.lock"`},
		{"recorded-path rule", ruleErr, []error{ErrUnconfinedPath, ErrUnsafeRecordedPath}, `materialize: path is not a regular file confined to the destination: ".git/hooks/x": unsafe recorded path: .git segment`},
	} {
		for _, w := range tc.want {
			if !errors.Is(tc.err, w) {
				t.Errorf("%s: %v does not match %v", tc.name, tc.err, w)
			}
		}
		if tc.err == nil || tc.err.Error() != tc.text {
			t.Errorf("%s: message = %v, want %q", tc.name, tc.err, tc.text)
		}
	}
}

// Spec: §6.6, §7.5.3 — a path refused because it resolves, through a link
// inside the destination, to a path the recorded-path rule refuses matches
// ErrUnconfinedPath and ErrUnsafeRecordedPath and names the package once.
// It covers the recorded-path read (resolveIn), the writer's directory-link
// check (checkResolvedTarget), and the merge-target link (resolveMergeTarget).
func TestResolvedRuleRefusals_MatchUnsafeRecordedPath(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{".git/HEAD": "ref", ".git/config": "git"})
	if err := os.MkdirAll(filepath.Join(dest, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustSymlink(t, filepath.Join(dest, ".git"), filepath.Join(dest, "gitlink"))
	mustSymlink(t, filepath.Join(dest, ".git", "hooks"), filepath.Join(dest, "skills"))
	mustSymlink(t, ".git/config", filepath.Join(dest, "GIT.json"))

	_, readErr := ReadRecorded(dest, "gitlink/HEAD")
	dirLinkErr := Write(dest, []adapter.File{{Path: "skills/post-checkout", Content: []byte("x")}})
	mergeErr := Write(dest, []adapter.File{{Path: "GIT.json", Op: adapter.OpMergeJSON, Content: []byte(`{"a":1}`)}})
	for name, err := range map[string]error{
		"resolveIn":           readErr,
		"checkResolvedTarget": dirLinkErr,
		"resolveMergeTarget":  mergeErr,
	} {
		if !errors.Is(err, ErrUnconfinedPath) || !errors.Is(err, ErrUnsafeRecordedPath) {
			t.Errorf("%s: %v, want ErrUnconfinedPath and ErrUnsafeRecordedPath", name, err)
			continue
		}
		if n := strings.Count(err.Error(), "materialize:"); n != 1 || !strings.Contains(err.Error(), "resolves to") {
			t.Errorf("%s: message %q must name the resolved path with one materialize: prefix", name, err)
		}
	}
	wantGone(t, filepath.Join(dest, ".git", "hooks", "post-checkout"))
	wantContent(t, filepath.Join(dest, ".git", "config"), "git")
}
