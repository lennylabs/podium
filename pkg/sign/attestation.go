package sign

import (
	"context"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"slices"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// AttestationInstance is a Sigstore instance of §4.7.11: a named trusted
// root with the logs the instance requires.
type AttestationInstance struct {
	// Name is the instance a signer names, such as public-good or github.
	Name string
	// Root supplies the instance's trusted root.
	Root AttestationRootSource
	// TransparencyLog requires a log entry with an inclusion proof and
	// admits the integrated time of a signed entry timestamp as a time.
	TransparencyLog bool
	// CertificateTransparency requires an SCT of the leaf that verifies
	// under a certificate transparency log of the root, even when the root
	// lists none, so such a root accepts no bundle. A root that lists CT
	// logs requires the SCT whatever this flag holds: the flag can add the
	// requirement and cannot remove it, so the zero value fails closed.
	CertificateTransparency bool
}

// VerifiedAttestation is a bundle that met every §4.7.11 acceptance
// condition that does not depend on a signer. AcceptsSigner applies the
// rest. Every member comes from bytes the verifier checked: the statement
// from the signed payload, the times from the timestamps and the signed
// entry timestamp, and the certificate fields from the raw extensions of
// the leaf in the bundle's verification content.
type VerifiedAttestation struct {
	// Instance is the instance the bundle verified under.
	Instance string
	// Statement is the signed in-toto statement.
	Statement AttestationStatement
	// VerifiedTimes lists every verified time in ascending order.
	VerifiedTimes []time.Time
	// AttestedTime is the earliest verified time.
	AttestedTime time.Time
	// Certificate holds the §4.7.11 fields of the leaf.
	Certificate CertificateFields
	// RSATimestampWithoutSHA256 marks a bundle with a timestamp whose RSA
	// signature uses a digest other than SHA-256, which TypeScript
	// consumers refuse.
	RSATimestampWithoutSHA256 bool
}

// AcceptsSigner applies the §4.7.11 identity comparison and the source
// and ref bindings of s. It returns nil when the bundle is accepted for s,
// and an *AttestationRefusal otherwise.
//
// Spec: §4.7.11.
func (v *VerifiedAttestation) AcceptsSigner(s AttestationSigner) error {
	return matchSigner(v.Certificate, s)
}

// VerifyAttestationBundle applies to raw the §4.7.11 acceptance conditions
// that do not depend on a signer, under inst. The media type, the
// envelope, the statement, and the timestamp condition are checked in
// Podium code before sigstore-go runs. The library then verifies each
// condition in turn, so a refusal names the condition that failed, and
// its full verifier runs last with its identity policy off. A refusal is
// an *AttestationRefusal; an error from inst.Root is returned as it is,
// wrapped with the instance name, and is no refusal of the bundle.
//
// Spec: §4.7.11.
func VerifyAttestationBundle(ctx context.Context, raw []byte, inst AttestationInstance) (*VerifiedAttestation, error) {
	if inst.Root == nil {
		return nil, fmt.Errorf("attestation: instance %q has no trusted root", inst.Name)
	}
	ar, err := inst.Root.AttestationRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("attestation: trusted root of instance %q: %w", inst.Name, err)
	}
	// A source that returns no root, or a zero AttestationRoot that no
	// loader built, holds no trust material; verification stops rather
	// than dereference it.
	if ar == nil || ar.material == nil {
		return nil, fmt.Errorf("attestation: trusted root of instance %q: %w: no trust material", inst.Name, ErrAttestationRootInvalid)
	}
	b, pb, err := parseAttestationBundle(raw)
	if err != nil {
		return nil, err
	}
	stmt, err := parseAttestationStatement(pb.GetDsseEnvelope().GetPayload())
	if err != nil {
		return nil, err
	}
	v := &bundleVerification{bundle: b, root: ar.material, inst: inst, sig: pb.GetDsseEnvelope().GetSignatures()[0].GetSig()}
	if err := v.run(); err != nil {
		return nil, err
	}
	return &VerifiedAttestation{
		Instance:                  inst.Name,
		Statement:                 stmt,
		VerifiedTimes:             v.times,
		AttestedTime:              v.times[0],
		Certificate:               v.fields,
		RSATimestampWithoutSHA256: v.rsaNonSHA256,
	}, nil
}

