package sign

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/lennylabs/podium/pkg/version"
)

// The §4.7.11 bundle and statement constants.
const (
	attestationMediaTypeV03       = "application/vnd.dev.sigstore.bundle.v0.3+json"
	attestationMediaTypeV03Legacy = "application/vnd.dev.sigstore.bundle+json;version=0.3"
	inTotoPayloadType             = "application/vnd.in-toto+json"
	inTotoStatementV1             = "https://in-toto.io/Statement/v1"
	// reservedSignedAtPrefix is the subject prefix that no accepted
	// certificate-signed bundle lists, and under which a key-signed bundle
	// lists its one signed-time subject.
	reservedSignedAtPrefix = ".podium/signed-at/"
)

// AttestationStatement is the in-toto Statement v1 a bundle carries, read
// by exact member name from the payload bytes the signature covers.
type AttestationStatement struct {
	// PredicateType is the statement's predicateType member.
	PredicateType string
	// Subjects lists the subject array in order.
	Subjects []AttestationSubject
}

// AttestationSubject is one statement subject. Digest holds every member
// of the subject's digest object as written, so the sha256 member keeps
// its letter case and a matcher applies the §4.7.11 lowercase rule.
type AttestationSubject struct {
	Name   string
	Digest map[string]string
}

// Lists reports whether the statement lists a subject named name whose
// sha256 member equals hexDigest, under the §4.7.11 subject rules: names
// compare byte for byte, the sha256 member is 64 lowercase hexadecimal
// characters, and a name that breaks the subject name character rule
// matches nothing. hexDigest is the hexadecimal part of a package or layer
// digest, which is lowercase.
//
// Spec: §4.7.11.
func (s AttestationStatement) Lists(name, hexDigest string) bool {
	if !validSubjectName(name) || !isLowerHex64(hexDigest) {
		return false
	}
	for _, sub := range s.Subjects {
		if sub.Name == name && sub.Digest["sha256"] == hexDigest {
			return true
		}
	}
	return false
}

// validSubjectName applies the §4.7.11 subject name character rule: valid
// UTF-8 with no control character, no white space character, and no
// backslash. The set is the one an ECMAScript reader of the checksums file
// splits and trims on, with the C0 and C1 controls and the backslash.
func validSubjectName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsFunc(name, excludedSubjectRune)
}

