package sync

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	})
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
	if err := EnsureGitignoreEntries(ws, entries); err == nil {
		t.Errorf("EnsureGitignoreEntries through an outside link must fail")
	}
	if got, _ := os.ReadFile(outside); string(got) != "outside\n" {
		t.Errorf("the outside file changed: %q", got)
	}
}
