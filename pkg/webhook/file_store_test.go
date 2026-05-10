package webhook_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/webhook"
)

// Spec: §7.3.2 — file-backed receiver store persists Put across
// reopens and List filters by tenant.
func TestFileStore_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "webhooks.json")
	s, err := webhook.LoadFileStore(path)
	if err != nil {
		t.Fatalf("LoadFileStore: %v", err)
	}
	rec := webhook.Receiver{
		ID: "alpha", TenantID: "default", URL: "https://example/hook",
		Secret: "shh", EventFilter: []string{"manifest.upserted"},
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	s2, err := webhook.LoadFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := s2.Get(context.Background(), "default", "alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.URL != rec.URL || got.Secret != rec.Secret {
		t.Errorf("got %+v, want %+v", got, rec)
	}
	list, err := s2.List(context.Background(), "default")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "alpha" {
		t.Errorf("list = %+v, want [alpha]", list)
	}
}

// Spec: §7.3.2 — Delete removes the receiver and persists.
func TestFileStore_DeletePersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "webhooks.json")
	s, _ := webhook.LoadFileStore(path)
	_ = s.Put(context.Background(), webhook.Receiver{ID: "x", TenantID: "default"})
	if err := s.Delete(context.Background(), "default", "x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	s2, _ := webhook.LoadFileStore(path)
	if _, err := s2.Get(context.Background(), "default", "x"); err == nil {
		t.Errorf("Get after Delete: want not_found")
	}
}

// Spec: §7.3.2 — List filters by tenant.
func TestFileStore_ListByTenant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "webhooks.json")
	s, _ := webhook.LoadFileStore(path)
	_ = s.Put(context.Background(), webhook.Receiver{ID: "a", TenantID: "default"})
	_ = s.Put(context.Background(), webhook.Receiver{ID: "b", TenantID: "default"})
	_ = s.Put(context.Background(), webhook.Receiver{ID: "c", TenantID: "other"})
	got, _ := s.List(context.Background(), "default")
	if len(got) != 2 {
		t.Errorf("list len = %d, want 2", len(got))
	}
	other, _ := s.List(context.Background(), "other")
	if len(other) != 1 || other[0].ID != "c" {
		t.Errorf("other tenant list = %+v", other)
	}
}
