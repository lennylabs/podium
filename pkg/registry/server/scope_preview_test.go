package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// scopePreviewServer boots an in-process registry whose default tenant
// carries the given §3.5 expose_scope_preview flag (nil = unset/default).
func scopePreviewServer(t *testing.T, flag *bool) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default", ExposeScopePreview: flag}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/run", Version: "1.0.0",
		ContentHash: "sha256:c", Type: "skill", Layer: "L",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §3.5 (F-3.5.9) — the endpoint is GET /v1/scope/preview; sibling
// read endpoints reject other methods with 405, and this one must too.
func TestScopePreview_RejectsNonGET(t *testing.T) {
	t.Parallel()
	ts := scopePreviewServer(t, nil)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, err := http.NewRequest(method, ts.URL+"/v1/scope/preview", nil)
		if err != nil {
			t.Fatalf("NewRequest %s: %v", method, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, resp.StatusCode)
		}
	}
}

// Spec: §3.5 (F-3.5.1) — when tenant config expose_scope_preview is false,
// GET /v1/scope/preview returns 403 with error code scope_preview_disabled.
func TestScopePreview_DisabledReturns403(t *testing.T) {
	t.Parallel()
	off := false
	ts := scopePreviewServer(t, &off)
	resp, err := http.Get(ts.URL + "/v1/scope/preview")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, body)
	}
	if env.Code != "scope_preview_disabled" {
		t.Errorf("error code = %q, want scope_preview_disabled (body: %s)", env.Code, body)
	}
}

// Spec: §3.5 (F-3.5.1) — with the gate enabled (explicit true), the
// endpoint serves the aggregate counts with a 200.
func TestScopePreview_EnabledReturns200(t *testing.T) {
	t.Parallel()
	on := true
	for name, flag := range map[string]*bool{"explicit-true": &on, "unset-default": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ts := scopePreviewServer(t, flag)
			resp, err := http.Get(ts.URL + "/v1/scope/preview")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
			}
		})
	}
}
