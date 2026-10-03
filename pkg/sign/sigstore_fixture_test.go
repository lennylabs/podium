package sign_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// stagingFixtureDir holds the envelope TestSigstoreKeyless_LiveSmoke
// records from the Sigstore staging instance under
// PODIUM_SIGSTORE_RECORD_DIR.
const stagingFixtureDir = "testdata/sigstore-staging"

// Spec: §4.7.9 — a staging envelope verifies offline under the recorded
// identity policy and trusted root. The fixture pins the wire formats the
// in-process generator cannot check independently: the RFC 3161 token
// the staging timestamp authority issues, the checkpoint syntax Rekor v2
// returns, and the hashedrekord v0.0.2 body field names. The envelope
// refuses a content hash it does not sign.
func TestSigstoreKeyless_VerifiesRecordedStagingEnvelope(t *testing.T) {
	envelopeJSON := readFixture(t, "envelope.json")
	var meta stagingMeta
	if err := json.Unmarshal(readFixture(t, "meta.json"), &meta); err != nil {
		t.Fatalf("parse meta.json: %v", err)
	}
	v := sign.SigstoreKeyless{
		TrustRoot: readFixture(t, "trusted_root.json"),
		Identity:  sign.NewIdentityPolicy(meta.Identity, meta.Issuer),
		Client:    &http.Client{Transport: failingTransport{t}},
	}
	ctx := context.Background()
	if err := v.Verify(ctx, meta.ContentHash, string(envelopeJSON)); err != nil {
		t.Fatalf("Verify recorded staging envelope: %v", err)
	}
	other := sha256.Sum256([]byte("acme other content"))
	err := v.Verify(ctx, "sha256:"+hex.EncodeToString(other[:]), string(envelopeJSON))
	if !errors.Is(err, sign.ErrSignatureInvalid) || !strings.Contains(err.Error(), "does not bind the digest") {
		t.Fatalf("Verify under another content hash = %v, want ErrSignatureInvalid naming %q", err, "does not bind the digest")
	}
}

// readFixture returns the named file of the recorded staging fixture.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stagingFixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}