// bundleVerification carries one bundle through the staged checks.
type bundleVerification struct {
	bundle       *bundle.Bundle
	root         *root.TrustedRoot
	inst         AttestationInstance
	sig          []byte
	leaf         *x509.Certificate
	times        []time.Time
	rsaNonSHA256 bool
	fields       CertificateFields
}

// run applies the conditions in the §4.7.11 reason order, so the first
// failure is the reason reported.
func (v *bundleVerification) run() error {
	steps := []func() error{
		v.checkTimes, v.checkLeafValidity, v.checkChain, v.checkLog, v.checkSignature,
		v.checkCertificateFields, v.checkWholeBundle,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// checkTimes collects the verified times: the timestamps that pass the
// timestamp condition and, on an instance with a transparency log, the
// integrated time of a signed entry timestamp that verifies.
func (v *bundleVerification) checkTimes() error {
	ev, err := checkTimestampCondition(v.bundle, v.root, v.sig, v.inst.TransparencyLog)
	if err != nil {
		return err
	}
	times := ev.times
	if v.inst.TransparencyLog {
		if t, ok := signedEntryTime(v.bundle, v.root); ok {
			times = append(times, t)
		}
	}
	if len(times) == 0 {
		return refuse(ReasonTime, "the bundle carries no verified time", nil)
	}
	v.times, v.rsaNonSHA256 = sortedTimes(times), ev.rsaNonSHA256
	return nil
}

// checkLeafValidity reads the leaf from the bundle's verification content
// and requires it to be valid at every verified time.
func (v *bundleVerification) checkLeafValidity() error {
	vc, err := v.bundle.VerificationContent()
	if err != nil {
		return refuse(ReasonParse, "the leaf certificate does not parse", err)
	}
	if v.leaf = vc.Certificate(); v.leaf == nil {
		return refuse(ReasonChain, "the bundle carries no leaf certificate", nil)
	}
	for _, t := range v.times {
		if t.Before(v.leaf.NotBefore) || t.After(v.leaf.NotAfter) {
			return refuse(ReasonTime, "the leaf certificate is not valid at a verified time", nil)
		}
	}
	return nil
}

// checkCertificateFields reads the §4.7.11 fields of the leaf the library
// verified and refuses a malformed certificate before the full verifier
// runs, so the refusal names the field. The library's certificate summary
// also refuses some of these certificates, and its refusal would name no
// field.
func (v *bundleVerification) checkCertificateFields() error {
	fields, err := readCertificateFields(v.leaf)
	v.fields = fields
	return err
}

// requireSCT reports whether the instance has certificate transparency
// logs: its root lists one, or its configuration requires them.
func (v *bundleVerification) requireSCT() bool {
	return v.inst.CertificateTransparency || len(v.root.CTLogs()) > 0
}

// checkChain verifies the leaf's chain at each verified time and its code
// signing usage, then its SCT where the instance has certificate
// transparency logs.
func (v *bundleVerification) checkChain() error {
	leaf := chainCheckCopy(v.leaf)
	var chains [][]*x509.Certificate
	for _, t := range v.times {
		c, err := verify.VerifyLeafCertificate(t, leaf, v.root)
		if err != nil {
			return refuse(ReasonChain, "the leaf does not chain to a certificate authority of the instance", err)
		}
		chains = c
	}
	// VerifyOptions.KeyUsages admits a leaf with no extended key usage and
	// one that lists only the any-purpose usage, because crypto/x509 skips
	// the check for the first and passes the second. §4.7.11 requires the
	// code-signing usage itself, as §4.7.9 does.
	if !slices.Contains(v.leaf.ExtKeyUsage, x509.ExtKeyUsageCodeSigning) {
		return refuse(ReasonChain, "the leaf's extended key usage does not list code signing", nil)
	}
	if !v.requireSCT() {
		return nil
	}
	if err := verify.VerifySignedCertificateTimestamp(chains, 1, v.root); err != nil {
		return refuse(ReasonCertificateTimestamp, "no signed certificate timestamp of the leaf verifies", err)
	}
	return nil
}

// chainCheckCopy returns a copy of leaf whose unhandled critical
// extensions omit the subject alternative name. crypto/x509 records a
// critical SAN that holds only an OtherName as unhandled, and sigstore-go's
// full verifier removes it the same way before its chain check, so the
// staged check agrees with the library. The identity comparison refuses
// such a leaf on its own.
func chainCheckCopy(leaf *x509.Certificate) *x509.Certificate {
	c := *leaf
	c.UnhandledCriticalExtensions = slices.DeleteFunc(slices.Clone(leaf.UnhandledCriticalExtensions), func(oid asn1.ObjectIdentifier) bool {
		return oid.Equal(oidSubjectAltName)
	})
	return &c
}

// checkLog requires, on an instance with a transparency log, one log entry
// with an inclusion proof that verifies under a log key of the instance.
func (v *bundleVerification) checkLog() error {
	if !v.inst.TransparencyLog {
		return nil
	}
	entries, err := v.bundle.TlogEntries()
	if err != nil || len(entries) != 1 || !entries[0].HasInclusionProof() {
		return refuse(ReasonLogEntry, "the bundle carries no log entry with an inclusion proof", err)
	}
	if _, err := verify.VerifyTlogEntry(v.bundle, v.root, 1, true); err != nil {
		return refuse(ReasonLogEntry, "the log entry does not verify", err)
	}
	return nil
}

// checkSignature verifies the DSSE signature under the leaf's key.
func (v *bundleVerification) checkSignature() error {
	// Neither call fails here: parseAttestationBundle admitted the DSSE
	// envelope, and checkLeafValidity parsed the verification content.
	// The checks keep a library change from passing a nil value on.
	sigContent, err := v.bundle.SignatureContent()
	if err != nil {
		return refuse(ReasonPayload, "the DSSE envelope does not parse", err)
	}
	vc, err := v.bundle.VerificationContent()
	if err != nil {
		return refuse(ReasonParse, "the leaf certificate does not parse", err)
	}
	if err := verify.VerifySignature(sigContent, vc, v.root); err != nil {
		return refuse(ReasonSignature, "the signature does not verify under the leaf's key", err)
	}
	return nil
}

// checkWholeBundle runs sigstore-go's full verifier over the bundle with
// the instance's requirements and its identity policy off. The identity
// comparison is Podium's (§4.7.11), so no library policy runs. Every
// earlier condition held, so a refusal here is reported under the last of
// the library's conditions.
func (v *bundleVerification) checkWholeBundle() error {
	opts := []verify.VerifierOption{verify.WithoutStatementPredicate()}
	if v.inst.TransparencyLog {
		opts = append(opts, verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
	} else {
		opts = append(opts, verify.WithSignedTimestamps(1))
	}
	if v.requireSCT() {
		opts = append(opts, verify.WithSignedCertificateTimestamps(1))
	}
	verifier, err := verify.NewVerifier(v.root, opts...)
	if err != nil {
		return fmt.Errorf("attestation: configure verifier: %w", err)
	}
	policy := verify.NewPolicy(verify.WithoutArtifactUnsafe(), verify.WithoutIdentitiesUnsafe())
	if _, err := verifier.Verify(v.bundle, policy); err != nil {
		return refuse(ReasonSignature, "the library refused the bundle", err)
	}
	return nil
}
