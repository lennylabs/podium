package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLoginCmd_PollBoundedByDeadline exercises the client-side 10-minute
// login deadline. spec: §6.3 / §7.7 — CLI commands "poll the
// IdP's token endpoint until the user completes the flow or a 10-minute
// timeout elapses". An IdP that keeps replying authorization_pending must
// not leave `podium login` blocked indefinitely; loginCmd bounds the whole
// flow with context.WithTimeout(loginTimeout), so the command terminates
// (exit 1) at the deadline instead of hanging.
func TestLoginCmd_PollBoundedByDeadline(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dc-123",
			"user_code":        "WXYZ-1234",
			"verification_uri": "https://idp.acme/activate",
			"expires_in":       600,
			"interval":         1,
		})
	})
	// The token endpoint never completes: it always reports the user has
	// not yet finished, which without a client deadline loops forever.
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Shrink the deadline so the test does not actually wait 10 minutes;
	// the production default is 10*time.Minute.
	prev := loginTimeout
	loginTimeout = 80 * time.Millisecond
	t.Cleanup(func() { loginTimeout = prev })

	// Neutralize env so flags fully control the flow.
	for _, k := range []string{
		"PODIUM_REGISTRY", "PODIUM_OAUTH_AUTHORIZATION_ENDPOINT",
		"PODIUM_OAUTH_TOKEN_URL", "PODIUM_OAUTH_AUDIENCE",
	} {
		t.Setenv(k, "")
	}

	done := make(chan int, 1)
	go func() {
		// A remote https registry needs auth; --issuer/--token-url skip
		// IdP discovery so the registry host is never contacted.
		done <- loginCmd([]string{
			"--registry", "https://podium.acme.com",
			"--issuer", srv.URL + "/device",
			"--token-url", srv.URL + "/token",
			"--no-browser",
		})
	}()

	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("login exit code = %d, want 1 (timed out)", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login did not honor the client-side deadline; it blocked past loginTimeout")
	}
}
