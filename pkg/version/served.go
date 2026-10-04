package version

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// This file is the Go form of the §4.7.10 verification procedure's decoding
// steps. Go's standard decoders are not that procedure: struct decoding folds
// member-name case, reads null as the zero value, replaces an unpaired
// surrogate escape with U+FFFD, and fails on a mistyped earlier occurrence of a
// repeated name, and base64.StdEncoding skips CR and LF and accepts a non-zero
// pad bit. Every Go consumer therefore reads a load_artifact body, a batch
// body, and a signature envelope through these functions and the exact-name
// member maps they return, so it refuses exactly the bodies the SDKs refuse.

// ErrMalformedRecord signals a response body or batch entry that fails one of
// §4.7.10 steps 1 to 4, or a batch inline body that fails step 6. Every error
// this file returns wraps it; the caller reports materialize.content_hash_mismatch.
var ErrMalformedRecord = errors.New("malformed delivery record")

// maxJSONDepth is the §4.7.10 step 1 nesting limit. The top-level array or
// object is level 1.
const maxJSONDepth = 64

// Wire member names that §7.2 "Record fields" and the §7.6.2 "Delivery record"
// bullet assign. They are read by exact name only.
const (
	memberID                = "id"
	memberVersion           = "version"
	memberType              = "type"
	memberContentHash       = "content_hash"
	memberSensitivity       = "sensitivity"
	memberArtifactRevision  = "artifact_revision"
	memberFrontmatter       = "frontmatter"
	memberManifestBody      = "manifest_body"
	memberSkillRaw          = "skill_raw"
	memberDeliveryHash      = "delivery_hash"
	memberDeliverySignature = "delivery_signature"
	memberResources         = "resources"
	memberResourcesBase64   = "resources_base64"
	memberLargeResources    = "large_resources"
	memberManifestBodyURL   = "manifest_body_url"
	memberPresignedURL      = "presigned_url"
	memberPath              = "path"
	memberInline            = "inline"
	memberInlineBase64      = "inline_base64"
	memberStatus            = "status"
)

// batchStatusOK is the §7.6.2 status of an entry that carries a record.
const batchStatusOK = "ok"

// skillType is the artifact type whose manifest document is SKILL.md.
const skillType = "skill"

// Link is one link object of a load_artifact body or one link reference of a
// batch entry. Members is the object's exact-name member map, from which a
// consumer reads a member outside the record, such as size or content_type.
type Link struct {
	// URL is the presigned_url member.
	URL string
	// ContentHash is the content_hash member, empty when absent.
	ContentHash string
	// Members holds every member of the link object by exact name.
	Members map[string]json.RawMessage
}

// Served is one decoded load_artifact body or ok batch entry. Record holds
// every delivery-record field the body frames. For a load_artifact body that
// carries manifest_body_url, the caller fetches the document, checks it, and
// completes Record with PlaceManifestDocument before it recomputes the hash.
type Served struct {
	// Record is the §4.7.10 delivery record the body frames.
	Record DeliveryRecord
	// Inline maps each inline resource path to its decoded body.
	Inline map[string][]byte
	// Links maps each linked resource path to its link.
	Links map[string]Link
	// ManifestLink is the manifest_body_url link, nil when absent.
	ManifestLink *Link
	// Hash is the served delivery_hash.
	Hash string
	// Signature is the served delivery_signature.
	Signature string
	// Members is the body's top-level exact-name member map. A consumer reads
	// a member outside the record, such as layer, from it by exact name and
	// unmarshals that single raw value.
	Members map[string]json.RawMessage
}

// BatchEntry is one element of a §7.6.2 batch response body.
type BatchEntry struct {
	// Status is the entry's status member.
	Status string
	// Served is the decoded record of an ok entry. For any other status only
	// Served.Members is set.
	Served Served
	// Err is the step 2 to 6 refusal of an ok entry, nil when it decoded.
	Err error
}

