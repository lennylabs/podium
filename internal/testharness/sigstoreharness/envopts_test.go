package sigstoreharness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"strings"
	"testing"
	"time"
)

// Spec: §4.7.9. Each EnvOpt changes the one property it names and keeps
// the envelope decodable, so a TEST-2 refusal case fails at the condition
// the option targets.
func TestEnvelope_Options(t *testing.T) {
	t.Parallel()
	h := New(t)
	hash, digest := testContentHash("options")
	otherHash, otherDigest := testContentHash("other content")
	later := h.Clock().Add(time.Hour)
	otherDER := h.FulcioIntermediate().Raw
	cases := []struct {
		name  string
		opts  []EnvOpt
		check func(t *testing.T, in inspection)
	}{
		{"SAN and URI SAN", []EnvOpt{WithSAN("carol@acme.com"), WithURISAN("https://github.com/acme/repo")}, func(t *testing.T, in inspection) {
			if len(in.leaf.EmailAddresses) != 1 || in.leaf.EmailAddresses[0] != "carol@acme.com" {
				t.Fatalf("emails = %v", in.leaf.EmailAddresses)
			}
			if len(in.leaf.URIs) != 1 || in.leaf.URIs[0].String() != "https://github.com/acme/repo" {
				t.Fatalf("uris = %v", in.leaf.URIs)
			}
		}},
		{"otherName SAN only", []EnvOpt{WithOtherNameSANOnly()}, otherNameSANIs(true)},
		{"non-critical otherName SAN", []EnvOpt{WithOtherNameSANOnly(), WithNonCriticalSAN()}, otherNameSANIs(false)},
		{"issuer extensions", []EnvOpt{WithIssuerExt(OIDIssuerV2, []byte{0xff}), WithIssuerExt(OIDIssuerV1, []byte(DefaultIssuer))}, func(t *testing.T, in inspection) {
			equalBytes(t, ".1.8", extValue(in.leaf, OIDIssuerV2), []byte{0xff})
			equalBytes(t, ".1.1", extValue(in.leaf, OIDIssuerV1), []byte(DefaultIssuer))
		}},
		{"without issuer", []EnvOpt{WithoutIssuer()}, func(t *testing.T, in inspection) {
			if extValue(in.leaf, OIDIssuerV2) != nil || extValue(in.leaf, OIDIssuerV1) != nil {
				t.Fatal("leaf carries an issuer extension")
			}
		}},
		{"without code signing", []EnvOpt{WithoutCodeSigning()}, ekuIs(x509.ExtKeyUsageServerAuth)},
		{"leaf without EKU", []EnvOpt{WithLeafWithoutEKU()}, ekuIs()},
		{"leaf any usage", []EnvOpt{WithLeafAnyUsage()}, ekuIs(x509.ExtKeyUsageAny)},
		{"Ed25519 leaf", []EnvOpt{WithEd25519Leaf()}, func(t *testing.T, in inspection) {
			pub, ok := in.leaf.PublicKey.(ed25519.PublicKey)
			if !ok || !ed25519.Verify(pub, digest, in.sig) {
				t.Fatal("leaf is not an Ed25519 signer of the digest")
			}
			if in.body.Spec.V002.Signature.Verifier.KeyDetails != keyDetailsEd25519 {
				t.Fatalf("keyDetails = %s", in.body.Spec.V002.Signature.Verifier.KeyDetails)
			}
			assertBodyBinds(t, in, digest)
		}},
		{"foreign signature", []EnvOpt{WithForeignSignature()}, func(t *testing.T, in inspection) {
			if ecdsa.VerifyASN1(in.leaf.PublicKey.(*ecdsa.PublicKey), digest, in.sig) {
				t.Fatal("foreign signature verifies over the digest")
			}
			assertBodyBinds(t, in, digest)
			imprint := sha256.Sum256(in.sig)
			equalBytes(t, "imprint", in.token.tst.MessageImprint.HashedMessage, imprint[:])
		}},
		{"leaf issued by TSA root", []EnvOpt{WithLeafIssuedByTSARoot()}, func(t *testing.T, in inspection) {
			if err := in.leaf.CheckSignatureFrom(h.TSARoot()); err != nil || len(in.intermediates) != 0 {
				t.Fatalf("leaf not issued by the TSA root alone: %v, %d intermediates", err, len(in.intermediates))
			}
		}},
		{"without tlog", []EnvOpt{WithoutTLog()}, func(t *testing.T, in inspection) {
			if in.env.TLog != nil {
				t.Fatal("envelope carries a tlog")
			}
		}},
		{"entry digest", []EnvOpt{WithEntryDigest(otherHash)}, func(t *testing.T, in inspection) {
			equalBytes(t, "entry digest", in.body.Spec.V002.Data.Digest, otherDigest)
		}},
		{"entry signature", []EnvOpt{WithEntrySignature([]byte("acme"))}, func(t *testing.T, in inspection) {
			equalBytes(t, "entry signature", in.body.Spec.V002.Signature.Content, []byte("acme"))
		}},
		{"entry cert", []EnvOpt{WithEntryCert(otherDER)}, func(t *testing.T, in inspection) {
			equalBytes(t, "entry cert", in.body.Spec.V002.Signature.Verifier.X509Certificate.RawBytes, otherDER)
		}},
		{"entry kind", []EnvOpt{WithEntryKind("hashedrekord", "0.0.1")}, func(t *testing.T, in inspection) {
			if in.body.APIVersion != "0.0.1" {
				t.Fatalf("apiVersion = %s", in.body.APIVersion)
			}
		}},
		{"without inclusion proof", []EnvOpt{WithoutInclusionProof()}, func(t *testing.T, in inspection) {
			if len(in.env.TLog.Hashes) != 0 || in.env.TLog.Checkpoint != "" || in.env.TLog.Body == "" {
				t.Fatal("hashes or checkpoint present, or body missing")
			}
		}},
		{"flipped proof hash", []EnvOpt{WithFlippedProofHash()}, func(t *testing.T, in inspection) {
			n, _ := parseNote(in.env.TLog.Checkpoint)
			if root, _ := in.proofRoot(5, 7); bytes.Equal(root, n.root) {
				t.Fatal("flipped proof still recomputes the root")
			}
		}},
		{"log index", []EnvOpt{WithLogIndex(4)}, func(t *testing.T, in inspection) {
			n, _ := parseNote(in.env.TLog.Checkpoint)
			if root, _ := in.proofRoot(5, 7); in.env.TLog.LogIndex != 4 || !bytes.Equal(root, n.root) {
				t.Fatalf("log_index = %d; proof for index 5 must still hold", in.env.TLog.LogIndex)
			}
		}},
		{"malformed checkpoint", []EnvOpt{WithMalformedCheckpoint()}, func(t *testing.T, in inspection) {
			if _, ok := parseNote(in.env.TLog.Checkpoint); ok {
				t.Fatal("malformed checkpoint parses")
			}
		}},
		{"without timestamp", []EnvOpt{WithoutTimestamp()}, func(t *testing.T, in inspection) {
			if in.token != nil {
				t.Fatal("envelope carries a timestamp")
			}
		}},
		{"timestamp time", []EnvOpt{WithTimestampTime(later)}, func(t *testing.T, in inspection) {
			if !in.token.tst.GenTime.Equal(later) {
				t.Fatalf("genTime = %v", in.token.tst.GenTime)
			}
		}},
		{"timestamp over", []EnvOpt{WithTimestampOver([]byte("acme"))}, func(t *testing.T, in inspection) {
			imprint := sha256.Sum256([]byte("acme"))
			equalBytes(t, "imprint", in.token.tst.MessageImprint.HashedMessage, imprint[:])
		}},
		{"flipped timestamp signature", []EnvOpt{WithFlippedTimestampSignature()}, func(t *testing.T, in inspection) {
			if in.token.attrsVerifyUnder(h.TSALeaf()) == nil {
				t.Fatal("flipped signature verifies")
			}
		}},
		{"untrusted TSA", []EnvOpt{WithUntrustedTSA()}, signerIssuedBy(nil, h.TSARoot(), x509.ExtKeyUsageTimeStamping)},
		{"timestamp signed by Fulcio CA", []EnvOpt{WithTimestampSignedByFulcioCA()}, signerIssuedBy(h.FulcioRoot(), h.TSARoot(), x509.ExtKeyUsageTimeStamping)},
		{"TSA leaf without time-stamping", []EnvOpt{WithTSALeafWithoutTimeStamping()}, signerIssuedBy(h.TSARoot(), nil)},
		{"TSA leaf any usage", []EnvOpt{WithTSALeafAnyUsage()}, signerIssuedBy(h.TSARoot(), nil, x509.ExtKeyUsageAny)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, inspect(t, h.Envelope(t, hash, tc.opts...)))
		})
	}
}

