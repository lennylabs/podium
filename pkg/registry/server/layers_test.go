package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func newLayerHarness(t *testing.T) (string, store.Store, func()) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	return ts.URL, st, ts.Close
}

func mustPost(t *testing.T, base, path string, body any) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func mustDelete(t *testing.T, base, path string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, base+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// Spec: §8.4 — unregistering a layer soft-deletes it (and the artifacts
// ingested from it) into a 30-day recovery window: the layer disappears
// from the normal list but appears under ?deleted=true, and
// /v1/layers/restore recovers it.
func TestLayerEndpoint_UnregisterSoftDeletesAndRestoreRecovers(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	// Register a user-defined layer (no admin auth needed to manage it).
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/tmp/x",
		"user_defined": true, "owner": "alice",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status %d, body=%s", resp.StatusCode, body)
	}
	// Seed an artifact ingested from that layer.
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/a", Version: "1.0.0", ContentHash: "h",
		Type: "skill", Layer: "alice-personal",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	// Unregister: soft-delete.
	resp, body = mustDelete(t, base, "/v1/layers?id=alice-personal")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status %d, body=%s", resp.StatusCode, body)
	}
	if _, err := st.GetLayerConfig(context.Background(), "t", "alice-personal"); err == nil {
		t.Errorf("layer still visible after unregister")
	}
	if _, err := st.GetManifest(context.Background(), "t", "skill/a", "1.0.0"); err == nil {
		t.Errorf("artifact still visible after unregister")
	}

	// The normal list excludes it; the deleted list includes it.
	if active := mustGet(t, base, "/v1/layers"); strings.Contains(string(active), "alice-personal") {
		t.Errorf("active list should not contain soft-deleted layer: %s", active)
	}
	if del := mustGet(t, base, "/v1/layers?deleted=true"); !strings.Contains(string(del), "alice-personal") {
		t.Fatalf("deleted list missing layer: %s", del)
	}

	// Restore: recover layer and artifact.
	resp, body = mustPost(t, base, "/v1/layers/restore?id=alice-personal", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore status %d, body=%s", resp.StatusCode, body)
	}
	if _, err := st.GetLayerConfig(context.Background(), "t", "alice-personal"); err != nil {
		t.Errorf("layer not recovered: %v", err)
	}
	if _, err := st.GetManifest(context.Background(), "t", "skill/a", "1.0.0"); err != nil {
		t.Errorf("artifact not recovered: %v", err)
	}

	// Restoring an unknown / non-deleted layer is 404.
	resp, _ = mustPost(t, base, "/v1/layers/restore?id=nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("restore of missing layer status = %d, want 404", resp.StatusCode)
	}
}

// Spec: §7.3.1 — POST /v1/layers registers a layer and returns the
// webhook URL + HMAC secret for git sources.
func TestLayerEndpoint_RegisterGitLayer(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id":           "team-finance",
		"source_type":  "git",
		"repo":         "git@github.com:acme/finance.git",
		"ref":          "main",
		"organization": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body=%s", resp.StatusCode, body)
	}
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Layer.ID != "team-finance" {
		t.Errorf("ID = %q", got.Layer.ID)
	}
	if got.WebhookSecret == "" {
		t.Errorf("WebhookSecret empty for git source")
	}
	if got.WebhookURL == "" {
		t.Errorf("WebhookURL empty for git source")
	}
}

// Spec: §14.10 — with a configured public base URL, register
// advertises an absolute webhook URL a developer can paste into a Git host's
// webhook configuration. A trailing slash on the base is collapsed.
func TestLayerEndpoint_RegisterGitLayer_AbsoluteWebhookURL(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithPublicBaseURL("https://podium.acme.com/")
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	_, body := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "community-skills", "source_type": "git",
		"repo": "https://github.com/podium-community/skills.git", "ref": "main",
	})
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if want := "https://podium.acme.com/v1/ingest/webhook/community-skills"; got.WebhookURL != want {
		t.Errorf("WebhookURL = %q, want %q", got.WebhookURL, want)
	}
}

