package attest

import (
	"bytes"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
)

// ErrTimestamp reports a bundle whose RFC 3161 timestamp does not parse as a
// DER TimeStampResp that carries a CMS signed-data token, so its embedded
// certificates cannot be removed. The timestamp condition refuses such a
// bundle at every verifier.
var ErrTimestamp = errors.New("attest: timestamp")

// The member names along the path to a bundle's RFC 3161 timestamps. The
// protobuf JSON parse of a Sigstore bundle accepts the JSON name and the
// protobuf field name of each member, so both are read.
var (
	verificationMaterialNames = []string{"verificationMaterial", "verification_material"}
	timestampDataNames        = []string{"timestampVerificationData", "timestamp_verification_data"}
	rfc3161Names              = []string{"rfc3161Timestamps", "rfc3161_timestamps"}
	signedTimestampNames      = []string{"signedTimestamp", "signed_timestamp"}
)

// oidSignedData is the CMS signed-data content type a timestamp token
// carries.
var oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

// splice replaces the bytes in [start, end) of a document with repl.
type splice struct {
	start, end int
	repl       []byte
}

// RemoveTimestampCertificates returns bundle with the certificates removed
// that each RFC 3161 timestamp embeds, which are the certificates member of
// the token's CMS signed data. Every other byte of the bundle is kept: the
// command replaces the value of each signedTimestamp member that held
// certificates and nothing else, so the DSSE envelope, its signature, the
// certificate, and the log entry are the ones the signing tool wrote, and a
// bundle whose timestamps embed no certificate is returned unchanged with
// changed false. The certificates lie outside the token's signature, which
// covers the signed attributes, and a verifier takes the authority's
// certificates from the trusted root, so the timestamp verifies as before.
// The removal exists because the Python Sigstore library refuses a timestamp
// whose embedded certificates are not in DER order. A timestamp that does not
// parse returns an error wrapping ErrTimestamp.
//
// Spec: §4.7.11, §7.3.5
func RemoveTimestampCertificates(bundle []byte) (out []byte, changed bool, err error) {
	var edits []splice
	err = visitMembers(bundle, 0, verificationMaterialNames, func(vm []byte, vmAt int) error {
		return visitMembers(vm, vmAt, timestampDataNames, func(td []byte, tdAt int) error {
			return visitMembers(td, tdAt, rfc3161Names, func(list []byte, listAt int) error {
				return visitElements(list, listAt, func(ts []byte, tsAt int) error {
					return visitMembers(ts, tsAt, signedTimestampNames, func(v []byte, vAt int) error {
						repl, ok, err := strippedTimestampValue(v)
						if err != nil || !ok {
							return err
						}
						edits = append(edits, splice{start: vAt, end: vAt + len(v), repl: repl})
						return nil
					})
				})
			})
		})
	})
	if err != nil {
		return nil, false, err
	}
	if len(edits) == 0 {
		return bundle, false, nil
	}
	return applySplices(bundle, edits), true, nil
}

// applySplices returns doc with each splice applied. The spans do not
// overlap, because each is the value of a distinct member.
func applySplices(doc []byte, edits []splice) []byte {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var b bytes.Buffer
	at := 0
	for _, e := range edits {
		b.Write(doc[at:e.start])
		b.Write(e.repl)
		at = e.end
	}
	b.Write(doc[at:])
	return b.Bytes()
}

// visitMembers calls fn with the value bytes and the absolute offset of
// each member of the JSON value doc, which begins at offset base of the
// bundle, whose name is one of names. A doc that is not an object, which the
// bundle parse refuses elsewhere, visits nothing.
func visitMembers(doc []byte, base int, names []string, fn func(v []byte, at int) error) error {
	dec := json.NewDecoder(bytes.NewReader(doc))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("attest: bundle member: %w", err)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("attest: bundle member %q: %w", tok, err)
		}
		key, _ := tok.(string)
		if !slices.Contains(names, key) {
			continue
		}
		// InputOffset is the end of the value just decoded, and a
		// RawMessage holds the value's bytes as written.
		if err := fn(v, base+int(dec.InputOffset())-len(v)); err != nil {
			return err
		}
	}
	return nil
}

// visitElements calls fn with the bytes and the absolute offset of each
// element of the JSON array doc, which begins at offset base. A doc that is
// not an array visits nothing.
func visitElements(doc []byte, base int, fn func(v []byte, at int) error) error {
	dec := json.NewDecoder(bytes.NewReader(doc))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil
	}
	for dec.More() {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("attest: bundle timestamp: %w", err)
		}
		if err := fn(v, base+int(dec.InputOffset())-len(v)); err != nil {
			return err
		}
	}
	return nil
}