// ekuIs checks the leaf's extended key usages.
func ekuIs(want ...x509.ExtKeyUsage) func(*testing.T, inspection) {
	return func(t *testing.T, in inspection) {
		t.Helper()
		if len(in.leaf.ExtKeyUsage) != len(want) || (len(want) == 1 && in.leaf.ExtKeyUsage[0] != want[0]) {
			t.Fatalf("leaf EKU = %v, want %v", in.leaf.ExtKeyUsage, want)
		}
	}
}

// signerIssuedBy checks the token embeds one signer, that the signed
// attributes verify under it, that issuer issued it (when non-nil) and
// notIssuer did not, and that it lists exactly the usages eku.
func signerIssuedBy(issuer, notIssuer *x509.Certificate, eku ...x509.ExtKeyUsage) func(*testing.T, inspection) {
	return func(t *testing.T, in inspection) {
		t.Helper()
		if len(in.token.embedded) != 1 {
			t.Fatalf("embedded = %d, want 1", len(in.token.embedded))
		}
		signer := in.token.embedded[0]
		if err := in.token.attrsVerifyUnder(signer); err != nil {
			t.Fatalf("attributes do not verify under the embedded signer: %v", err)
		}
		if issuer != nil {
			if err := signer.CheckSignatureFrom(issuer); err != nil {
				t.Fatalf("signer not issued by %s: %v", issuer.Subject.CommonName, err)
			}
		}
		if notIssuer != nil && signer.CheckSignatureFrom(notIssuer) == nil {
			t.Fatalf("signer issued by %s", notIssuer.Subject.CommonName)
		}
		if len(signer.ExtKeyUsage) != len(eku) || (len(eku) == 1 && signer.ExtKeyUsage[0] != eku[0]) {
			t.Fatalf("signer EKU = %v, want %v", signer.ExtKeyUsage, eku)
		}
		if len(eku) == 1 && eku[0] == x509.ExtKeyUsageTimeStamping && !hasCriticalTimeStamping(signer) {
			t.Fatal("time-stamping usage is not critical")
		}
	}
}

