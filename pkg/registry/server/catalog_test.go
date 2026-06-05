package server_test

import (
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

func catalogFixture(t *testing.T) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	_ = st.CreateTenant(t.Context(), store.Tenant{ID: "default"})
	put := func(id, typ, desc string) {
		if err := st.PutManifest(t.Context(), store.ManifestRecord{
			TenantID: "default", ArtifactID: id, Version: "1.0.0", ContentHash: "sha256:" + id,
			Type: typ, Description: desc, Layer: "L",
		}); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	put("finance/ap/pay", "skill", "pay vendors")
	put("finance/close/run", "context", "close the books")
	put("other/thing", "skill", "unrelated")
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// spec: §4.5.2 — GET /v1/catalog?scope= returns the visible
// catalog under the scope prefix: a flat id list plus lean descriptors.
func TestHandleCatalog_ScopeFilter(t *testing.T) {
	t.Parallel()
	ts := catalogFixture(t)
	resp, err := http.Get(ts.URL + "/v1/catalog?scope=finance")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out server.CatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Ids) != 2 {
		t.Fatalf("ids = %v, want 2 finance artifacts", out.Ids)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v, want 2", out.Artifacts)
	}
	found := false
	for _, a := range out.Artifacts {
		if a.ID == "finance/ap/pay" {
			found = true
			if a.Type != "skill" || a.Summary != "pay vendors" {
				t.Errorf("descriptor = %+v, want type/summary", a)
			}
		}
		if a.ID == "other/thing" {
			t.Errorf("out-of-scope artifact leaked: %+v", a)
		}
	}
	if !found {
		t.Errorf("finance/ap/pay missing from %+v", out.Artifacts)
	}
}

// spec: §4.5.2 — an empty scope returns the whole visible catalog.
func TestHandleCatalog_EmptyScopeReturnsAll(t *testing.T) {
	t.Parallel()
	ts := catalogFixture(t)
	resp, err := http.Get(ts.URL + "/v1/catalog")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out server.CatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Ids) != 3 {
		t.Errorf("ids = %v, want all 3", out.Ids)
	}
}

// spec: §4.5.2 — a non-GET method is rejected, matching the sibling read
// endpoints.
func TestHandleCatalog_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts := catalogFixture(t)
	resp, err := http.Post(ts.URL+"/v1/catalog", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}
