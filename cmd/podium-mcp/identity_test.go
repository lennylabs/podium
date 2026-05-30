package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// mockIdP serves the RFC 8628 device-authorization and token endpoints so a
// test drives the §6.3 oauth-device-code flow without a real IdP.
func mockIdP(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"DC","user_code":"WXYZ-1234",` +
			`"verification_uri":"https://idp.acme.com/activate","interval":1,"expires_in":300}`))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"AT-123","token_type":"Bearer","expires_in":900}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §6.2 / §6.3 — bearerToken selects the credential per
// PODIUM_IDENTITY_PROVIDER. F-6.3.3.
func TestBearerToken_ProviderSelection(t *testing.T) {
	t.Parallel()

	t.Run("injected-session-token reads the env/file token", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, &config{identityProvider: "injected-session-token", sessionToken: "injected-abc"})
		tok, err := s.bearerToken()
		if err != nil || tok != "injected-abc" {
			t.Errorf("tok=%q err=%v, want injected-abc", tok, err)
		}
	})

	t.Run("oauth-device-code without IdP falls back to injected token", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, &config{identityProvider: "oauth-device-code", sessionToken: "fallback-tok"})
		tok, err := s.bearerToken()
		if err != nil || tok != "fallback-tok" {
			t.Errorf("tok=%q err=%v, want fallback-tok", tok, err)
		}
	})

	t.Run("oauth-device-code uses a cached keychain token", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, &config{
			identityProvider:  "oauth-device-code",
			oauthAuthEndpoint: "https://idp.acme.com/device", // configured, but not reached
			registry:          "https://reg.acme.com",
		})
		store := identity.NewMemoryStore()
		_ = store.Save("https://reg.acme.com", "cached-AT")
		s.tokens = store
		tok, err := s.bearerToken()
		if err != nil || tok != "cached-AT" {
			t.Errorf("tok=%q err=%v, want cached-AT (no flow)", tok, err)
		}
	})

	t.Run("unknown provider label reads the injected token", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, &config{identityProvider: "oidc", sessionToken: "label-tok"})
		tok, err := s.bearerToken()
		if err != nil || tok != "label-tok" {
			t.Errorf("tok=%q err=%v, want label-tok", tok, err)
		}
	})
}

// Spec: §6.3 / §14 — with an IdP configured and no cached token, the bridge
// runs the device flow, surfaces the URL + code via MCP elicitation, and
// caches the resulting token in the keychain. F-6.3.3.
func TestDeviceCodeToken_RunsFlowElicitsAndCaches(t *testing.T) {
	t.Parallel()
	idp := mockIdP(t)
	s := newTestServer(t, &config{
		identityProvider:  "oauth-device-code",
		oauthAuthEndpoint: idp.URL + "/device",
		oauthTokenURL:     idp.URL + "/token",
		oauthClientID:     "podium-cli",
		registry:          "https://reg.acme.com",
	})
	tokenStore := identity.NewMemoryStore()
	s.tokens = tokenStore
	var elicit bytes.Buffer
	s.out = json.NewEncoder(&elicit)

	tok, err := s.bearerToken()
	if err != nil {
		t.Fatalf("bearerToken: %v", err)
	}
	if tok != "AT-123" {
		t.Errorf("tok = %q, want AT-123", tok)
	}
	// Token cached for reuse by later bridge processes.
	if cached, err := tokenStore.Load("https://reg.acme.com"); err != nil || cached != "AT-123" {
		t.Errorf("cached = %q err = %v, want AT-123", cached, err)
	}
	// Elicitation surfaced the verification URL + user code.
	out := elicit.String()
	if !strings.Contains(out, "elicitation/create") {
		t.Errorf("no elicitation/create emitted: %s", out)
	}
	if !strings.Contains(out, "WXYZ-1234") || !strings.Contains(out, "idp.acme.com/activate") {
		t.Errorf("elicitation missing URL/code: %s", out)
	}

	// A second call reuses the cached token without re-eliciting.
	elicit.Reset()
	tok2, err := s.bearerToken()
	if err != nil || tok2 != "AT-123" {
		t.Errorf("second call tok=%q err=%v", tok2, err)
	}
	if strings.Contains(elicit.String(), "elicitation/create") {
		t.Errorf("second call re-elicited: %s", elicit.String())
	}
}
