package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
)

// jwtWithExp builds a minimal unsigned JWT carrying only an exp claim, so a
// test can exercise the bridge's local expiry detection without a real IdP.
func jwtWithExp(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	return header + "." + payload + ".sig"
}

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
// PODIUM_IDENTITY_PROVIDER.
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
// caches the resulting token in the keychain.
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

// Spec: §6.9 — accessTokenExpired drives the "Auth token expired
// (oauth-device-code)" trigger. A JWT past its exp is expired; an opaque
// (non-JWT) token, or one without a numeric exp, cannot be evaluated locally
// and is treated as not-expired so it is used as-is.
func TestAccessTokenExpired(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"opaque non-jwt", "AT-opaque", false},
		{"empty", "", false},
		{"two segments", "a.b", false},
		{"malformed payload", "h.@@@.s", false},
		{"no exp claim", jwtWithExp(0), false},
		{"future exp", jwtWithExp(now.Unix() + 3600), false},
		{"past exp", jwtWithExp(now.Unix() - 3600), true},
		{"within skew window", jwtWithExp(now.Unix() + 10), true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := accessTokenExpired(tc.token, now); got != tc.want {
				t.Errorf("accessTokenExpired(%q) = %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}

// Spec: §6.9 "Auth token expired (oauth-device-code): Trigger refresh". A
// cached access token past its exp is silently refreshed with the stored
// refresh token; the renewed token is returned and persisted, and no
// interactive device-flow elicitation is emitted.
func TestDeviceCodeToken_RefreshesExpiredAccessToken(t *testing.T) {
	t.Parallel()
	var sawRefreshGrant bool
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") == "refresh_token" && r.FormValue("refresh_token") == "RT-old" {
			sawRefreshGrant = true
			_, _ = w.Write([]byte(`{"access_token":"AT-new","refresh_token":"RT-new","token_type":"Bearer","expires_in":900}`))
			return
		}
		t.Errorf("unexpected token request: %v", r.Form)
		w.WriteHeader(http.StatusBadRequest)
	})
	mux.HandleFunc("/device", func(http.ResponseWriter, *http.Request) {
		t.Error("device flow ran; a valid refresh token should renew silently")
	})
	idp := httptest.NewServer(mux)
	t.Cleanup(idp.Close)

	s := newTestServer(t, &config{
		identityProvider:  "oauth-device-code",
		oauthAuthEndpoint: idp.URL + "/device",
		oauthTokenURL:     idp.URL + "/token",
		oauthClientID:     "podium-cli",
		registry:          "https://reg.acme.com",
	})
	store := identity.NewMemoryStore()
	_ = store.Save("https://reg.acme.com", jwtWithExp(time.Now().Unix()-60)) // expired
	_ = store.Save(identity.RefreshLabel("https://reg.acme.com"), "RT-old")
	s.tokens = store
	var elicit bytes.Buffer
	s.out = json.NewEncoder(&elicit)

	tok, err := s.bearerToken()
	if err != nil {
		t.Fatalf("bearerToken: %v", err)
	}
	if tok != "AT-new" {
		t.Errorf("tok = %q, want AT-new (refreshed)", tok)
	}
	if !sawRefreshGrant {
		t.Error("the IdP token endpoint never saw a refresh_token grant")
	}
	if strings.Contains(elicit.String(), "elicitation/create") {
		t.Errorf("silent refresh must not elicit: %s", elicit.String())
	}
	// Renewed access and rotated refresh tokens are persisted for reuse.
	if got, _ := store.Load("https://reg.acme.com"); got != "AT-new" {
		t.Errorf("cached access = %q, want AT-new", got)
	}
	if got, _ := store.Load(identity.RefreshLabel("https://reg.acme.com")); got != "RT-new" {
		t.Errorf("cached refresh = %q, want RT-new (rotated)", got)
	}
}

// Spec: §6.9 — "if interactive refresh required, surface ... via MCP
// elicitation". When the refresh token is rejected (revoked / expired), the
// bridge falls back to the interactive device flow and elicits the
// verification URL + code.
func TestDeviceCodeToken_ReauthsWhenRefreshRejected(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"DC","user_code":"WXYZ-1234",` +
			`"verification_uri":"https://idp.acme.com/activate","interval":1,"expires_in":300}`))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") == "refresh_token" {
			// Refresh rejected → caller must drive interactive reauth.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"access_denied"}`))
			return
		}
		// device_code grant → the user "completed" the flow.
		_, _ = w.Write([]byte(`{"access_token":"AT-reauth","token_type":"Bearer","expires_in":900}`))
	})
	idp := httptest.NewServer(mux)
	t.Cleanup(idp.Close)

	s := newTestServer(t, &config{
		identityProvider:  "oauth-device-code",
		oauthAuthEndpoint: idp.URL + "/device",
		oauthTokenURL:     idp.URL + "/token",
		oauthClientID:     "podium-cli",
		registry:          "https://reg.acme.com",
	})
	store := identity.NewMemoryStore()
	_ = store.Save("https://reg.acme.com", jwtWithExp(time.Now().Unix()-60))
	_ = store.Save(identity.RefreshLabel("https://reg.acme.com"), "RT-revoked")
	s.tokens = store
	var elicit bytes.Buffer
	s.out = json.NewEncoder(&elicit)

	tok, err := s.bearerToken()
	if err != nil {
		t.Fatalf("bearerToken: %v", err)
	}
	if tok != "AT-reauth" {
		t.Errorf("tok = %q, want AT-reauth (device flow)", tok)
	}
	out := elicit.String()
	if !strings.Contains(out, "elicitation/create") ||
		!strings.Contains(out, "WXYZ-1234") || !strings.Contains(out, "idp.acme.com/activate") {
		t.Errorf("expected reauth elicitation with URL + code: %s", out)
	}
}
