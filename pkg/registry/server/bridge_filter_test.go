package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// bridgeServer serves a skill and an mcp-server artifact under ops.
func bridgeServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(t.Context(), store.Tenant{ID: "default"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.ManifestRecord{
		{TenantID: "default", ArtifactID: "ops/greet", Version: "1.0.0", ContentHash: "sha256:g", Type: "skill", Description: "acme session helper", Layer: "L"},
		{TenantID: "default", ArtifactID: "ops/db", Version: "1.0.0", ContentHash: "sha256:d", Type: "mcp-server", Description: "acme session database", Layer: "L"},
	} {
		if err := st.PutManifest(t.Context(), storetest.Seal(t, m, nil, nil)); err != nil {
			t.Fatal(err)
		}
	}
	reg := core.New(st, "default", []layer.Layer{{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}}})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// bridgeGet sends a GET for path with the X-Podium-Client name and returns
// the response and its body.
func bridgeGet(t *testing.T, ts *httptest.Server, path, client string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(audit.ClientHeader, "name="+client)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// Spec: §5, §7.2.2 — the registry reads the client name from X-Podium-Client:
// for name=mcp, search_artifacts and load_domain leave out the mcp-server
// artifact and load_artifact answers 404 registry.not_found, while name=sdk
// receives it. Each of the three endpoints sends Vary: X-Podium-Client, on a
// success and on the refusal alike, so a shared cache keeps the answers apart.
func TestBridgeFilter_ClientHeaderAndVary(t *testing.T) {
	t.Parallel()
	ts := bridgeServer(t)
	cases := []struct {
		path       string
		bridgeCode int
	}{
		{"/v1/search_artifacts?query=acme", http.StatusOK},
		{"/v1/load_domain?path=ops", http.StatusOK},
		{"/v1/load_artifact?id=ops/db", http.StatusNotFound},
	}
	for _, c := range cases {
		for _, client := range []string{"mcp", "sdk"} {
			resp, body := bridgeGet(t, ts, c.path, client)
			if vary := resp.Header.Values("Vary"); !containsFold(vary, audit.ClientHeader) {
				t.Errorf("%s as %s: Vary = %v, want %s", c.path, client, vary, audit.ClientHeader)
			}
			has := strings.Contains(body, "ops/db") && resp.StatusCode == http.StatusOK
			if client == "mcp" {
				if has || resp.StatusCode != c.bridgeCode {
					t.Errorf("%s as mcp: status %d, body %s; want %d without ops/db", c.path, resp.StatusCode, body, c.bridgeCode)
				}
				if resp.StatusCode == http.StatusNotFound {
					var env struct{ Code, Message string }
					if err := json.Unmarshal([]byte(body), &env); err != nil || env.Code != "registry.not_found" || !strings.HasSuffix(env.Message, "artifact ops/db") {
						t.Errorf("%s as mcp: envelope %s, want registry.not_found for artifact ops/db", c.path, body)
					}
				}
				continue
			}
			if !has {
				t.Errorf("%s as sdk: status %d, body %s; want ops/db", c.path, resp.StatusCode, body)
			}
		}
	}
}

// containsFold reports whether any comma-separated member of values equals
// want, ignoring case.
func containsFold(values []string, want string) bool {
	for _, v := range values {
		for _, member := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(member), want) {
				return true
			}
		}
	}
	return false
}
