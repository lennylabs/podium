package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §7.3.4 — the posture read's body, its unauthenticated status, and the
// fields it carries and does not carry.

func posture(t *testing.T, p server.SessionPosture) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, server.PathWebUISession, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// Spec: §7.3.4 — the read requires no credential, reports the deployment's
// posture, and carries no field the statement does not name.
func TestSessionPosture_Body(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   server.SessionPosture
		want map[string]any
	}{
		{
			name: "no identity provider",
			in:   server.SessionPosture{},
			want: map[string]any{
				"identity_provider_configured": false,
				"public_mode":                  false,
				"browser_auth":                 map[string]any{"enabled": false},
				"layer_capabilities":           map[string]any{"manage_any_layer": false},
			},
		},
		{
			name: "public mode",
			in:   server.SessionPosture{PublicMode: true},
			want: map[string]any{
				"identity_provider_configured": false,
				"public_mode":                  true,
				"browser_auth":                 map[string]any{"enabled": false},
				"layer_capabilities":           map[string]any{"manage_any_layer": false},
			},
		},
		{
			name: "browser flow enabled",
			in: server.SessionPosture{
				IdentityProviderConfigured: true,
				BrowserAuthEnabled:         true,
			},
			want: map[string]any{
				"identity_provider_configured": true,
				"public_mode":                  false,
				"browser_auth": map[string]any{
					"enabled": true,
					// The paths the mux registers, so no authentication route
					// path is spelled inside the bundle.
					"sign_in_path":  server.PathWebUISignIn,
					"sign_out_path": server.PathWebUISignOut,
				},
				"layer_capabilities": map[string]any{"manage_any_layer": false},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := posture(t, tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("body = %v, want exactly the fields %v", got, tc.want)
			}
			for k, v := range tc.want {
				switch want := v.(type) {
				case map[string]any:
					sub, ok := got[k].(map[string]any)
					if !ok || len(sub) != len(want) {
						t.Fatalf("%s = %v, want exactly %v", k, got[k], want)
					}
					for sk, sv := range want {
						if sub[sk] != sv {
							t.Errorf("%s.%s = %v, want %v", k, sk, sub[sk], sv)
						}
					}
				default:
					if got[k] != v {
						t.Errorf("%s = %v, want %v", k, got[k], v)
					}
				}
			}
			if _, ok := got["subject"]; ok {
				t.Error("a request that resolves no subject carries no subject field")
			}
		})
	}
}

// Spec: §7.3.4 — layer_capabilities and its member are always present, and a
// read whose capability seam is not wired reports the member false.
//
// D11: the closed default lives here rather than at the callers because
// NewLayerEndpoint installs an admitting authAdmin by default
// (pkg/registry/server/layers.go), so a reporting surface that inherited that
// default would over-report on every deployment that wires no admin arm.
func TestSessionPosture_LayerCapabilitiesDefaultClosed(t *testing.T) {
	t.Parallel()
	caps, ok := posture(t, server.SessionPosture{})["layer_capabilities"].(map[string]any)
	if !ok {
		t.Fatal("layer_capabilities absent from a posture read with no capability seam")
	}
	if len(caps) != 1 || caps["manage_any_layer"] != false {
		t.Errorf("layer_capabilities = %v, want exactly manage_any_layer false", caps)
	}
}

// Spec: §7.3.4 — the handler serializes what the seam reports rather than
// recomputing it, so a seam that admits the caller is reported as admitting.
func TestSessionPosture_LayerCapabilitiesFromSeam(t *testing.T) {
	t.Parallel()
	body := posture(t, server.SessionPosture{
		Capabilities: func(*http.Request) server.LayerCapabilities {
			return server.LayerCapabilities{ManageAnyLayer: true}
		},
	})
	caps, ok := body["layer_capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("layer_capabilities = %v, want an object", body["layer_capabilities"])
	}
	if caps["manage_any_layer"] != true {
		t.Errorf("manage_any_layer = %v, want the value the seam reported", caps["manage_any_layer"])
	}
	// The read still resolves no subject, so the capability is reported
	// independently of whether one resolves.
	if _, ok := body["subject"]; ok {
		t.Error("a request that resolves no subject carries no subject field")
	}
}

// Spec: §7.3.4 — subject is present only when one resolves. An unverifiable
// session resolves the anonymous caller through the same resolver the layer
// endpoint uses, so the read answers 200 and omits subject.
func TestSessionPosture_Subject(t *testing.T) {
	t.Parallel()
	authenticated := server.SessionPosture{
		IdentityProviderConfigured: true,
		BrowserAuthEnabled:         true,
		Identity: func(*http.Request) layer.Identity {
			return layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}
		},
	}
	if got := posture(t, authenticated)["subject"]; got != "alice@acme.com" {
		t.Errorf("subject = %v, want the resolved subject", got)
	}
	anonymous := server.SessionPosture{
		IdentityProviderConfigured: true,
		Identity: func(*http.Request) layer.Identity {
			return layer.Identity{IsPublic: true}
		},
	}
	if _, ok := posture(t, anonymous)["subject"]; ok {
		t.Error("an anonymous caller resolved a subject field")
	}
}

// Spec: §7.3.4 — the read answers on GET.
func TestSessionPosture_MethodRefusal(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	server.SessionPosture{}.Handler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, server.PathWebUISession, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
