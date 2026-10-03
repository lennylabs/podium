package sign

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Fulcio OIDC-issuer certificate extensions. The .1.8 form is a DER
// UTF8String; the deprecated .1.1 form carries the issuer as raw bytes.
var (
	oidFulcioIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	oidFulcioIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
)

// IdentityPolicy is the signer identity a Sigstore-keyless verification
// accepts. A leaf matches when one of its email or URI subject alternative
// names equals an entry of Identities and the OIDC issuer its Fulcio
// issuer extension carries equals Issuer. Both comparisons are byte for
// byte. Other SAN types are never matched: an otherName SAN, such as
// Fulcio's username SAN (OID 1.3.6.1.4.1.57264.1.7), identifies no signer
// to this policy, so a leaf whose only identity is one is refused.
//
// Spec: §4.7.9, §6.2.
type IdentityPolicy struct {
	// Identities lists the accepted email or URI subject alternative names.
	Identities []string
	// Issuer is the accepted OIDC issuer URL.
	Issuer string
}

// NewIdentityPolicy builds a policy from the PODIUM_SIGSTORE_CERT_IDENTITY
// and PODIUM_SIGSTORE_CERT_OIDC_ISSUER values. identities is split on
// commas, each entry is trimmed of surrounding whitespace, and empty
// entries are dropped. issuer is trimmed, so a whitespace-only issuer is
// empty and Validate refuses it.
//
// Spec: §6.2.
func NewIdentityPolicy(identities, issuer string) IdentityPolicy {
	var list []string
	for _, entry := range strings.Split(identities, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			list = append(list, entry)
		}
	}
	return IdentityPolicy{Identities: list, Issuer: strings.TrimSpace(issuer)}
}

// Validate refuses a policy that would accept no envelope: verification
// fails closed when either half of the policy is unset. Each error names
// the variable that configures the missing half.
//
// Spec: §4.7.9.
func (p IdentityPolicy) Validate() error {
	if len(p.Identities) == 0 {
		return errors.New("no certificate identity configured (PODIUM_SIGSTORE_CERT_IDENTITY)")
	}
	if p.Issuer == "" {
		return errors.New("no OIDC issuer configured (PODIUM_SIGSTORE_CERT_OIDC_ISSUER)")
	}
	return nil
}

// match checks the leaf's SANs and OIDC issuer against the policy.
//
// Spec: §4.7.9.
func (p IdentityPolicy) match(leaf *x509.Certificate) error {
	sans := append([]string(nil), leaf.EmailAddresses...)
	for _, u := range leaf.URIs {
		sans = append(sans, u.String())
	}
	if !slices.ContainsFunc(sans, func(san string) bool { return slices.Contains(p.Identities, san) }) {
		return fmt.Errorf("certificate identity mismatch: certificate carries %s", strings.Join(sans, ", "))
	}
	issuer, err := leafIssuer(leaf)
	if err != nil {
		return err
	}
	if issuer != p.Issuer {
		return fmt.Errorf("OIDC issuer mismatch: certificate carries %s", issuer)
	}
	return nil
}

// leafIssuer returns the OIDC issuer from the Fulcio issuer extension.
// The .1.8 extension takes precedence. A .1.8 value that does not decode
// as one UTF8String is refused rather than falling back to .1.1, because
// a fallback would let a certificate present two issuers and have the
// verifier pick the one that matches.
func leafIssuer(leaf *x509.Certificate) (string, error) {
	var v1 []byte
	for _, ext := range leaf.Extensions {
		switch {
		case ext.Id.Equal(oidFulcioIssuerV2):
			var issuer string
			rest, err := asn1.UnmarshalWithParams(ext.Value, &issuer, "utf8")
			if err != nil || len(rest) > 0 {
				return "", errors.New("malformed OIDC issuer extension")
			}
			return issuer, nil
		case ext.Id.Equal(oidFulcioIssuerV1):
			v1 = ext.Value
		}
	}
	if v1 == nil {
		return "", errors.New("certificate carries no OIDC issuer extension")
	}
	return string(v1), nil
}