// Spec: §4.7.9. Envelope reports a content hash that is not alg:hex as a
// harness setup failure.
func TestEnvelope_RejectsMalformedContentHash(t *testing.T) {
	t.Parallel()
	h := New(t)
	for _, bad := range []string{"no-colon", "sha256:zz"} {
		if _, err := h.buildEnvelope(bad, defaultEnvConfig()); err == nil || !strings.Contains(err.Error(), "content hash") {
			t.Fatalf("%q: err = %v", bad, err)
		}
	}
	cfg := defaultEnvConfig()
	cfg.entryDigest = "no-colon"
	hash, _ := testContentHash("x")
	if _, err := h.buildEnvelope(hash, cfg); err == nil {
		t.Fatal("malformed entry digest accepted")
	}
	cfg = defaultEnvConfig()
	cfg.proof.size, cfg.proof.index = 3, 3
	if _, err := h.buildEnvelope(hash, cfg); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("out-of-range tree position: err = %v", err)
	}
	cfg = defaultEnvConfig()
	cfg.leaf.uris = []string{"://bad"}
	if _, err := h.buildEnvelope(hash, cfg); err == nil || !strings.Contains(err.Error(), "uri SAN") {
		t.Fatalf("bad URI SAN: err = %v", err)
	}
}

// otherNameSANIs checks that the leaf carries no email or URI SAN and that
// its SAN extension has the given criticality.
func otherNameSANIs(critical bool) func(t *testing.T, in inspection) {
	return func(t *testing.T, in inspection) {
		t.Helper()
		if len(in.leaf.EmailAddresses)+len(in.leaf.URIs) != 0 {
			t.Fatalf("emails %v uris %v", in.leaf.EmailAddresses, in.leaf.URIs)
		}
		for _, e := range in.leaf.Extensions {
			if e.Id.Equal(oidSubjectAltName) {
				if e.Critical != critical {
					t.Fatalf("SAN critical = %v, want %v", e.Critical, critical)
				}
				return
			}
		}
		t.Fatal("leaf carries no SAN extension")
	}
}
