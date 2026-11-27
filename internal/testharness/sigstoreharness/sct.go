package sigstoreharness

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"fmt"
)

// oidSCTList is the RFC 6962 §3.3 embedded SCT list extension.
var oidSCTList = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}

// RFC 6962 and RFC 5246 constants of a version 1 SCT over a precertificate.
const (
	sctVersion1          = 0
	sctSignatureCertTime = 0
	sctEntryPrecert      = 1
	tlsHashSHA256        = 4
	tlsSignatureECDSA    = 3
)

// sctExtension returns the embedded SCT list extension for the
// precertificate preDER, which is the leaf without that extension. The
// CT log key, or a fresh key when foreign is set, signs the RFC 6962
// §3.2 precertificate entry at the harness clock: the issuer key hash
// and the TBS bytes. A verifier rebuilds the same entry by removing the
// extension from the final leaf's TBS.
func (h *Harness) sctExtension(preDER []byte, issuer *x509.Certificate, foreign bool) (pkix.Extension, error) {
	pre, err := x509.ParseCertificate(preDER)
	if err != nil {
		return pkix.Extension{}, fmt.Errorf("parse precertificate: %w", err)
	}
	key := h.ctKey
	if foreign {
		if key, err = newECKey(); err != nil {
			return pkix.Extension{}, err
		}
	}
	timestamp := uint64(h.clock.UnixMilli())
	issuerKeyHash := sha256.Sum256(issuer.RawSubjectPublicKeyInfo)
	signed := []byte{sctVersion1, sctSignatureCertTime}
	signed = binary.BigEndian.AppendUint64(signed, timestamp)
	signed = binary.BigEndian.AppendUint16(signed, sctEntryPrecert)
	signed = append(signed, issuerKeyHash[:]...)
	signed = appendUint24Prefixed(signed, pre.RawTBSCertificate)
	signed = binary.BigEndian.AppendUint16(signed, 0) // no CT extensions
	digest := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return pkix.Extension{}, fmt.Errorf("sign SCT: %w", err)
	}
	logID, err := ctLogID(&h.ctKey.PublicKey)
	if err != nil {
		return pkix.Extension{}, err
	}
	sct := []byte{sctVersion1}
	sct = append(sct, logID...)
	sct = binary.BigEndian.AppendUint64(sct, timestamp)
	sct = binary.BigEndian.AppendUint16(sct, 0) // no CT extensions
	sct = append(sct, tlsHashSHA256, tlsSignatureECDSA)
	sct = appendUint16Prefixed(sct, sig)
	list := appendUint16Prefixed(nil, appendUint16Prefixed(nil, sct))
	return pkix.Extension{Id: oidSCTList, Value: octets(list)}, nil
}

// ctLogID is the RFC 6962 log ID: SHA-256 of the log's PKIX public key.
func ctLogID(pub *ecdsa.PublicKey) ([]byte, error) {
	raw, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

// appendUint16Prefixed appends b with a two-byte big-endian length.
func appendUint16Prefixed(dst, b []byte) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(b)))
	return append(dst, b...)
}

// appendUint24Prefixed appends b with a three-byte big-endian length.
func appendUint24Prefixed(dst, b []byte) []byte {
	n := len(b)
	dst = append(dst, byte(n>>16), byte(n>>8), byte(n))
	return append(dst, b...)
}
