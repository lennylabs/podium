package sign

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"net/url"
	"unicode/utf8"

	"golang.org/x/crypto/cryptobyte"
	cbasn1 "golang.org/x/crypto/cryptobyte/asn1"
)

// The §4.7.11 certificate field extensions beside the Issuer (V2)
// extension, oidFulcioIssuerV2.
var (
	oidSubjectAltName          = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidSourceRepository        = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 12}
	oidSourceRepositoryDigest  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 13}
	oidSourceRepositoryRef     = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 14}
	oidSourceRepositoryID      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 15}
	oidSourceRepositoryOwner   = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 16}
	oidSourceRepositoryOwnerID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 17}
)

// GeneralName tags of the two subject alternative name kinds §4.7.11
// accepts: rfc822Name is [1] IMPLICIT IA5String and
// uniformResourceIdentifier is [6] IMPLICIT IA5String, both primitive.
var (
	sanTagEmail = cbasn1.Tag(1).ContextSpecific()
	sanTagURI   = cbasn1.Tag(6).ContextSpecific()
)

// CertField is one value read from the leaf certificate. Present is false
// when the certificate carries no extension for it.
type CertField struct {
	Value   string
	Present bool
}

// CertificateFields are the §4.7.11 values of a verified leaf, read from
// its raw extension bytes. The identity is the one email or URI subject
// alternative name; IdentityIsURI records which kind it is.
type CertificateFields struct {
	Issuer                  CertField
	Identity                CertField
	IdentityIsURI           bool
	SourceRepository        CertField
	SourceRepositoryDigest  CertField
	SourceRepositoryRef     CertField
	SourceRepositoryID      CertField
	SourceRepositoryOwner   CertField
	SourceRepositoryOwnerID CertField
}

// fieldSpec binds one extension to the field it fills and the reason a
// malformed value yields.
type fieldSpec struct {
	oid    asn1.ObjectIdentifier
	field  func(*CertificateFields) *CertField
	reason AttestationReason
}

// utf8Fields are the extensions whose value is one DER UTF8String, in the
// §4.7.11 reason order. The source commit has no policy member, and a
// malformed commit is reported with the source binding because it
// describes the source.
var utf8Fields = []fieldSpec{
	{oidFulcioIssuerV2, func(f *CertificateFields) *CertField { return &f.Issuer }, ReasonIssuer},
	{oidSourceRepository, func(f *CertificateFields) *CertField { return &f.SourceRepository }, ReasonSourceBinding},
	{oidSourceRepositoryDigest, func(f *CertificateFields) *CertField { return &f.SourceRepositoryDigest }, ReasonSourceBinding},
	{oidSourceRepositoryID, func(f *CertificateFields) *CertField { return &f.SourceRepositoryID }, ReasonSourceBinding},
	{oidSourceRepositoryOwner, func(f *CertificateFields) *CertField { return &f.SourceRepositoryOwner }, ReasonSourceBinding},
	{oidSourceRepositoryOwnerID, func(f *CertificateFields) *CertField { return &f.SourceRepositoryOwnerID }, ReasonSourceBinding},
	{oidSourceRepositoryRef, func(f *CertificateFields) *CertField { return &f.SourceRepositoryRef }, ReasonRefBinding},
}

// readCertificateFields reads the §4.7.11 fields from leaf.Extensions,
// which hold the raw DER of each extension. It reads neither the parsed
// URI or email lists of crypto/x509, which hold a re-serialized form, nor
// any library summary. A certificate is refused, whatever the signer, when
// it carries no Issuer (V2) extension, when its subject alternative name is
// not exactly one email or URI name, or when an extension of the table
// occurs twice or holds no single DER UTF8String. crypto/x509 refuses to
// parse a certificate with a repeated extension, so a leaf that reaches
// this function carries each extension once; the repeat check keeps the
// rule independent of that parser.
//
// Spec: §4.7.11.
func readCertificateFields(leaf *x509.Certificate) (CertificateFields, error) {
	var (
		out       CertificateFields
		malformed = map[AttestationReason]error{}
	)
	for _, spec := range utf8Fields {
		value, present, err := extensionUTF8(leaf, spec.oid)
		if err != nil {
			malformed[spec.reason] = err
			continue
		}
		*spec.field(&out) = CertField{Value: value, Present: present}
	}
	identity, isURI, err := sanIdentity(leaf)
	if err != nil {
		malformed[ReasonIdentity] = err
	} else {
		out.Identity, out.IdentityIsURI = CertField{Value: identity, Present: true}, isURI
	}
	if err := malformed[ReasonIssuer]; err != nil {
		return out, refuse(ReasonIssuer, "the issuer extension is malformed", err)
	}
	if !out.Issuer.Present {
		return out, refuse(ReasonIssuer, "the certificate carries no Issuer (V2) extension", nil)
	}
	for _, reason := range []AttestationReason{ReasonIdentity, ReasonSourceBinding, ReasonRefBinding} {
		if err := malformed[reason]; err != nil {
			return out, refuse(reason, "a certificate field is malformed", err)
		}
	}
	return out, nil
}

