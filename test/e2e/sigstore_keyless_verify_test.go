package e2e

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// keylessFixture is the trust material, envelopes, and counting fake
// Sigstore server one keyless CLI test drives the podium binary against.
type keylessFixture struct {
	files    sigstoreharness.Files
	srvURL   string
	requests *atomic.Int64
}

// newKeylessFixture writes the harness trusted root and envelopes to a
// temporary directory and starts a fake Fulcio, Rekor, and timestamp
// authority that counts every request it receives.
func newKeylessFixture(t *testing.T) keylessFixture {
	t.Helper()
	h := sigstoreharness.New(t)
	var requests atomic.Int64
	srv := h.FakeServer(t, sigstoreharness.WithRequestCounter(&requests))
	return keylessFixture{
		files:    h.WriteFiles(t, t.TempDir()),
		srvURL:   srv.URL,
		requests: &requests,
	}
}

// envelope returns the contents of the named envelope file, which every
// case passes as the --signature value.
func (f keylessFixture) envelope(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(f.files.Envelopes[name])
	if err != nil {
		t.Fatalf("read envelope %s: %v", name, err)
	}
	return string(body)
}

// env sets every PODIUM_SIGSTORE_* variable explicitly. mergeEnv does not
// scrub that prefix, so a developer shell carrying the live-smoke variables
// would otherwise leak into the subprocess. The three endpoints point at the
// counting server, because an empty URL takes its public-good default.
// overrides replaces the defaults below by key.
func (f keylessFixture) env(overrides map[string]string) []string {
	vars := map[string]string{
		"PODIUM_SIGSTORE_TRUSTED_ROOT_FILE":   f.files.TrustedRoot,
		"PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE": "",
		"PODIUM_SIGSTORE_FULCIO_URL":          f.srvURL,
		"PODIUM_SIGSTORE_REKOR_URL":           f.srvURL,
		"PODIUM_SIGSTORE_TSA_URL":             f.srvURL + "/api/v1/timestamp",
		"PODIUM_SIGSTORE_OIDC_TOKEN":          "",
		"PODIUM_SIGSTORE_CERT_IDENTITY":       sigstoreharness.DefaultSAN,
		"PODIUM_SIGSTORE_CERT_OIDC_ISSUER":    sigstoreharness.DefaultIssuer,
		"PODIUM_SIGSTORE_REQUEST_TIMEOUT":     "",
		"PODIUM_SIGNATURE_PROVIDER":           "",
	}
	for k, v := range overrides {
		vars[k] = v
	}
	out := make([]string, 0, len(vars))
	for k, v := range vars {
		out = append(out, k+"="+v)
	}
	return out
}

// Spec: §4.7.9, §6.2 — podium verify --provider sigstore-keyless accepts an
// envelope whose timestamp, inclusion proof, entry binding, chain, and leaf
// identity hold against the trusted_root.json at
// PODIUM_SIGSTORE_TRUSTED_ROOT_FILE and the identity policy in
// PODIUM_SIGSTORE_CERT_IDENTITY and PODIUM_SIGSTORE_CERT_OIDC_ISSUER, and
// refuses otherwise. The harness clock is 2026-01-15, so every accepted leaf
// has expired on the wall clock: acceptance shows the chain check runs at the
// attested time. Verification is offline, so the counting server sees no
// request in any case.
func TestPodiumVerify_SigstoreKeyless(t *testing.T) {
	t.Parallel()
	f := newKeylessFixture(t)
	cases := []struct {
		name      string
		envelope  string
		overrides map[string]string
		wantExit  int
		want      []string
	}{
		{
			name:     "matching identity and issuer",
			envelope: sigstoreharness.EnvelopeValid,
			want:     []string{"verify ok"},
		},
		{
			name:      "identity list",
			envelope:  sigstoreharness.EnvelopeValid,
			overrides: map[string]string{"PODIUM_SIGSTORE_CERT_IDENTITY": "bob@acme.com, alice@acme.com"},
			want:      []string{"verify ok"},
		},
		{
			name:     "identity mismatch",
			envelope: sigstoreharness.EnvelopeForeignSAN,
			wantExit: 1,
			want:     []string{"verify failed", "certificate identity mismatch"},
		},
		{
			name:      "identity empty",
			envelope:  sigstoreharness.EnvelopeValid,
			overrides: map[string]string{"PODIUM_SIGSTORE_CERT_IDENTITY": ""},
			wantExit:  1,
			want:      []string{"verify failed", "PODIUM_SIGSTORE_CERT_IDENTITY"},
		},
		{
			name:      "issuer empty",
			envelope:  sigstoreharness.EnvelopeValid,
			overrides: map[string]string{"PODIUM_SIGSTORE_CERT_OIDC_ISSUER": ""},
			wantExit:  1,
			want:      []string{"verify failed", "PODIUM_SIGSTORE_CERT_OIDC_ISSUER"},
		},
		{
			name:     "pre-change envelope",
			envelope: sigstoreharness.EnvelopeLegacy,
			wantExit: 1,
			want:     []string{"verify failed", "no transparency-log entry"},
		},
		{
			name:     "only the removed PEM variable",
			envelope: sigstoreharness.EnvelopeValid,
			overrides: map[string]string{
				"PODIUM_SIGSTORE_TRUSTED_ROOT_FILE":   "",
				"PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE": f.files.TrustedRoot,
			},
			wantExit: 1,
			want:     []string{"verify failed", "no trust root configured"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := runPodium(t, "", f.env(c.overrides), "verify", "--provider", "sigstore-keyless",
				"--content-hash", f.files.ContentHash, "--signature", f.envelope(t, c.envelope))
			if res.Exit != c.wantExit {
				t.Fatalf("exit = %d, want %d\nstderr: %s", res.Exit, c.wantExit, res.Stderr)
			}
			for _, want := range c.want {
				if !strings.Contains(res.Stderr, want) {
					t.Errorf("stderr %q does not contain %q", res.Stderr, want)
				}
			}
			if n := f.requests.Load(); n != 0 {
				t.Errorf("verify sent %d requests to the Sigstore endpoints, want 0", n)
			}
		})
	}
}

// Spec: §4.7.9, §6.2 — podium sign --provider sigstore-keyless with every
// endpoint set and PODIUM_SIGSTORE_OIDC_TOKEN empty refuses with
// "sigstore-keyless not configured" before any network call.
func TestPodiumSign_SigstoreKeylessWithoutOIDCTokenRefused(t *testing.T) {
	t.Parallel()
	f := newKeylessFixture(t)
	res := runPodium(t, "", f.env(nil), "sign", "--provider", "sigstore-keyless",
		"--content-hash", f.files.ContentHash)
	if res.Exit != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "sigstore-keyless not configured") {
		t.Errorf("stderr %q does not contain %q", res.Stderr, "sigstore-keyless not configured")
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("sign sent %d requests to the Sigstore endpoints, want 0", n)
	}
}
