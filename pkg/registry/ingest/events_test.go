package ingest_test

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/core"
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
	publish := func(_ context.Context, _ core.EventScope, typ string, data map[string]any) {
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
	publish := func(_ context.Context, _ core.EventScope, typ string, _ map[string]any) {
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

// spec: §7.3.2 — artifact.deprecated fires when "a manifest update
// flipped deprecated: true", i.e. a new deprecated version supersedes a
// prior non-deprecated version of the same artifact_id. The flip ingest
// emits both artifact.published and artifact.deprecated.
func TestIngest_PublishesArtifactDeprecatedOnFlip(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	types := []string{}
	publish := func(_ context.Context, _ core.EventScope, typ string, _ map[string]any) {
		types = append(types, typ)
	}
	mk := func(version string, deprecated bool) fstest.MapFS {
		dep := ""
		if deprecated {
			dep = "deprecated: true\n"
		}
		return fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: " + version +
					"\ndescription: x\nsensitivity: low\n" + dep + "---\n\nbody\n"),
			},
		}
	}
	// v1 is not deprecated: only artifact.published fires.
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", PublishEvent: publish, Files: mk("1.0.0", false),
	}); err != nil {
		t.Fatalf("Ingest v1: %v", err)
	}
	for _, typ := range types {
		if typ == "artifact.deprecated" {
			t.Fatalf("artifact.deprecated fired on the non-deprecated v1 publish: %v", types)
		}
	}
	// v2 flips deprecated: artifact.published + artifact.deprecated.
	types = nil
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", PublishEvent: publish, Files: mk("2.0.0", true),
	}); err != nil {
		t.Fatalf("Ingest v2: %v", err)
	}
	expected := map[string]bool{"artifact.published": false, "artifact.deprecated": false}
	for _, typ := range types {
		if _, ok := expected[typ]; ok {
			expected[typ] = true
		}
	}
	for typ, seen := range expected {
		if !seen {
			t.Errorf("expected event type %q not emitted on flip; got %v", typ, types)
		}
	}
}

// spec: §7.3.2 — a version born deprecated on first publish is not a
// "flip"; only artifact.published fires, never artifact.deprecated,
// because no prior non-deprecated version of the artifact existed.
func TestIngest_BornDeprecatedDoesNotPublishDeprecated(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	types := []string{}
	publish := func(_ context.Context, _ core.EventScope, typ string, _ map[string]any) {
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
	sawPublished := false
	for _, typ := range types {
		if typ == "artifact.deprecated" {
			t.Errorf("artifact.deprecated fired on first publish of a born-deprecated version: %v", types)
		}
		if typ == "artifact.published" {
			sawPublished = true
		}
	}
	if !sawPublished {
		t.Errorf("artifact.published not emitted; got %v", types)
	}
}

// scopedEvent records one emitted event together with the §7.6 scope
// its publisher set.
type scopedEvent struct {
	typ   string
	scope core.EventScope
}

// recordScopes returns an emitter that appends every event to out.
func recordScopes(out *[]scopedEvent) ingest.EventEmitter {
	return func(_ context.Context, scope core.EventScope, typ string, _ map[string]any) {
		*out = append(*out, scopedEvent{typ, scope})
	}
}

// scopeOf returns the scope of the first recorded event of type typ.
func scopeOf(t *testing.T, events []scopedEvent, typ string) core.EventScope {
	t.Helper()
	for _, e := range events {
		if e.typ == typ {
			return e.scope
		}
	}
	t.Fatalf("no %s event among %+v", typ, events)
	return core.EventScope{}
}

// Spec: §7.6 — every ingest publish site names the event's tenant and
// layer in its scope, plus the artifact ID on artifact events and the
// domain path on domain.published, so the stream can evaluate
// subscriber visibility without reading payload keys.
func TestIngest_EventScopes(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	mk := func(version, dep string) fstest.MapFS {
		return fstest.MapFS{
			"finance/DOMAIN.md": &fstest.MapFile{Data: []byte("---\ndescription: finance\n---\n\n# Finance\n")},
			"finance/run/ARTIFACT.md": &fstest.MapFile{Data: []byte("---\ntype: context\nversion: " + version +
				"\ndescription: x\nsensitivity: low\n" + dep + "---\n\nbody\n")},
		}
	}
	var events []scopedEvent
	for _, files := range []fstest.MapFS{mk("1.0.0", ""), mk("2.0.0", "deprecated: true\n")} {
		if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: "L", Files: files, PublishEvent: recordScopes(&events),
		}); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	cases := []struct {
		typ  string
		want core.EventScope
	}{
		{"domain.published", core.EventScope{TenantID: "t", Layers: []string{"L"}, Path: "finance"}},
		{"artifact.published", core.EventScope{TenantID: "t", Layers: []string{"L"}, Path: "finance/run"}},
		{"artifact.deprecated", core.EventScope{TenantID: "t", Layers: []string{"L"}, Path: "finance/run"}},
	}
	for _, c := range cases {
		if got := scopeOf(t, events, c.typ); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s scope = %+v, want %+v", c.typ, got, c.want)
		}
	}
}

// Spec: §7.6 — the orchestrator's layer-level events carry the
// layer's tenant and ID and no path.
func TestSourceIngest_LayerEventScopes(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	cfg := store.LayerConfig{TenantID: "t", ID: "L", SourceType: "fake", LastIngestedRef: "old-sha"}
	_ = st.PutLayerConfig(context.Background(), cfg)
	provider := &fakeProvider{files: fstest.MapFS{}, reference: "new-sha", historyRewritten: true}
	var events []scopedEvent
	if _, err := ingest.SourceIngestWithOptions(context.Background(), st, provider, cfg,
		ingest.SourceIngestOptions{PublishEvent: recordScopes(&events)}); err != nil {
		t.Fatalf("SourceIngestWithOptions: %v", err)
	}
	want := core.EventScope{TenantID: "t", Layers: []string{"L"}}
	for _, typ := range []string{"layer.history_rewritten", "layer.ingested"} {
		if got := scopeOf(t, events, typ); !reflect.DeepEqual(got, want) {
			t.Errorf("%s scope = %+v, want %+v", typ, got, want)
		}
	}
}