// extensionValue returns the raw value of the extension oid, false when
// the leaf carries none, and an error when it carries several.
func extensionValue(leaf *x509.Certificate, oid asn1.ObjectIdentifier) ([]byte, bool, error) {
	var (
		value []byte
		found bool
	)
	for _, ext := range leaf.Extensions {
		if !ext.Id.Equal(oid) {
			continue
		}
		if found {
			return nil, false, errors.New("the extension occurs more than once")
		}
		value, found = ext.Value, true
	}
	return value, found, nil
}

// extensionUTF8 returns the content of the one DER UTF8String that the
// extension oid holds.
func extensionUTF8(leaf *x509.Certificate, oid asn1.ObjectIdentifier) (string, bool, error) {
	value, found, err := extensionValue(leaf, oid)
	if err != nil || !found {
		return "", false, err
	}
	input := cryptobyte.String(value)
	var content cryptobyte.String
	if !input.ReadASN1(&content, cbasn1.UTF8String) || !input.Empty() || !utf8.Valid(content) {
		return "", false, errors.New("the extension value is not exactly one DER UTF8String")
	}
	return string(content), true, nil
}

// sanIdentity returns the content of the subject alternative name
// extension's one general name, and whether it is a URI. The extension
// must hold exactly one name, an rfc822Name or a uniformResourceIdentifier.
func sanIdentity(leaf *x509.Certificate) (identity string, isURI bool, err error) {
	value, found, err := extensionValue(leaf, oidSubjectAltName)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, errors.New("the certificate carries no subject alternative name")
	}
	input := cryptobyte.String(value)
	var names cryptobyte.String
	if !input.ReadASN1(&names, cbasn1.SEQUENCE) || !input.Empty() {
		return "", false, errors.New("the subject alternative name does not parse")
	}
	var (
		content cryptobyte.String
		tag     cbasn1.Tag
	)
	if !names.ReadAnyASN1(&content, &tag) {
		return "", false, errors.New("the subject alternative name holds no name")
	}
	if !names.Empty() {
		return "", false, errors.New("the subject alternative name holds more than one name")
	}
	switch tag {
	case sanTagEmail:
		return string(content), false, nil
	case sanTagURI:
		return string(content), true, nil
	default:
		return "", false, errors.New("the subject alternative name is neither an email address nor a URI")
	}
}

// AttestationSigner is a signer of a requirement (§4.7.11): an OIDC issuer
// and a certificate identity, compared byte for byte, with the source and
// ref binding members. An empty binding member is not named.
type AttestationSigner struct {
	Issuer   string
	Identity string

	SourceRepository        string
	SourceRepositoryOwner   string
	SourceRepositoryID      string
	SourceRepositoryOwnerID string
	// AnySource accepts a certificate that records no source fields. It
	// exists for an issuer whose certificates carry none.
	AnySource bool

	SourceRepositoryRef string
	// AnyRef accepts any source repository ref for a bound signer.
	AnyRef bool

	// PublicKey is the §4.7.11 public_key of a key-based signer, one PEM
	// block of an ECDSA P-256 key. A signer that names it names no other
	// member.
	PublicKey string
}

// ErrAttestationSignerInvalid signals a signer entry that a policy loader
// refuses (§4.7.11).
var ErrAttestationSignerInvalid = errors.New("attestation signer invalid")

// bound reports whether s names a source binding member.
func (s AttestationSigner) bound() bool {
	return s.SourceRepository != "" || s.SourceRepositoryOwner != "" ||
		s.SourceRepositoryID != "" || s.SourceRepositoryOwnerID != ""
}