// Spec: §14.10 — without a configured public base URL the webhook
// URL falls back to the relative path (e.g. an embedding harness that does not
// know its own external address).
func TestLayerEndpoint_RegisterGitLayer_RelativeWebhookURLWithoutBase(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t) // no WithPublicBaseURL
	defer cleanup()

	_, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "vendor", "source_type": "git",
		"repo": "git@github.com:acme/vendor.git", "ref": "main",
	})
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if want := "/v1/ingest/webhook/vendor"; got.WebhookURL != want {
		t.Errorf("WebhookURL = %q, want relative %q", got.WebhookURL, want)
	}
}

// Spec: §7.3.1 — GET /v1/layers lists registered layers in Order.
func TestLayerEndpoint_ListReturnsRegisteredLayers(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "a", "source_type": "local", "local_path": "/tmp/a",
	})
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "b", "source_type": "local", "local_path": "/tmp/b",
	})

	resp, err := http.Get(base + "/v1/layers")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var listResp struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listResp.Layers) != 2 {
		t.Fatalf("got %d layers, want 2", len(listResp.Layers))
	}
}

// Spec: §7.3.1 — DELETE /v1/layers?id=X unregisters a user-defined layer.
func TestLayerEndpoint_Unregister(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "joan-personal", "source_type": "local",
		"local_path":   "/tmp/joan",
		"user_defined": true, "owner": "joan",
	})
	resp, _ := mustDelete(t, base, "/v1/layers?id=joan-personal")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}
	resp2, _ := mustDelete(t, base, "/v1/layers?id=joan-personal")
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", resp2.StatusCode)
	}
}

// Spec: §7.3.1 — User-defined layers carry implicit users:[owner].
func TestLayerEndpoint_UserDefinedSetsImplicitUsers(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "joan-personal", "source_type": "local",
		"local_path":   "/tmp/joan",
		"user_defined": true, "owner": "joan",
	})
	cfg, err := st.GetLayerConfig(context.Background(), "t", "joan-personal")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if len(cfg.Users) != 1 || cfg.Users[0] != "joan" {
		t.Errorf("Users = %v, want [joan]", cfg.Users)
	}
}

// Spec: §7.3.1 — POST /v1/layers/reorder re-sequences the list.
func TestLayerEndpoint_Reorder(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "a", "source_type": "local", "local_path": "/x",
	})
	mustPost(t, base, "/v1/layers", map[string]any{
		"id": "b", "source_type": "local", "local_path": "/y",
	})

	resp, body := mustPost(t, base, "/v1/layers/reorder", map[string]any{
		"order": []string{"b", "a"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body=%s", resp.StatusCode, body)
	}
	var listResp struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listResp.Layers) != 2 {
		t.Fatalf("got %d", len(listResp.Layers))
	}
	if listResp.Layers[0].ID != "b" {
		t.Errorf("first layer = %q, want b", listResp.Layers[0].ID)
	}
}

// Spec: §6.10 — admin-only ops without admin auth fail with auth.forbidden.
func TestLayerEndpoint_AdminAuthRequired(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAdminAuth(func(*http.Request) error {
			return server.ErrAdminRequired
		})
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	resp, body := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "admin-layer", "source_type": "local", "local_path": "/x",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if !strings.Contains(string(body), "auth.forbidden") {
		t.Errorf("body missing auth.forbidden: %s", body)
	}
}