// DecodeJSONObject applies the §4.7.10 step 1 JSON rule to text and returns
// its top-level members by exact name. A top-level value other than an
// object, including the literal null, is refused. When two members share a
// name the later one is kept.
// Spec: §4.7.10
func DecodeJSONObject(text []byte) (map[string]json.RawMessage, error) {
	if err := checkJSONText(text); err != nil {
		return nil, err
	}
	return decodeObject(text, "top-level value")
}

// DecodeBase64 decodes s as standard base64 and accepts it only in canonical
// form, where re-encoding the decoded bytes reproduces s exactly. The round
// trip refuses a line break, whitespace, missing padding, and a non-zero pad
// bit. base64.StdEncoding.Strict cannot replace it, because Strict still skips
// CR and LF.
// Spec: §4.7.10
func DecodeBase64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: base64: %w", ErrMalformedRecord, err)
	}
	if base64.StdEncoding.EncodeToString(b) != s {
		return nil, fmt.Errorf("%w: base64 value is not in canonical form", ErrMalformedRecord)
	}
	return b, nil
}

// ParseLoadResponse applies §4.7.10 steps 1 to 5 to a load_artifact body. It
// reads only the members §7.2 "Record fields" names, by exact name, and frames
// artifact_revision into the record. A body that carries manifest_body_url
// returns with ManifestLink set and the manifest document unplaced.
// Spec: §4.7.10, §7.2
func ParseLoadResponse(body []byte) (Served, error) {
	m, err := DecodeJSONObject(body)
	if err != nil {
		return Served{}, err
	}
	s, err := readRecordMembers(m)
	if err != nil {
		return Served{}, err
	}
	if err := readLoadResources(m, &s); err != nil {
		return Served{}, err
	}
	if s.ManifestLink, err = optionalLink(m, memberManifestBodyURL); err != nil {
		return Served{}, err
	}
	return s, nil
}

// PlaceManifestDocument completes the record of a body that carried
// manifest_body_url with the fetched document and the body the caller
// derived from it. The document goes to SkillRaw for a skill and to
// Frontmatter for every other type, framed as the fetched bytes.
// Spec: §4.7.10
func (s *Served) PlaceManifestDocument(doc []byte, body string) {
	if s.Record.Type == skillType {
		s.Record.SkillRaw = string(doc)
	} else {
		s.Record.Frontmatter = string(doc)
	}
	s.Record.ManifestBody = body
}

// ParseBatchResponse applies §4.7.10 step 1 to a whole batch body and steps 2
// to 6 to each ok entry. A body that fails step 1 returns an error and no
// entries. An ok entry that fails a later step carries the refusal in Err, and
// the other entries decode.
// Spec: §4.7.10, §7.6.2
func ParseBatchResponse(body []byte) ([]BatchEntry, error) {
	if err := checkJSONText(body); err != nil {
		return nil, err
	}
	if err := expectType(body, '[', "an array", "batch body"); err != nil {
		return nil, err
	}
	var elems []json.RawMessage
	if err := unmarshalAdmitted(body, &elems, "batch body"); err != nil {
		return nil, err
	}
	entries := make([]BatchEntry, 0, len(elems))
	for i, raw := range elems {
		m, err := decodeObject(raw, fmt.Sprintf("batch entry %d", i))
		if err != nil {
			return nil, err
		}
		entries = append(entries, parseBatchEntry(m))
	}
	return entries, nil
}

// parseBatchEntry decodes one batch entry whose top-level object is m.
func parseBatchEntry(m map[string]json.RawMessage) BatchEntry {
	entry := BatchEntry{Served: Served{Members: m}}
	status, err := optionalString(m, memberStatus)
	if err != nil {
		entry.Err = err
		return entry
	}
	entry.Status = status
	if status != batchStatusOK {
		return entry
	}
	s, err := readRecordMembers(m)
	if err == nil {
		err = readBatchResources(m, &s)
	}
	if err != nil {
		entry.Err = err
		return entry
	}
	entry.Served = s
	return entry
}

