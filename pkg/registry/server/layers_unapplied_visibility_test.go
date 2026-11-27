package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// unappliedVisibilityMessage is the fragment every §7.3.1
// unapplied-visibility warning carries.
const unappliedVisibilityMessage = "this registry verifies no caller and applies no layer visibility, so every caller sees the layer and can update or unregister it"

// newUnappliedVisibilityHarness serves a layer endpoint over HTTP the way
// serverboot wires a registry with no request-time identity verifier: every
// caller resolves as anonymous-public and passes the admin arm. defaultVis is
// the PODIUM_DEFAULT_LAYER_VISIBILITY the endpoint stamps on a registration
// that declares no visibility.
func newUnappliedVisibilityHarness(t *testing.T, defaultVis string) (string, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithDefaultVisibility(defaultVis).
		WithUnappliedVisibility()
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return ts.URL, st
}

// layerWarnings decodes the warnings member of a layer write response and
// reports whether the member was present at all.
func layerWarnings(t *testing.T, body []byte) ([]string, bool) {
	t.Helper()
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response %s: %v", body, err)
	}
	raw, ok := resp["warnings"]
	if !ok {
		return nil, false
	}
	var warnings []string
	if err := json.Unmarshal(raw, &warnings); err != nil {
		t.Fatalf("decode warnings %s: %v", raw, err)
	}
	return warnings, true
}

// Spec: §7.3.1 (Unapplied-visibility warning) — on a registry with no
// request-time identity verifier, a registration whose layer sets no public
// grant answers with a warnings member that names the layer and its declared
// visibility, says every caller sees it, and says the declaration takes
// effect once an identity provider is configured. A user-defined layer's
// implicit users entry and a layer stored under the private default are
// declarations under the rule. The warning changes neither the status nor
// what the registration stores.
// Spec: §4.6 (No-identity bypass)
func TestLayerRegister_WarnsWhenVisibilityIsNotApplied(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		defaultVis string
		body       map[string]any
		want       []string
	}{
		{
			name: "organization",
			body: map[string]any{"organization": true},
			want: []string{`layer "team" declares visibility organization: true`, "takes effect once an identity provider is configured"},
		},
		{
			name: "groups",
			body: map[string]any{"groups": []string{"acme-finance", "acme-finance-leads"}},
			want: []string{`groups: ["acme-finance", "acme-finance-leads"]`, "takes effect once an identity provider is configured"},
		},
		{
			name: "users",
			body: map[string]any{"users": []string{"bob@acme.com"}},
			want: []string{`users: ["bob@acme.com"]`},
		},
		{
			name: "organization and groups together",
			body: map[string]any{"organization": true, "groups": []string{"acme-eng"}},
			want: []string{`organization: true; groups: ["acme-eng"]`},
		},
		{
			name: "user-defined with an owner",
			body: map[string]any{"user_defined": true, "owner": "alice@acme.com"},
			want: []string{`users: ["alice@acme.com"]`, "takes effect once an identity provider is configured"},
		},
		{
			// An entry holding a comma and a space is quoted, so it does not
			// read as the two entries "alice@acme.com" and "bob@acme.com".
			name: "an entry holding a comma",
			body: map[string]any{"users": []string{"alice@acme.com, bob@acme.com"}},
			want: []string{`users: ["alice@acme.com, bob@acme.com"]`},
		},
		{
			name:       "the private default",
			defaultVis: "private",
			body:       map[string]any{},
			want:       []string{`layer "team" is private`, "the private declaration applies once an identity provider is added"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, st := newUnappliedVisibilityHarness(t, tc.defaultVis)
			body := map[string]any{"id": "team", "source_type": "git", "repo": "https://github.com/acme/team.git", "ref": "main"}
			for k, v := range tc.body {
				body[k] = v
			}
			resp, out := mustPost(t, base, "/v1/layers", body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("register status = %d, want 201; body=%s", resp.StatusCode, out)
			}
			warnings, ok := layerWarnings(t, out)
			if !ok || len(warnings) != 1 {
				t.Fatalf("warnings = %v (present %v), want one warning; body=%s", warnings, ok, out)
			}
			for _, w := range append([]string{unappliedVisibilityMessage}, tc.want...) {
				if !strings.Contains(warnings[0], w) {
					t.Errorf("warning %q lacks %q", warnings[0], w)
				}
			}
			stored, err := st.GetLayerConfig(context.Background(), "t", "team")
			if err != nil {
				t.Fatalf("GetLayerConfig: %v", err)
			}
			if stored.Public {
				t.Errorf("stored layer is public; the warning must not change what is stored")
			}
		})
	}
}

