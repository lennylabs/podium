package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// narrowingStore narrows the layer's visibility, as a cycle does, between
// the handler's read of the layer rows and its write.
type narrowingStore struct {
	store.Store
	narrow func()
}

func (s *narrowingStore) ListLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	out, err := s.Store.ListLayerConfigs(ctx, tenantID)
	if s.narrow != nil {
		s.narrow()
		s.narrow = nil
	}
	return out, err
}

func (s *narrowingStore) GetLayerConfig(ctx context.Context, tenantID, id string) (store.LayerConfig, error) {
	out, err := s.Store.GetLayerConfig(ctx, tenantID, id)
	if s.narrow != nil {
		s.narrow()
		s.narrow = nil
	}
	return out, err
}

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Writes that keep
// the visibility, Updates), §7.3.1 — a reorder and an update that carries no
// visibility member write a row their handler read before a cycle narrowed
// it, and keep the narrowing; an update that sets the visibility writes it
// and, on a --layer-path layer under require, warns that the next passed
// cycle applies the signed .layer-config again.
func TestLayerWrites_KeepAVisibilityACycleNarrowed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemory()
	if err := mem.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"boot", "other"} {
		if err := mem.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: id, SourceType: "local", LocalPath: "/x", Order: (i + 1) * 10,
			Public: true, VisibilitySource: store.VisibilitySourceDeclared, TrustMode: "require"}); err != nil {
			t.Fatal(err)
		}
	}
	st := &narrowingStore{Store: mem}
	narrow := func() {
		cur, _ := mem.GetLayerConfig(ctx, "t", "boot")
		next := cur.VisibilityState()
		next.Visibility = store.LayerVisibility{Groups: []string{"acme-finance"}}
		if err := mem.SetLayerVisibilityIf(ctx, "t", "boot", cur.VisibilityState(), next); err != nil {
			t.Error(err)
		}
	}
	ts := httptest.NewServer(server.NewLayerEndpoint(st, "t", server.NewModeTracker()).WithTrustDefault("none").Handler())
	t.Cleanup(ts.Close)
	send := func(method, path, body string) server.LayerRegisterResponse {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader([]byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s = %d", method, path, resp.StatusCode)
		}
		var out server.LayerRegisterResponse
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	groups := func() []string {
		lc, err := mem.GetLayerConfig(ctx, "t", "boot")
		if err != nil {
			t.Fatal(err)
		}
		if lc.Public {
			return []string{"public"}
		}
		return lc.Groups
	}

	st.narrow = narrow
	send(http.MethodPost, "/v1/layers/reorder", `{"order":["other","boot"]}`)
	if got := groups(); !slices.Equal(got, []string{"acme-finance"}) {
		t.Fatalf("after a stale reorder: %v", got)
	}
	if lc, _ := mem.GetLayerConfig(ctx, "t", "boot"); lc.Order != 20 {
		t.Fatalf("reorder did not apply its order: %d", lc.Order)
	}

	if err := mem.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "boot", SourceType: "local", LocalPath: "/x", Order: 20,
		Public: true, VisibilitySource: store.VisibilitySourceDeclared, TrustMode: "require"}); err != nil {
		t.Fatal(err)
	}
	st.narrow = narrow
	resp := send(http.MethodPut, "/v1/layers/update?id=boot", `{"local_path":"/y"}`)
	if got := groups(); !slices.Equal(got, []string{"acme-finance"}) || resp.Layer.Public || len(resp.Warnings) != 0 {
		t.Fatalf("after a stale update: %v, response public %v warnings %q", got, resp.Layer.Public, resp.Warnings)
	}

	resp = send(http.MethodPut, "/v1/layers/update?id=boot", `{"groups":["incident"]}`)
	if got := groups(); !slices.Equal(got, []string{"incident"}) || len(resp.Warnings) != 1 ||
		!strings.Contains(resp.Warnings[0], "the next ingest cycle that passes the layer step applies the signed .layer-config again") {
		t.Fatalf("an update that sets the visibility: %v, warnings %q", got, resp.Warnings)
	}
	// A layer under none, and a layer --layer-path does not load, get no
	// such warning.
	if err := mem.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "other", SourceType: "local", LocalPath: "/x", Order: 10, Public: true, TrustMode: "require"}); err != nil {
		t.Fatal(err)
	}
	if resp := send(http.MethodPut, "/v1/layers/update?id=other", `{"organization":true}`); len(resp.Warnings) != 0 {
		t.Fatalf("a registered layer: warnings %q", resp.Warnings)
	}
	if err := mem.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "other", SourceType: "local", LocalPath: "/x", Order: 10, Public: true, VisibilitySource: "declared"}); err != nil {
		t.Fatal(err)
	}
	if resp := send(http.MethodPut, "/v1/layers/update?id=other", `{"organization":true}`); len(resp.Warnings) != 0 {
		t.Fatalf("a layer under none: warnings %q", resp.Warnings)
	}
}

