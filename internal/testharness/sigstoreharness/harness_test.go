package sigstoreharness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"testing"
	"time"
)

// Spec: §4.7.9. A default envelope satisfies every acceptance condition
// for an independent verifier: the leaf chains to the Fulcio root with
// the code-signing usage at the clock and carries the default identity,
// the signature verifies over the digest, the hashedrekord v0.0.2 body
// binds the digest, signature, and leaf, and the timestamp covers the
// signature at the clock.
func TestEnvelope_DefaultIsWellFormed(t *testing.T) {
	t.Parallel()
	h := New(t)
	hash, digest := testContentHash("default")
	in := inspect(t, h.Envelope(t, hash))

	roots, inters := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(h.FulcioRoot())
	for _, c := range in.intermediates {
		inters.AddCert(c)
	}
	if _, err := in.leaf.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inters, CurrentTime: h.Clock(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}); err != nil {
		t.Fatalf("leaf chain: %v", err)
	}
	if got := in.leaf.EmailAddresses; len(got) != 1 || got[0] != DefaultSAN {
		t.Fatalf("leaf SANs = %v", got)
	}
	if v := extValue(in.leaf, OIDIssuerV2); !bytes.Equal(v, IssuerValue(DefaultIssuer)) {
		t.Fatalf("issuer extension = %x", v)
	}
	if !in.leaf.NotAfter.Equal(h.Clock().Add(15*time.Minute)) || !in.leaf.NotBefore.Equal(h.Clock().Add(-5*time.Minute)) {
		t.Fatalf("leaf window = %v..%v", in.leaf.NotBefore, in.leaf.NotAfter)
	}
	if !ecdsa.VerifyASN1(in.leaf.PublicKey.(*ecdsa.PublicKey), digest, in.sig) {
		t.Fatal("signature does not verify over the digest")
	}
	assertBodyBinds(t, in, digest)
	if in.env.TLog.LogIndex != 5 {
		t.Fatalf("log_index = %d, want 5", in.env.TLog.LogIndex)
	}
	imprint := sha256.Sum256(in.sig)
	equalBytes(t, "imprint", in.token.tst.MessageImprint.HashedMessage, imprint[:])
	if !in.token.tst.GenTime.Equal(h.Clock()) {
		t.Fatalf("genTime = %v, want %v", in.token.tst.GenTime, h.Clock())
	}
}

// assertBodyBinds checks the entry body binds the envelope's digest,
// signature, and leaf as a hashedrekord v0.0.2 entry.
func assertBodyBinds(t *testing.T, in inspection, digest []byte) {
	t.Helper()
	if in.body.Kind != "hashedrekord" || in.body.APIVersion != "0.0.2" {
		t.Fatalf("entry kind = %s %s", in.body.Kind, in.body.APIVersion)
	}
	v := in.body.Spec.V002
	if v.Data.Algorithm != "SHA2_256" {
		t.Fatalf("entry algorithm = %s", v.Data.Algorithm)
	}
	equalBytes(t, "entry digest", v.Data.Digest, digest)
	equalBytes(t, "entry signature", v.Signature.Content, in.sig)
	equalBytes(t, "entry certificate", v.Signature.Verifier.X509Certificate.RawBytes, in.leaf.Raw)
}

// Spec: §4.7.9. A generated checkpoint verifies under the exported log
// key: the P-256 key by default and the Ed25519 key under
// WithEd25519Checkpoint. A checkpoint signed by a foreign key verifies
// under neither.
func TestEnvelope_CheckpointVerifiesUnderLogKey(t *testing.T) {
	t.Parallel()
	h := New(t)
	hash, _ := testContentHash("checkpoint")
	cases := []struct {
		name     string
		opts     []EnvOpt
		p256, ed bool
	}{
		{name: "P-256", p256: true},
		{name: "Ed25519", opts: []EnvOpt{WithEd25519Checkpoint()}, ed: true},
		{name: "foreign key", opts: []EnvOpt{WithCheckpointSignedByForeignKey()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := inspect(t, h.Envelope(t, hash, tc.opts...))
			n, ok := parseNote(in.env.TLog.Checkpoint)
			if !ok {
				t.Fatalf("checkpoint does not parse: %q", in.env.TLog.Checkpoint)
			}
			if got := n.verifiesUnder(h.LogPublicKey()); got != tc.p256 {
				t.Fatalf("verifies under P-256 log key = %v, want %v", got, tc.p256)
			}
			if got := n.verifiesUnder(h.Ed25519LogPublicKey()); got != tc.ed {
				t.Fatalf("verifies under Ed25519 log key = %v, want %v", got, tc.ed)
			}
			if n.size != 7 {
				t.Fatalf("checkpoint size = %d, want 7", n.size)
			}
		})
	}
}

