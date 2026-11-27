package sign

import (
	"encoding/asn1"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// oidFulcioIssuerV2 is the Fulcio Issuer (V2) extension, a DER UTF8String.
// The older Issuer extension, 1.3.6.1.4.1.57264.1.1, is not read.
var oidFulcioIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}

// IdentityPolicy is the signer identity a Sigstore-keyless verification
// accepts. A leaf matches when the identity the §4.7.11 identity comparison
// reads from it, the content of its one email or URI subject alternative
// name, equals an entry of Identities, and the issuer that comparison reads
// from its Issuer (V2) extension equals Issuer. Both comparisons are byte
// for byte. A leaf whose subject alternative name holds several names, or
// a name of another kind such as Fulcio's username OtherName, identifies no
// signer to this policy and is refused.
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

// Validate refuses a policy that would accept no bundle: verification
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

// match checks the identity and issuer that readCertificateFields read
// from the verified leaf against the policy. readCertificateFields has
// already refused a leaf with no Issuer (V2) extension or without exactly
// one email or URI name. An empty value matches nothing, so a policy built
// without NewIdentityPolicy that lists an empty entry still fails closed.
//
// Spec: §4.7.9.
func (p IdentityPolicy) match(f CertificateFields) error {
	if f.Identity.Value == "" || !slices.Contains(p.Identities, f.Identity.Value) {
		return fmt.Errorf("certificate identity mismatch: certificate carries %q", f.Identity.Value)
	}
	if f.Issuer.Value == "" || f.Issuer.Value != p.Issuer {
		return fmt.Errorf("OIDC issuer mismatch: certificate carries %q", f.Issuer.Value)
	}
	return nil
}
