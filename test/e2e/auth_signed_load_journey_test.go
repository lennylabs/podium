package e2e

// Signed artifact verifies on load and a tampered blob is refused (gap
// G-AUTH-15).
//
// The filesystem bootstrap attaches no signatures, so signature verification
// and tamper detection skipped end to end. The G-INFRA-8 signedArtifactFixture
// produces a real registry-managed signature envelope over an offline keypair
// and drives the shipped podium-mcp verifier. This is the journey the gap
// names: under the default-on verifier (no PODIUM_VERIFY_SIGNATURES set, which
// falls back to the secure medium-and-above policy), a validly-signed
// medium-sensitivity artifact loads and verifies, then tampering its stored
// bytes makes the same load abort with materialize.signature_invalid, while a
// co-resident untampered signed artifact keeps loading. The untampered control
// proves the verifier is selective: it blocks the tampered blob without
// breaking a clean one.
//
// Two independent signed fixtures back the journey so the tamper to one cannot
// affect the other. Each holds a real signature from its own offline key,
// verified consumer-side with that key. The policy is left unset so the binary
// exercises its own default rather than an explicitly-configured one (§6.2: an
// absent PODIUM_VERIFY_SIGNATURES defaults to medium-and-above).
//
// Spec: §4.7.9 (each version is signed by a registry-managed key at ingest; the
// MCP server verifies on materialization for sensitivity >= medium; a signature
// failure aborts with materialize.signature_invalid before anything is written
// to disk), §6.2 (PODIUM_VERIFY_SIGNATURES defaults to medium-and-above), §6.6
// step 2 (content-hash match over the delivered bytes). Gap G-AUTH-15.

import (
	"strings"
	"testing"
)

// signedDefaultEnv returns the fixture's consumer env with the verification
// policy left unset, so the bridge falls back to its secure default
// (medium-and-above). f.Env sets PODIUM_VERIFY_SIGNATURES to the passed value;
// passing the empty string makes the binary treat it as "not configured."
func signedDefaultEnv(t *testing.T, f *signedArtifactFixture) []string {
	t.Helper()
	env := f.Env(t, "")
	// Drop the empty PODIUM_VERIFY_SIGNATURES= entry so the bridge's
	// os.Getenv-based default kicks in rather than reading an explicit empty
	// value. (An empty value is already treated as unset, but removing it makes
	// the "use the default" intent unambiguous and survives a future change to
	// the empty-string handling.)
	out := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "PODIUM_VERIFY_SIGNATURES=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// TestAuthSignedLoadJourney_VerifyThenTamperRefused loads two validly-signed
// medium-sensitivity artifacts under the default-on verifier, asserts both
// verify, tampers one artifact's stored bytes, then asserts the tampered load
// aborts with the signature error while the untampered artifact still loads.
func TestAuthSignedLoadJourney_VerifyThenTamperRefused(t *testing.T) {
	t.Parallel()

	// Two independent signed artifacts, each with its own offline key. The
	// keep fixture is the untampered control; the tamper fixture is mutated
	// mid-journey.
	keep := newSignedArtifactFixture(t, signedArtifactSpec{ID: "finance/policy/retention"})
	tampered := newSignedArtifactFixture(t, signedArtifactSpec{ID: "finance/policy/access-control"})

	keepEnv := signedDefaultEnv(t, keep)
	tamperEnv := signedDefaultEnv(t, tampered)

	// ---- Baseline: both signed artifacts verify and load under the default ----
	if errStr, result := loadSignedArtifact(t, keepEnv, keep.ID()); errStr != "" {
		t.Fatalf("untampered control should load under the default verifier, got: %s\nresult=%v", errStr, result)
	}
	if errStr, result := loadSignedArtifact(t, tamperEnv, tampered.ID()); errStr != "" {
		t.Fatalf("signed artifact should load under the default verifier before tampering, got: %s\nresult=%v", errStr, result)
	}
	if keep.LoadHits() == 0 || tampered.LoadHits() == 0 {
		t.Errorf("a fixture registry was never consulted (keep=%d, tampered=%d)", keep.LoadHits(), tampered.LoadHits())
	}

	// ---- Tamper the stored bytes of one artifact -----------------------------
	// Rewriting the served content hash to a value the offline signature does
	// not cover is the signed-then-tampered case: the default-on verifier
	// recomputes against the signature and refuses the load before materializing.
	tampered.TamperContentHash()

	errStr, result := loadSignedArtifact(t, tamperEnv, tampered.ID())
	if !strings.Contains(errStr, "materialize.signature_invalid") {
		t.Fatalf("tampered blob must be refused with materialize.signature_invalid under the default verifier, got: %q\nresult=%v", errStr, result)
	}

	// ---- The untampered artifact still loads ---------------------------------
	// The verifier is selective: blocking the tampered blob does not break a
	// clean signed artifact loaded under the same default policy.
	if errStr, result := loadSignedArtifact(t, keepEnv, keep.ID()); errStr != "" {
		t.Fatalf("untampered artifact must still load after the sibling tamper was refused, got: %s\nresult=%v", errStr, result)
	}
}
