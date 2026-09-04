package webhook_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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

// Spec: §7.3.2 — the per-receiver debounce window round-trips
// through the JSON file. FileStore serializes the whole Receiver, so
// Debounce is additive: a debounced receiver written to disk reloads
// with its window intact.
func TestFileStore_DebouncePersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "webhooks.json")
	s, err := webhook.LoadFileStore(path)
	if err != nil {
		t.Fatalf("LoadFileStore: %v", err)
	}
	rec := webhook.Receiver{
		ID: "ci", TenantID: "default", URL: "https://example/hook",
		Secret: "shh", EventFilter: []string{"layer.ingested"},
		Debounce: 90 * time.Second,
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	s2, err := webhook.LoadFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := s2.Get(context.Background(), "default", "ci")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Debounce != rec.Debounce {
		t.Errorf("Debounce = %v, want %v", got.Debounce, rec.Debounce)
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

// Spec: §7.3.2 — the §7.2.1 rename of the receiver's wire members is a
// projection in pkg/registry/server, so webhook.Receiver takes no struct tag
// and an operator store file written before the rename loads unchanged. The
// fixture below is the on-disk form the documented "flat array of Receiver
// structs" produces from an untagged struct.
func TestFileStore_LoadsAnOperatorFileWrittenBeforeTheWireRename(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "webhooks.json")
	const fixture = `[
  {
    "ID": "alpha",
    "TenantID": "default",
    "URL": "https://example.test/hook",
    "Secret": "shh",
    "EventFilter": ["manifest.upserted"],
    "Disabled": true,
    "FailureCount": 7,
    "LastDelivery": "2026-06-30T12:00:00Z",
    "LastFailure": "2026-06-30T13:00:00Z",
    "CreatedAt": "2026-06-01T09:00:00Z",
    "Debounce": 60000000000
  }
]`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s, err := webhook.LoadFileStore(path)
	if err != nil {
		t.Fatalf("LoadFileStore: %v", err)
	}
	got, err := s.Get(context.Background(), "default", "alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := webhook.Receiver{
		ID: "alpha", TenantID: "default", URL: "https://example.test/hook",
		Secret: "shh", EventFilter: []string{"manifest.upserted"},
		Disabled: true, FailureCount: 7,
		LastDelivery: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		LastFailure:  time.Date(2026, 6, 30, 13, 0, 0, 0, time.UTC),
		CreatedAt:    time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
		Debounce:     time.Minute,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaded receiver = %+v, want %+v", got, want)
	}
}
