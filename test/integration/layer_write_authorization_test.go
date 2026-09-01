package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// layerWriteCaller is the identity one run of the layer endpoint resolves.
type layerWriteCaller struct {
	name string
	id   layer.Identity
}

// newLayerWriteEndpoint builds the layer endpoint the way serverboot wires it
// on a registry with an identity provider configured: the admin arm is
// pkg/registry/core.AdminAuthorize over the §4.7.2 admin grant table, the
// identity hook resolves the caller, and the reingest runner drives the real
// local-source ingest pipeline.
func newLayerWriteEndpoint(t *testing.T, st store.Store, caller layer.Identity) string {
	t.Helper()
	reg := core.New(st, "t", nil)
	e := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return caller, nil }).
		WithAdminAuth(func(r *http.Request) error {
			return reg.AdminAuthorize(r.Context(), caller)
		}).
		WithReingestRunner(localReingestRunner(st, nil))
	ts := httptest.NewServer(e.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func layerWritePost(t *testing.T, base, path string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	if _, err := out.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read %s response: %v", path, err)
	}
	return resp.StatusCode, out.Bytes()
}

func layerWriteErrCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope %q: %v", body, err)
	}
	return env.Code
}

// Spec: §7.3.1 — the layer-write authorization rule with the layer endpoint
// wired to its real collaborators: a file-backed SQLite store, the §4.7.2
// admin grant table behind pkg/registry/core.AdminAuthorize, and the ingest
// pipeline behind the reingest runner. A user-defined layer's stored owner
// reingests it and the pipeline runs; a different verified subject and a
// caller resolving no subject are refused with 403 auth.forbidden and no
// ingest runs; a tenant admin reingests the admin-defined layer that the same
// non-admin subject is refused on, whatever that layer's stored owner names.
func TestLayerWriteAuthorization_OverAdminGrantTable(t *testing.T) {
	t.Parallel()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.GrantAdmin(ctx, store.AdminGrant{UserID: "ops@acme.com", OrgID: "t"}); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}

	// alice owns a user-defined layer; the org layer is admin-defined and
	// carries alice as its caller-supplied owner field.
	userDir := t.TempDir()
	writeArtifact(t, filepath.Join(userDir, "personal"), "alice personal artifact")
	orgDir := t.TempDir()
	writeArtifact(t, filepath.Join(orgDir, "org"), "org artifact")
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "alice-personal", SourceType: "local", LocalPath: userDir,
		UserDefined: true, Owner: "alice@acme.com", Users: []string{"alice@acme.com"},
	}); err != nil {
		t.Fatalf("PutLayerConfig(alice-personal): %v", err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "org", SourceType: "local", LocalPath: orgDir, Owner: "alice@acme.com",
	}); err != nil {
		t.Fatalf("PutLayerConfig(org): %v", err)
	}

	alice := layerWriteCaller{name: "owner", id: layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}}
	bob := layerWriteCaller{name: "other-subject", id: layer.Identity{Sub: "bob@acme.com", IsAuthenticated: true}}
	anon := layerWriteCaller{name: "no-subject", id: layer.Identity{IsPublic: true}}
	ops := layerWriteCaller{name: "admin", id: layer.Identity{Sub: "ops@acme.com", IsAuthenticated: true}}

	cases := []struct {
		caller  layerWriteCaller
		layerID string
		want    int
	}{
		{caller: bob, layerID: "alice-personal", want: http.StatusForbidden},
		{caller: anon, layerID: "alice-personal", want: http.StatusForbidden},
		{caller: alice, layerID: "alice-personal", want: http.StatusOK},
		{caller: bob, layerID: "org", want: http.StatusForbidden},
		{caller: alice, layerID: "org", want: http.StatusForbidden},
		{caller: ops, layerID: "org", want: http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.layerID+"/"+c.caller.name, func(t *testing.T) {
			before, err := st.GetLayerConfig(ctx, "t", c.layerID)
			if err != nil {
				t.Fatalf("GetLayerConfig: %v", err)
			}
			base := newLayerWriteEndpoint(t, st, c.caller.id)
			status, body := layerWritePost(t, base, "/v1/layers/reingest?id="+c.layerID, nil)
			if status != c.want {
				t.Fatalf("reingest status = %d, want %d: %s", status, c.want, body)
			}
			after, err := st.GetLayerConfig(ctx, "t", c.layerID)
			if err != nil {
				t.Fatalf("GetLayerConfig after: %v", err)
			}
			if c.want != http.StatusForbidden {
				if after.LastIngestedAt == nil {
					t.Errorf("authorized reingest did not stamp last_ingested_at")
				}
				return
			}
			if code := layerWriteErrCode(t, body); code != "auth.forbidden" {
				t.Errorf("code = %q, want auth.forbidden", code)
			}
			if before.LastIngestedAt == nil && after.LastIngestedAt != nil {
				t.Errorf("refused reingest ran the pipeline: last_ingested_at was stamped")
			}
		})
	}

	// A registration under alice's layer ID by another verified subject is
	// refused, and the stored layer keeps its owner and its source.
	base := newLayerWriteEndpoint(t, st, bob.id)
	status, body := layerWritePost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": t.TempDir(),
		"user_defined": true, "owner": "bob@acme.com",
	})
	if status != http.StatusForbidden {
		t.Fatalf("bob re-registration status = %d, want 403: %s", status, body)
	}
	if code := layerWriteErrCode(t, body); code != "auth.forbidden" {
		t.Errorf("bob re-registration code = %q, want auth.forbidden", code)
	}
	got, err := st.GetLayerConfig(ctx, "t", "alice-personal")
	if err != nil {
		t.Fatalf("GetLayerConfig(alice-personal): %v", err)
	}
	if got.Owner != "alice@acme.com" || got.LocalPath != userDir {
		t.Errorf("refused registration rewrote the stored layer: %+v", got)
	}
}
