package e2e

// End-to-end coverage for the §7.3.4 posture read and the §6.3.4
// browser-origin gate, driven through the real podium binary. The mount
// predicate and the gate's installation both live in the boot wiring, which
// runs only inside the spawned process, so a package test cannot reach them.
// Both binaries here configure no identity provider, which is the stack fact
// that fixes the status of a path the registry does not register.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Spec: §7.3.4 — a registry started with --web-ui and no browser flow serves
// the posture read with browser_auth.enabled false, and one started without
// --web-ui never registers the path.
func TestServerFlags_WebUISessionPostureRead(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--web-ui", "--layer-path", reg)

	st, body := getRaw(t, srv.BaseURL+"/v1/ui/session")
	if st != 200 {
		t.Fatalf("GET /v1/ui/session = %d, want 200\nlog:\n%s", st, srv.log())
	}
	var posture struct {
		IdentityProviderConfigured bool `json:"identity_provider_configured"`
		PublicMode                 bool `json:"public_mode"`
		BrowserAuth                struct {
			Enabled     bool   `json:"enabled"`
			SignInPath  string `json:"sign_in_path"`
			SignOutPath string `json:"sign_out_path"`
		} `json:"browser_auth"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(body, &posture); err != nil {
		t.Fatalf("decode posture: %v\nbody: %s", err, body)
	}
	if posture.IdentityProviderConfigured || posture.PublicMode {
		t.Errorf("posture = %+v, want a standalone registry with no identity provider", posture)
	}
	if posture.BrowserAuth.Enabled {
		t.Error("browser_auth.enabled is true on a registry started without --web-ui-auth")
	}
	if posture.BrowserAuth.SignInPath != "" || posture.BrowserAuth.SignOutPath != "" {
		t.Errorf("browser_auth = %+v, want no path fields where the routes are unregistered", posture.BrowserAuth)
	}
	if posture.Subject != "" {
		t.Errorf("subject = %q, want none for an uncredentialed request", posture.Subject)
	}

	// The authentication routes are unregistered on this deployment, so the
	// request falls to the catch-all.
	if st := getStatus(t, srv.BaseURL+"/v1/ui/auth/sign-in"); st != 404 {
		t.Errorf("GET /v1/ui/auth/sign-in = %d, want 404 with the flow disabled", st)
	}
}

// Spec: §7.3.4 — the posture read is mounted on the web UI alone, so a
// registry started without --web-ui answers the path as it answers any path
// it does not register.
func TestServerFlags_NoWebUINoPostureRead(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/v1/ui/session"); st != 404 {
		t.Errorf("GET /v1/ui/session = %d without --web-ui, want 404", st)
	}
}

// Spec: §6.3.4 — the browser-origin gate is installed over the boot mux, so a
// cross-site layer write is refused before the handler runs even on a
// registry that enables no browser flow, and a write carrying no such
// evidence is admitted.
func TestServerFlags_BrowserOriginGateCoversLayerWrites(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"my-skill/ARTIFACT.md": smallteamLowArtifact("ui artifact"),
	})
	srv := startServer(t, reg)

	forged, err := http.NewRequest(http.MethodDelete, srv.BaseURL+"/v1/layers/does-not-exist", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	forged.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := httpClient.Do(forged)
	if err != nil {
		t.Fatalf("forged DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forged cross-site DELETE = %d, want 403\nlog:\n%s", resp.StatusCode, srv.log())
	}
	var env struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != "auth.csrf_invalid" {
		t.Errorf("code = %q, want auth.csrf_invalid", env.Code)
	}

	// The same write carrying no browser-origin evidence, which is what a CLI
	// sends, reaches the handler and takes the route's own outcome.
	plain, err := http.NewRequest(http.MethodDelete, srv.BaseURL+"/v1/layers/does-not-exist", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	admitted, err := httpClient.Do(plain)
	if err != nil {
		t.Fatalf("plain DELETE: %v", err)
	}
	defer admitted.Body.Close()
	if admitted.StatusCode == http.StatusForbidden {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(admitted.Body).Decode(&e)
		if e.Code == "auth.csrf_invalid" {
			t.Error("a write carrying no browser-origin evidence was refused by the gate")
		}
	}
}
