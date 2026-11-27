package sign

import "github.com/sigstore/sigstore-go/pkg/verify"

// BundleSummary is what an author attestation bundle states before any of
// its signatures, chains, or times are verified: the signed statement and the
// issuer and identity its leaf certificate names. `podium attest import`
// reads it to group bundles by signer and predicate type when it prunes an
// attestation directory, and nothing that reads it may treat it as an
// accepted bundle.
type BundleSummary struct {
	// Statement is the in-toto statement of the DSSE payload.
	Statement AttestationStatement
	// Issuer and Identity are read from the leaf certificate as §4.7.11
	// names them. Each is empty when the certificate carries no such field
	// or the field is malformed.
	Issuer   string
	Identity string
	// Certificate holds every §4.7.11 field of the leaf, read as
	// VerifyAttestationBundle reads it, without a check of the chain.
	Certificate CertificateFields
}

// MatchesSigner applies the §4.7.11 identity comparison and the source and
// ref bindings of s to the leaf certificate, with no check of the chain, the
// time, or the signature. A process that is behind a §4.7.11 root record
// applies it to a bundle the registry verified when it stored the bundle, to
// downgrade a link whose signer left the policy whatever its root. It
// returns nil when the certificate matches, and an *AttestationRefusal
// otherwise.
//
// Spec: §4.7.11 (Re-evaluation)
func (b BundleSummary) MatchesSigner(s AttestationSigner) error {
	return matchSigner(b.Certificate, s)
}

// InspectAttestationBundle parses raw under the §4.7.11 bundle format, with
// the parse that VerifyAttestationBundle applies, and reads its statement
// and the issuer and identity of its leaf certificate without verifying
// anything. A bundle that is not in the format returns an
// *AttestationRefusal. A certificate field that is absent or malformed
// leaves its member empty and refuses nothing, because a verifier refuses
// such a bundle for every signer and the caller only groups bundles.
//
// Spec: §4.7.11, §7.3.5.
func InspectAttestationBundle(raw []byte) (BundleSummary, error) {
	b, pb, err := parseAttestationBundle(raw)
	if err != nil {
		return BundleSummary{}, err
	}
	stmt, err := parseAttestationStatement(pb.GetDsseEnvelope().GetPayload())
	if err != nil {
		return BundleSummary{}, err
	}
	out := BundleSummary{Statement: stmt}
	// A bundle that passed the library parse carries verification content;
	// a bundle with a public key hint has no leaf, and its signer is empty.
	vc, err := b.VerificationContent()
	if err != nil {
		return out, nil
	}
	if leaf := vc.Certificate(); leaf != nil {
		fields, _ := readCertificateFields(leaf)
		out.Issuer, out.Identity, out.Certificate = fields.Issuer.Value, fields.Identity.Value, fields
	}
	return out, nil
}

// CheckAttestationSelfSignature parses raw as InspectAttestationBundle does
// and verifies its DSSE signature under the public key of its own leaf
// certificate. It checks no chain, time, log entry, or trusted root, so it
// proves only that the holder of that certificate's key signed the
// statement. `podium attest import` applies it before it groups an imported
// bundle by the signer the certificate names, so a bundle with a corrupted
// or substituted signature cannot remove another bundle of that signer. A
// bundle without a leaf certificate, or whose signature does not verify,
// returns an *AttestationRefusal.
//
// Spec: §7.3.5.
func CheckAttestationSelfSignature(raw []byte) error {
	b, _, err := parseAttestationBundle(raw)
	if err != nil {
		return err
	}
	// parseAttestationBundle admitted the bundle through the library's
	// parse, which builds its verification and signature content; the
	// checks keep a library change from passing a nil value on.
	vc, err := b.VerificationContent()
	if err != nil {
		return refuse(ReasonParse, "the leaf certificate does not parse", err)
	}
	if vc.Certificate() == nil {
		return refuse(ReasonChain, "the bundle carries no leaf certificate", nil)
	}
	sc, err := b.SignatureContent()
	if err != nil {
		return refuse(ReasonPayload, "the DSSE envelope does not parse", err)
	}
	// With a certificate, the library takes the verifier from the leaf's
	// key and reads no trusted material.
	if err := verify.VerifySignature(sc, vc, nil); err != nil {
		return refuse(ReasonSignature, "the signature does not verify under the leaf's key", err)
	}
	return nil
}
