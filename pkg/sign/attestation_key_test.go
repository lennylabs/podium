package sign_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// signedAt is the signed time of the key-signed fixtures.
var signedAt = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// keyStatement is a key-signed statement of the layer subject, its
// signed-time subject, and one artifact.
func keyStatement(t testing.TB) []byte {
	return sh.Statement(t, slsaProvenance,
		sh.Subject{Name: layerSubject, SHA256: digestA},
		sh.SignedTimeSubject(signedAt, digestA),
		sh.Subject{Name: "finance/close-books@1.4.0", SHA256: digestA})
}

// keySigner returns a signing key and the key-based signer of its public
// key.
func keySigner(t testing.TB) (*ecdsa.PrivateKey, sign.AttestationSigner) {
	t.Helper()
	key := sh.NewSigningKey(t)
	return key, sign.AttestationSigner{PublicKey: sh.PublicKeyPEM(t, key.Public())}
}

// mustKey parses the public key of s.
func mustKey(t testing.TB, s sign.AttestationSigner) sign.AttestationPublicKey {
	t.Helper()
	k, err := sign.ParseAttestationPublicKey(s.PublicKey)
	if err != nil {
		t.Fatalf("ParseAttestationPublicKey: %v", err)
	}
	return k
}

// TestVerifyKeySigned_Accepts verifies a key-signed bundle with no
// timestamp, reads its signed time, and accepts it for its signer alone.
//
// Spec: §4.7.11 (Key-based signers)
func TestVerifyKeySigned_Accepts(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	key, s := keySigner(t)
	raw := h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key))
	v, err := sign.VerifyKeySignedAttestation(raw, mustKey(t, s))
	if err != nil {
		t.Fatalf("VerifyKeySignedAttestation: %v", err)
	}
	if !v.AttestedTime.Equal(signedAt) {
		t.Fatalf("AttestedTime = %s, want %s", v.AttestedTime, signedAt)
	}
	if want := sh.KeyIdentifier(t, key.Public()); v.Key != want || v.SignerIdentity() != want || v.SignerIssuer() != "" || v.Instance != "" {
		t.Fatalf("signer = (%q, %q, %q, key %q), want the empty instance and issuer and %s", v.Instance, v.SignerIssuer(), v.SignerIdentity(), v.Key, want)
	}
	if err := v.AcceptsSigner(s); err != nil {
		t.Fatalf("AcceptsSigner(own key) = %v", err)
	}
	_, other := keySigner(t)
	wantReason(t, v.AcceptsSigner(other), sign.ReasonSignature)
	wantReason(t, v.AcceptsSigner(emailSigner()), sign.ReasonIssuer)
	wantReason(t, v.AcceptsSigner(sign.AttestationSigner{PublicKey: "not a key"}), sign.ReasonSignature)
}

