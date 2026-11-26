package sync

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/materialize"
)

// podiumLinkTarget returns a target whose .podium is a symbolic link to a
// directory beside it, and that directory.
func podiumLinkTarget(t *testing.T) (target, elsewhere string) {
	t.Helper()
	base := t.TempDir()
	target = filepath.Join(base, "ws")
	elsewhere = filepath.Join(base, "shared-podium")
	for _, d := range []string{target, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	if err := os.Symlink(elsewhere, filepath.Join(target, ".podium")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return target, elsewhere
}

// wantStateDirError asserts err reports the unusable .podium of target.
func wantStateDirError(t *testing.T, what string, err error, target string) {
	t.Helper()
	if !errors.Is(err, ErrStateDirUnusable) {
		t.Errorf("%s = %v, want ErrStateDirUnusable", what, err)
		return
	}
	if !strings.Contains(err.Error(), filepath.Join(target, ".podium")) || !strings.Contains(err.Error(), "replace it with a directory") {
		t.Errorf("%s error %q must name %s and the remedy", what, err, filepath.Join(target, ".podium"))
	}
	if !errors.Is(err, materialize.ErrPodiumNotDirectory) || !errors.Is(err, materialize.ErrUnconfinedPath) {
		t.Errorf("%s = %v, want it to match the materialize refusal it reports", what, err)
	}
	if n := strings.Count(err.Error(), "symbolic link or not a directory"); n != 1 || strings.Contains(err.Error(), "materialize:") {
		t.Errorf("%s error %q must state the refusal once and carry no nested materialize error", what, err)
	}
}

// Spec: §7.5.3 — a workspace whose .podium is a symbolic link or not a
// directory fails the sync with an error that names .podium and the remedy,
// before anything is written, instead of skipping the lock and orphaning
// later stale output. podium sync override, save-as, and profile edit fail
// the same way, and nothing lands in the directory the link names.
func TestRun_PodiumNotDirectoryFailsRun(t *testing.T) {
	t.Parallel()
	registry := agentRegistry(t, "team/glossary")
	t.Run("link", func(t *testing.T) {
		t.Parallel()
		target, elsewhere := podiumLinkTarget(t)
		_, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "none"})
		wantStateDirError(t, "Run", err, target)
		if _, serr := os.Stat(filepath.Join(target, "team", "glossary", "ARTIFACT.md")); !errors.Is(serr, fs.ErrNotExist) {
			t.Errorf("the failed run wrote artifact output; stat err = %v", serr)
		}
		_, err = Override(OverrideOptions{Target: target, Add: []string{"team/glossary"}})
		wantStateDirError(t, "Override", err, target)
		_, err = SaveAs(SaveAsOptions{Target: target, Profile: "p"})
		wantStateDirError(t, "SaveAs", err, target)
		_, err = ProfileEdit(ProfileEditOptions{Target: target, Profile: "p", AddInclude: []string{"x/**"}})
		wantStateDirError(t, "ProfileEdit", err, target)
		err = WriteWorkspaceConfig(target, "sync.yaml", []byte("defaults: {}\n"))
		wantStateDirError(t, "WriteWorkspaceConfig", err, target)
		wantStateDirError(t, "CheckWorkspaceConfig", CheckWorkspaceConfig(target, "sync.yaml"), target)
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("files landed behind the .podium link: %v", entries)
		}
	})
	t.Run("regular file", func(t *testing.T) {
		t.Parallel()
		target := t.TempDir()
		writeFile(t, filepath.Join(target, ".podium"), "not a directory\n")
		_, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "none"})
		wantStateDirError(t, "Run", err, target)
		wantStateDirError(t, "CheckWorkspaceConfig", CheckWorkspaceConfig(target, "sync.local.yaml"), target)
	})
}