// readRecordMembers reads the string members a load_artifact body and a batch
// entry share: the record's scalar fields, the delivery hash, and the
// signature.
func readRecordMembers(m map[string]json.RawMessage) (Served, error) {
	s := Served{Members: m}
	fields := []struct {
		name string
		dst  *string
	}{
		{memberID, &s.Record.ID},
		{memberVersion, &s.Record.Version},
		{memberType, &s.Record.Type},
		{memberContentHash, &s.Record.ContentHash},
		{memberSensitivity, &s.Record.Sensitivity},
		{memberArtifactRevision, &s.Record.ArtifactRevision},
		{memberFrontmatter, &s.Record.Frontmatter},
		{memberManifestBody, &s.Record.ManifestBody},
		{memberSkillRaw, &s.Record.SkillRaw},
		{memberDeliveryHash, &s.Hash},
		{memberDeliverySignature, &s.Signature},
	}
	for _, f := range fields {
		v, err := optionalString(m, f.name)
		if err != nil {
			return Served{}, err
		}
		*f.dst = v
	}
	return s, nil
}

// readLoadResources reads resources, resources_base64, and large_resources
// from a load_artifact body, refuses a path named in both maps, and fills the
// record's resource map.
func readLoadResources(m map[string]json.RawMessage, s *Served) error {
	b64, err := optionalBool(m, memberResourcesBase64)
	if err != nil {
		return err
	}
	if s.Inline, err = readInlineMap(m, b64); err != nil {
		return err
	}
	if s.Links, err = readLinkMap(m); err != nil {
		return err
	}
	linkHashes := make(map[string]string, len(s.Links))
	for path, link := range s.Links {
		if _, dup := s.Inline[path]; dup {
			return fmt.Errorf("%w: resource %q is in both resources and large_resources", ErrMalformedRecord, path)
		}
		linkHashes[path] = link.ContentHash
	}
	s.Record.Resources = ResourceHashes(s.Inline, linkHashes)
	return nil
}

// readInlineMap reads the resources object, decoding each value as base64
// when b64 is set.
func readInlineMap(m map[string]json.RawMessage, b64 bool) (map[string][]byte, error) {
	obj, err := optionalObject(m, memberResources)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(obj))
	for path, raw := range obj {
		v, present, err := stringValue(raw, memberResources+"["+strconv.Quote(path)+"]")
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		body, err := bodyOf(v, b64)
		if err != nil {
			return nil, fmt.Errorf("resource %q: %w", path, err)
		}
		out[path] = body
	}
	return out, nil
}

// readLinkMap reads the large_resources object.
func readLinkMap(m map[string]json.RawMessage) (map[string]Link, error) {
	obj, err := optionalObject(m, memberLargeResources)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Link, len(obj))
	for path, raw := range obj {
		if isAbsent(raw) {
			continue
		}
		link, err := parseLink(raw, memberLargeResources+"["+strconv.Quote(path)+"]")
		if err != nil {
			return nil, err
		}
		out[path] = link
	}
	return out, nil
}

// readBatchResources reads a batch entry's resources array, classifies each
// reference by §4.7.10 step 2, refuses a repeated path, checks each inline
// body by step 6, and fills the record's resource map from the references'
// content hashes.
func readBatchResources(m map[string]json.RawMessage, s *Served) error {
	refs, err := optionalArray(m, memberResources)
	if err != nil {
		return err
	}
	s.Inline = make(map[string][]byte)
	s.Links = make(map[string]Link)
	hashes := make(map[string]string, len(refs))
	for i, raw := range refs {
		ref, err := decodeObject(raw, fmt.Sprintf("resources[%d]", i))
		if err != nil {
			return err
		}
		path, hash, err := readReference(ref, s)
		if err != nil {
			return fmt.Errorf("resources[%d]: %w", i, err)
		}
		hashes[path] = hash
	}
	s.Record.Resources = ResourceHashes(s.Inline, hashes)
	return nil
}

