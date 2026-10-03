package sigstoreharness

import (
	"bytes"
	"encoding/asn1"
	"sort"
)

// DER tag bytes the CMS and RFC 3161 encoders write by hand.
const (
	tagUTF8String  = 0x0c
	tagOctetString = 0x04
	tagSequence    = 0x30
	tagSet         = 0x31
	tagContext0    = 0xa0 // [0] constructed
	tagContext0Raw = 0x80 // [0] primitive
)

// tlv encodes one DER element. The tag byte carries the class and the
// constructed bit, so callers pass the exact first octet.
func tlv(tag byte, content []byte) []byte {
	n := len(content)
	out := []byte{tag}
	if n < 0x80 {
		out = append(out, byte(n))
	} else {
		var lb []byte
		for v := n; v > 0; v >>= 8 {
			lb = append([]byte{byte(v)}, lb...)
		}
		out = append(out, 0x80|byte(len(lb)))
		out = append(out, lb...)
	}
	return append(out, content...)
}

// seq encodes a SEQUENCE of already-encoded elements.
func seq(parts ...[]byte) []byte { return tlv(tagSequence, bytes.Join(parts, nil)) }

// setOf encodes a SET OF in DER order: the element encodings sorted
// ascending, as X.690 §11.6 requires.
func setOf(parts ...[]byte) []byte {
	sorted := append([][]byte(nil), parts...)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
	return tlv(tagSet, bytes.Join(sorted, nil))
}

// octets encodes an OCTET STRING.
func octets(b []byte) []byte { return tlv(tagOctetString, b) }

// utf8String encodes a UTF8String.
func utf8String(s string) []byte { return tlv(tagUTF8String, []byte(s)) }

// derWriter collects the first encoding error so a builder that marshals
// many values checks one error at the end, in the style of an errWriter.
type derWriter struct{ err error }

// marshal encodes v with encoding/asn1 and records the first failure.
func (w *derWriter) marshal(v any, params string) []byte {
	if w.err != nil {
		return nil
	}
	b, err := asn1.MarshalWithParams(v, params)
	if err != nil {
		w.err = err
	}
	return b
}
