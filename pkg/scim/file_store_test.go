package scim_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lennylabs/podium/pkg/scim"
)

// Spec: §6.3.1 — file-backed SCIM store persists CreateUser /
// CreateGroup / replace / delete across reopens.
func TestFileStore_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scim.json")
	s1, err := scim.LoadFileStore(path)
	if err != nil {
		t.Fatalf("LoadFileStore: %v", err)
	}
	user, err := s1.CreateUser(context.Background(), scim.User{
		ID: "u-1", UserName: "alice", Email: "alice@example",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := s1.CreateGroup(context.Background(), scim.Group{
		ID: "g-1", DisplayName: "engineering", MemberIDs: []string{user.ID},
	}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	s2, err := scim.LoadFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	gotUser, err := s2.GetUser(context.Background(), "u-1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if gotUser.UserName != "alice" {
		t.Errorf("UserName = %q, want alice", gotUser.UserName)
	}
	gotGroup, err := s2.GetGroup(context.Background(), "g-1")
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if gotGroup.DisplayName != "engineering" {
		t.Errorf("DisplayName = %q, want engineering", gotGroup.DisplayName)
	}
	if len(gotGroup.MemberIDs) != 1 || gotGroup.MemberIDs[0] != "u-1" {
		t.Errorf("MemberIDs = %+v, want [u-1]", gotGroup.MemberIDs)
	}
}

// Spec: §6.3.1 — MembersOf still resolves correctly after a
// reopen so the visibility evaluator's group expansion doesn't
// silently break across server restarts.
func TestFileStore_MembersOfAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scim.json")
	s1, _ := scim.LoadFileStore(path)
	_, _ = s1.CreateUser(context.Background(), scim.User{ID: "u-1", UserName: "alice"})
	_, _ = s1.CreateUser(context.Background(), scim.User{ID: "u-2", UserName: "bob"})
	_, _ = s1.CreateGroup(context.Background(), scim.Group{
		ID: "g-1", DisplayName: "engineering",
		MemberIDs: []string{"u-1", "u-2"},
	})
	s2, _ := scim.LoadFileStore(path)
	got, err := s2.MembersOf(context.Background(), "engineering")
	if err != nil {
		t.Fatalf("MembersOf: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %v, want 2 members", got)
	}
}

// Spec: §6.3.1 — Delete persists.
func TestFileStore_DeleteUserPersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scim.json")
	s1, _ := scim.LoadFileStore(path)
	_, _ = s1.CreateUser(context.Background(), scim.User{ID: "u-1", UserName: "alice"})
	if err := s1.DeleteUser(context.Background(), "u-1"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	s2, _ := scim.LoadFileStore(path)
	if _, err := s2.GetUser(context.Background(), "u-1"); err == nil {
		t.Errorf("Get after Delete: want not_found")
	}
}

// loadCase is one TestLoadFileStore_UsableAndUnusable row. setup prepares a
// fresh directory and returns the store path to load. An empty wantErr marks
// a usable store.
type loadCase struct {
	name         string
	setup        func(t *testing.T, dir string) string
	wantErr      string
	wantENOTDIR  bool
	needsNonRoot bool
}

// loadFileStoreCases covers each clause of the §13.12 "unusable" definition
// for PODIUM_SCIM_STORE_PATH and each store that loads as an empty directory.
func loadFileStoreCases() []loadCase {
	return []loadCase{
		{
			name:  "missing file in existing dir loads empty",
			setup: func(_ *testing.T, dir string) string { return filepath.Join(dir, "scim.json") },
		},
		{
			name:  "missing parent dir is created",
			setup: func(_ *testing.T, dir string) string { return filepath.Join(dir, "a", "b", "scim.json") },
		},
		{
			name: "empty file loads empty",
			setup: func(t *testing.T, dir string) string {
				return writeFile(t, filepath.Join(dir, "scim.json"), "")
			},
		},
		{
			name: "malformed JSON is a parse error",
			setup: func(t *testing.T, dir string) string {
				return writeFile(t, filepath.Join(dir, "scim.json"), "{not json")
			},
			wantErr: "scim: parse",
		},
		{
			name: "path is a directory is a read error",
			setup: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "scim.json")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return path
			},
			wantErr: "scim: read",
		},
		{
			name: "regular-file parent is a read error",
			setup: func(t *testing.T, dir string) string {
				parent := writeFile(t, filepath.Join(dir, "parent"), "x")
				return filepath.Join(parent, "scim.json")
			},
			wantErr:     "scim: read",
			wantENOTDIR: true,
		},
		{
			// A dangling symlink reads as not-exist, so load succeeds and
			// MkdirAll fails on the existing non-directory entry. The case
			// does not depend on permission bits and runs as root too.
			name: "dangling-symlink parent is a prepare error",
			setup: func(t *testing.T, dir string) string {
				link := filepath.Join(dir, "link")
				if err := os.Symlink(filepath.Join(dir, "absent"), link); err != nil {
					t.Fatalf("symlink: %v", err)
				}
				return filepath.Join(link, "scim.json")
			},
			wantErr: "scim: prepare",
		},
		{
			name: "read-only parent dir is a probe error",
			setup: func(t *testing.T, dir string) string {
				ro := filepath.Join(dir, "ro")
				if err := os.Mkdir(ro, 0o500); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				// Restore the mode so t.TempDir cleanup can remove the tree.
				t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
				return filepath.Join(ro, "scim.json")
			},
			wantErr:      "scim: probe",
			needsNonRoot: true,
		},
	}
}