// TestVerifyKeySigned_Refusals covers each §4.7.11 refusal of a key-signed
// bundle under its own key.
//
// Spec: §4.7.11 (Key-based signers, Refusal reasons)
func TestVerifyKeySigned_Refusals(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	key, s := keySigner(t)
	other := sh.NewSigningKey(t)
	stmt := func(subjects ...sh.Subject) []byte { return sh.Statement(t, slsaProvenance, subjects...) }
	layer := sh.Subject{Name: layerSubject, SHA256: digestA}
	art := sh.Subject{Name: "finance/close-books@1.4.0", SHA256: digestA}
	for _, c := range []struct {
		name   string
		raw    []byte
		reason sign.AttestationReason
	}{
		{"two signed-time subjects", h.DSSEBundle(stmt(layer, sh.SignedTimeSubject(signedAt, digestA), sh.SignedTimeSubject(signedAt.Add(time.Second), digestA)), sh.WithSigningKey(key)), sign.ReasonReservedSubject},
		{"layer subject without a signed time", h.DSSEBundle(stmt(layer, art), sh.WithSigningKey(key)), sign.ReasonReservedSubject},
		{"layer subject of a root without a signed time", h.DSSEBundle(stmt(sh.Subject{Name: ".podium/layer/skills", SHA256: digestA}), sh.WithSigningKey(key)), sign.ReasonReservedSubject},
		{"leading zero", h.DSSEBundle(stmt(layer, sh.Subject{Name: sh.SignedAtPrefix + "0123", SHA256: digestA}), sh.WithSigningKey(key)), sign.ReasonReservedSubject},
		{"zero", h.DSSEBundle(stmt(layer, sh.Subject{Name: sh.SignedAtPrefix + "0", SHA256: digestA}), sh.WithSigningKey(key)), sign.ReasonReservedSubject},
		{"twelve digits", h.DSSEBundle(stmt(layer, sh.Subject{Name: sh.SignedAtPrefix + "100000000000", SHA256: digestA}), sh.WithSigningKey(key)), sign.ReasonReservedSubject},
		{"sign", h.DSSEBundle(stmt(layer, sh.Subject{Name: sh.SignedAtPrefix + "+1768478400", SHA256: digestA}), sh.WithSigningKey(key)), sign.ReasonReservedSubject},
		{"empty seconds", h.DSSEBundle(stmt(layer, sh.Subject{Name: sh.SignedAtPrefix, SHA256: digestA}), sh.WithSigningKey(key)), sign.ReasonReservedSubject},
		{"log entry", h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithRekorV1Entry()), sign.ReasonLogEntry},
		{"another key's signature", h.DSSEBundle(keyStatement(t), sh.WithSigningKey(other)), sign.ReasonSignature},
		{"bad signature", h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithBadSignature()), sign.ReasonSignature},
		{"certificate-signed", ghBundle(t, h), sign.ReasonSignature},
		{"certificate-signed with a signed time", h.DSSEBundle(keyStatement(t), sh.WithTimestamp(sh.TSAPrimary)), sign.ReasonSignature},
		{"two signatures", h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithSecondSignature()), sign.ReasonPayload},
		{"version 0.1", h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithMediaType(sh.BundleMediaTypeV01)), sign.ReasonMediaType},
		{"message signature", h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithMessageSignature()), sign.ReasonPayload},
		{"not JSON", []byte("{"), sign.ReasonParse},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := sign.VerifyKeySignedAttestation(c.raw, mustKey(t, s))
			wantReason(t, err, c.reason)
		})
	}
}

// TestVerifyKeySigned_IgnoresTimestampsAndHints verifies a key-signed bundle
// whose timestamp does not verify, whose authority no root lists, and whose
// keyid and hint name another key, and refuses one that names the signer's
// key and carries another key's signature.
//
// Spec: §4.7.11 (Key-based signers)
func TestVerifyKeySigned_IgnoresTimestampsAndHints(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	key, s := keySigner(t)
	other := sh.NewSigningKey(t)
	k := mustKey(t, s)
	for name, raw := range map[string][]byte{
		"valid timestamp":                 h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithTimestamp(sh.TSAPrimary)),
		"timestamp that does not verify":  h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithTimestamp(sh.TSAPrimary, sh.WithCorruptTimestamp())),
		"two timestamps of one authority": h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithTimestamp(sh.TSAPrimary), sh.WithTimestamp(sh.TSAPrimary)),
		"keyid and hint of another key": h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key),
			sh.WithKeyID(sh.KeyIdentifier(t, other.Public())), sh.WithKeyHint(sh.KeyIdentifier(t, other.Public()))),
		"no signed time and no layer subject": h.DSSEBundle(sh.Statement(t, slsaProvenance, sh.Subject{Name: "finance/close-books@1.4.0", SHA256: digestA}), sh.WithSigningKey(key)),
	} {
		t.Run(name, func(t *testing.T) {
			v, err := sign.VerifyKeySignedAttestation(raw, k)
			if err != nil {
				t.Fatalf("VerifyKeySignedAttestation: %v", err)
			}
			if err := v.AcceptsSigner(s); err != nil {
				t.Fatalf("AcceptsSigner: %v", err)
			}
		})
	}
	v, err := sign.VerifyKeySignedAttestation(h.DSSEBundle(sh.Statement(t, slsaProvenance, sh.Subject{Name: "a/b@1.0.0", SHA256: digestA}), sh.WithSigningKey(key)), k)
	if err != nil || !v.AttestedTime.IsZero() {
		t.Fatalf("a bundle with no signed time: err %v, attested time %s; want no attested time", err, v.AttestedTime)
	}
	named := h.DSSEBundle(keyStatement(t), sh.WithSigningKey(other),
		sh.WithKeyID(k.ID()), sh.WithKeyHint(k.ID()))
	_, err = sign.VerifyKeySignedAttestation(named, k)
	wantReason(t, err, sign.ReasonSignature)
	if _, err := sign.VerifyKeySignedAttestation(named, sign.AttestationPublicKey{}); !errors.Is(err, sign.ErrAttestationKeyInvalid) {
		t.Fatalf("zero key: err = %v, want ErrAttestationKeyInvalid", err)
	}
}

