package sign_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §4.7.9 — end-to-end smoke against a live Sigstore stack.
// Gated on PODIUM_SIGSTORE_* env vars so default test runs skip
// cleanly. The intended targets are:
//
//   - sigstage.dev (recommended for nightly CI):
//       PODIUM_SIGSTORE_FULCIO_URL=https://fulcio.sigstage.dev
//       PODIUM_SIGSTORE_REKOR_URL=https://rekor.sigstage.dev
//   - a locally-run sigstore-stack (for hermetic local testing):
//       PODIUM_SIGSTORE_FULCIO_URL=http://localhost:5555
//       PODIUM_SIGSTORE_REKOR_URL=http://localhost:3000
//
// PODIUM_SIGSTORE_OIDC_TOKEN supplies the OIDC token Fulcio will
// accept for the chosen issuer. PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE
// points at the PEM file containing the trust anchors for the
// chosen Sigstore deployment (the public-good root, the staging
// root, or a local stack's CA).
//
// Phase: 1
func TestSigstoreKeyless_LiveSmoke(t *testing.T) {
	testharness.RequirePhase(t, 1)
	fulcio := os.Getenv("PODIUM_SIGSTORE_FULCIO_URL")
	rekor := os.Getenv("PODIUM_SIGSTORE_REKOR_URL")
	token := os.Getenv("PODIUM_SIGSTORE_OIDC_TOKEN")
	rootFile := os.Getenv("PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE")
	if fulcio == "" || token == "" || rootFile == "" {
		t.Skip("PODIUM_SIGSTORE_* unset; skipping live Sigstore smoke")
	}
	rootPEM, err := os.ReadFile(rootFile)
	if err != nil {
		t.Fatalf("read trust root: %v", err)
	}
	provider := sign.SigstoreKeyless{
		FulcioURL: fulcio,
		RekorURL:  rekor,
		OIDCToken: token,
		TrustRoot: rootPEM,
	}
	body := []byte("podium live smoke")
	h := sha256.Sum256(body)
	contentHash := "sha256:" + hex.EncodeToString(h[:])
	envelopeStr, err := provider.Sign(contentHash)
	if err != nil {
		t.Fatalf("Sign live: %v", err)
	}
	if err := provider.Verify(contentHash, envelopeStr); err != nil {
		t.Fatalf("Verify live: %v", err)
	}
}