// Spec: §7.3.1 (Unapplied-visibility warning) — a layer that sets public is
// visible to every caller on any registry, so its registration carries no
// warnings member, whether the request granted public or the deployment
// default stamped it.
func TestLayerRegister_PublicLayerCarriesNoWarning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		defaultVis string
		body       map[string]any
	}{
		{"explicit public grant", "", map[string]any{"public": true, "groups": []string{"acme-eng"}}},
		{"public default", "public", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, _ := newUnappliedVisibilityHarness(t, tc.defaultVis)
			body := map[string]any{"id": "team", "source_type": "local", "local_path": "/tmp/team"}
			for k, v := range tc.body {
				body[k] = v
			}
			resp, out := mustPost(t, base, "/v1/layers", body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("register status = %d, want 201; body=%s", resp.StatusCode, out)
			}
			if warnings, ok := layerWarnings(t, out); ok {
				t.Errorf("warnings = %v, want the member omitted for a public layer", warnings)
			}
		})
	}
}

// Spec: §7.3.1 (Unapplied-visibility warning) — an update whose resulting
// layer sets no public grant carries the warning, including an update that
// stores nothing new, and an update that grants public carries none.
func TestLayerUpdate_WarnsWhenVisibilityIsNotApplied(t *testing.T) {
	t.Parallel()
	base, _ := newUnappliedVisibilityHarness(t, "")
	resp, out := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team", "source_type": "local", "local_path": "/tmp/team", "public": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d; body=%s", resp.StatusCode, out)
	}

	resp, out = putJSON(t, base, "/v1/layers/update?id=team", map[string]any{"public": false, "groups": []string{"acme-eng"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d; body=%s", resp.StatusCode, out)
	}
	warnings, ok := layerWarnings(t, out)
	if !ok || len(warnings) != 1 || !strings.Contains(warnings[0], `groups: ["acme-eng"]`) {
		t.Errorf("narrowing update warnings = %v (present %v), want the groups warning", warnings, ok)
	}

	// A body restating the stored layer stores nothing and still reports it.
	resp, out = putJSON(t, base, "/v1/layers/update?id=team", map[string]any{"groups": []string{"acme-eng"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-op update status = %d; body=%s", resp.StatusCode, out)
	}
	if warnings, ok := layerWarnings(t, out); !ok || len(warnings) != 1 {
		t.Errorf("no-op update warnings = %v (present %v), want one warning", warnings, ok)
	}

	resp, out = putJSON(t, base, "/v1/layers/update?id=team", map[string]any{"public": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("widening update status = %d; body=%s", resp.StatusCode, out)
	}
	if warnings, ok := layerWarnings(t, out); ok {
		t.Errorf("widening update warnings = %v, want the member omitted", warnings)
	}
}

// Spec: §7.3.1 (Unapplied-visibility warning) — restoring a soft-deleted
// layer that sets no public grant carries the warning beside the restored
// ID, and restoring a public layer carries none.
// Spec: §8.4
func TestLayerRestore_WarnsWhenVisibilityIsNotApplied(t *testing.T) {
	t.Parallel()
	base, _ := newUnappliedVisibilityHarness(t, "")
	for _, l := range []map[string]any{
		{"id": "alice-personal", "source_type": "local", "local_path": "/tmp/a", "user_defined": true, "owner": "alice@acme.com"},
		{"id": "open", "source_type": "local", "local_path": "/tmp/o", "public": true},
	} {
		if resp, out := mustPost(t, base, "/v1/layers", l); resp.StatusCode != http.StatusCreated {
			t.Fatalf("register %v status = %d; body=%s", l["id"], resp.StatusCode, out)
		}
		if resp, out := mustDelete(t, base, "/v1/layers?id="+l["id"].(string)); resp.StatusCode != http.StatusOK {
			t.Fatalf("unregister %v status = %d; body=%s", l["id"], resp.StatusCode, out)
		}
	}

	resp, out := mustPost(t, base, "/v1/layers/restore?id=alice-personal", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore status = %d; body=%s", resp.StatusCode, out)
	}
	warnings, ok := layerWarnings(t, out)
	if !ok || len(warnings) != 1 || !strings.Contains(warnings[0], `layer "alice-personal" declares visibility users: ["alice@acme.com"]`) {
		t.Errorf("restore warnings = %v (present %v), want the users warning; body=%s", warnings, ok, out)
	}
	var restored struct {
		Restored string `json:"restored"`
	}
	if err := json.Unmarshal(out, &restored); err != nil || restored.Restored != "alice-personal" {
		t.Errorf("restored = %q (%v), want alice-personal", restored.Restored, err)
	}

	resp, out = mustPost(t, base, "/v1/layers/restore?id=open", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore public status = %d; body=%s", resp.StatusCode, out)
	}
	if warnings, ok := layerWarnings(t, out); ok {
		t.Errorf("public restore warnings = %v, want the member omitted", warnings)
	}
}

// Spec: §7.3.1 (Unapplied-visibility warning) — a registry that wires a
// request-time identity verifier applies visibility, so its register,
// update, and restore responses carry no warnings member for a restricted
// layer.
func TestLayerWrites_NoWarningWithAVerifier(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// The resolver stands in for a verifier: it resolves the caller as the
	// tenant admin, which the admin callback admits.
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithDefaultVisibility("private").
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
			return layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}, nil
		})
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	resp, out := mustPost(t, ts.URL, "/v1/layers", map[string]any{
		"id": "team", "source_type": "local", "local_path": "/tmp/team", "groups": []string{"acme-eng"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d; body=%s", resp.StatusCode, out)
	}
	if warnings, ok := layerWarnings(t, out); ok {
		t.Errorf("register warnings = %v, want the member omitted", warnings)
	}
	resp, out = putJSON(t, ts.URL, "/v1/layers/update?id=team", map[string]any{"users": []string{"bob@acme.com"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d; body=%s", resp.StatusCode, out)
	}
	if warnings, ok := layerWarnings(t, out); ok {
		t.Errorf("update warnings = %v, want the member omitted", warnings)
	}
	if resp, out := mustDelete(t, ts.URL, "/v1/layers?id=team"); resp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status = %d; body=%s", resp.StatusCode, out)
	}
	resp, out = mustPost(t, ts.URL, "/v1/layers/restore?id=team", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore status = %d; body=%s", resp.StatusCode, out)
	}
	if warnings, ok := layerWarnings(t, out); ok {
		t.Errorf("restore warnings = %v, want the member omitted", warnings)
	}
}

// Spec: §7.3.1 (Unapplied-visibility warning) — a public-mode registry refuses
// an identity provider and admits a write only with the operator token, so its
// warning names public mode, says the declaration takes effect once public
// mode is turned off and a provider is configured, and does not say every
// caller can change the layer.
func TestLayerRegister_PublicModeWarning(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	const token = "operator-token-0123456789-abcdefghij"
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithPublicMode(token).
		WithUnappliedVisibility()
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	register := func(id string, extra map[string]any) []string {
		t.Helper()
		body := map[string]any{"id": id, "source_type": "local", "local_path": "/tmp/" + id}
		for k, v := range extra {
			body[k] = v
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/layers", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Podium-Operator-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/layers: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		out, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("register %s status = %d; body=%s", id, resp.StatusCode, out)
		}
		warnings, ok := layerWarnings(t, out)
		if !ok || len(warnings) != 1 {
			t.Fatalf("register %s warnings = %v (present %v), want one", id, warnings, ok)
		}
		return warnings
	}

	got := register("team", map[string]any{"groups": []string{"acme-eng"}})[0]
	want := `layer "team" declares visibility groups: ["acme-eng"], but this registry runs in public mode and applies no layer visibility, so every caller sees the layer; the declaration takes effect once public mode is turned off and an identity provider is configured`
	if got != want {
		t.Errorf("warning = %q, want %q", got, want)
	}
	got = register("sealed", nil)[0]
	want = `layer "sealed" is private, but this registry runs in public mode and applies no layer visibility, so every caller sees the layer; the private declaration applies once public mode is turned off and an identity provider is added`
	if got != want {
		t.Errorf("private warning = %q, want %q", got, want)
	}
}
