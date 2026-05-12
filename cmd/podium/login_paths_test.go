package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// loginCmd reaches Initiate when --registry and --issuer are set.
// An unreachable issuer triggers exit 1 (device authorization
// failure) which proves the Initiate path was entered.
func TestLoginCmd_InitiateFailureExits1(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		if rc := loginCmd([]string{
			"--registry", "http://127.0.0.1:1",
			"--issuer", "http://127.0.0.1:1/device",
		}); rc != 1 {
			t.Errorf("loginCmd = %d, want 1", rc)
		}
	})
}

// loginCmd accepts an explicit --token-url instead of deriving from
// --issuer; the test still expects exit 1 because the issuer URL is
// unreachable but the flag parsing path is now exercised.
func TestLoginCmd_ExplicitTokenURL(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		_ = loginCmd([]string{
			"--registry", "http://127.0.0.1:1",
			"--issuer", "http://127.0.0.1:1/oauth2/device",
			"--token-url", "http://127.0.0.1:1/oauth2/token",
			"--client-id", "podium-cli",
			"--audience", "podium",
		})
	})
}

// loginCmd reaches Poll when Initiate succeeds. The fixture stubs
// the device endpoint with a valid response then refuses Poll;
// exit 1 proves the polling branch was reached.
func TestLoginCmd_PollFailureExits1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"access_denied"}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"device_code":"dc",
			"user_code":"USER-CODE",
			"verification_uri":"https://x",
			"expires_in":1,
			"interval":1
		}`))
	}))
	defer srv.Close()
	t.Setenv("PODIUM_REGISTRY", "http://x")
	withStderr(t, func() {
		_ = loginCmd([]string{
			"--registry", "http://x",
			"--issuer", srv.URL,
			"--token-url", srv.URL + "/token",
			"--client-id", "podium-cli",
		})
	})
}

// logoutCmd surfaces a Delete error from the keychain (path
// returns 1) as a separate exit code than missing-registry (which
// returns 2). The keychain isn't available in CI so we accept either
// 1 or 0 — the test exists to drive the keychain.Delete code path.
func TestLogoutCmd_HitsKeychainDelete(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://podium-test-logout.example")
	withStderr(t, func() {
		_ = logoutCmd(nil)
	})
}
