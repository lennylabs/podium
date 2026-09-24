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
// under the enforcing always policy with no verification error. The
// signature envelope is the real registry-managed envelope, and the consumer
// verifies it with the offline public key wired through
// PODIUM_SIGNATURE_VERIFY_KEY.
func TestSignedArtifact_ValidSignatureLoads(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{})
	env := f.Env(t, "always")

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
	env := f.Env(t, "always")

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
	env := f.Env(t, "always")

	if errStr, result := loadSignedArtifact(t, env, f.ID()); errStr != "" {
		t.Fatalf("baseline signed load should pass, got error: %s\nresult=%v", errStr, result)
	}

	f.TamperBody()
	errStr, result := loadSignedArtifact(t, env, f.ID())
	if !strings.Contains(errStr, "materialize.content_hash_mismatch") {
		t.Fatalf("tampered body must trip the content-hash gate, got: %q\nresult=%v", errStr, result)
	}
}

// TestSignedArtifact_LowSensitivityStillVerifiesAPresentSignature proves that
// a served signature is verified under any policy above never, whatever the
// artifact's sensitivity: an untampered low-sensitivity artifact loads, and one
// whose served attestation no longer matches its signature is refused with
// materialize.signature_invalid.
//
// Spec: §4.7.9
func TestSignedArtifact_LowSensitivityStillVerifiesAPresentSignature(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{Sensitivity: "low"})
	env := f.Env(t, "always")

	if errStr, result := loadSignedArtifact(t, env, f.ID()); errStr != "" {
		t.Fatalf("untampered low-sensitivity artifact should load, got error: %s\nresult=%v", errStr, result)
	}

	f.TamperContentHash()
	errStr, result := loadSignedArtifact(t, env, f.ID())
	if !strings.Contains(errStr, "materialize.signature_invalid") {
		t.Fatalf("a low-sensitivity artifact whose signature does not validate must be refused, got: %q\nresult=%v", errStr, result)
	}
}

// readSignedResource issues MCP resources/read for id through the real bridge
// and returns (errString, text). A successful read has an empty errString.
func readSignedResource(t *testing.T, env []string, id string) (string, string) {
	t.Helper()
	res := mcpExec(t, env, rpcReq{ID: 1, Method: "resources/read", Params: map[string]any{"uri": "podium://artifact/" + id}})
	result := rpcResult(t, res.Stdout, 1)
	if e, _ := result["error"].(string); e != "" {
		return e, ""
	}
	contents, _ := result["contents"].([]any)
	if len(contents) == 0 {
		t.Fatalf("resources/read returned neither an error nor contents: %v", result)
	}
	first, _ := contents[0].(map[string]any)
	text, _ := first["text"].(string)
	return "", text
}

// TestSignedArtifact_ResourcesReadVerifies proves that the §5.0 resources/read
// mirror runs the verification load_artifact runs. An untouched artifact
// returns its text; a tamper that invalidates the signature, a tamper of the
// body under the signature, and a stripped signature each return the code
// load_artifact returns, and no text. Each arm runs on its own fixture so no
// tamper carries into the next.
//
// Spec: §5.0, §4.7.9
func TestSignedArtifact_ResourcesReadVerifies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		tamper func(*signedArtifactFixture)
		want   string
	}{
		{name: "untouched"},
		{name: "signature invalid", tamper: (*signedArtifactFixture).TamperContentHash, want: "materialize.signature_invalid"},
		{name: "body tampered", tamper: (*signedArtifactFixture).TamperBody, want: "materialize.content_hash_mismatch"},
		{name: "signature stripped", tamper: (*signedArtifactFixture).StripSignature, want: "materialize.signature_missing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newSignedArtifactFixture(t, signedArtifactSpec{})
			if c.tamper != nil {
				c.tamper(f)
			}
			errStr, text := readSignedResource(t, f.Env(t, "always"), f.ID())
			if c.want == "" {
				if errStr != "" || !strings.Contains(text, "Signed policy body.") {
					t.Fatalf("untouched artifact should read, got error %q, text %q", errStr, text)
				}
				return
			}
			if !strings.HasPrefix(errStr, c.want) {
				t.Errorf("error = %q, want a leading %s", errStr, c.want)
			}
			if text != "" {
				t.Errorf("refused read returned text %q", text)
			}
		})
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
	env := f.Env(t, "always")
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
