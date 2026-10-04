package e2e

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
	"github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// unsignedOIDCToken builds a JWT for subject with a placeholder signature.
// podium sign reads only the payload to compute the proof of possession;
// the hanging endpoint and the refused configuration never verify it.
func unsignedOIDCToken(t *testing.T, subject string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]string{"email": subject, "iss": sigstoreharness.DefaultIssuer})
	if err != nil {
		t.Fatalf("token payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// hangingEndpoint starts a server whose handler never answers. The handler
// returns when the client gives up or when the test ends. The release
// channel closes in a cleanup registered after the server's own, so it runs
// first and httptest.Server.Close does not wait on a blocked handler.
func hangingEndpoint(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv.URL
}

// Spec: §4.7.9, §6.2 — podium sign --provider sigstore-keyless sends each
// Sigstore request once under PODIUM_SIGSTORE_REQUEST_TIMEOUT. A Fulcio
// endpoint that accepts the connection and never answers fails the command
// with exit 1 once the deadline passes. The message names the service, the
// endpoint URL, and the deadline, no envelope is printed, and no timestamp
// or Rekor request follows.
func TestPodiumSign_SigstoreKeylessHangingEndpointTimesOut(t *testing.T) {
	t.Parallel()
	f := newKeylessFixture(t)
	hangURL := hangingEndpoint(t)
	env := f.env(map[string]string{
		"PODIUM_SIGSTORE_FULCIO_URL":      hangURL,
		"PODIUM_SIGSTORE_REQUEST_TIMEOUT": "300ms",
		"PODIUM_SIGSTORE_OIDC_TOKEN":      unsignedOIDCToken(t, "alice@acme.com"),
	})
	// Build the binary before the clock starts, so the timed window holds
	// only the signing run and not a cold compile with coverage enabled.
	cmdharness.Bin(t, "podium")

	start := time.Now()
	res := runPodium(t, "", env, "sign", "--provider", "sigstore-keyless",
		"--content-hash", f.files.ContentHash)
	elapsed := time.Since(start)

	if res.Exit != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", res.Exit, res.Stdout, res.Stderr)
	}
	if elapsed >= 15*time.Second {
		t.Errorf("podium sign took %s against a hanging Fulcio, want under 15s", elapsed)
	}
	for _, want := range []string{"sign failed: fulcio:", hangURL, "no response within 300ms"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr %q does not contain %q", res.Stderr, want)
		}
	}
	if res.Stdout != "" {
		t.Errorf("stdout = %q, want empty (no envelope)", res.Stdout)
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("sign sent %d timestamp or Rekor requests after the Fulcio timeout, want 0", n)
	}
}

// Spec: §6.2 — a PODIUM_SIGSTORE_REQUEST_TIMEOUT that does not parse as a
// positive duration refuses podium sign with config.invalid naming the
// variable before it contacts any Sigstore endpoint.
// Matrix: §6.10 (config.invalid)
func TestPodiumSign_SigstoreKeylessInvalidRequestTimeoutRefused(t *testing.T) {
	t.Parallel()
	f := newKeylessFixture(t)
	res := runPodium(t, "", f.env(map[string]string{
		"PODIUM_SIGSTORE_REQUEST_TIMEOUT": "abc",
		"PODIUM_SIGSTORE_OIDC_TOKEN":      unsignedOIDCToken(t, "alice@acme.com"),
	}), "sign", "--provider", "sigstore-keyless", "--content-hash", f.files.ContentHash)
	if res.Exit != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", res.Exit, res.Stdout, res.Stderr)
	}
	for _, want := range []string{"config.invalid", "PODIUM_SIGSTORE_REQUEST_TIMEOUT"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr %q does not contain %q", res.Stderr, want)
		}
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("sign sent %d requests to the Sigstore endpoints, want 0", n)
	}
}