// Validate applies the §4.7.11 load rules: an issuer and an identity are
// named, a signer whose identity is a URI names a source binding member or
// AnySource, and a bound signer names a ref or AnyRef. A key-based signer
// names a public key that ParseAttestationPublicKey admits and no other
// member. A loader refuses a policy that holds a signer this rejects.
//
// Spec: §4.7.11.
func (s AttestationSigner) Validate() error {
	if s.PublicKey != "" {
		return s.validateKey()
	}
	switch {
	case s.Issuer == "":
		return errors.Join(ErrAttestationSignerInvalid, errors.New("the signer names no issuer"))
	case s.Identity == "":
		return errors.Join(ErrAttestationSignerInvalid, errors.New("the signer names no identity"))
	case isURIIdentity(s.Identity) && !s.bound() && !s.AnySource:
		return errors.Join(ErrAttestationSignerInvalid, errors.New("a signer with a URI identity names no source binding and no any_source"))
	case s.bound() && s.SourceRepositoryRef == "" && !s.AnyRef:
		return errors.Join(ErrAttestationSignerInvalid, errors.New("a signer with a source binding names no source_repository_ref and no any_ref"))
	}
	return nil
}

// validateKey applies the load rules of a key-based signer.
func (s AttestationSigner) validateKey() error {
	if s.Issuer != "" || s.Identity != "" || s.bound() || s.AnySource || s.SourceRepositoryRef != "" || s.AnyRef {
		return errors.Join(ErrAttestationSignerInvalid, errors.New("a signer that names public_key names no issuer, identity, source binding member, any_source, source_repository_ref, or any_ref"))
	}
	if _, err := ParseAttestationPublicKey(s.PublicKey); err != nil {
		return errors.Join(ErrAttestationSignerInvalid, err)
	}
	return nil
}

// isURIIdentity reports whether a policy identity is a URI: it opens with
// an RFC 3986 scheme. An email address carries no scheme.
func isURIIdentity(identity string) bool {
	u, err := url.Parse(identity)
	return err == nil && u.Scheme != ""
}

// matchSigner applies the §4.7.11 identity comparison of s to the fields
// of a verified leaf, in the order issuer, identity, source binding, and
// ref binding. Each comparison is byte equality. readCertificateFields
// already refused a leaf with a malformed field or with no issuer or
// identity.
//
// Spec: §4.7.11.
func matchSigner(f CertificateFields, s AttestationSigner) error {
	// A signer that names no issuer or no identity accepts nothing, even
	// when its loader skipped Validate, so an empty value never matches.
	if s.Issuer == "" {
		return refuse(ReasonIssuer, "the signer names no issuer", nil)
	}
	if s.Identity == "" {
		return refuse(ReasonIdentity, "the signer names no identity", nil)
	}
	if f.Issuer.Value != s.Issuer {
		return refuse(ReasonIssuer, "the certificate issuer differs from the signer's", nil)
	}
	if f.Identity.Value != s.Identity {
		return refuse(ReasonIdentity, "the certificate identity differs from the signer's", nil)
	}
	if err := matchSourceBinding(f, s); err != nil {
		return err
	}
	return matchRefBinding(f, s)
}

// matchSourceBinding checks each named source member. A URI identity with
// no binding and no AnySource accepts nothing, so a signer that skipped
// Validate still fails closed.
func matchSourceBinding(f CertificateFields, s AttestationSigner) error {
	if f.IdentityIsURI && !s.bound() && !s.AnySource {
		return refuse(ReasonSourceBinding, "the signer has a URI identity and names no source binding", nil)
	}
	for _, m := range []struct {
		want string
		got  CertField
	}{
		{s.SourceRepository, f.SourceRepository},
		{s.SourceRepositoryOwner, f.SourceRepositoryOwner},
		{s.SourceRepositoryID, f.SourceRepositoryID},
		{s.SourceRepositoryOwnerID, f.SourceRepositoryOwnerID},
	} {
		if m.want != "" && (!m.got.Present || m.got.Value != m.want) {
			return refuse(ReasonSourceBinding, "a source binding member is absent or differs", nil)
		}
	}
	return nil
}

// matchRefBinding checks the ref a signer names, and refuses a bound
// signer that names neither a ref nor AnyRef.
func matchRefBinding(f CertificateFields, s AttestationSigner) error {
	ref := f.SourceRepositoryRef
	switch {
	case s.SourceRepositoryRef != "":
		if !ref.Present || ref.Value != s.SourceRepositoryRef {
			return refuse(ReasonRefBinding, "the source repository ref is absent or differs", nil)
		}
	case s.bound() && !s.AnyRef:
		return refuse(ReasonRefBinding, "the signer has a source binding and names no ref binding", nil)
	}
	return nil
}