// strippedTimestampValue decodes v, the JSON string of a signedTimestamp
// member, removes the token's certificates, and returns the new JSON string
// in standard base64 with padding. It returns false when the token embeds no
// certificate.
func strippedTimestampValue(v []byte) ([]byte, bool, error) {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return nil, false, fmt.Errorf("%w: signedTimestamp is not a string", ErrTimestamp)
	}
	der, err := decodeBase64(s)
	if err != nil {
		return nil, false, fmt.Errorf("%w: signedTimestamp is not base64: %v", ErrTimestamp, err)
	}
	stripped, ok, err := StripTimestampResponse(der)
	if err != nil || !ok {
		return nil, false, err
	}
	out, err := json.Marshal(base64.StdEncoding.EncodeToString(stripped))
	if err != nil {
		return nil, false, fmt.Errorf("%w: encode: %v", ErrTimestamp, err)
	}
	return out, true, nil
}

// decodeBase64 decodes s in each base64 form the protobuf JSON parse
// accepts for a bytes field: standard or URL-safe, with or without padding.
func decodeBase64(s string) ([]byte, error) {
	var first error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		b, err := enc.DecodeString(s)
		if err == nil {
			return b, nil
		}
		if first == nil {
			first = err
		}
	}
	return nil, first
}

// StripTimestampResponse returns the DER TimeStampResp resp with the
// certificates member removed from the CMS signed data of its token, and
// false when the token embeds no certificate. The status and every other
// member of the response, the token, and the signed data keep their bytes,
// and only the lengths of the enclosing structures change. A response that
// is not a DER TimeStampResp with a signed-data token returns an error
// wrapping ErrTimestamp.
//
// Spec: §4.7.11, §7.3.5
func StripTimestampResponse(resp []byte) ([]byte, bool, error) {
	outer, err := derElements(resp, "the response")
	if err != nil {
		return nil, false, err
	}
	if len(outer) < 2 {
		return nil, false, fmt.Errorf("%w: the response carries no token", ErrTimestamp)
	}
	token, ok, err := stripToken(outer[1].FullBytes)
	if err != nil || !ok {
		return nil, false, err
	}
	members := make([][]byte, len(outer))
	for i, e := range outer {
		members[i] = e.FullBytes
	}
	members[1] = token
	out, err := derSequence(members)
	return out, true, err
}

// stripToken removes the certificates of a ContentInfo that carries signed
// data.
func stripToken(token []byte) ([]byte, bool, error) {
	ci, err := derElements(token, "the token")
	if err != nil {
		return nil, false, err
	}
	var oid asn1.ObjectIdentifier
	if len(ci) != 2 || !isUnmarshalled(ci[0].FullBytes, &oid) || !oid.Equal(oidSignedData) ||
		ci[1].Class != asn1.ClassContextSpecific || ci[1].Tag != 0 || !ci[1].IsCompound {
		return nil, false, fmt.Errorf("%w: the token is not CMS signed data", ErrTimestamp)
	}
	sd, err := derElements(ci[1].Bytes, "the signed data")
	if err != nil {
		return nil, false, err
	}
	kept := make([][]byte, 0, len(sd))
	for _, e := range sd {
		// The certificates member is the [0] IMPLICIT SET of the
		// SignedData; [1] holds CRLs and stays.
		if e.Class == asn1.ClassContextSpecific && e.Tag == 0 {
			continue
		}
		kept = append(kept, e.FullBytes)
	}
	if len(kept) == len(sd) {
		return nil, false, nil
	}
	signedData, err := derSequence(kept)
	if err != nil {
		return nil, false, err
	}
	wrapped, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: signedData})
	if err != nil {
		return nil, false, fmt.Errorf("%w: encode: %v", ErrTimestamp, err)
	}
	out, err := derSequence([][]byte{ci[0].FullBytes, wrapped})
	return out, true, err
}

// derElements decodes der as one SEQUENCE with no trailing bytes and returns
// its elements. what names the structure in an error.
func derElements(der []byte, what string) ([]asn1.RawValue, error) {
	var seq asn1.RawValue
	rest, err := asn1.Unmarshal(der, &seq)
	if err != nil || len(rest) > 0 || seq.Class != asn1.ClassUniversal || seq.Tag != asn1.TagSequence || !seq.IsCompound {
		return nil, fmt.Errorf("%w: %s is not a DER SEQUENCE", ErrTimestamp, what)
	}
	var out []asn1.RawValue
	for b := seq.Bytes; len(b) > 0; {
		var e asn1.RawValue
		if b, err = asn1.Unmarshal(b, &e); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrTimestamp, what, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// derSequence encodes members, each a complete DER element, as a SEQUENCE.
func derSequence(members [][]byte) ([]byte, error) {
	out, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: bytes.Join(members, nil)})
	// encoding/asn1 does not fail on a RawValue with a universal tag; the
	// guard keeps a library change from writing a truncated token.
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrTimestamp, err)
	}
	return out, nil
}

// isUnmarshalled reports whether der decodes into v with no trailing bytes.
func isUnmarshalled(der []byte, v any) bool {
	rest, err := asn1.Unmarshal(der, v)
	return err == nil && len(rest) == 0
}
