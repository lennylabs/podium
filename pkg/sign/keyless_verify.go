package sign

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
	"errors"
	"fmt"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
)

// keylessInstance describes the §4.7.9 trust requirements to the shared
// bundle checks: a transparency log is required, and the trusted root's
// certificate transparency logs, when it lists any, require an SCT.
var keylessInstance = AttestationInstance{Name: "sigstore-keyless", TransparencyLog: true}

// verify runs the §4.7.9 acceptance conditions over a keyless bundle. It
// reuses the §4.7.11 bundle checks with the keyless differences: the
// bundle carries a message signature over the digest instead of a DSSE
// envelope, the integrated time of a log entry is no verified time, the
// log entry is a Rekor v2 hashedrekord 0.0.2 entry whose log key window
// contains each verified time, the leaf key is ECDSA P-256, and the
// identity is matched against the configured identity list.
//
// Spec: §4.7.9.
func (s SigstoreKeyless) verify(contentHash, signature string) error {
	if err := s.Identity.Validate(); err != nil {
		return err
	}
	if len(s.TrustRoot) == 0 {
		return errors.New("no trust root configured")
	}
	ar, err := ParseAttestationRoot(s.TrustRoot)
	if err != nil {
		return fmt.Errorf("trust root does not load: %w", err)
	}
	digest, err := sha256Digest(contentHash)
	if err != nil {
		return err
	}
	b, pb, err := parseKeylessBundle([]byte(signature), digest)
	if err != nil {
		return err
	}
	v := &bundleVerification{
		bundle: b, root: ar.material, inst: keylessInstance,
		sig: pb.GetMessageSignature().GetSignature(), artifactDigest: digest,
	}
	err = v.run(
		v.checkTimes, v.checkLeafValidity, v.checkKeylessLeafKey, v.checkChain, v.checkKeylessEntry,
		v.checkLog, v.checkSignature, v.checkCertificateFields, v.checkWholeBundle,
	)
	if err != nil {
		return err
	}
	return s.Identity.match(v.fields)
}

// parseKeylessBundle applies the shared bundle conditions and requires a
// message signature over the SHA-256 digest being verified. A keyless
// envelope of 0.7.0 or earlier carries no mediaType member, so it fails
// the media type condition, and the refusal says so.
func parseKeylessBundle(raw, digest []byte) (*bundle.Bundle, *protobundle.Bundle, error) {
	b, pb, err := parseSigstoreBundle(raw)
	if reason, _ := AttestationReasonOf(err); reason == ReasonMediaType {
		return nil, nil, refuse(ReasonMediaType, "the signature is not a version 0.3 Sigstore bundle; a keyless envelope from 0.7.0 or earlier is not accepted", nil)
	}
	if err != nil {
		return nil, nil, err
	}
	msg := pb.GetMessageSignature()
	switch {
	case msg == nil:
		return nil, nil, refuse(ReasonPayload, "the bundle carries no message signature", nil)
	case msg.GetMessageDigest().GetAlgorithm() != protocommon.HashAlgorithm_SHA2_256:
		return nil, nil, refuse(ReasonPayload, "the message digest is not SHA-256", nil)
	case !bytes.Equal(msg.GetMessageDigest().GetDigest(), digest):
		return nil, nil, refuse(ReasonPayload, "the message digest is not the digest being verified", nil)
	}
	return b, pb, nil
}

// checkKeylessEntry requires the bundle's log entry to have a Rekor v2
// body, of a log the trusted root lists, whose key window contains each
// verified time. The entry form is a §4.7.11 rule, which checkLog applies
// through checkLogEntryForm: the kind and version come from the signed
// body, and a Rekor v2 body is a hashedrekord 0.0.2 body under a
// checkpoint whose origin is the log's host, where the library rebuilds
// the entry from the bundle. §4.7.9 admits the Rekor v2 form alone, so a
// Rekor v1 body, which that rule accepts under a v1 checkpoint, is refused
// here. sigstore-go selects the log by the entry's log ID and does not
// read the window for an inclusion proof, so the window is checked here,
// as §4.7.9 requires of every trust-root entry. A log listed more than
// once under one log ID is known to the library by its last listing, and
// that listing's window applies.
//
// Spec: §4.7.9.
func (v *bundleVerification) checkKeylessEntry() error {
	entries, err := v.bundle.TlogEntries()
	if err != nil || len(entries) != 1 {
		return refuse(ReasonLogEntry, "the bundle carries no log entry", err)
	}
	if !entries[0].IsRekorV2() {
		return refuse(ReasonLogEntry, "the log entry body is not a Rekor v2 hashedrekord 0.0.2 entry", nil)
	}
	log, ok := v.root.RekorLogs()[hex.EncodeToString([]byte(entries[0].LogKeyID()))]
	if !ok {
		return refuse(ReasonLogEntry, "the log entry names no transparency log of the trusted root", nil)
	}
	for _, t := range v.times {
		if (!log.ValidityPeriodStart.IsZero() && t.Before(log.ValidityPeriodStart)) ||
			(!log.ValidityPeriodEnd.IsZero() && t.After(log.ValidityPeriodEnd)) {
			return refuse(ReasonLogEntry, "the log key's window excludes a verified time", nil)
		}
	}
	return nil
}

// checkKeylessLeafKey requires an ECDSA P-256 leaf key, the key type whose
// SHA-256 signature the hashedrekord entry records and that podium sign
// generates.
//
// Spec: §4.7.9.
func (v *bundleVerification) checkKeylessLeafKey() error {
	pub, ok := v.leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return refuse(ReasonSignature, "the leaf key is not an ECDSA P-256 key", nil)
	}
	return nil
}

// keylessRefusal wraps a verification failure in ErrSignatureInvalid. A
// refusal keeps its Podium-authored detail and names its reason, and the
// library error, when there is one, follows it, because podium verify
// reports the cause to the operator who runs it.
func keylessRefusal(err error) error {
	var r *AttestationRefusal
	if !errors.As(err, &r) {
		return fmt.Errorf("%w: %w", ErrSignatureInvalid, err)
	}
	if r.cause == nil {
		return fmt.Errorf("%w: %s (%s)", ErrSignatureInvalid, r.detail, r.Reason)
	}
	return fmt.Errorf("%w: %s (%s): %w", ErrSignatureInvalid, r.detail, r.Reason, r.cause)
}