// excludedSubjectRune reports whether r is excluded from a subject name.
func excludedSubjectRune(r rune) bool {
	switch {
	case r <= 0x20, r >= 0x7f && r <= 0xa0, r >= 0x2000 && r <= 0x200a:
		return true
	}
	switch r {
	case '\\', 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return false
}

// isLowerHex64 reports whether s is 64 lowercase hexadecimal characters.
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// parseAttestationBundle decodes raw as a Sigstore bundle and applies the
// §4.7.11 media type and envelope conditions. The media type is compared
// before sigstore-go validates the bundle, because the library also
// validates a version 0.1 bundle, which carries an inclusion promise and
// no inclusion proof. Where the bundle names the media type twice, once as
// mediaType and once as media_type, protojson refuses the duplicate field.
//
// Spec: §4.7.11.
func parseAttestationBundle(raw []byte) (*bundle.Bundle, *protobundle.Bundle, error) {
	b, pb, err := parseSigstoreBundle(raw)
	if err != nil {
		return nil, nil, err
	}
	env := pb.GetDsseEnvelope()
	switch {
	case env == nil:
		return nil, nil, refuse(ReasonPayload, "the bundle carries no DSSE envelope", nil)
	case env.GetPayloadType() != inTotoPayloadType:
		return nil, nil, refuse(ReasonPayload, "the DSSE payload type is not the in-toto type", nil)
	case len(env.GetSignatures()) != 1:
		return nil, nil, refuse(ReasonPayload, "the DSSE envelope does not carry exactly one signature", nil)
	}
	return b, pb, nil
}

// parseSigstoreBundle applies the bundle conditions that the §4.7.11
// attestation and the §4.7.9 keyless signature share: the §4.7.10 JSON
// rule, the version 0.3 media type read by exact member name, and the
// library's bundle parse.
//
// Spec: §4.7.9, §4.7.11.
func parseSigstoreBundle(raw []byte) (*bundle.Bundle, *protobundle.Bundle, error) {
	// protojson also accepts the protobuf field name media_type, so the
	// media type is read by exact member name before it runs. A bundle
	// that names its media type only as media_type has none.
	members, err := version.DecodeJSONObject(raw)
	if err != nil {
		return nil, nil, refuse(ReasonParse, "the bundle fails the JSON rule", err)
	}
	if mt, ok := jsonString(members["mediaType"]); !ok || (mt != attestationMediaTypeV03 && mt != attestationMediaTypeV03Legacy) {
		return nil, nil, refuse(ReasonMediaType, "the mediaType member names no accepted bundle version", nil)
	}
	pb := new(protobundle.Bundle)
	if err := protojson.Unmarshal(raw, pb); err != nil {
		return nil, nil, refuse(ReasonParse, "the bundle does not parse", err)
	}
	b, err := bundle.NewBundle(pb)
	if err != nil {
		return nil, nil, refuse(ReasonParse, "the bundle is not a valid version 0.3 bundle", err)
	}
	return b, pb, nil
}

// parseAttestationStatement reads payload as an in-toto Statement v1 under
// the §4.7.10 JSON rule, by exact member name, and refuses a statement
// that lists a subject under .podium/signed-at/, which no
// certificate-signed bundle may list.
//
// Spec: §4.7.11.
func parseAttestationStatement(payload []byte) (AttestationStatement, error) {
	st, err := parseStatementPayload(payload)
	if err != nil {
		return AttestationStatement{}, err
	}
	if err := refuseSignedTimeSubjects(st); err != nil {
		return AttestationStatement{}, err
	}
	return st, nil
}

// parseStatementPayload reads payload as an in-toto Statement v1 under the
// §4.7.10 JSON rule, by exact member name. The reserved names are the
// caller's: a certificate-signed bundle refuses every subject under
// .podium/signed-at/, and a key-signed one applies the signed-time rule.
//
// Spec: §4.7.11.
func parseStatementPayload(payload []byte) (AttestationStatement, error) {
	members, err := version.DecodeJSONObject(payload)
	if err != nil {
		return AttestationStatement{}, refuse(ReasonPayload, "the payload fails the JSON rule", err)
	}
	if t, ok := jsonString(members["_type"]); !ok || t != inTotoStatementV1 {
		return AttestationStatement{}, refuse(ReasonPayload, "the payload is not an in-toto Statement v1", nil)
	}
	var st AttestationStatement
	if pt, ok := jsonString(members["predicateType"]); ok && pt != "" {
		st.PredicateType = pt
	} else {
		return AttestationStatement{}, refuse(ReasonPayload, "the statement has no predicate type", nil)
	}
	if st.Subjects, err = parseSubjects(members["subject"]); err != nil {
		return AttestationStatement{}, err
	}
	return st, nil
}

// parseSubjects reads a non-empty subject array whose entries each carry a
// non-empty digest object of string values and, where present, a string
// name. in-toto Statement v1 requires the digest and leaves the name
// optional; a subject with no name matches no artifact.
func parseSubjects(raw json.RawMessage) ([]AttestationSubject, error) {
	var elems []json.RawMessage
	if !isJSONKind(raw, '[') || json.Unmarshal(raw, &elems) != nil || len(elems) == 0 {
		return nil, refuse(ReasonPayload, "the statement has no subject array", nil)
	}
	out := make([]AttestationSubject, 0, len(elems))
	for _, e := range elems {
		s, ok := parseSubject(e)
		if !ok {
			return nil, refuse(ReasonPayload, "a statement subject is malformed", nil)
		}
		out = append(out, s)
	}
	return out, nil
}

// parseSubject reads one subject object. The text passed the JSON rule as
// part of the payload, so the nested decodes check only member types.
func parseSubject(raw json.RawMessage) (AttestationSubject, bool) {
	if !isJSONKind(raw, '{') {
		return AttestationSubject{}, false
	}
	members, err := version.DecodeJSONObject(raw)
	if err != nil {
		return AttestationSubject{}, false
	}
	name, ok := jsonString(members["name"])
	if !ok && !isAbsent(members["name"]) {
		return AttestationSubject{}, false
	}
	if !isJSONKind(members["digest"], '{') {
		return AttestationSubject{}, false
	}
	digestMembers, err := version.DecodeJSONObject(members["digest"])
	if err != nil || len(digestMembers) == 0 {
		return AttestationSubject{}, false
	}
	digest := make(map[string]string, len(digestMembers))
	for alg, v := range digestMembers {
		if digest[alg], ok = jsonString(v); !ok {
			return AttestationSubject{}, false
		}
	}
	return AttestationSubject{Name: name, Digest: digest}, true
}

// jsonString decodes raw as a JSON string. An absent or null member and a
// value of another type report false.
func jsonString(raw json.RawMessage) (string, bool) {
	if !isJSONKind(raw, '"') {
		return "", false
	}
	var s string
	// The text passed the JSON rule, so a value that opens with a quote is
	// a valid JSON string and this decode does not fail.
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// isAbsent reports whether a member is absent or null, which §4.7.10
// treats alike.
func isAbsent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}

// isJSONKind reports whether raw is a JSON value that opens with open.
func isJSONKind(raw json.RawMessage, open byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == open
}
