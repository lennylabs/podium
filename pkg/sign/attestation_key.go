package sign

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/signature"
	"google.golang.org/protobuf/proto"
)

// The §4.7.11 names a key-signed statement reads: the layer subject, whose
// presence requires a signed-time subject, and the signed-time subject.
const (
	layerSubjectName   = ".podium/layer"
	layerSubjectPrefix = ".podium/layer/"
	// maxSignedTimeDigits is the most digits of <seconds>.
	maxSignedTimeDigits = 11
)

// ErrAttestationKeyInvalid wraps every refusal of a §4.7.11 public_key
// value: a value that is not one PEM block of type PUBLIC KEY, and a key of
// an algorithm or curve other than ECDSA P-256.
var ErrAttestationKeyInvalid = errors.New("attestation public key invalid")

// AttestationPublicKey is the public key of a §4.7.11 key-based signer: an
// ECDSA P-256 key and its identifier.
type AttestationPublicKey struct {
	key *ecdsa.PublicKey
	id  string
}

// ID returns the identifier of the key: sha256: and the 64 lowercase
// hexadecimal digits of the SHA-256 digest of its DER SubjectPublicKeyInfo
// encoding, with the curve point in uncompressed form.
//
// Spec: §4.7.11 (Key-based signers)
func (k AttestationPublicKey) ID() string { return k.id }