// alwaysChanged reports every conditional visibility write as contended.
type alwaysChanged struct{ store.Store }

func (alwaysChanged) SetLayerVisibilityIf(context.Context, string, string, store.VisibilityState, store.VisibilityState) error {
	return store.ErrVisibilityChanged
}

// Spec: §7.3.1, §4.7.11 (Visibility of a path-bootstrapped layer: Writes
// that keep the visibility) — an update that carries one visibility member
// writes the visibility only while the row holds the members its handler
// read. A cycle narrowed organization to a group after that read, so the
// update reads the row again and adds its users to the group: organization
// is not restored. The §7.6 event carries the narrowed visibility it
// replaced. An update that meets a changed row on every attempt is answered
// 503 registry.unavailable and writes no visibility.
func TestLayerUpdate_PartialVisibilityKeepsAConcurrentNarrowing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemory()
	if err := mem.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := mem.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "boot", SourceType: "local", LocalPath: "/x", Order: 10,
		Organization: true, VisibilitySource: store.VisibilitySourceDeclared, TrustMode: "require"}); err != nil {
		t.Fatal(err)
	}
	st := &narrowingStore{Store: mem}
	st.narrow = func() {
		cur, _ := mem.GetLayerConfig(ctx, "t", "boot")
		next := cur.VisibilityState()
		next.Visibility = store.LayerVisibility{Groups: []string{"acme-finance"}}
		if err := mem.SetLayerVisibilityIf(ctx, "t", "boot", cur.VisibilityState(), next); err != nil {
			t.Error(err)
		}
	}
	rec := &eventRecorder{}
	ts := httptest.NewServer(server.NewLayerEndpoint(st, "t", server.NewModeTracker()).WithTrustDefault("none").WithEventPublisher(rec.publish).Handler())
	t.Cleanup(ts.Close)
	put := func(url, body string) (int, server.LayerRegisterResponse) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader([]byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out server.LayerRegisterResponse
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	status, resp := put(ts.URL+"/v1/layers/update?id=boot", `{"users":["bob@acme.com"]}`)
	lc, _ := mem.GetLayerConfig(ctx, "t", "boot")
	if status != http.StatusOK || lc.Organization || !slices.Equal(lc.Groups, []string{"acme-finance"}) || !slices.Equal(lc.Users, []string{"bob@acme.com"}) {
		t.Fatalf("status %d, row org=%v groups=%v users=%v", status, lc.Organization, lc.Groups, lc.Users)
	}
	if resp.Layer.Organization || !slices.Equal(resp.Layer.Groups, []string{"acme-finance"}) {
		t.Fatalf("response layer %+v", resp.Layer.LayerConfig)
	}
	scope := rec.scopeFor(t, "update")
	if scope.Prior == nil || scope.Prior.Organization || !slices.Equal(scope.Prior.Groups, []string{"acme-finance"}) {
		t.Fatalf("event prior %+v, want the narrowed visibility", scope.Prior)
	}

	contended := httptest.NewServer(server.NewLayerEndpoint(alwaysChanged{mem}, "t", server.NewModeTracker()).Handler())
	t.Cleanup(contended.Close)
	if status, _ := put(contended.URL+"/v1/layers/update?id=boot", `{"organization":true}`); status != http.StatusServiceUnavailable {
		t.Fatalf("contended update = %d, want 503", status)
	}
	if lc, _ := mem.GetLayerConfig(ctx, "t", "boot"); lc.Organization {
		t.Fatal("a contended update wrote the visibility")
	}
}
