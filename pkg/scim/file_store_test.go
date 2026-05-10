package scim_test

import (
	"context"
	"path/filepath"
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