// Spec: §6.3.1, §13.12 — LoadFileStore refuses a store the registry cannot
// read, parse, or write, and loads a missing or empty file as an empty
// directory without leaving a probe file behind.
func TestLoadFileStore_UsableAndUnusable(t *testing.T) {
	t.Parallel()
	for _, tc := range loadFileStoreCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.needsNonRoot && os.Geteuid() == 0 {
				t.Skip("root bypasses directory permission bits")
			}
			path := tc.setup(t, t.TempDir())
			s, err := scim.LoadFileStore(path)
			if tc.wantErr != "" {
				assertLoadError(t, err, tc)
				return
			}
			if err != nil {
				t.Fatalf("LoadFileStore: %v", err)
			}
			assertUsableStore(t, s, path)
		})
	}
}

// Spec: §6.3.1, §13.12 — the writability probe leaves save() intact: a store
// whose directory the load created persists a CreateUser across a reopen.
func TestLoadFileStore_CreatedDirPersists(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "scim.json")
	s1, err := scim.LoadFileStore(path)
	if err != nil {
		t.Fatalf("LoadFileStore: %v", err)
	}
	if _, err := s1.CreateUser(context.Background(), scim.User{ID: "u-1", UserName: "alice"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	s2, err := scim.LoadFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, err := s2.GetUser(context.Background(), "u-1"); err != nil || got.UserName != "alice" {
		t.Fatalf("GetUser after reopen = %+v, %v; want alice", got, err)
	}
	assertNoProbeFiles(t, filepath.Dir(path))
}

func assertLoadError(t *testing.T, err error, tc loadCase) {
	t.Helper()
	if err == nil {
		t.Fatalf("LoadFileStore: want error containing %q, got nil", tc.wantErr)
	}
	if !strings.Contains(err.Error(), tc.wantErr) {
		t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
	}
	if tc.wantENOTDIR && !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("error = %v, want errors.Is ENOTDIR", err)
	}
}

func assertUsableStore(t *testing.T, s *scim.FileStore, path string) {
	t.Helper()
	users, err := s.ListUsers(context.Background(), scim.Filter{})
	if err != nil || len(users) != 0 {
		t.Fatalf("ListUsers = %v, %v; want an empty directory", users, err)
	}
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("parent dir %s missing after load: %v", dir, err)
	}
	assertNoProbeFiles(t, dir)
}

func assertNoProbeFiles(t *testing.T, dir string) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(dir, ".scim-probe-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("probe files left behind: %v", left)
	}
}

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
