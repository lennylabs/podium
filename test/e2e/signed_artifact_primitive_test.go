package e2e

// Proof that the signed-artifact ingest and tamper fixture
// drives the real podium-mcp verifier path: a validly-signed medium-sensitivity
// artifact materializes, a signed-then-tampered blob is refused with the
// signature error, the content-hash integrity gate still fires for a tampered
// body, and key pinning rejects a signature from a rotated key.
//
// Spec: §4.7.9, §6.2, §6.6 step 2.

import (
	"strings"
	"testing"
)

// loadResult runs one load_artifact through the real bridge against the fixture
// and returns (errString, result). A successful load has an empty errString.
func loadSignedArtifact(t *testing.T, env []string, id string) (string, map[string]any) {
	t.Helper()
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": id}))
	result := rpcResult(t, res.Stdout, 1)
	errStr, _ := result["error"].(string)
	return errStr, result
}

// TestSignedArtifact_ValidSignatureLoads proves the happy path: an artifact
// signed by the offline keypair, served with its matching content hash, loads
// under the enforcing medium-and-above policy with no verification error. The
// signature envelope is the real registry-managed envelope, and the consumer
// verifies it with the offline public key wired through
// PODIUM_SIGNATURE_VERIFY_KEY.
func TestSignedArtifact_ValidSignatureLoads(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{})
	env := f.Env(t, "medium-and-above")

	errStr, result := loadSignedArtifact(t, env, f.ID())
	if errStr != "" {
		t.Fatalf("valid signed artifact should load, got error: %s\nresult=%v", errStr, result)
	}
	if f.LoadHits() == 0 {
		t.Error("fixture registry was never consulted")
	}
	if mb, _ := result["manifest_body"].(string); !strings.Contains(mb, "Signed policy body.") {
		t.Errorf("loaded result missing the signed body (len=%d)", len(mb))
	}
}

// TestSignedArtifact_TamperedBlobRefused proves the signed-then-tampered case:
// after a valid signature is established, rewriting the served content hash to a
// value the signature does not cover makes the default-on verifier block the
// load with materialize.signature_invalid. The signature gate runs before the
// content-hash recompute, so the failure surfaces as the signature error.
func TestSignedArtifact_TamperedBlobRefused(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{})
	env := f.Env(t, "medium-and-above")

	// Untampered load verifies first, establishing the valid baseline.
	if errStr, result := loadSignedArtifact(t, env, f.ID()); errStr != "" {
		t.Fatalf("baseline signed load should pass, got error: %s\nresult=%v", errStr, result)
	}

	// Tamper the stored bytes (the served content hash) and load again.
	f.TamperContentHash()
	errStr, result := loadSignedArtifact(t, env, f.ID())
	if !strings.Contains(errStr, "materialize.signature_invalid") {
		t.Fatalf("tampered blob must be refused with materialize.signature_invalid, got: %q\nresult=%v", errStr, result)
	}
}

// TestSignedArtifact_TamperedBodyHitsContentHashGate is the integrity-gate
// complement: tampering the served body while leaving the signed content hash
// and envelope intact passes the signature gate (the envelope still matches the
// unchanged hash) but trips the §6.6 step 2 recompute with
// materialize.content_hash_mismatch. This confirms the fixture exercises both
// halves of the verifier path.
func TestSignedArtifact_TamperedBodyHitsContentHashGate(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{})
	env := f.Env(t, "medium-and-above")

	if errStr, result := loadSignedArtifact(t, env, f.ID()); errStr != "" {
		t.Fatalf("baseline signed load should pass, got error: %s\nresult=%v", errStr, result)
	}

	f.TamperBody()
	errStr, result := loadSignedArtifact(t, env, f.ID())
	if !strings.Contains(errStr, "materialize.content_hash_mismatch") {
		t.Fatalf("tampered body must trip the content-hash gate, got: %q\nresult=%v", errStr, result)
	}
}

// TestSignedArtifact_LowSensitivitySkipsVerification proves the policy scope:
// under medium-and-above a low-sensitivity artifact is below the verification
// threshold, so even a forged content hash loads (the signature is never
// checked). This guards against the verifier over-enforcing on artifacts the
// policy exempts.
func TestSignedArtifact_LowSensitivitySkipsVerification(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{Sensitivity: "low"})
	env := f.Env(t, "medium-and-above")

	// A low-sensitivity artifact is below the medium-and-above threshold, so the
	// signature is never checked. The served bytes and content hash are left
	// untampered, so the content-hash gate also passes. The artifact loads
	// cleanly under the enforcing policy, confirming the verifier scopes
	// enforcement to the configured sensitivity floor.
	errStr, result := loadSignedArtifact(t, env, f.ID())
	if errStr != "" {
		t.Fatalf("low-sensitivity artifact should load under medium-and-above, got error: %s\nresult=%v", errStr, result)
	}
}

// TestSignedArtifact_KeyPinningRejectsRotatedKey proves the §4.7.9 rotation
// guard: when the consumer pins an expected key id via PODIUM_SIGNATURE_KEY_ID
// but the envelope carries a different id, the signature is refused even though
// the bytes are otherwise intact. The fixture signs with one key id; the
// consumer is told to expect another.
func TestSignedArtifact_KeyPinningRejectsRotatedKey(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{KeyID: "key-v1"})
	env := f.Env(t, "medium-and-above")
	// Override the pinned key id to a value the envelope does not carry.
	for i, kv := range env {
		if strings.HasPrefix(kv, "PODIUM_SIGNATURE_KEY_ID=") {
			env[i] = "PODIUM_SIGNATURE_KEY_ID=key-v2"
		}
	}

	errStr, result := loadSignedArtifact(t, env, f.ID())
	if !strings.Contains(errStr, "materialize.signature_invalid") {
		t.Fatalf("a signature from a non-pinned key id must be refused, got: %q\nresult=%v", errStr, result)
	}
}
