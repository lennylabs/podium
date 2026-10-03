package sign_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §4.7.9 — end-to-end smoke against a live Sigstore stack.
// Gated on PODIUM_SIGSTORE_* env vars so default test runs skip
// cleanly. The intended target is the Sigstore staging instance:
//
//	PODIUM_SIGSTORE_FULCIO_URL=https://fulcio.sigstage.dev
//	PODIUM_SIGSTORE_REKOR_URL=<the staging Rekor v2 shard URL>
//	PODIUM_SIGSTORE_TSA_URL=https://timestamp.sigstage.dev/api/v1/timestamp
//
// PODIUM_SIGSTORE_OIDC_TOKEN supplies the OIDC token Fulcio accepts for
// the chosen issuer. PODIUM_SIGSTORE_TRUSTED_ROOT_FILE names the
// trusted_root.json of the chosen instance. PODIUM_SIGSTORE_CERT_IDENTITY
// and PODIUM_SIGSTORE_CERT_OIDC_ISSUER configure the identity policy
// Verify enforces. The policy comes from those variables rather than from
// the token: a policy taken from the token that signed can only pass, and
// a Fulcio SAN for a CI issuer is a workflow URI rather than the token's
// subject.
//
// When PODIUM_SIGSTORE_RECORD_DIR is set, the test writes envelope.json,
// a copy of the trusted root as trusted_root.json, and meta.json into
// that directory after Verify passes. Pointing it at the absolute path of
// pkg/sign/testdata/sigstore-staging refreshes the fixture that
// TestSigstoreKeyless_VerifiesRecordedStagingEnvelope verifies offline.
func TestSigstoreKeyless_LiveSmoke(t *testing.T) {
	fulcio := os.Getenv("PODIUM_SIGSTORE_FULCIO_URL")
	rekor := os.Getenv("PODIUM_SIGSTORE_REKOR_URL")
	tsa := os.Getenv("PODIUM_SIGSTORE_TSA_URL")
	token := os.Getenv("PODIUM_SIGSTORE_OIDC_TOKEN")
	rootFile := os.Getenv("PODIUM_SIGSTORE_TRUSTED_ROOT_FILE")
	identity := os.Getenv("PODIUM_SIGSTORE_CERT_IDENTITY")
	issuer := os.Getenv("PODIUM_SIGSTORE_CERT_OIDC_ISSUER")
	if fulcio == "" || rekor == "" || tsa == "" || token == "" || rootFile == "" || identity == "" || issuer == "" {
		t.Skip("PODIUM_SIGSTORE_* unset; skipping live Sigstore smoke")
	}
	trustedRoot, err := os.ReadFile(rootFile)
	if err != nil {
		t.Fatalf("read trusted root: %v", err)
	}
	provider := sign.SigstoreKeyless{
		FulcioURL: fulcio,
		RekorURL:  rekor,
		TSAURL:    tsa,
		OIDCToken: token,
		TrustRoot: trustedRoot,
		Identity:  sign.NewIdentityPolicy(identity, issuer),
	}
	if err := provider.Identity.Validate(); err != nil {
		t.Fatalf("identity policy: %v", err)
	}
	body := []byte("podium live smoke")
	h := sha256.Sum256(body)
	contentHash := "sha256:" + hex.EncodeToString(h[:])
	envelopeStr, err := provider.Sign(context.Background(), contentHash)
	if err != nil {
		t.Fatalf("Sign live: %v", err)
	}
	if err := provider.Verify(context.Background(), contentHash, envelopeStr); err != nil {
		t.Fatalf("Verify live: %v", err)
	}
	if dir := os.Getenv("PODIUM_SIGSTORE_RECORD_DIR"); dir != "" {
		recordFixture(t, dir, envelopeStr, trustedRoot, stagingMeta{
			ContentHash: contentHash,
			Identity:    identity,
			Issuer:      issuer,
		})
	}
}

// stagingMeta is meta.json of the recorded staging fixture: the content
// hash the envelope signs and the identity policy it verifies under.
type stagingMeta struct {
	ContentHash string `json:"content_hash"`
	Identity    string `json:"identity"`
	Issuer      string `json:"issuer"`
}

// recordFixture writes the verified envelope, the trusted root, and the
// metadata into dir.
func recordFixture(t *testing.T, dir, envelopeStr string, trustedRoot []byte, meta stagingMeta) {
	t.Helper()
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create record dir: %v", err)
	}
	files := map[string][]byte{
		"envelope.json":     []byte(strings.TrimSpace(envelopeStr) + "\n"),
		"trusted_root.json": trustedRoot,
		"meta.json":         append(metaJSON, '\n'),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Logf("recorded staging fixture in %s", dir)
}