// TestVerifyKeySigned_CertificatePath refuses a key-signed bundle under
// every instance and a certificate-signed bundle that lists a signed-time
// subject, so a certificate signer accepts neither.
//
// Spec: §4.7.11 (Key-based signers, Acceptance conditions)
func TestVerifyKeySigned_CertificatePath(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	key, _ := keySigner(t)
	_, err := sign.VerifyAttestationBundle(context.Background(), h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key)), githubInstance(t, h))
	wantReason(t, err, sign.ReasonReservedSubject)
	_, err = sign.VerifyAttestationBundle(context.Background(), h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key), sh.WithTimestamp(sh.TSAPrimary)), githubInstance(t, h))
	wantReason(t, err, sign.ReasonReservedSubject)
	_, err = sign.VerifyAttestationBundle(context.Background(), h.DSSEBundle(sh.Statement(t, slsaProvenance, sh.Subject{Name: "a/b@1.0.0", SHA256: digestA}), sh.WithSigningKey(key), sh.WithTimestamp(sh.TSAPrimary)), githubInstance(t, h))
	wantReason(t, err, sign.ReasonChain)
	_, err = verify(t, githubInstance(t, h), ghBundle(t, h, sh.WithLeaf()), sign.AttestationSigner{PublicKey: sh.PublicKeyPEM(t, key.Public())})
	wantReason(t, err, sign.ReasonSignature)
}

// TestParseAttestationPublicKey covers the §4.7.11 public_key rules.
//
// Spec: §4.7.11 (Key-based signers)
func TestParseAttestationPublicKey(t *testing.T) {
	t.Parallel()
	key := sh.NewSigningKey(t)
	good := sh.PublicKeyPEM(t, key.Public())
	k, err := sign.ParseAttestationPublicKey("\n  " + good + "\n\n")
	if err != nil {
		t.Fatalf("surrounding white space: %v", err)
	}
	if k.ID() != sh.KeyIdentifier(t, key.Public()) {
		t.Fatalf("ID = %s, want %s", k.ID(), sh.KeyIdentifier(t, key.Public()))
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(good))
	compressed := compressedSPKI(t, key)
	for name, text := range map[string]string{
		"empty":                "",
		"RSA":                  sh.PublicKeyPEM(t, rsaKey.Public()),
		"Ed25519":              sh.PublicKeyPEM(t, edPub),
		"P-384":                sh.PublicKeyPEM(t, p384.Public()),
		"second block":         good + good,
		"trailing text":        good + "trailing\n",
		"leading text":         "cosign.pub:\n" + good,
		"private key type":     string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: block.Bytes})),
		"header":               string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Headers: map[string]string{"Comment": "x"}, Bytes: block.Bytes})),
		"undecodable":          string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("not der")})),
		"broken base64":        "-----BEGIN PUBLIC KEY-----\n!!!\n-----END PUBLIC KEY-----\n",
		"compressed point":     string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: compressed})),
		"trailing DER garbage": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: append(append([]byte{}, block.Bytes...), 0)})),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := sign.ParseAttestationPublicKey(text)
			if !errors.Is(err, sign.ErrAttestationKeyInvalid) {
				t.Fatalf("err = %v, want ErrAttestationKeyInvalid", err)
			}
		})
	}
	if _, err := sign.ParseAttestationPublicKey(sh.PublicKeyPEM(t, rsaKey.Public())); err == nil || !strings.Contains(err.Error(), "RSA") {
		t.Fatalf("RSA refusal %v does not name the algorithm", err)
	}
	if _, err := sign.ParseAttestationPublicKey(sh.PublicKeyPEM(t, edPub)); err == nil || !strings.Contains(err.Error(), "Ed25519") {
		t.Fatalf("Ed25519 refusal %v does not name the algorithm", err)
	}
}

