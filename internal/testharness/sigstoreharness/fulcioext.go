package sigstoreharness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
)

// Fulcio source extensions of a GitHub Actions or GitLab CI leaf. Each
// holds a DER UTF8String.
var (
	OIDSourceRepository        = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 12}
	OIDSourceRepositoryDigest  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 13}
	OIDSourceRepositoryRef     = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 14}
	OIDSourceRepositoryID      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 15}
	OIDSourceRepositoryOwner   = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 16}
	OIDSourceRepositoryOwnerID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 17}
	// OIDSubjectAltName is the subject alternative name extension.
	OIDSubjectAltName = oidSubjectAltName
)

// GitHubActionsIssuer is the OIDC issuer of a GitHub Actions leaf.
const GitHubActionsIssuer = "https://token.actions.githubusercontent.com"

// Source is the set of Fulcio source extensions a CI leaf records. An
// empty member writes no extension.
type Source struct {
	Repository string
	Digest     string
	Ref        string
	ID         string
	Owner      string
	OwnerID    string
}

// extensions returns the DER UTF8String extension of each non-empty member.
func (s Source) extensions() []pkix.Extension {
	var out []pkix.Extension
	for _, f := range []struct {
		oid asn1.ObjectIdentifier
		v   string
	}{
		{OIDSourceRepository, s.Repository},
		{OIDSourceRepositoryDigest, s.Digest},
		{OIDSourceRepositoryRef, s.Ref},
		{OIDSourceRepositoryID, s.ID},
		{OIDSourceRepositoryOwner, s.Owner},
		{OIDSourceRepositoryOwnerID, s.OwnerID},
	} {
		if f.v != "" {
			out = append(out, pkix.Extension{Id: f.oid, Value: utf8String(f.v)})
		}
	}
	return out
}

// UTF8Value returns the DER UTF8String a Fulcio extension holds for v.
func UTF8Value(v string) []byte { return utf8String(v) }

// EmailName returns a GeneralName rfc822Name ([1] IMPLICIT IA5String).
func EmailName(email string) []byte { return tlv(0x81, []byte(email)) }

// URIName returns a GeneralName uniformResourceIdentifier ([6] IMPLICIT
// IA5String).
func URIName(uri string) []byte { return tlv(0x86, []byte(uri)) }

// OtherName returns a GeneralName otherName that holds a Fulcio username.
func OtherName(username string) []byte {
	var w derWriter
	oid := w.marshal(oidFulcioUsername, "")
	return tlv(tagContext0, append(oid, tlv(tagContext0, utf8String(username))...))
}

// SANValue returns the value of a subject alternative name extension
// that holds names in order.
func SANValue(names ...[]byte) []byte { return seq(names...) }

// LeafOpt adjusts the leaf certificate of one bundle.
type LeafOpt func(*leafConfig)

// LeafEmail sets the leaf's subject alternative name to one email name.
func LeafEmail(email string) LeafOpt {
	return func(c *leafConfig) { c.email, c.uris, c.rawSAN = email, nil, nil }
}

// LeafURI sets the leaf's subject alternative name to one URI name.
func LeafURI(uri string) LeafOpt {
	return func(c *leafConfig) { c.email, c.uris, c.rawSAN = "", []string{uri}, nil }
}

// LeafSAN writes the leaf's subject alternative name extension with value,
// which SANValue builds, so the leaf can carry several names or names of
// another kind.
func LeafSAN(value []byte) LeafOpt {
	return func(c *leafConfig) { c.email, c.uris, c.rawSAN = "", nil, value }
}

// LeafOtherNameOnly writes a critical subject alternative name whose only
// entry is a Fulcio username OtherName, as Fulcio issues for a leaf with
// an empty subject.
func LeafOtherNameOnly() LeafOpt {
	return func(c *leafConfig) { c.email, c.uris, c.rawSAN, c.otherNameOnly = "", nil, nil, true }
}

// LeafIssuer sets the Issuer (V2) extension to issuer.
func LeafIssuer(issuer string) LeafOpt {
	return func(c *leafConfig) { c.setIssuerExt(OIDIssuerV2, IssuerValue(issuer)) }
}

// LeafIssuerRaw sets the Issuer (V2) extension to the raw value, which
// need not be a UTF8String.
func LeafIssuerRaw(value []byte) LeafOpt {
	return func(c *leafConfig) { c.setIssuerExt(OIDIssuerV2, value) }
}

// LeafWithoutIssuer writes neither Issuer extension.
func LeafWithoutIssuer() LeafOpt { return func(c *leafConfig) { c.issuerExts = nil } }

// LeafIssuerV1Only writes issuer in the older Issuer extension alone.
func LeafIssuerV1Only(issuer string) LeafOpt {
	return func(c *leafConfig) {
		c.issuerExts = []pkix.Extension{{Id: OIDIssuerV1, Value: []byte(issuer)}}
	}
}

// LeafSource adds the Fulcio source extensions of s.
func LeafSource(s Source) LeafOpt {
	return func(c *leafConfig) { c.extra = append(c.extra, s.extensions()...) }
}

// LeafExtension adds the extension oid with the raw value, which need not
// be a UTF8String.
func LeafExtension(oid asn1.ObjectIdentifier, value []byte) LeafOpt {
	return func(c *leafConfig) { c.extra = append(c.extra, pkix.Extension{Id: oid, Value: value}) }
}

