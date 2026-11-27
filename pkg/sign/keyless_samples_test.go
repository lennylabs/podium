package sign_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// The sample under testdata/keyless is a real §4.7.9 keyless signature.
// TestSigstoreKeyless_LiveSmoke signed it on 2026-10-03 against the Sigstore
// staging instance and wrote it in the keyless envelope format that 0.7.0
// ships, as pkg/sign/testdata/sigstore-staging. staging.sigstore.json
// carries the same leaf certificate, signature, Rekor v2 entry, inclusion
// proof, checkpoint, and RFC 3161 token in a version 0.3 bundle: the log
// ID is that of the log2025-alpha3 key in the trusted root, the root hash
// and tree size come from the checkpoint, and the token is wrapped in a
// TimeStampResp with the granted status. No signed byte changed, so the
// sample pins the formats the harness cannot check independently: the
// staging Fulcio certificate and its SCT, the Ed25519 checkpoint of a
// Rekor v2 shard with its witness cosignatures, and the staging timestamp
// authority's token. staging.trusted_root.json is the staging trusted root
// recorded with it.
const keylessSampleDir = "testdata/keyless"

// keylessSampleMeta is staging.meta.json: the content hash the sample
// signs and the identity policy it verifies under.
type keylessSampleMeta struct {
	ContentHash string `json:"content_hash"`
	Identity    string `json:"identity"`
	Issuer      string `json:"issuer"`
}

// readKeylessSample returns the named file of the staging sample.
func readKeylessSample(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(keylessSampleDir, name))
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	return data
}

// Spec: §4.7.9 — a real staging signature in the bundle format verifies
// offline under the recorded identity policy and trusted root, and the
// bundle refuses a content hash it does not sign.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifiesStagingSample(t *testing.T) {
	t.Parallel()
	var meta keylessSampleMeta
	if err := json.Unmarshal(readKeylessSample(t, "staging.meta.json"), &meta); err != nil {
		t.Fatalf("parse meta: %v", err)
	}
	bundle := string(readKeylessSample(t, "staging.sigstore.json"))
	v := sign.SigstoreKeyless{
		TrustRoot: readKeylessSample(t, "staging.trusted_root.json"),
		Identity:  sign.NewIdentityPolicy(meta.Identity, meta.Issuer),
		Client:    &http.Client{Transport: failingTransport{t}},
	}
	ctx := context.Background()
	if err := v.Verify(ctx, meta.ContentHash, bundle); err != nil {
		t.Fatalf("Verify staging sample: %v", err)
	}
	other := sha256.Sum256([]byte("acme other content"))
	wantRefused(t, v.Verify(ctx, "sha256:"+hex.EncodeToString(other[:]), bundle), "message digest is not the digest being verified")
	v.Identity = sign.NewIdentityPolicy("alice@acme.com", meta.Issuer)
	wantRefused(t, v.Verify(ctx, meta.ContentHash, bundle), "certificate identity mismatch")
}
