package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.6 — ingest.Request.PublishEvent fires artifact.published
// for each accepted manifest with the canonical metadata fields.
func TestIngest_PublishesArtifactPublished(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	type evt struct {
		typ  string
		data map[string]any
	}
	events := []evt{}
	publish := func(typ string, data map[string]any) {
		events = append(events, evt{typ, data})
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:     "t",
		LayerID:      "L",
		PublishEvent: publish,
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1", res.Accepted)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1: %+v", len(events), events)
	}
	if events[0].typ != "artifact.published" {
		t.Errorf("type = %q, want artifact.published", events[0].typ)
	}
	if events[0].data["id"] != "x" || events[0].data["version"] != "1.0.0" {
		t.Errorf("data = %v, want {id:x, version:1.0.0,...}", events[0].data)
	}
	if events[0].data["layer"] != "L" {
		t.Errorf("layer = %v, want L", events[0].data["layer"])
	}
	if events[0].data["tenant"] != "t" {
		t.Errorf("tenant = %v, want t", events[0].data["tenant"])
	}
}

// Spec: §7.6 — re-ingesting an unchanged manifest is idempotent and
// does not republish the artifact.published event. Subscribers
// only see one event per genuine state change.
func TestIngest_IdempotentDoesNotRepublish(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	count := 0
	publish := func(typ string, _ map[string]any) {
		if typ == "artifact.published" {
			count++
		}
	}
	files := fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{
			Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
		},
	}
	for i := 0; i < 3; i++ {
		_, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: "L", Files: files, PublishEvent: publish,
		})
		if err != nil {
			t.Fatalf("Ingest #%d: %v", i, err)
		}
	}
	if count != 1 {
		t.Errorf("artifact.published emitted %d times, want 1", count)
	}
}

// Spec: §7.6 — when an ingested manifest sets deprecated:true, both
// artifact.published and artifact.deprecated fire. Subscribers can
// filter on either type independently.
func TestIngest_PublishesArtifactDeprecated(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	types := []string{}
	publish := func(typ string, _ map[string]any) {
		types = append(types, typ)
	}
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", PublishEvent: publish,
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\ndeprecated: true\n---\n\nbody\n"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	expected := map[string]bool{"artifact.published": false, "artifact.deprecated": false}
	for _, typ := range types {
		if _, ok := expected[typ]; ok {
			expected[typ] = true
		}
	}
	for typ, seen := range expected {
		if !seen {
			t.Errorf("expected event type %q not emitted; got %v", typ, types)
		}
	}
}