// LeafDuplicateExtension writes the extension oid twice, each time with
// the UTF8String value. crypto/x509 refuses to create or parse such a
// certificate, so the harness writes the second copy into the signed TBS
// bytes itself.
func LeafDuplicateExtension(oid asn1.ObjectIdentifier, value string) LeafOpt {
	return func(c *leafConfig) {
		c.extra = append(c.extra, pkix.Extension{Id: oid, Value: utf8String(value)})
		c.duplicate = &pkix.Extension{Id: oid, Value: utf8String(value)}
	}
}

// LeafWithoutCodeSigning issues the leaf with the server-authentication
// extended key usage only.
func LeafWithoutCodeSigning() LeafOpt {
	return func(c *leafConfig) { c.eku = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth} }
}

// GitHubActionsLeaf configures a leaf the way Fulcio issues one to a
// GitHub Actions run: the URI identity of the workflow, the Actions
// issuer, and the source extensions of the calling repository.
func GitHubActionsLeaf(identity string, s Source) LeafOpt {
	return func(c *leafConfig) {
		LeafURI(identity)(c)
		LeafIssuer(GitHubActionsIssuer)(c)
		LeafSource(s)(c)
	}
}

// placeholderArc is the last arc of the stand-in OID whose encoding the
// duplicate rewrite replaces. Its DER encoding has the length of every
// 1.3.6.1.4.1.57264.1.x extension with x below 128.
const placeholderArc = 99

// sctMode selects the embedded SCT of a leaf.
type sctMode int

const (
	sctNone sctMode = iota
	sctTrusted
	sctForeign
)

// issueLeafDER issues a leaf for cfg under the Fulcio intermediate over
// key, valid around clock. Unless sct is sctNone it embeds an SCT over the
// precertificate, signed by the CT log key or, for sctForeign, by a key no
// root lists. When cfg names a duplicate it writes the second copy into
// the TBS and signs the result itself.
func (h *Harness) issueLeafDER(cfg leafConfig, key *ecdsa.PrivateKey, sct sctMode) ([]byte, error) {
	spec, err := cfg.spec(h.clock)
	if err != nil {
		return nil, err
	}
	if cfg.duplicate != nil {
		spec.extra = append(spec.extra, pkix.Extension{Id: placeholderOID(cfg.duplicate.Id), Value: cfg.duplicate.Value})
	}
	tmpl, err := spec.template(key.Public())
	if err != nil {
		return nil, err
	}
	issuer := h.fulcioInter
	if sct != sctNone {
		pre, err := x509.CreateCertificate(rand.Reader, tmpl, issuer.cert, key.Public(), issuer.key)
		if err != nil {
			return nil, fmt.Errorf("create precertificate: %w", err)
		}
		ext, err := h.sctExtension(pre, issuer.cert, sct == sctForeign)
		if err != nil {
			return nil, err
		}
		tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, ext)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer.cert, key.Public(), issuer.key)
	if err != nil {
		return nil, fmt.Errorf("create leaf: %w", err)
	}
	if cfg.duplicate == nil {
		return der, nil
	}
	return resignWithDuplicate(der, cfg.duplicate.Id, issuer.key.(*ecdsa.PrivateKey))
}

// placeholderOID returns oid with its last arc replaced by placeholderArc.
func placeholderOID(oid asn1.ObjectIdentifier) asn1.ObjectIdentifier {
	out := append(asn1.ObjectIdentifier(nil), oid...)
	out[len(out)-1] = placeholderArc
	return out
}

// rawCertificate is a certificate split into its TBS bytes, its
// signature algorithm, and its signature.
type rawCertificate struct {
	TBS       asn1.RawValue
	Algorithm pkix.AlgorithmIdentifier
	Signature asn1.BitString
}

// resignWithDuplicate rewrites the placeholder OID in der's TBS to oid,
// so the certificate carries the extension twice, and signs the new TBS
// with the issuer key.
func resignWithDuplicate(der []byte, oid asn1.ObjectIdentifier, issuerKey *ecdsa.PrivateKey) ([]byte, error) {
	var cert rawCertificate
	if _, err := asn1.Unmarshal(der, &cert); err != nil {
		return nil, fmt.Errorf("split leaf: %w", err)
	}
	from, err := asn1.Marshal(placeholderOID(oid))
	if err != nil {
		return nil, err
	}
	to, err := asn1.Marshal(oid)
	if err != nil {
		return nil, err
	}
	if len(from) != len(to) || bytes.Count(cert.TBS.FullBytes, from) != 1 {
		return nil, errors.New("duplicate extension: placeholder OID is not unique or differs in length")
	}
	tbs := bytes.Replace(cert.TBS.FullBytes, from, to, 1)
	digest := sha256.Sum256(tbs)
	sig, err := ecdsa.SignASN1(rand.Reader, issuerKey, digest[:])
	if err != nil {
		return nil, err
	}
	var w derWriter
	alg := w.marshal(cert.Algorithm, "")
	bits := w.marshal(asn1.BitString{Bytes: sig, BitLength: len(sig) * 8}, "")
	return seq(tbs, alg, bits), w.err
}
