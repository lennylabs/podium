package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// Spec: §6.3 / RFC 6749 §6 — Refresh exchanges a refresh_token
// for a fresh access_token. The IdP may rotate the refresh
// token; when it doesn't, the prior refresh token carries
// through.
func TestRefresh_RotatesAccessTokenAndKeepsRefresh(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","token_type":"Bearer","expires_in":3600}`))
	}))
	defer ts.Close()
	flow := identity.DeviceCodeFlow{TokenURL: ts.URL, ClientID: "podium-cli"}
	got, err := flow.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got.AccessToken != "new-access" {
		t.Errorf("AccessToken = %q, want new-access", got.AccessToken)
	}
	if got.RefreshToken != "old-refresh" {
		t.Errorf("RefreshToken = %q, want passthrough of old-refresh", got.RefreshToken)
	}
}

// Spec: §6.3 — when the IdP rotates the refresh token, the new
// value replaces the old one in Tokens.RefreshToken.
func TestRefresh_PicksUpRotatedRefreshToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"rotated","token_type":"Bearer"}`))
	}))
	defer ts.Close()
	flow := identity.DeviceCodeFlow{TokenURL: ts.URL, ClientID: "x"}
	got, err := flow.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got.RefreshToken != "rotated" {
		t.Errorf("RefreshToken = %q, want rotated", got.RefreshToken)
	}
}

// Spec: §6.3 — invalid_grant maps to ErrAccessDenied so callers
// drop the cached token and drive the user through Initiate.
func TestRefresh_InvalidGrantMapsToAccessDenied(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh expired"}`))
	}))
	defer ts.Close()
	flow := identity.DeviceCodeFlow{TokenURL: ts.URL, ClientID: "x"}
	_, err := flow.Refresh(context.Background(), "expired-refresh")
	if !errors.Is(err, identity.ErrAccessDenied) {
		t.Errorf("err = %v, want ErrAccessDenied", err)
	}
}

// Spec: §6.3 — Refresh requires both TokenURL and a non-empty
// refresh token.
func TestRefresh_RequiresFlowConfig(t *testing.T) {
	flow := identity.DeviceCodeFlow{ClientID: "x"}
	_, err := flow.Refresh(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "TokenURL") {
		t.Errorf("missing TokenURL: err = %v", err)
	}
	flow.TokenURL = "http://example"
	_, err = flow.Refresh(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "refresh_token") {
		t.Errorf("missing refresh_token: err = %v", err)
	}
}