// readReference decodes one batch reference into s and returns its path and
// content hash.
func readReference(ref map[string]json.RawMessage, s *Served) (string, string, error) {
	raw, ok := ref[memberPath]
	if !ok || isAbsent(raw) {
		return "", "", fmt.Errorf("%w: reference has no path", ErrMalformedRecord)
	}
	path, _, err := stringValue(raw, memberPath)
	if err != nil {
		return "", "", err
	}
	if _, dup := s.Inline[path]; dup {
		return "", "", fmt.Errorf("%w: resource %q is named more than once", ErrMalformedRecord, path)
	}
	if _, dup := s.Links[path]; dup {
		return "", "", fmt.Errorf("%w: resource %q is named more than once", ErrMalformedRecord, path)
	}
	vals, err := readStrings(ref, memberContentHash, memberPresignedURL, memberInline)
	if err != nil {
		return "", "", err
	}
	hash, url, inline := vals[0], vals[1], vals[2]
	b64, err := optionalBool(ref, memberInlineBase64)
	if err != nil {
		return "", "", err
	}
	if url != "" {
		s.Links[path] = Link{URL: url, ContentHash: hash, Members: ref}
		return path, hash, nil
	}
	body, err := bodyOf(inline, b64)
	if err != nil {
		return "", "", fmt.Errorf("resource %q: %w", path, err)
	}
	if err := CheckLinked(body, hash); err != nil {
		return "", "", fmt.Errorf("%w: resource %q: %w", ErrMalformedRecord, path, err)
	}
	s.Inline[path] = body
	return path, hash, nil
}

// parseLink decodes one link object. A link whose presigned_url is absent or
// empty is refused.
func parseLink(raw json.RawMessage, where string) (Link, error) {
	m, err := decodeObject(raw, where)
	if err != nil {
		return Link{}, err
	}
	vals, err := readStrings(m, memberPresignedURL, memberContentHash)
	if err != nil {
		return Link{}, fmt.Errorf("%s: %w", where, err)
	}
	if vals[0] == "" {
		return Link{}, fmt.Errorf("%w: %s has no presigned_url", ErrMalformedRecord, where)
	}
	return Link{URL: vals[0], ContentHash: vals[1], Members: m}, nil
}

// optionalLink reads the link object member name, nil when absent.
func optionalLink(m map[string]json.RawMessage, name string) (*Link, error) {
	raw, ok := m[name]
	if !ok || isAbsent(raw) {
		return nil, nil
	}
	link, err := parseLink(raw, name)
	if err != nil {
		return nil, err
	}
	return &link, nil
}

// bodyOf returns the bytes of an inline value, decoding it as canonical
// base64 when b64 is set.
func bodyOf(v string, b64 bool) ([]byte, error) {
	if !b64 {
		return []byte(v), nil
	}
	return DecodeBase64(v)
}

// readStrings reads each named string member of m in order.
func readStrings(m map[string]json.RawMessage, names ...string) ([]string, error) {
	out := make([]string, len(names))
	for i, name := range names {
		v, err := optionalString(m, name)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// optionalString reads the string member name, empty when absent or null.
func optionalString(m map[string]json.RawMessage, name string) (string, error) {
	v, _, err := stringValue(m[name], name)
	return v, err
}

// stringValue decodes raw as a JSON string. present is false when raw is
// absent or null.
func stringValue(raw json.RawMessage, where string) (v string, present bool, err error) {
	if isAbsent(raw) {
		return "", false, nil
	}
	if err := expectType(raw, '"', "a string", where); err != nil {
		return "", false, err
	}
	if err := unmarshalAdmitted(raw, &v, where); err != nil {
		return "", false, err
	}
	return v, true, nil
}

// optionalBool reads the boolean member name, false when absent or null.
func optionalBool(m map[string]json.RawMessage, name string) (bool, error) {
	raw := m[name]
	if isAbsent(raw) {
		return false, nil
	}
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%w: %s is not a boolean", ErrMalformedRecord, name)
}

// optionalObject reads the object member name, nil when absent or null.
func optionalObject(m map[string]json.RawMessage, name string) (map[string]json.RawMessage, error) {
	raw := m[name]
	if isAbsent(raw) {
		return nil, nil
	}
	return decodeObject(raw, name)
}

// optionalArray reads the array member name, nil when absent or null.
func optionalArray(m map[string]json.RawMessage, name string) ([]json.RawMessage, error) {
	raw := m[name]
	if isAbsent(raw) {
		return nil, nil
	}
	if err := expectType(raw, '[', "an array", name); err != nil {
		return nil, err
	}
	var out []json.RawMessage
	if err := unmarshalAdmitted(raw, &out, name); err != nil {
		return nil, err
	}
	return out, nil
}

// decodeObject decodes raw, which checkJSONText has already admitted as part
// of the enclosing text, into its exact-name member map.
func decodeObject(raw json.RawMessage, where string) (map[string]json.RawMessage, error) {
	if err := expectType(raw, '{', "an object", where); err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := unmarshalAdmitted(raw, &m, where); err != nil {
		return nil, err
	}
	return m, nil
}

// unmarshalAdmitted decodes raw, a value checkJSONText admitted whose type
// expectType has confirmed, into v. Such a decode does not fail; the error
// return keeps a future decoder change from reading as an empty value.
func unmarshalAdmitted(raw json.RawMessage, v any, where string) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrMalformedRecord, where, err)
	}
	return nil
}

