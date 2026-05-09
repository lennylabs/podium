package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/identity"
)

// fakeIdP simulates the device-authorization and token endpoints. The
// next* fields drive the behavior; tests adjust them between requests
// to simulate the user completing or denying the flow.
type fakeIdP struct {
	mu sync.Mutex

	// device-auth response
	deviceCode      string
	userCode        string
	verificationURI string
	interval        int

	// poll behavior: the first N polls return authorization_pending,
	// subsequent polls return tokens / errors.
	polls          int
	pendingPolls   int
	slowdownPolls  int
	terminalError  string
	accessToken    string
	refreshToken   string
}

func (f *fakeIdP) handleDeviceAuth(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := map[string]any{
		"device_code":      f.deviceCode,
		"user_code":        f.userCode,
		"verification_uri": f.verificationURI,
		"expires_in":       600,
		"interval":         f.interval,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeIdP) handleToken(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.polls <= f.pendingPolls {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
		return
	}
	if f.polls <= f.pendingPolls+f.slowdownPolls {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
		return
	}
	if f.terminalError != "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": f.terminalError})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  f.accessToken,
		"refresh_token": f.refreshToken,
		"token_type":    "Bearer",
		"expires_in":    900,
	})
}

func newFakeIdP(t *testing.T, idp *fakeIdP) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/device", idp.handleDeviceAuth)
	mux.HandleFunc("/oauth2/token", idp.handleToken)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// Spec: §6.3 — Initiate POSTs to the device-auth endpoint and decodes
// device_code, user_code, verification_uri, interval.
// Phase: 11
func TestDeviceCodeFlow_Initiate(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	idp := &fakeIdP{
		deviceCode:      "DC-1234",
		userCode:        "ABCD-1234",
		verificationURI: "https://idp.example.com/device",
		interval:        1,
	}
	srv := newFakeIdP(t, idp)
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: srv.URL + "/oauth2/device",
		TokenURL:      srv.URL + "/oauth2/token",
		ClientID:      "podium-cli",
	}
	auth, err := flow.Initiate(context.Background())
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	if auth.DeviceCode != "DC-1234" {
		t.Errorf("DeviceCode = %q", auth.DeviceCode)
	}
	if auth.UserCode != "ABCD-1234" {
		t.Errorf("UserCode = %q", auth.UserCode)
	}
	if !strings.Contains(auth.VerificationURL, "/device") {
		t.Errorf("VerificationURL = %q", auth.VerificationURL)
	}
	if auth.Interval != time.Second {
		t.Errorf("Interval = %v", auth.Interval)
	}
}

// Spec: §6.3 / §6.10 — PollOnce returns ErrAuthorizationPending while
// the user is completing the flow.
// Phase: 11
// Matrix: §6.10 (auth.device_code_pending)
func TestDeviceCodeFlow_PollOncePending(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	idp := &fakeIdP{
		deviceCode: "DC", verificationURI: "https://x", interval: 1,
		pendingPolls: 1, accessToken: "tok",
	}
	srv := newFakeIdP(t, idp)
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: srv.URL + "/oauth2/device",
		TokenURL:      srv.URL + "/oauth2/token",
		ClientID:      "podium-cli",
	}
	auth, err := flow.Initiate(context.Background())
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	_, err = flow.PollOnce(context.Background(), auth)
	if !errors.Is(err, identity.ErrAuthorizationPending) {
		t.Errorf("got %v, want ErrAuthorizationPending", err)
	}
}

// Spec: §6.3 — Poll loops until the IdP returns tokens; pending and
// slow_down responses extend the interval.
// Phase: 11
func TestDeviceCodeFlow_PollSucceedsAfterPending(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	idp := &fakeIdP{
		deviceCode: "DC", verificationURI: "https://x", interval: 1,
		pendingPolls: 1, slowdownPolls: 1, accessToken: "tok-success",
		refreshToken: "rt",
	}
	srv := newFakeIdP(t, idp)
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: srv.URL + "/oauth2/device",
		TokenURL:      srv.URL + "/oauth2/token",
		ClientID:      "podium-cli",
	}
	auth, err := flow.Initiate(context.Background())
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	auth.Interval = 10 * time.Millisecond // speed up the test
	tokens, err := flow.Poll(context.Background(), auth)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if tokens.AccessToken != "tok-success" {
		t.Errorf("AccessToken = %q", tokens.AccessToken)
	}
	if tokens.RefreshToken != "rt" {
		t.Errorf("RefreshToken = %q", tokens.RefreshToken)
	}
}

// Spec: §6.3 — access_denied surfaces as ErrAccessDenied.
// Phase: 11
func TestDeviceCodeFlow_AccessDenied(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	idp := &fakeIdP{
		deviceCode: "DC", verificationURI: "https://x", interval: 1,
		terminalError: "access_denied",
	}
	srv := newFakeIdP(t, idp)
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: srv.URL + "/oauth2/device",
		TokenURL:      srv.URL + "/oauth2/token",
		ClientID:      "podium-cli",
	}
	auth, _ := flow.Initiate(context.Background())
	_, err := flow.PollOnce(context.Background(), auth)
	if !errors.Is(err, identity.ErrAccessDenied) {
		t.Errorf("got %v, want ErrAccessDenied", err)
	}
}

// Spec: §6.3 — expired_token surfaces as ErrExpiredToken.
// Phase: 11
func TestDeviceCodeFlow_ExpiredToken(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	idp := &fakeIdP{
		deviceCode: "DC", verificationURI: "https://x", interval: 1,
		terminalError: "expired_token",
	}
	srv := newFakeIdP(t, idp)
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: srv.URL + "/oauth2/device",
		TokenURL:      srv.URL + "/oauth2/token",
		ClientID:      "podium-cli",
	}
	auth, _ := flow.Initiate(context.Background())
	_, err := flow.PollOnce(context.Background(), auth)
	if !errors.Is(err, identity.ErrExpiredToken) {
		t.Errorf("got %v, want ErrExpiredToken", err)
	}
}

// Spec: §6.3 — Poll honors context cancellation so a CLI Ctrl+C
// returns immediately without one more interval-wait.
// Phase: 11
func TestDeviceCodeFlow_PollContextCanceled(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	idp := &fakeIdP{
		deviceCode: "DC", verificationURI: "https://x", interval: 1,
		pendingPolls: 100, // never completes
	}
	srv := newFakeIdP(t, idp)
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: srv.URL + "/oauth2/device",
		TokenURL:      srv.URL + "/oauth2/token",
		ClientID:      "podium-cli",
	}
	auth, _ := flow.Initiate(context.Background())
	auth.Interval = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := flow.Poll(ctx, auth)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}