// Spec: §7.5.3, §7.7 — CheckWorkspaceConfig passes a workspace whose config
// WriteWorkspaceConfig can write, an existing sync.yaml included, writes
// nothing, and reports a refused temporary unchanged because it is not an
// unusable .podium.
func TestCheckWorkspaceConfig(t *testing.T) {
	t.Parallel()
	fresh := t.TempDir()
	if err := CheckWorkspaceConfig(fresh, "sync.yaml"); err != nil {
		t.Errorf("CheckWorkspaceConfig on a fresh workspace = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(fresh, ".podium")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CheckWorkspaceConfig created .podium; stat err = %v", err)
	}
	writeFile(t, filepath.Join(fresh, ".podium", "sync.yaml"), "defaults: {}\n")
	if err := CheckWorkspaceConfig(fresh, "sync.yaml"); err != nil {
		t.Errorf("CheckWorkspaceConfig over an existing sync.yaml = %v, want nil", err)
	}
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "ws", ".podium"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(filepath.Join(base, "victim"), filepath.Join(base, "ws", ".podium", "sync.yaml.tmp")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := CheckWorkspaceConfig(filepath.Join(base, "ws"), "sync.yaml")
	if !errors.Is(err, materialize.ErrUnconfinedPath) || errors.Is(err, ErrStateDirUnusable) {
		t.Errorf("CheckWorkspaceConfig over a temporary link = %v, want ErrUnconfinedPath without ErrStateDirUnusable", err)
	}
}

// Spec: §7.5.3, §7.7 — a directory at the config file's name fails
// CheckWorkspaceConfig with an error that names the directory by its path
// below the workspace, as the unusable-.podium error names .podium, states
// the remedy once, and matches the materialize refusal it reports.
func TestCheckWorkspaceConfig_ConfigDirectory(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".podium", "sync.local.yaml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	err := CheckWorkspaceConfig(ws, "sync.local.yaml")
	if !errors.Is(err, materialize.ErrStateFileIsDirectory) || !errors.Is(err, materialize.ErrUnconfinedPath) || errors.Is(err, ErrStateDirUnusable) {
		t.Fatalf("CheckWorkspaceConfig over a config directory = %v, want ErrStateFileIsDirectory and ErrUnconfinedPath", err)
	}
	if want := "sync: " + dir + " is a directory, which the write cannot replace; remove it"; err.Error() != want {
		t.Errorf("CheckWorkspaceConfig error = %q, want %q", err, want)
	}
}

// Spec: §7.5.3 — save-as and profile edit write sync.yaml through an
// exclusively created temporary: a committed .podium/sync.yaml.tmp link to a
// file outside the workspace refuses the write and leaves that file as it
// was.
func TestConfigWrites_CommittedTempLinkRefused(t *testing.T) {
	t.Parallel()
	for name, write := range map[string]func(target string) error{
		"save-as": func(target string) error {
			if err := WriteLock(target, &LockFile{Harness: "claude-code"}); err != nil {
				return err
			}
			_, err := SaveAs(SaveAsOptions{Target: target, Profile: "p"})
			return err
		},
		"profile edit": func(target string) error {
			_, err := ProfileEdit(ProfileEditOptions{Target: target, Profile: "p", AddInclude: []string{"x/**"}})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			target := filepath.Join(base, "ws")
			victim := filepath.Join(base, "victim.txt")
			writeFile(t, victim, "VICTIM\n")
			if err := os.MkdirAll(filepath.Join(target, ".podium"), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.Symlink(victim, filepath.Join(target, ".podium", "sync.yaml.tmp")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := write(target); err == nil {
				t.Errorf("%s over a committed temporary link must fail", name)
			}
			if got, _ := os.ReadFile(victim); string(got) != "VICTIM\n" {
				t.Errorf("the file outside the workspace was overwritten: %q", got)
			}
		})
	}
}

// Spec: §7.5.3 — profile edit does not read a sync.yaml link that resolves
// outside the workspace, so the outside file's content is never copied into
// the workspace's sync.yaml.
func TestProfileEdit_OutsideConfigLinkNotRead(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "ws")
	secret := filepath.Join(base, "secret.yaml")
	writeFile(t, secret, "token: SECRET-123\n")
	if err := os.MkdirAll(filepath.Join(target, ".podium"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(target, ".podium", "sync.yaml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ProfileEdit(ProfileEditOptions{Target: target, Profile: "p", AddInclude: []string{"x/**"}}); err == nil {
		t.Errorf("profile edit through an outside sync.yaml link must fail")
	}
	if got, _ := os.ReadFile(secret); string(got) != "token: SECRET-123\n" {
		t.Errorf("the outside file changed: %q", got)
	}
}

// Spec: §7.7 — init's .gitignore update creates the file, appends only the
// missing entries, updates a .gitignore linked to a file inside the
// workspace through the link, and refuses a link that leaves the workspace.
func TestEnsureGitignoreEntries(t *testing.T) {
	t.Parallel()
	entries := []string{".podium/sync.local.yaml", ".podium/overlay/"}

	fresh := t.TempDir()
	if err := EnsureGitignoreEntries(fresh, entries); err != nil {
		t.Fatalf("EnsureGitignoreEntries into a fresh workspace: %v", err)
	}
	if got := string(readTargetFile(t, fresh, ".gitignore")); got != ".podium/sync.local.yaml\n.podium/overlay/\n" {
		t.Errorf(".gitignore = %q, want both entries", got)
	}

	linked := t.TempDir()
	writeFile(t, filepath.Join(linked, "shared", "ignore"), "node_modules/\n.podium/overlay/")
	if err := os.Symlink("shared/ignore", filepath.Join(linked, ".gitignore")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := EnsureGitignoreEntries(linked, entries); err != nil {
		t.Fatalf("EnsureGitignoreEntries through an in-workspace link: %v", err)
	}
	if got := string(readTargetFile(t, linked, "shared/ignore")); got != "node_modules/\n.podium/overlay/\n.podium/sync.local.yaml\n" {
		t.Errorf("linked .gitignore = %q, want only the missing entry appended", got)
	}
	if info, err := os.Lstat(filepath.Join(linked, ".gitignore")); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf(".gitignore must stay a link; lstat = (%v, %v)", info, err)
	}

	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	writeFile(t, outside, "outside\n")
	ws := filepath.Join(base, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, ".gitignore")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := EnsureGitignoreEntries(ws, entries); !errors.Is(err, ErrGitignoreLinkSkipped) {
		t.Errorf("EnsureGitignoreEntries through an outside link = %v, want ErrGitignoreLinkSkipped", err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "outside\n" {
		t.Errorf("the outside file changed: %q", got)
	}
}

// Spec: §7.7 — a .gitignore that is a dangling symbolic link, relative or
// absolute, is skipped with ErrGitignoreLinkSkipped and stays a link: init
// neither replaces the link with a regular file nor creates the file it
// names.
func TestEnsureGitignoreEntries_DanglingLinkSkipped(t *testing.T) {
	t.Parallel()
	for name, dest := range map[string]string{
		"relative": "shared/missing",
		"absolute": filepath.Join(t.TempDir(), "nonexistent-dir", "x"),
	} {
		ws := t.TempDir()
		link := filepath.Join(ws, ".gitignore")
		if err := os.Symlink(dest, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := EnsureGitignoreEntries(ws, []string{".podium/overlay/"}); !errors.Is(err, ErrGitignoreLinkSkipped) {
			t.Errorf("%s: EnsureGitignoreEntries = %v, want ErrGitignoreLinkSkipped", name, err)
		}
		if info, err := os.Lstat(link); err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("%s: .gitignore must stay a link; lstat = (%v, %v)", name, info, err)
		}
		if _, err := os.Stat(link); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: the file the link names must not be created; stat err = %v", name, err)
		}
	}
}

// Spec: §7.5.3 — save-as does not read a sync.yaml link that resolves outside
// the workspace, so the outside file's content is never copied into the
// workspace's sync.yaml: the command fails, the link stays, and the outside
// file is unchanged.
func TestSaveAs_OutsideConfigLinkNotRead(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "ws")
	outside := filepath.Join(base, "outside-sync.yaml")
	const secret = "defaults:\n  registry: https://secret.example/registry\n  identity: SECRET-ID\n"
	writeFile(t, outside, secret)
	if err := WriteLock(target, &LockFile{Harness: "claude-code"}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	link := filepath.Join(target, ".podium", "sync.yaml")
	if err := os.Symlink("../../outside-sync.yaml", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := SaveAs(SaveAsOptions{Target: target, Profile: "p1"}); err == nil {
		t.Errorf("save-as through an outside sync.yaml link must fail")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("sync.yaml must stay the link; lstat = (%v, %v)", info, err)
	}
	if got, _ := os.ReadFile(outside); string(got) != secret {
		t.Errorf("the outside file changed: %q", got)
	}
}

// Spec: §7.5.3 — a lock that is a link to another regular file inside
// .podium/ is read, so a scope change still removes the output the prior run
// wrote; a lock the run cannot read is named on standard error.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_LockLinkInsidePodiumRead(t *testing.T) {
	registry := agentRegistry(t, "personal/greet", "finance/invoice")
	target := t.TempDir()
	opts := Options{RegistryPath: registry, Target: target, AdapterID: "none"}
	syncChanged(t, opts)
	lock := filepath.Join(target, ".podium", "sync.lock")
	if err := os.Rename(lock, filepath.Join(target, ".podium", "real.lock")); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := os.Symlink("real.lock", lock); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	opts.Scope = ScopeFilter{Exclude: []string{"finance/**"}}
	stderr := captureStderr(t, func() { syncChanged(t, opts) })
	if strings.Contains(stderr, "cannot read") {
		t.Errorf("stderr = %q, want the in-.podium lock link read", stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "finance", "invoice", "ARTIFACT.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the excluded artifact's output must be removed; stat err = %v", err)
	}
}

// Spec: §7.5.3 — a lock the run cannot read, such as a lock link that
// resolves outside the target, is named on standard error, and the run
// proceeds as if no prior lock existed.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_UnreadableLockWarns(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "ws")
	writeFile(t, filepath.Join(base, "outside.lock"), "version: 1\n")
	if err := os.MkdirAll(filepath.Join(target, ".podium"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(filepath.Join(base, "outside.lock"), filepath.Join(target, ".podium", "sync.lock")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	opts := Options{RegistryPath: agentRegistry(t, "personal/greet"), Target: target, AdapterID: "none"}
	stderr := captureStderr(t, func() { syncChanged(t, opts) })
	if !strings.Contains(stderr, "cannot read "+LockFilePath(target)) || !strings.Contains(stderr, "as if no prior lock existed") {
		t.Errorf("stderr = %q, want the unreadable lock named", stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(base, "outside.lock")); string(got) != "version: 1\n" {
		t.Errorf("the outside lock changed: %q", got)
	}
}

// podiumLinkingAdapter is a stub HarnessAdapter that replaces the target's
// .podium with a symbolic link while it adapts, which happens after the run
// read the prior lock and before it writes the next one.
type podiumLinkingAdapter struct {
	target, elsewhere string
}

func (podiumLinkingAdapter) ID() string { return "podium-linking" }

func (a podiumLinkingAdapter) Adapt(_ context.Context, src adapter.Source) ([]adapter.File, error) {
	link := filepath.Join(a.target, ".podium")
	if _, err := os.Lstat(link); errors.Is(err, fs.ErrNotExist) {
		if err := os.Symlink(a.elsewhere, link); err != nil {
			return nil, err
		}
	}
	return []adapter.File{{Path: "out/" + src.ArtifactID + ".txt", Content: []byte("x\n")}}, nil
}

// Spec: §7.5.3 — a .podium replaced by a symbolic link while the run
// materializes fails the run at the lock write with ErrStateDirUnusable,
// rather than completing without a lock.
func TestRun_PodiumLinkedDuringRunFailsLockWrite(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "ws")
	elsewhere := filepath.Join(base, "elsewhere")
	for _, d := range []string{target, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	reg := adapter.NewRegistry()
	if err := reg.Register(podiumLinkingAdapter{target: target, elsewhere: elsewhere}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err := Run(Options{RegistryPath: agentRegistry(t, "team/glossary"), Target: target, AdapterID: "podium-linking", AdapterRegistry: reg})
	wantStateDirError(t, "Run", err, target)
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("the lock landed behind the .podium link: %v", entries)
	}
}
