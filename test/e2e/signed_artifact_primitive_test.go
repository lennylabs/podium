package e2e

// Proof that the signed-artifact fixture drives the real podium-mcp verifier
// path: a validly-signed medium-sensitivity artifact materializes, a record
// whose served bytes were altered is refused by the delivery-hash comparison
// whatever its signature, a delivery signature over another value is refused
// with the signature error, and key pinning rejects a signature from a rotated
// key.
//
// Spec: §4.7.9, §4.7.10, §6.2, §6.6 step 2.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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

// TestSignedArtifact_ValidSignatureLoads proves the happy path: a record whose
// delivery hash the offline keypair signed, served with that delivery pair and
// no signature field, loads under the enforcing always policy with no
// verification error. The envelope is the real registry-managed envelope, and
// the consumer verifies it with the offline public key wired through
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
// after a valid record is established, rewriting the served content hash, a
// framed field, makes the default-on verifier block the load with
// materialize.content_hash_mismatch. The delivery-hash comparison runs before
// the signature policy, so an altered record reports the comparison's code
// whatever its signature. A delivery signature over another value, on a record
// that is otherwise intact, is refused with materialize.signature_invalid.
func TestSignedArtifact_TamperedBlobRefused(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		tamper func(*signedArtifactFixture)
		want   string
	}{
		"content hash":       {(*signedArtifactFixture).TamperContentHash, "materialize.content_hash_mismatch"},
		"delivery signature": {(*signedArtifactFixture).TamperDeliverySignature, "materialize.signature_invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newSignedArtifactFixture(t, signedArtifactSpec{})
			env := f.Env(t, "always")

			// Untampered load verifies first, establishing the valid baseline.
			if errStr, result := loadSignedArtifact(t, env, f.ID()); errStr != "" {
				t.Fatalf("baseline signed load should pass, got error: %s\nresult=%v", errStr, result)
			}

			tc.tamper(f)
			errStr, result := loadSignedArtifact(t, env, f.ID())
			if !strings.Contains(errStr, tc.want) {
				t.Fatalf("tampered record must be refused with %s, got: %q\nresult=%v", tc.want, errStr, result)
			}
		})
	}
}

// TestSignedArtifact_ForgedManifestBodyRefused proves the delivery record
// covers the manifest body the consumer returns to the agent: changing only
// the served manifest_body, with the frontmatter and the delivery pair intact,
// is refused with materialize.content_hash_mismatch under always.
//
// Spec: §6.6
func TestSignedArtifact_ForgedManifestBodyRefused(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{})
	f.ForgeManifestBody()
	errStr, result := loadSignedArtifact(t, f.Env(t, "always"), f.ID())
	if !strings.Contains(errStr, "materialize.content_hash_mismatch") {
		t.Fatalf("a forged manifest body must be refused with materialize.content_hash_mismatch, got: %q\nresult=%v", errStr, result)
	}
}

// TestSignedArtifact_TamperedBodyHitsContentHashGate is the integrity-gate
// complement: tampering the served body while leaving the delivery pair intact
// trips the §6.6 step 2 delivery-hash comparison with
// materialize.content_hash_mismatch.
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
// a served delivery signature is verified under any policy above never,
// whatever the artifact's sensitivity: an untampered low-sensitivity artifact
// loads, and one whose delivery signature was made over another value is
// refused with materialize.signature_invalid.
//
// Spec: §4.7.9
func TestSignedArtifact_LowSensitivityStillVerifiesAPresentSignature(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{Sensitivity: "low"})
	env := f.Env(t, "always")

	if errStr, result := loadSignedArtifact(t, env, f.ID()); errStr != "" {
		t.Fatalf("untampered low-sensitivity artifact should load, got error: %s\nresult=%v", errStr, result)
	}

	f.TamperDeliverySignature()
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
		{name: "signature invalid", tamper: (*signedArtifactFixture).TamperDeliverySignature, want: "materialize.signature_invalid"},
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

// TestSignedArtifact_UntrustedKeyRefused proves the §4.7.9 key-set rule: a
// delivery signature made under a key absent from the consumer's
// PODIUM_SIGNATURE_VERIFY_KEY list is refused, even though the envelope's
// key_id names the key that signed it and the bytes are intact. The key_id
// selects only the order in which the listed keys are tried.
//
// Spec: §4.7.9
// Matrix: §6.10 (materialize.signature_invalid)
func TestSignedArtifact_UntrustedKeyRefused(t *testing.T) {
	t.Parallel()
	listed := make([]string, 2)
	for i := range listed {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		listed[i] = base64.StdEncoding.EncodeToString(pub)
	}
	f := newSignedArtifactFixture(t, signedArtifactSpec{})
	env := f.Env(t, "always")
	for i, kv := range env {
		if strings.HasPrefix(kv, "PODIUM_SIGNATURE_VERIFY_KEY=") {
			env[i] = "PODIUM_SIGNATURE_VERIFY_KEY=" + strings.Join(listed, ",")
		}
	}
	errStr, result := loadSignedArtifact(t, env, f.ID())
	if !strings.Contains(errStr, "materialize.signature_invalid") {
		t.Fatalf("a signature from a key outside the verification key set must be refused, got: %q\nresult=%v", errStr, result)
	}
}

// TestSignedArtifact_VerifiesUnderListedKeySet proves a consumer whose
// PODIUM_SIGNATURE_VERIFY_KEY lists several keys accepts a delivery signature
// made under the last of them, the state a consumer is in across a rotation.
//
// Spec: §4.7.9, §6.2
func TestSignedArtifact_VerifiesUnderListedKeySet(t *testing.T) {
	t.Parallel()
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	f := newSignedArtifactFixture(t, signedArtifactSpec{ExtraTrustedKeys: []ed25519.PublicKey{other}})
	if !strings.Contains(f.VerifyKeyList(), ",") {
		t.Fatalf("VerifyKeyList = %q, want a comma-separated list", f.VerifyKeyList())
	}
	errStr, result := loadSignedArtifact(t, f.Env(t, "always"), f.ID())
	if errStr != "" {
		t.Fatalf("load under a listed key = %q, want success\nresult=%v", errStr, result)
	}
}
