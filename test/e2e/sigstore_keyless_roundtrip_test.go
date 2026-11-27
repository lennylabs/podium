package e2e

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// fakeOIDCToken returns an unsigned JWT whose payload names subject. The
// fake Fulcio does not verify the token, and podium sign reads only the
// subject for the proof of possession.
func fakeOIDCToken(t *testing.T, subject string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"email": subject, "iss": sigstoreharness.DefaultIssuer})
	if err != nil {
		t.Fatalf("token payload: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// Spec: §4.7.9, §6.2 — podium sign --provider sigstore-keyless against
// Fulcio, a timestamp authority, and Rekor v2 prints a version 0.3
// Sigstore bundle that carries the leaf, the log entry, the timestamp, and
// the message signature over the content hash, and podium verify accepts
// that bundle offline. The same bundle is refused under another content
// hash.
func TestPodiumSignVerify_SigstoreKeylessRoundTrip(t *testing.T) {
	t.Parallel()
	f := newKeylessFixture(t)
	env := f.env(map[string]string{"PODIUM_SIGSTORE_OIDC_TOKEN": fakeOIDCToken(t, sigstoreharness.DefaultSAN)})
	signed := runPodium(t, "", env, "sign", "--provider", "sigstore-keyless", "--content-hash", f.files.ContentHash)
	if signed.Exit != 0 {
		t.Fatalf("sign exit = %d\nstderr: %s", signed.Exit, signed.Stderr)
	}
	bundle := strings.TrimSpace(signed.Stdout)
	var parsed struct {
		MediaType            string `json:"mediaType"`
		VerificationMaterial struct {
			Certificate *struct{} `json:"certificate"`
			TLogEntries []struct {
				KindVersion struct {
					Kind    string `json:"kind"`
					Version string `json:"version"`
				} `json:"kindVersion"`
			} `json:"tlogEntries"`
			TimestampVerificationData struct {
				RFC3161Timestamps []struct{} `json:"rfc3161Timestamps"`
			} `json:"timestampVerificationData"`
		} `json:"verificationMaterial"`
		MessageSignature *struct{} `json:"messageSignature"`
	}
	if err := json.Unmarshal([]byte(bundle), &parsed); err != nil {
		t.Fatalf("sign printed no JSON bundle: %v\n%s", err, bundle)
	}
	vm := parsed.VerificationMaterial
	if parsed.MediaType != sigstoreharness.BundleMediaTypeV03 || vm.Certificate == nil || len(vm.TLogEntries) != 1 ||
		vm.TLogEntries[0].KindVersion.Kind != "hashedrekord" || vm.TLogEntries[0].KindVersion.Version != "0.0.2" ||
		len(vm.TimestampVerificationData.RFC3161Timestamps) != 1 || parsed.MessageSignature == nil {
		t.Fatalf("bundle lacks a member: %s", bundle)
	}
	verified := runPodium(t, "", env, "verify", "--provider", "sigstore-keyless",
		"--content-hash", f.files.ContentHash, "--signature", bundle)
	if verified.Exit != 0 || !strings.Contains(verified.Stderr, "verify ok") {
		t.Fatalf("verify exit = %d\nstderr: %s", verified.Exit, verified.Stderr)
	}
	other := "sha256:" + strings.Repeat("ab", 32)
	refused := runPodium(t, "", env, "verify", "--provider", "sigstore-keyless",
		"--content-hash", other, "--signature", bundle)
	if refused.Exit != 1 || !strings.Contains(refused.Stderr, "message digest is not the digest being verified") {
		t.Fatalf("verify under another hash: exit = %d\nstderr: %s", refused.Exit, refused.Stderr)
	}
}
