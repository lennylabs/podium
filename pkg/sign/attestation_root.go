package sign

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	"github.com/sigstore/sigstore-go/pkg/root"
)

// ErrAttestationRootInvalid signals a trusted root that the attestation
// verifier refuses to load: one that does not parse as a Sigstore trusted
// root, or one with a timestamp authority entry that begins with a CA
// certificate.
var ErrAttestationRootInvalid = errors.New("attestation trusted root invalid")

// AttestationRootSource supplies the trusted root of one Sigstore instance
// for a verification. A file-backed or caller-built root returns itself; a
// source that refreshes the root through TUF returns its current verified
// copy, or an error when no copy may be used. The verifier calls it once
// per bundle and makes no network call of its own, so a source keeps any
// refresh outside this call.
type AttestationRootSource interface {
	AttestationRoot(ctx context.Context) (*AttestationRoot, error)
}

// AttestationRoot is a parsed Sigstore trusted root that passed the
// §4.7.11 load rules.
type AttestationRoot struct {
	material *root.TrustedRoot
	sha1TSAs []string
}

// AttestationRoot returns r, so a loaded root serves as its own source.
func (r *AttestationRoot) AttestationRoot(context.Context) (*AttestationRoot, error) {
	return r, nil
}

// SHA1TimestampAuthorities names, by URI, each timestamp authority whose
// chain in the root holds a SHA-1 signature. The TypeScript verifier
// refuses the timestamps of such an authority, so a start reports them.
//
// Spec: §4.7.11.
func (r *AttestationRoot) SHA1TimestampAuthorities() []string {
	return append([]string(nil), r.sha1TSAs...)
}

// LoadAttestationRootFile reads and parses the trusted root at path.
//
// Spec: §4.7.11.
func LoadAttestationRootFile(path string) (*AttestationRoot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("attestation: read trusted root %s: %w", path, err)
	}
	r, err := ParseAttestationRoot(raw)
	if err != nil {
		return nil, fmt.Errorf("attestation: trusted root %s: %w", path, err)
	}
	return r, nil
}

// ParseAttestationRoot parses a Sigstore trusted_root.json document. It
// refuses a timestamp authority entry whose first certificate is a CA
// certificate: sigstore-go then takes the signing certificate from the
// token, while the SDK libraries take it from the root alone, so such an
// entry verifies differently across consumers. A root whose authorities
// include expired, rotated-out entries loads, because each entry's chain is
// checked at the time of each timestamp it verifies.
//
// Spec: §4.7.11.
func ParseAttestationRoot(raw []byte) (*AttestationRoot, error) {
	pb, err := root.NewTrustedRootProtobuf(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAttestationRootInvalid, err)
	}
	out := &AttestationRoot{}
	for i, tsa := range pb.GetTimestampAuthorities() {
		sha1, err := checkTimestampAuthorityEntry(tsa.GetCertChain().GetCertificates())
		if err != nil {
			return nil, fmt.Errorf("%w: timestamp authority %d (%s): %w", ErrAttestationRootInvalid, i, tsa.GetUri(), err)
		}
		if sha1 {
			out.sha1TSAs = append(out.sha1TSAs, tsa.GetUri())
		}
	}
	if out.material, err = root.NewTrustedRootFromProtobuf(pb); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAttestationRootInvalid, err)
	}
	return out, nil
}

// checkTimestampAuthorityEntry refuses a chain whose first certificate is a
// CA certificate, and reports whether a certificate of the chain carries a
// SHA-1 signature.
func checkTimestampAuthorityEntry(chain []*protocommon.X509Certificate) (sha1 bool, err error) {
	if len(chain) == 0 {
		return false, errors.New("certificate chain is empty")
	}
	for i, c := range chain {
		cert, err := x509.ParseCertificate(c.GetRawBytes())
		if err != nil {
			return false, fmt.Errorf("certificate %d: %w", i, err)
		}
		if i == 0 && cert.IsCA {
			return false, errors.New("the first certificate is a CA certificate; an entry begins with the authority's own certificate")
		}
		switch cert.SignatureAlgorithm {
		case x509.SHA1WithRSA, x509.ECDSAWithSHA1:
			sha1 = true
		}
	}
	return sha1, nil
}