// Spec: §4.7.9. For every tree size from 1 through 9 and every index, the
// generated audit path recomputes the checkpoint root by the RFC 9162
// §2.1.3.2 algorithm, and the envelope's log_index is the entry's index.
func TestEnvelope_AuditPathRecomputesCheckpointRoot(t *testing.T) {
	t.Parallel()
	h := New(t)
	hash, _ := testContentHash("audit path")
	for size := 1; size <= 9; size++ {
		for index := 0; index < size; index++ {
			t.Run(fmt.Sprintf("%d_of_%d", index, size), func(t *testing.T) {
				in := inspect(t, h.Envelope(t, hash, WithTreeSize(size, index)))
				n, ok := parseNote(in.env.TLog.Checkpoint)
				if !ok || !n.verifiesUnder(h.LogPublicKey()) {
					t.Fatal("checkpoint does not verify under the log key")
				}
				if n.size != int64(size) || in.env.TLog.LogIndex != int64(index) {
					t.Fatalf("size %d index %d, want %d %d", n.size, in.env.TLog.LogIndex, size, index)
				}
				root, ok := in.proofRoot(int64(index), n.size)
				if !ok || !bytes.Equal(root, n.root) {
					t.Fatalf("audit path recomputes %x (fits %v), checkpoint root %x", root, ok, n.root)
				}
			})
		}
	}
}

// Spec: §4.7.9. A generated token's signed attributes, re-tagged from
// [0] IMPLICIT to SET, verify under the timestamp-authority signer; the
// messageDigest attribute is SHA-256 of the TSTInfo; and the signer
// chains to the timestamp-authority root with the time-stamping usage at
// the token's genTime.
func TestTimestampToken_SignedAttributesVerifyUnderTSALeaf(t *testing.T) {
	t.Parallel()
	h := New(t)
	sig := []byte("acme signature bytes")
	tok := parseToken(t, h.TimestampToken(sig))

	if len(tok.signers) != 1 {
		t.Fatalf("signers = %d, want 1", len(tok.signers))
	}
	if err := tok.attrsVerifyUnder(h.TSALeaf()); err != nil {
		t.Fatalf("signed attributes do not verify: %v", err)
	}
	md := sha256.Sum256(tok.eContent)
	equalBytes(t, "messageDigest", tok.messageDigest(t), md[:])
	if !tok.eContentType.Equal(oidTSTInfo) {
		t.Fatalf("eContentType = %v", tok.eContentType)
	}
	imprint := sha256.Sum256(sig)
	equalBytes(t, "imprint", tok.tst.MessageImprint.HashedMessage, imprint[:])
	if len(tok.embedded) != 1 || !tok.embedded[0].Equal(h.TSALeaf()) {
		t.Fatalf("embedded certificates = %d", len(tok.embedded))
	}
	if !hasCriticalTimeStamping(h.TSALeaf()) {
		t.Fatal("TSA signer lacks the critical time-stamping usage")
	}
	roots := x509.NewCertPool()
	roots.AddCert(h.TSARoot())
	if _, err := h.TSALeaf().Verify(x509.VerifyOptions{
		Roots: roots, CurrentTime: tok.tst.GenTime,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	}); err != nil {
		t.Fatalf("TSA chain: %v", err)
	}
	if len(h.TSARoot().ExtKeyUsage) != 0 || len(h.FulcioRoot().ExtKeyUsage) != 0 {
		t.Fatal("a harness root carries an extended key usage")
	}
}

// extValue returns the value of the extension oid, or nil.
func extValue(c *x509.Certificate, oid asn1.ObjectIdentifier) []byte {
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return e.Value
		}
	}
	return nil
}

// hasCriticalTimeStamping reports whether c lists the time-stamping usage
// in a critical extension.
func hasCriticalTimeStamping(c *x509.Certificate) bool {
	for _, e := range c.Extensions {
		if e.Id.Equal(oidExtKeyUsage) && e.Critical {
			return len(c.ExtKeyUsage) == 1 && c.ExtKeyUsage[0] == x509.ExtKeyUsageTimeStamping
		}
	}
	return false
}
