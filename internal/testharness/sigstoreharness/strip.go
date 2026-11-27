package sigstoreharness

import (
	"encoding/asn1"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// StripTimestampCertificates returns bundle with the embedded certificates
// removed from each RFC 3161 token, the change podium attest import makes
// so a bundle meets the one-certificate limit whatever its authority. The
// token signature covers the signed attributes and not the certificate
// set, so the stripped token verifies under the trusted root's chain.
//
// Spec: §4.7.11.
func StripTimestampCertificates(t testing.TB, bundle []byte) []byte {
	t.Helper()
	var b bundleJSON
	must(t, json.Unmarshal(bundle, &b))
	if data := b.VerificationMaterial.TimestampVerificationData; data != nil {
		for i, ts := range data.RFC3161Timestamps {
			stripped, err := stripResponseCertificates(ts.SignedTimestamp)
			must(t, err)
			data.RFC3161Timestamps[i].SignedTimestamp = stripped
		}
	}
	out, err := json.MarshalIndent(b, "", "  ")
	must(t, err)
	return out
}

// stripResponseCertificates rewrites a TimeStampResp without the
// certificates of its token's SignedData.
func stripResponseCertificates(resp []byte) ([]byte, error) {
	var outer struct {
		Status asn1.RawValue
		Token  asn1.RawValue
	}
	if _, err := asn1.Unmarshal(resp, &outer); err != nil {
		return nil, fmt.Errorf("timestamp response: %w", err)
	}
	var ci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,tag:0"`
	}
	if _, err := asn1.Unmarshal(outer.Token.FullBytes, &ci); err != nil {
		return nil, fmt.Errorf("timestamp token: %w", err)
	}
	var sd asn1.RawValue
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("signed data: %w", err)
	}
	var kept [][]byte
	for rest := sd.Bytes; len(rest) > 0; {
		var elem asn1.RawValue
		var err error
		if rest, err = asn1.Unmarshal(rest, &elem); err != nil {
			return nil, fmt.Errorf("signed data member: %w", err)
		}
		// The certificates member is the [0] IMPLICIT SET.
		if elem.Class == asn1.ClassContextSpecific && elem.Tag == 0 {
			continue
		}
		kept = append(kept, elem.FullBytes)
	}
	if len(kept) == 0 {
		return nil, errors.New("signed data is empty")
	}
	var w derWriter
	token := seq(w.marshal(ci.ContentType, ""), tlv(tagContext0, seq(kept...)))
	return seq(outer.Status.FullBytes, token), w.err
}