// Spec: §14.10 — the advertised webhook URL is the URL the ingest endpoint
// answers on. A layer id is an operator-chosen string that may contain a space
// or a slash, so register percent-escapes it into a single path segment; the
// unescaped form produces a request the {id} route never matches.
func TestLayerEndpoint_RegisterGitLayer_WebhookURLEscapesLayerID(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	api := httptest.NewServer(endpoint.Handler())
	defer api.Close()
	ingest := httptest.NewServer(endpoint.WebhookHandler())
	defer ingest.Close()

	_, body := mustPost(t, api.URL, "/v1/layers", map[string]any{
		"id": "team space/layer", "source_type": "git",
		"repo": "https://github.com/acme/artifacts.git", "ref": "main",
	})
	var got server.LayerRegisterResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if want := "/v1/ingest/webhook/team%20space%2Flayer"; got.WebhookURL != want {
		t.Fatalf("WebhookURL = %q, want %q", got.WebhookURL, want)
	}

	// The advertised URL reaches the handler: the delivery is rejected for a
	// missing signature (401) rather than lost to a routing 404.
	req, err := http.NewRequest(http.MethodPost, ingest.URL+got.WebhookURL, strings.NewReader(`{"ref":"refs/heads/main"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("POST %s = 404, advertised webhook URL does not reach the ingest endpoint", got.WebhookURL)
	}
}

// layerArmOpts configures newLayerArmHarness. The admin callback, the
// identity resolver, and the group resolver are the three seams the §7.3.1
// layer read evaluates, and each field is the constant answer the harness
// gives on every request. The identity seam carries an error because the read
// refuses a credential the verifier could not verify, and every arm of
// TestLayerEndpoint_ListRefusesUnverifiedCredential is defined by that error.
type layerArmOpts struct {
	adminErr     error
	callerID     layer.Identity
	callerErr    error
	resolveGroup layer.GroupResolver
	configs      []store.LayerConfig
}

// newLayerArmHarness serves the layer endpoint over a memory store seeded
// with opts.configs, with the admin, identity, and group seams wired to the
// constant answers opts carries. A config whose DeletedAt is non-nil is
// written and then soft-deleted, so it appears on the ?deleted=true arm.
func newLayerArmHarness(t *testing.T, opts layerArmOpts) (string, store.Store, func()) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, cfg := range opts.configs {
		deleted := cfg.DeletedAt != nil
		cfg.TenantID = "t"
		cfg.DeletedAt = nil
		if err := st.PutLayerConfig(ctx, cfg); err != nil {
			t.Fatalf("PutLayerConfig %s: %v", cfg.ID, err)
		}
		if deleted {
			if err := st.DeleteLayerConfig(ctx, "t", cfg.ID); err != nil {
				t.Fatalf("DeleteLayerConfig %s: %v", cfg.ID, err)
			}
		}
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAdminAuth(func(*http.Request) error { return opts.adminErr }).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
			return opts.callerID, opts.callerErr
		}).
		WithGroupResolver(opts.resolveGroup)
	ts := httptest.NewServer(endpoint.Handler())
	return ts.URL, st, ts.Close
}

// layerIDs returns the layer ids in a list or reorder response body.
func layerIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var resp struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	ids := make([]string, 0, len(resp.Layers))
	for _, l := range resp.Layers {
		ids = append(ids, l.ID)
	}
	return ids
}

func layerGet(t *testing.T, base, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// armLayers is the seeded tenant the read-scope cases share: one private
// admin-defined layer, one public admin-defined layer, and one user-defined
// layer owned by alice, which stores its registrant in Users the way the
// register handler does.
func armLayers() []store.LayerConfig {
	return []store.LayerConfig{
		{ID: "private", SourceType: "local", LocalPath: "/srv/private-source", Order: 10},
		{ID: "public", SourceType: "local", LocalPath: "/srv/public", Order: 20, Public: true},
		{ID: "alice-personal", SourceType: "local", LocalPath: "/srv/alice", Order: 30,
			UserDefined: true, Owner: "alice", Users: []string{"alice"}},
	}
}

// TestLayerEndpoint_ListArmsByCallerRole pins the §7.3.1 layer read: a caller
// the admin callback admits reads the tenant's whole list, any other
// authenticated caller reads the layers §4.6 admits for them, and a caller
// resolving no verified subject reads none.
//
// Spec: §4.6, §7.3.1
func TestLayerEndpoint_ListArmsByCallerRole(t *testing.T) {
	t.Parallel()
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	bob := layer.Identity{Sub: "bob", IsAuthenticated: true}

	cases := []struct {
		name     string
		opts     layerArmOpts
		path     string
		wantIDs  []string
		wantBody string   // exact compacted body, when non-empty
		absent   []string // substrings the body must not carry
	}{
		{
			// "private" sets no §4.6 visibility field, which is the record
			// §4.6 states matches no condition in the evaluator: the whole
			// list reaches this caller through §7.3.1's admin arm, which
			// reads the caller's role rather than the layer's fields, and
			// the case below pins that a subject-resolving non-admin does
			// not reach it.
			name:    "admin_reads_whole_tenant",
			opts:    layerArmOpts{adminErr: nil, callerID: bob, configs: armLayers()},
			path:    "/v1/layers",
			wantIDs: []string{"private", "public", "alice-personal"},
		},
		{
			// The out-of-evaluator half of §4.6's rule on a record setting
			// no visibility field: with the admin arm denied, "private"
			// reaches no caller the registry resolves to a subject.
			name:    "user_reads_effective_view",
			opts:    layerArmOpts{adminErr: server.ErrAdminRequired, callerID: bob, configs: armLayers()},
			path:    "/v1/layers",
			wantIDs: []string{"public"},
			absent:  []string{"private", "/srv/private-source", "alice"},
		},
		{
			name:    "owner_reads_own_user_defined_layer",
			opts:    layerArmOpts{adminErr: server.ErrAdminRequired, callerID: alice, configs: armLayers()},
			path:    "/v1/layers",
			wantIDs: []string{"public", "alice-personal"},
			absent:  []string{"/srv/private-source"},
		},
		{
			name: "anonymous_reads_nothing",
			opts: layerArmOpts{adminErr: server.ErrAdminRequired,
				callerID: layer.Identity{}, configs: armLayers()},
			path:     "/v1/layers",
			wantIDs:  []string{},
			wantBody: `{"layers":[]}`,
		},
		{
			// A deployment naming a free-form PODIUM_IDENTITY_PROVIDER label
			// installs no request-time verifier, so its caller resolves the
			// anonymous-public identity that layer.VisibleWith admits
			// everywhere, and its admin callback refuses. The authentication
			// guard is what keeps that caller off the tenant's list.
			name: "nil_verifier_reads_nothing",
			opts: layerArmOpts{adminErr: server.ErrAdminRequired,
				callerID: layer.Identity{IsPublic: true}, configs: armLayers()},
			path:     "/v1/layers",
			wantIDs:  []string{},
			wantBody: `{"layers":[]}`,
		},
		{
			name: "admin_check_error",
			opts: layerArmOpts{adminErr: fmt.Errorf("admin check: %w", core.ErrUnavailable),
				callerID: bob, configs: armLayers()},
			path:    "/v1/layers",
			wantIDs: []string{"public"},
		},
		{
			name: "group_resolver_seam_member",
			opts: layerArmOpts{
				adminErr: server.ErrAdminRequired,
				callerID: bob,
				resolveGroup: func(g string) []string {
					if g == "finance-readers" {
						return []string{"bob"}
					}
					return nil
				},
				configs: []store.LayerConfig{
					{ID: "finance", SourceType: "local", LocalPath: "/srv/fin", Groups: []string{"finance-readers"}},
				},
			},
			path:    "/v1/layers",
			wantIDs: []string{"finance"},
		},
		{
			name: "group_resolver_seam_non_member",
			opts: layerArmOpts{
				adminErr:     server.ErrAdminRequired,
				callerID:     bob,
				resolveGroup: func(string) []string { return []string{"carol"} },
				configs: []store.LayerConfig{
					{ID: "finance", SourceType: "local", LocalPath: "/srv/fin", Groups: []string{"finance-readers"}},
				},
			},
			path:    "/v1/layers",
			wantIDs: []string{},
		},
		{
			name: "deleted_arm_admin_reads_tombstone",
			opts: layerArmOpts{callerID: bob, configs: []store.LayerConfig{
				{ID: "gone", SourceType: "local", LocalPath: "/srv/gone", UserDefined: true,
					Owner: "alice", Users: []string{"alice"}, DeletedAt: &deletedMarker},
			}},
			path:    "/v1/layers?deleted=true",
			wantIDs: []string{"gone"},
		},
		{
			name: "deleted_arm_owner_reads_tombstone",
			opts: layerArmOpts{adminErr: server.ErrAdminRequired, callerID: alice,
				configs: []store.LayerConfig{
					{ID: "gone", SourceType: "local", LocalPath: "/srv/gone", UserDefined: true,
						Owner: "alice", Users: []string{"alice"}, DeletedAt: &deletedMarker},
				}},
			path:    "/v1/layers?deleted=true",
			wantIDs: []string{"gone"},
		},
		{
			name: "deleted_arm_takes_the_same_rule",
			opts: layerArmOpts{adminErr: server.ErrAdminRequired, callerID: bob,
				configs: []store.LayerConfig{
					{ID: "gone", SourceType: "local", LocalPath: "/srv/gone", UserDefined: true,
						Owner: "alice", Users: []string{"alice"}, DeletedAt: &deletedMarker},
				}},
			path:    "/v1/layers?deleted=true",
			wantIDs: []string{},
			absent:  []string{"gone", "alice"},
		},
		{
			name: "deleted_arm_anonymous_reads_nothing",
			opts: layerArmOpts{adminErr: server.ErrAdminRequired, callerID: layer.Identity{},
				configs: []store.LayerConfig{
					{ID: "gone", SourceType: "local", LocalPath: "/srv/gone", UserDefined: true,
						Owner: "alice", Users: []string{"alice"}, DeletedAt: &deletedMarker},
				}},
			path:     "/v1/layers?deleted=true",
			wantIDs:  []string{},
			wantBody: `{"layers":[]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, _, cleanup := newLayerArmHarness(t, tc.opts)
			defer cleanup()

			resp, body := layerGet(t, base, tc.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
			}
			if got := layerIDs(t, body); !equalStringSets(got, tc.wantIDs) {
				t.Errorf("layer ids = %v, want %v", got, tc.wantIDs)
			}
			if tc.wantBody != "" {
				var compact bytes.Buffer
				if err := json.Compact(&compact, body); err != nil {
					t.Fatalf("compact %s: %v", body, err)
				}
				if compact.String() != tc.wantBody {
					t.Errorf("body = %s, want %s", compact.String(), tc.wantBody)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(string(body), s) {
					t.Errorf("body discloses %q: %s", s, body)
				}
			}
		})
	}
}

// deletedMarker flags a seeded config the harness soft-deletes. Its value is
// never read; store.DeleteLayerConfig stamps the tombstone.
var deletedMarker = time.Unix(0, 0)

func equalStringSets(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

// TestLayerEndpoint_ReorderResponseTakesTheReadRule pins that the reorder
// response body reports the same set the list read would, so a caller who
// owns one personal layer does not read the tenant's whole list back through
// a reorder.
//
// Spec: §4.6, §7.3.1
func TestLayerEndpoint_ReorderResponseTakesTheReadRule(t *testing.T) {
	t.Parallel()
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}

	t.Run("owner_reads_own_view", func(t *testing.T) {
		t.Parallel()
		base, _, cleanup := newLayerArmHarness(t, layerArmOpts{
			adminErr: server.ErrAdminRequired, callerID: alice, configs: armLayers()})
		defer cleanup()

		resp, body := mustPost(t, base, "/v1/layers/reorder",
			map[string]any{"order": []string{"alice-personal"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		if got := layerIDs(t, body); !equalStringSets(got, []string{"public", "alice-personal"}) {
			t.Errorf("layer ids = %v, want [alice-personal public]", got)
		}
		for _, s := range []string{"private", "/srv/private-source"} {
			if strings.Contains(string(body), s) {
				t.Errorf("body discloses %q: %s", s, body)
			}
		}
	})

	t.Run("admin_reads_whole_tenant", func(t *testing.T) {
		t.Parallel()
		base, _, cleanup := newLayerArmHarness(t, layerArmOpts{callerID: alice, configs: armLayers()})
		defer cleanup()

		resp, body := mustPost(t, base, "/v1/layers/reorder",
			map[string]any{"order": []string{"alice-personal"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		if got := layerIDs(t, body); !equalStringSets(got, []string{"private", "public", "alice-personal"}) {
			t.Errorf("layer ids = %v, want all three", got)
		}
	})
}

// TestLayerEndpoint_ListRefusesUnverifiedCredential pins that a credential
// the request-time verifier could not verify is refused on both list arms and
// on reorder, with the §6.10 envelope that failure already carries, and that
// the write paths keep their auth.forbidden disposition.
//
// Spec: §6.3.2, §6.3.3, §6.10, §7.3.1
func TestLayerEndpoint_ListRefusesUnverifiedCredential(t *testing.T) {
	t.Parallel()

	refusals := []struct {
		name     string
		opts     layerArmOpts
		wantCode string
		wantISS  string
	}{
		{
			name: "expired",
			opts: layerArmOpts{adminErr: server.ErrAdminRequired,
				callerErr: identity.ErrTokenExpired, configs: armLayers()},
			wantCode: "auth.token_expired",
		},
		{
			name: "untrusted_token",
			opts: layerArmOpts{adminErr: server.ErrAdminRequired,
				callerErr: &identity.UntrustedTokenError{Issuer: "https://idp.example"},
				configs:   armLayers()},
			wantCode: "auth.untrusted_token",
			wantISS:  "https://idp.example",
		},
		{
			name: "untrusted_runtime",
			opts: layerArmOpts{adminErr: server.ErrAdminRequired,
				callerErr: identity.ErrUntrustedRuntime, configs: armLayers()},
			wantCode: "auth.untrusted_runtime",
		},
		{
			// The constructor default admits every caller, so an admin arm
			// evaluated before the guard would serve a credential the
			// registry just failed to verify.
			name:     "admitting_admin_callback_does_not_rescue",
			opts:     layerArmOpts{callerErr: identity.ErrTokenExpired, configs: armLayers()},
			wantCode: "auth.token_expired",
		},
	}

	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, _, cleanup := newLayerArmHarness(t, tc.opts)
			defer cleanup()

			for _, path := range []string{"/v1/layers", "/v1/layers?deleted=true"} {
				resp, body := layerGet(t, base, path)
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("GET %s status = %d, want 401: %s", path, resp.StatusCode, body)
				}
				if code := layerErrorCode(t, body); code != tc.wantCode {
					t.Errorf("GET %s code = %q, want %q", path, code, tc.wantCode)
				}
				if tc.wantISS != "" && !strings.Contains(string(body), tc.wantISS) {
					t.Errorf("GET %s body missing token_iss %q: %s", path, tc.wantISS, body)
				}
			}
		})
	}

	t.Run("reorder", func(t *testing.T) {
		t.Parallel()
		base, st, cleanup := newLayerArmHarness(t, layerArmOpts{
			callerErr: identity.ErrTokenExpired, configs: armLayers()})
		defer cleanup()

		resp, body := mustPost(t, base, "/v1/layers/reorder",
			map[string]any{"order": []string{"alice-personal", "public", "private"}})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: %s", resp.StatusCode, body)
		}
		if code := layerErrorCode(t, body); code != "auth.token_expired" {
			t.Errorf("code = %q, want auth.token_expired", code)
		}
		// The guard runs before the restamp, so the stored order is intact.
		stored, err := st.ListLayerConfigs(context.Background(), "t")
		if err != nil {
			t.Fatalf("ListLayerConfigs: %v", err)
		}
		want := map[string]int{"private": 10, "public": 20, "alice-personal": 30}
		for _, cfg := range stored {
			if cfg.Order != want[cfg.ID] {
				t.Errorf("layer %s order = %d, want %d", cfg.ID, cfg.Order, want[cfg.ID])
			}
		}
	})

	t.Run("writes_unchanged", func(t *testing.T) {
		t.Parallel()
		base, _, cleanup := newLayerArmHarness(t, layerArmOpts{
			adminErr: server.ErrAdminRequired, callerErr: identity.ErrTokenExpired})
		defer cleanup()

		resp, body := mustPost(t, base, "/v1/layers", map[string]any{
			"id": "mine", "source_type": "local", "local_path": "/tmp/mine", "user_defined": true,
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("register status = %d, want 403: %s", resp.StatusCode, body)
		}
		if code := layerErrorCode(t, body); code != "auth.forbidden" {
			t.Errorf("register code = %q, want auth.forbidden", code)
		}
	})
}

// layerErrorCode reads the §6.10 error code out of a response body.
func layerErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	if env.Error.Code != "" {
		return env.Error.Code
	}
	return env.Code
}
