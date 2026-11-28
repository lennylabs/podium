package server_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Updates), §7.3.1 —
// an update that changes the visibility of a layer that --layer-path
// bootstraps sets visibility_source to declared and clears a pending
// visibility equal to the one it stores; an update that restates the stored
// visibility changes neither member, and a layer of another class gains no
// source.
func TestLayerUpdate_BootstrapVisibilityMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	put := func(cfg store.LayerConfig) {
		cfg.TenantID, cfg.SourceType, cfg.LocalPath = "t", "local", "/x"
		if err := st.PutLayerConfig(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}
	put(store.LayerConfig{ID: "boot", Groups: []string{"acme-finance"}, VisibilitySource: store.VisibilitySourceDefault,
		VisibilityPending: &store.LayerVisibility{Organization: true}})
	put(store.LayerConfig{ID: "other", Groups: []string{"acme-finance"}})
	ts := httptest.NewServer(server.NewLayerEndpoint(st, "t", server.NewModeTracker()).Handler())
	t.Cleanup(ts.Close)
	update := func(id, body string) store.LayerConfig {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/layers/update?id="+id, bytes.NewReader([]byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("update %s %s = %d", id, body, resp.StatusCode)
		}
		lc, err := st.GetLayerConfig(ctx, "t", id)
		if err != nil {
			t.Fatal(err)
		}
		return lc
	}
	if lc := update("boot", `{"groups":["acme-finance"],"visibility_source":"declared"}`); lc.VisibilitySource != "default" || lc.VisibilityPending == nil {
		t.Fatalf("a restating update changed %q %+v", lc.VisibilitySource, lc.VisibilityPending)
	}
	if lc := update("boot", `{"public":true}`); lc.VisibilitySource != "declared" || lc.VisibilityPending == nil {
		t.Fatalf("an update to another visibility: %q %+v", lc.VisibilitySource, lc.VisibilityPending)
	}
	if lc := update("boot", `{"public":false,"organization":true,"groups":[]}`); lc.VisibilitySource != "declared" || lc.VisibilityPending != nil {
		t.Fatalf("an update to the pending visibility: %q %+v", lc.VisibilitySource, lc.VisibilityPending)
	}
	if lc := update("other", `{"organization":true}`); lc.VisibilitySource != "" {
		t.Fatalf("a registered layer gained the source %q", lc.VisibilitySource)
	}
}