// ParseAttestationPublicKey reads a §4.7.11 public_key value: one PEM block
// of type PUBLIC KEY, with only white space around it, that holds the DER
// SubjectPublicKeyInfo encoding of an ECDSA P-256 key. A second block, other
// text before or after the block, a header line inside it, a block that does
// not decode, and a key of another algorithm or curve are refused with an
// error wrapping ErrAttestationKeyInvalid.
//
// The block must hold the encoding x509.MarshalPKIXPublicKey writes for the
// key, so the identifier, which digests that encoding, is the digest of the
// bytes the operator supplied; cosign and OpenSSL write that encoding.
//
// Spec: §4.7.11 (Key-based signers)
func ParseAttestationPublicKey(text string) (AttestationPublicKey, error) {
	trimmed := strings.Trim(text, " \t\r\n")
	// pem.Decode skips any text before the first block, so the value must
	// open with the block itself.
	if !strings.HasPrefix(trimmed, "-----BEGIN ") {
		return AttestationPublicKey{}, keyRefusal("the value does not open with a PEM block")
	}
	block, rest := pem.Decode([]byte(trimmed))
	switch {
	case block == nil:
		return AttestationPublicKey{}, keyRefusal("the PEM block does not decode")
	case block.Type != "PUBLIC KEY":
		return AttestationPublicKey{}, keyRefusal(fmt.Sprintf("the PEM block has the type %q, not PUBLIC KEY", block.Type))
	case len(block.Headers) > 0:
		return AttestationPublicKey{}, keyRefusal("the PEM block holds a header line")
	case len(bytes.Trim(rest, " \t\r\n")) > 0:
		return AttestationPublicKey{}, keyRefusal("the value holds text or a second block after the PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return AttestationPublicKey{}, fmt.Errorf("%w: the PEM block holds no SubjectPublicKeyInfo that decodes: %v", ErrAttestationKeyInvalid, err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return AttestationPublicKey{}, keyRefusal(fmt.Sprintf("the key is a %s key; a key-based signer takes an ECDSA P-256 key", keyAlgorithm(pub)))
	}
	if ec.Curve != elliptic.P256() {
		return AttestationPublicKey{}, keyRefusal(fmt.Sprintf("the key is on the %s curve; a key-based signer takes an ECDSA P-256 key", ec.Params().Name))
	}
	der, err := x509.MarshalPKIXPublicKey(ec)
	if err != nil || !bytes.Equal(der, block.Bytes) {
		return AttestationPublicKey{}, keyRefusal("the PEM block does not hold the DER encoding of the key with an uncompressed point")
	}
	sum := sha256.Sum256(der)
	return AttestationPublicKey{key: ec, id: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

// keyAlgorithm names the algorithm of a parsed public key for a refusal.
func keyAlgorithm(pub any) string {
	switch pub.(type) {
	case *rsa.PublicKey:
		return "RSA"
	case ed25519.PublicKey:
		return "Ed25519"
	case *ecdh.PublicKey:
		return "X25519"
	}
	return fmt.Sprintf("%T", pub)
}

// keyRefusal builds a refusal of a public_key value.
func keyRefusal(why string) error {
	return fmt.Errorf("%w: %s", ErrAttestationKeyInvalid, why)
}

// SignedTimeSubjectName returns the §4.7.11 signed-time subject name of t:
// .podium/signed-at/ followed by the seconds of t since the Unix epoch, in
// one to eleven digits with no sign and no leading zero. A time at or before
// the epoch, or after the eleven-digit range, has no name.
//
// Spec: §4.7.11 (Key-based signers), §7.3.5
func SignedTimeSubjectName(t time.Time) (string, error) {
	secs := t.Unix()
	if secs <= 0 || len(strconv.FormatInt(secs, 10)) > maxSignedTimeDigits {
		return "", fmt.Errorf("attestation: the time %s has no signed-time subject name", t.UTC().Format(time.RFC3339))
	}
	return reservedSignedAtPrefix + strconv.FormatInt(secs, 10), nil
}

// parseSignedTimeName reads <seconds> from a signed-time subject name and
// reports false for a name of another form.
func parseSignedTimeName(name string) (time.Time, bool) {
	digits, ok := strings.CutPrefix(name, reservedSignedAtPrefix)
	if !ok || digits == "" || len(digits) > maxSignedTimeDigits || digits[0] == '0' ||
		strings.Trim(digits, "0123456789") != "" {
		return time.Time{}, false
	}
	// Eleven digits fit in an int64, so the parse does not fail.
	secs, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0).UTC(), true
}

// isLayerSubjectName reports whether name is a layer subject name of any
// root: .podium/layer or a name below .podium/layer/.
func isLayerSubjectName(name string) bool {
	return name == layerSubjectName || strings.HasPrefix(name, layerSubjectPrefix)
}

// keySignedTime applies the §4.7.11 signed-time rule to the statement of a
// key-signed bundle: at most one subject under .podium/signed-at/, in the
// signed-time form, and one whenever the statement lists a layer subject. It
// returns the signed time, or false for a statement that lists none.
//
// Spec: §4.7.11 (Key-based signers)
func keySignedTime(st AttestationStatement) (time.Time, bool, error) {
	var names []string
	layer := false
	for _, s := range st.Subjects {
		if strings.HasPrefix(s.Name, reservedSignedAtPrefix) {
			names = append(names, s.Name)
		}
		layer = layer || isLayerSubjectName(s.Name)
	}
	switch {
	case len(names) > 1:
		return time.Time{}, false, refuse(ReasonReservedSubject, "the key-signed statement lists more than one subject under .podium/signed-at/", nil)
	case len(names) == 1:
		t, ok := parseSignedTimeName(names[0])
		if !ok {
			return time.Time{}, false, refuse(ReasonReservedSubject, "the signed-time subject is not .podium/signed-at/<seconds> with one to eleven digits and no leading zero", nil)
		}
		return t, true, nil
	case layer:
		return time.Time{}, false, refuse(ReasonReservedSubject, "the key-signed statement lists a layer subject and no signed-time subject", nil)
	}
	return time.Time{}, false, nil
}

// refuseSignedTimeSubjects refuses a statement that lists a subject under
// .podium/signed-at/, which no certificate-signed bundle may list.
func refuseSignedTimeSubjects(st AttestationStatement) error {
	for _, s := range st.Subjects {
		if strings.HasPrefix(s.Name, reservedSignedAtPrefix) {
			return refuse(ReasonReservedSubject, "the statement lists a subject under .podium/signed-at/", nil)
		}
	}
	return nil
}

// VerifyKeySignedAttestation applies the §4.7.11 acceptance conditions of a
// key-based signer with key to raw: the bundle, envelope, and statement
// conditions every bundle meets, the signed-time rule, a public key hint and
// no certificate, no log entry, and a DSSE signature that verifies under key.
// It reads no timestamp: the timestamps, which lie outside the signed bytes
// and which any holder of the bundle can replace, are removed from a copy of
// the bundle before sigstore-go verifies it, so a timestamp that does not
// verify changes nothing. The keyid of the signature and the bundle's public
// key hint lie outside the signed bytes too: the key handed to the library
// is key whatever the hint names, and the keyid is cleared in the copy,
// because the library's DSSE verifier passes over a signature whose keyid
// differs from its own fingerprint of the key.
//
// The result carries the key's identifier as Key and the signed time, when
// the statement lists one, as AttestedTime. A refusal is an
// *AttestationRefusal.
//
// Spec: §4.7.11 (Key-based signers)
func VerifyKeySignedAttestation(raw []byte, key AttestationPublicKey) (*VerifiedAttestation, error) {
	if key.key == nil {
		return nil, fmt.Errorf("%w: no key", ErrAttestationKeyInvalid)
	}
	_, pb, err := parseAttestationBundle(raw)
	if err != nil {
		return nil, err
	}
	// A bundle signed under a certificate is refused before its statement
	// is read, so the refusal names the kind of bundle rather than a
	// signed-time rule that applies to key-signed statements alone.
	vm := pb.GetVerificationMaterial()
	if vm.GetPublicKey() == nil {
		return nil, refuse(ReasonSignature, "the bundle is signed under a certificate, and a key-based signer accepts a key-signed bundle alone", nil)
	}
	stmt, err := parseStatementPayload(pb.GetDsseEnvelope().GetPayload())
	if err != nil {
		return nil, err
	}
	signedAt, hasTime, err := keySignedTime(stmt)
	if err != nil {
		return nil, err
	}
	if len(vm.GetTlogEntries()) > 0 {
		return nil, refuse(ReasonLogEntry, "the key-signed bundle carries a log entry", nil)
	}
	b, err := keyVerificationBundle(pb)
	if err != nil {
		return nil, err
	}
	if err := verifyUnderKey(b, key); err != nil {
		return nil, err
	}
	out := &VerifiedAttestation{Statement: stmt, Key: key.id}
	if hasTime {
		out.AttestedTime = signedAt
	}
	return out, nil
}

// keyVerificationBundle returns the bundle sigstore-go verifies for a
// key-signed bundle: a copy of pb without its timestamps and with the keyid
// of its one signature cleared.
func keyVerificationBundle(pb *protobundle.Bundle) (*bundle.Bundle, error) {
	c, ok := proto.Clone(pb).(*protobundle.Bundle)
	if !ok {
		// proto.Clone returns a message of the type it is given.
		return nil, refuse(ReasonParse, "the bundle does not copy", nil)
	}
	c.VerificationMaterial.TimestampVerificationData = nil
	for _, s := range c.GetDsseEnvelope().GetSignatures() {
		s.Keyid = ""
	}
	b, err := bundle.NewBundle(c)
	if err != nil {
		// The copy differs from a bundle that already parsed only in
		// members the parse does not read.
		return nil, refuse(ReasonParse, "the bundle is not a valid version 0.3 bundle", err)
	}
	return b, nil
}

// verifyUnderKey verifies the DSSE signature of b under key, first alone and
// then through sigstore-go's full verifier with no time required, which a
// key-signed bundle has none of.
func verifyUnderKey(b *bundle.Bundle, key AttestationPublicKey) error {
	verifier, err := signature.LoadECDSAVerifier(key.key, crypto.SHA256)
	if err != nil {
		return fmt.Errorf("attestation: load the verifier of key %s: %w", key.id, err)
	}
	// The library asks the trusted material for the key that the bundle's
	// hint names. The answer is the signer's key whatever the hint says.
	material := root.NewTrustedPublicKeyMaterial(func(string) (root.TimeConstrainedVerifier, error) {
		return root.NewExpiringKey(verifier, time.Time{}, time.Time{}), nil
	})
	sc, err := b.SignatureContent()
	if err != nil {
		return refuse(ReasonPayload, "the DSSE envelope does not parse", err)
	}
	vc, err := b.VerificationContent()
	if err != nil {
		return refuse(ReasonParse, "the public key hint does not parse", err)
	}
	if err := verify.VerifySignature(sc, vc, material); err != nil {
		return refuse(ReasonSignature, "the signature does not verify under the signer's public key", err)
	}
	v, err := verify.NewVerifier(material, verify.WithNoObserverTimestamps())
	if err != nil {
		return fmt.Errorf("attestation: configure the key verifier: %w", err)
	}
	if _, err := v.Verify(b, verify.NewPolicy(verify.WithoutArtifactUnsafe(), verify.WithKey())); err != nil {
		return refuse(ReasonSignature, "the library refused the key-signed bundle", err)
	}
	return nil
}