// compressedSPKI is the SubjectPublicKeyInfo of key with its point in
// compressed form, which no accepted public_key holds.
func compressedSPKI(t testing.TB, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	// The uncompressed point is the last 65 bytes of the encoding: 0x04,
	// X, and Y. The compressed form is 0x02 or 0x03 and X, 32 bytes
	// shorter, so the BIT STRING and SEQUENCE lengths shrink by 32.
	point := der[len(der)-65:]
	prefix := byte(0x02)
	if point[64]&1 == 1 {
		prefix = 0x03
	}
	head := append([]byte{}, der[:len(der)-65]...)
	head[1] -= 32
	head[len(head)-2] -= 32
	return append(append(head, prefix), point[1:33]...)
}

// TestSignedTimeSubjectName writes and bounds the signed-time subject name.
//
// Spec: §4.7.11 (Key-based signers), §7.3.5
func TestSignedTimeSubjectName(t *testing.T) {
	t.Parallel()
	name, err := sign.SignedTimeSubjectName(signedAt)
	if err != nil || name != ".podium/signed-at/1768478400" {
		t.Fatalf("SignedTimeSubjectName = %q, %v", name, err)
	}
	for _, bad := range []time.Time{time.Unix(0, 0), time.Unix(-5, 0), time.Unix(100000000000, 0)} {
		if _, err := sign.SignedTimeSubjectName(bad); err == nil {
			t.Fatalf("SignedTimeSubjectName(%s) = nil error", bad)
		}
	}
}

// TestAttestationSigner_ValidateKey applies the load rules of a key-based
// signer.
//
// Spec: §4.7.11 (Key-based signers, Policy rules)
func TestAttestationSigner_ValidateKey(t *testing.T) {
	t.Parallel()
	_, s := keySigner(t)
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate = %v", err)
	}
	for name, mut := range map[string]func(*sign.AttestationSigner){
		"issuer":   func(s *sign.AttestationSigner) { s.Issuer = sh.DefaultIssuer },
		"identity": func(s *sign.AttestationSigner) { s.Identity = sh.DefaultSAN },
		"binding":  func(s *sign.AttestationSigner) { s.SourceRepository = skillsRepo },
		"any":      func(s *sign.AttestationSigner) { s.AnySource = true },
		"ref":      func(s *sign.AttestationSigner) { s.SourceRepositoryRef = mainRef },
		"any_ref":  func(s *sign.AttestationSigner) { s.AnyRef = true },
		"bad key":  func(s *sign.AttestationSigner) { s.PublicKey = "x" },
	} {
		c := s
		mut(&c)
		if err := c.Validate(); !errors.Is(err, sign.ErrAttestationSignerInvalid) {
			t.Fatalf("%s: Validate = %v, want ErrAttestationSignerInvalid", name, err)
		}
	}
}

// TestInspectAttestationBundle_KeySigned reads a key-signed bundle under the
// signed-time rule and refuses one that breaks it.
//
// Spec: §4.7.11 (Key-based signers), §7.3.5
func TestInspectAttestationBundle_KeySigned(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	key, _ := keySigner(t)
	sum, err := sign.InspectAttestationBundle(h.DSSEBundle(keyStatement(t), sh.WithSigningKey(key)))
	if err != nil || !sum.KeySigned || sum.Issuer != "" || sum.Identity != "" || len(sum.Statement.Subjects) != 3 {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
	_, err = sign.InspectAttestationBundle(h.DSSEBundle(sh.Statement(t, slsaProvenance, sh.Subject{Name: layerSubject, SHA256: digestA}), sh.WithSigningKey(key)))
	wantReason(t, err, sign.ReasonReservedSubject)
	_, err = sign.InspectAttestationBundle(h.DSSEBundle(keyStatement(t)))
	wantReason(t, err, sign.ReasonReservedSubject)
}