// expectType refuses raw unless its first non-space byte is first. A JSON
// value's type is fixed by its first byte.
func expectType(raw json.RawMessage, first byte, want, where string) error {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != first {
		return fmt.Errorf("%w: %s is not %s", ErrMalformedRecord, where, want)
	}
	return nil
}

// isAbsent reports whether a member is missing or null, which §4.7.10 step 2
// treats alike.
func isAbsent(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

// checkJSONText applies the §4.7.10 step 1 JSON rule. json.Valid refuses a
// byte order mark, NaN, Infinity, and trailing data. One byte scan then
// enforces the depth limit and the surrogate rule over the whole text. The
// scan runs before any decode, so it covers a member that a later member of
// the same name replaces, which a check on the decoded value never sees. The
// SDKs port this scan.
// Spec: §4.7.10
func checkJSONText(text []byte) error {
	if !utf8.Valid(text) {
		return fmt.Errorf("%w: body is not valid UTF-8", ErrMalformedRecord)
	}
	if !json.Valid(text) {
		return fmt.Errorf("%w: body is not a JSON text", ErrMalformedRecord)
	}
	return scanJSONText(text)
}

// scanJSONText tracks string and escape state over text, which json.Valid has
// admitted. Outside a string each [ and { opens one level and each ] and }
// closes one. Inside a string each \u escape is checked for surrogates.
func scanJSONText(text []byte) error {
	depth := 0
	inString := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inString {
			switch c {
			case '"':
				inString = false
			case '\\':
				n, err := escapeLen(text, i)
				if err != nil {
					return err
				}
				i += n - 1
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[', '{':
			depth++
			if depth > maxJSONDepth {
				return fmt.Errorf("%w: body nests deeper than %d levels", ErrMalformedRecord, maxJSONDepth)
			}
		case ']', '}':
			depth--
		}
	}
	return nil
}

// escapeLen returns the byte length of the escape that starts at text[i], a
// backslash inside a string. A high surrogate escape is consumed together
// with the low surrogate escape that must follow it directly; any other
// surrogate escape is refused.
func escapeLen(text []byte, i int) (int, error) {
	if text[i+1] != 'u' {
		return 2, nil
	}
	cp := hex4(text, i+2)
	switch {
	case cp >= 0xDC00 && cp <= 0xDFFF:
		return 0, errUnpairedSurrogate(i)
	case cp >= 0xD800 && cp <= 0xDBFF:
		next := i + 6
		if next+6 > len(text) || text[next] != '\\' || text[next+1] != 'u' {
			return 0, errUnpairedSurrogate(i)
		}
		if lo := hex4(text, next+2); lo < 0xDC00 || lo > 0xDFFF {
			return 0, errUnpairedSurrogate(i)
		}
		return 12, nil
	}
	return 6, nil
}

// hex4 parses the four hexadecimal digits at text[i:i+4], which json.Valid
// has guaranteed are present and valid.
func hex4(text []byte, i int) uint64 {
	v, _ := strconv.ParseUint(string(text[i:i+4]), 16, 16)
	return v
}

func errUnpairedSurrogate(offset int) error {
	return fmt.Errorf("%w: unpaired surrogate escape at offset %d", ErrMalformedRecord, offset)
}
