package sign

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Sigstore keyDetails values for the transparency-log keys the verifier
// uses. A key with any other value is skipped.
const (
	keyDetailsP256    = "PKIX_ECDSA_P256_SHA_256"
	keyDetailsEd25519 = "PKIX_ED25519"
)

// trustedRootDoc declares the members of Sigstore's trusted_root.json the
// verifier reads. encoding/json ignores the rest (ctlogs, mediaType,
// tlogs[].logId, and the descriptive members). Each rawBytes member is
// standard base64, which []byte decodes, and each validFor bound is the
// RFC 3339 form protojson writes, which time.Time parses.
type trustedRootDoc struct {
	CertificateAuthorities []authorityDoc `json:"certificateAuthorities"`
	TLogs                  []tlogDoc      `json:"tlogs"`
	TimestampAuthorities   []authorityDoc `json:"timestampAuthorities"`
}

type authorityDoc struct {
	CertChain struct {
		Certificates []struct {
			RawBytes []byte `json:"rawBytes"`
		} `json:"certificates"`
	} `json:"certChain"`
	ValidFor validFor `json:"validFor"`
}

type tlogDoc struct {
	PublicKey struct {
		RawBytes   []byte   `json:"rawBytes"`
		KeyDetails string   `json:"keyDetails"`
		ValidFor   validFor `json:"validFor"`
	} `json:"publicKey"`
}

// validFor is a trusted-root validity window. A nil bound leaves that
// side unbounded.
type validFor struct {
	Start *time.Time `json:"start"`
	End   *time.Time `json:"end"`
}

// contains reports whether t falls inside the window, both ends included.
//
// Spec: §6.2.
func (v validFor) contains(t time.Time) bool {
	if v.Start != nil && t.Before(*v.Start) {
		return false
	}
	if v.End != nil && t.After(*v.End) {
		return false
	}
	return true
}

// trustedAuthority is one certificate chain of the trusted root: the last
// certificate is the anchor and the others are intermediates.
type trustedAuthority struct {
	anchor        *x509.Certificate
	intermediates []*x509.Certificate
	window        validFor
}

// trustedLogKey is one listing of a transparency-log key. A key listed
// more than once yields one trustedLogKey per listing, each with its own
// window, so the key is usable when any listing's window contains the
// attested time.
type trustedLogKey struct {
	key    crypto.PublicKey
	window validFor
}

// trustedRoot keeps the three roles of the trusted root apart, so a
// timestamp-authority root never anchors a code-signing leaf and a
// certificate-authority root never anchors a timestamp signer.
type trustedRoot struct {
	certificateAuthorities []trustedAuthority
	logKeys                []trustedLogKey
	timestampAuthorities   []trustedAuthority
}

// parseTrustedRoot decodes a Sigstore trusted_root.json. The operator
// supplies an authentic copy; Podium neither fetches it nor verifies TUF
// metadata.
//
// Spec: §4.7.9, §6.2.
func parseTrustedRoot(raw []byte) (trustedRoot, error) {
	if len(raw) == 0 {
		return trustedRoot{}, errors.New("no trust root configured")
	}
	var doc trustedRootDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return trustedRoot{}, fmt.Errorf("trust root does not parse: %w", err)
	}
	var root trustedRoot
	var err error
	if root.certificateAuthorities, err = parseAuthorities(doc.CertificateAuthorities); err != nil {
		return trustedRoot{}, err
	}
	if root.timestampAuthorities, err = parseAuthorities(doc.TimestampAuthorities); err != nil {
		return trustedRoot{}, err
	}
	root.logKeys = parseLogKeys(doc.TLogs)
	switch {
	case len(root.certificateAuthorities) == 0:
		return trustedRoot{}, errors.New("trust root carries no certificate authority")
	case len(root.logKeys) == 0:
		return trustedRoot{}, errors.New("trust root carries no usable transparency-log key")
	case len(root.timestampAuthorities) == 0:
		return trustedRoot{}, errors.New("trust root carries no timestamp authority")
	}
	return root, nil
}

// parseAuthorities parses each authority's chain, ordered leaf to root.
func parseAuthorities(docs []authorityDoc) ([]trustedAuthority, error) {
	out := make([]trustedAuthority, 0, len(docs))
	for i, d := range docs {
		certs := d.CertChain.Certificates
		if len(certs) == 0 {
			return nil, fmt.Errorf("trust root authority %d carries no certificate", i)
		}
		chain := make([]*x509.Certificate, len(certs))
		for j, c := range certs {
			cert, err := x509.ParseCertificate(c.RawBytes)
			if err != nil {
				return nil, fmt.Errorf("trust root certificate: %w", err)
			}
			chain[j] = cert
		}
		last := len(chain) - 1
		out = append(out, trustedAuthority{anchor: chain[last], intermediates: chain[:last], window: d.ValidFor})
	}
	return out, nil
}

// parseLogKeys returns the usable log keys. Sigstore's own file lists
// keys of types the verifier does not check, so an unsupported or
// unparseable key is skipped rather than refusing the whole document.
func parseLogKeys(docs []tlogDoc) []trustedLogKey {
	var out []trustedLogKey
	for _, d := range docs {
		key, ok := parseLogKey(d.PublicKey.KeyDetails, d.PublicKey.RawBytes)
		if ok {
			out = append(out, trustedLogKey{key: key, window: d.PublicKey.ValidFor})
		}
	}
	return out
}

// parseLogKey parses a DER SubjectPublicKeyInfo as the type keyDetails
// names, reporting false when it is not that type.
func parseLogKey(keyDetails string, der []byte) (crypto.PublicKey, bool) {
	if keyDetails != keyDetailsP256 && keyDetails != keyDetailsEd25519 {
		return nil, false
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, false
	}
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		return k, keyDetails == keyDetailsP256 && k.Curve == elliptic.P256()
	case ed25519.PublicKey:
		return k, keyDetails == keyDetailsEd25519
	default:
		return nil, false
	}
}

// authorityPools builds the root and intermediate pools from the
// authorities whose window contains t, adding extra as intermediates. ok
// is false when no authority is valid at t.
func authorityPools(auths []trustedAuthority, t time.Time, extra []*x509.Certificate) (roots, intermediates *x509.CertPool, ok bool) {
	roots, intermediates = x509.NewCertPool(), x509.NewCertPool()
	for _, a := range auths {
		if !a.window.contains(t) {
			continue
		}
		ok = true
		roots.AddCert(a.anchor)
		for _, c := range a.intermediates {
			intermediates.AddCert(c)
		}
	}
	for _, c := range extra {
		intermediates.AddCert(c)
	}
	return roots, intermediates, ok
}

// logKeysAt returns the log keys whose listing window contains t.
func (r trustedRoot) logKeysAt(t time.Time) []crypto.PublicKey {
	var out []crypto.PublicKey
	for _, k := range r.logKeys {
		if k.window.contains(t) {
			out = append(out, k.key)
		}
	}
	return out
}

// stamp formats an attested time for an error message.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
