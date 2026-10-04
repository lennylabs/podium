package version

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// mustJSON marshals v for a test body.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// Spec: §4.7.10 — step 1 refuses invalid UTF-8, a byte order mark, NaN,
// trailing data, nesting deeper than 64 levels, and every surrogate escape
// outside a high-low pair, including one in a member a later member replaces.
func TestCheckJSONText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		ok   bool
	}{
		{"object", `{"a":"b"}`, true},
		{"depth 64", strings.Repeat("[", 64) + strings.Repeat("]", 64), true},
		{"depth 65", strings.Repeat("[", 65) + strings.Repeat("]", 65), false},
		{"brackets in string", `{"a":"[[[[{{{{"}`, true},
		{"escaped quote", `{"a":"x\"[","b":"\\"}`, true},
		{"surrogate pair", `{"a":"\ud83d\ude00"}`, true},
		{"bmp escape", `{"a":"\u00e9\n"}`, true},
		{"lone high", `{"a":"\ud800"}`, false},
		{"high then text", `{"a":"\ud800x"}`, false},
		{"high then high", `{"a":"\ud800\ud800"}`, false},
		{"lone low", `{"a":"\udc00"}`, false},
		{"surrogate in name", `{"\udfff":1}`, false},
		{"surrogate in replaced member", `{"a":"\ud800","a":"ok"}`, false},
		{"invalid utf8", "{\"a\":\"\xff\"}", false},
		{"bom", "\xef\xbb\xbf{}", false},
		{"nan", `{"a":NaN}`, false},
		{"trailing data", `{} {}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkJSONText([]byte(tc.text))
			if tc.ok != (err == nil) {
				t.Fatalf("checkJSONText(%q) = %v, want ok=%v", tc.text, err, tc.ok)
			}
			if err != nil && !errors.Is(err, ErrMalformedRecord) {
				t.Fatalf("error %v does not wrap ErrMalformedRecord", err)
			}
		})
	}
}

// Spec: §4.7.10 — a top-level value other than an object is refused, and of
// two members with one name the later is kept.
func TestDecodeJSONObject(t *testing.T) {
	t.Parallel()
	for _, text := range []string{`null`, `[]`, `"x"`, `{`} {
		if _, err := DecodeJSONObject([]byte(text)); !errors.Is(err, ErrMalformedRecord) {
			t.Errorf("DecodeJSONObject(%s) = %v, want ErrMalformedRecord", text, err)
		}
	}
	m, err := DecodeJSONObject([]byte(`{"a":1,"A":2,"a":3}`))
	if err != nil {
		t.Fatalf("DecodeJSONObject: %v", err)
	}
	if string(m["a"]) != "3" || string(m["A"]) != "2" {
		t.Fatalf("members = %v", m)
	}
}

// Spec: §4.7.10 — base64 is accepted only when the round trip reproduces it.
func TestDecodeBase64(t *testing.T) {
	t.Parallel()
	got, err := DecodeBase64("aGk=")
	if err != nil || string(got) != "hi" {
		t.Fatalf("DecodeBase64 = %q, %v", got, err)
	}
	if got, err := DecodeBase64(""); err != nil || len(got) != 0 {
		t.Fatalf("DecodeBase64(empty) = %q, %v", got, err)
	}
	for _, s := range []string{"aGk", "aGl=", "aG\nk=", "aGk=\r\n", "a-k=", "aGk= "} {
		if _, err := DecodeBase64(s); !errors.Is(err, ErrMalformedRecord) {
			t.Errorf("DecodeBase64(%q) = %v, want ErrMalformedRecord", s, err)
		}
	}
}

// Spec: §4.7.10 — a link's content hash is framed by membership, including an
// empty value, and every other path frames the digest of its body.
func TestResourceHashes(t *testing.T) {
	t.Parallel()
	got := ResourceHashes(
		map[string][]byte{"a": []byte("x"), "big": []byte("y")},
		map[string]string{"big": "", "c": "sha256:cc"},
	)
	want := map[string]string{"a": ResourceDigest([]byte("x")), "big": "", "c": "sha256:cc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResourceHashes = %v, want %v", got, want)
	}
	if d := ResourceDigest(nil); d != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("ResourceDigest(nil) = %s", d)
	}
}

// Spec: §4.7.10 — step 6 compares the body digest with the link hash and
// skips an empty link hash.
func TestCheckLinked(t *testing.T) {
	t.Parallel()
	body := []byte("payload")
	if err := CheckLinked(body, ResourceDigest(body)); err != nil {
		t.Fatalf("matching: %v", err)
	}
	if err := CheckLinked(body, ""); err != nil {
		t.Fatalf("empty want: %v", err)
	}
	if err := CheckLinked(body, strings.ToUpper(ResourceDigest(body))); !errors.Is(err, ErrLinkedHashMismatch) {
		t.Fatalf("uppercase want = %v, want ErrLinkedHashMismatch", err)
	}
}

// loadBody returns a valid load_artifact body as a member map.
func loadBody() map[string]any {
	return map[string]any{
		"id": "team/a", "version": "1.0.0", "type": "skill", "content_hash": "sha256:00",
		"sensitivity": "internal", "artifact_revision": "2025-01-01T00:00:00.000000Z",
		"frontmatter": "---\nname: a\n---\n", "manifest_body": "body\n", "skill_raw": "---\nname: a\n---\nbody\n",
		"delivery_hash": "sha256:dh", "delivery_signature": "sig", "layer": 7, "extends_pin": false,
		"resources":        map[string]any{"a.txt": "aGk=", "gone": nil},
		"resources_base64": true,
		"large_resources": map[string]any{
			"big.bin": map[string]any{"presigned_url": "https://x/big", "content_hash": "sha256:bb", "size": "n/a"},
			"none":    nil,
		},
	}
}

// Spec: §4.7.10, §7.2 — ParseLoadResponse frames every §7.2 record member,
// artifact_revision included, decodes base64 inline bodies, takes a link's
// hash by membership, and ignores members outside the record whatever their
// type.
func TestParseLoadResponse(t *testing.T) {
	t.Parallel()
	s, err := ParseLoadResponse(mustJSON(t, loadBody()))
	if err != nil {
		t.Fatalf("ParseLoadResponse: %v", err)
	}
	want := DeliveryRecord{
		ID: "team/a", Version: "1.0.0", Type: "skill", ContentHash: "sha256:00", Sensitivity: "internal",
		ArtifactRevision: "2025-01-01T00:00:00.000000Z", Frontmatter: "---\nname: a\n---\n",
		ManifestBody: "body\n", SkillRaw: "---\nname: a\n---\nbody\n",
		Resources: map[string]string{"a.txt": ResourceDigest([]byte("hi")), "big.bin": "sha256:bb"},
	}
	if !reflect.DeepEqual(s.Record, want) {
		t.Fatalf("Record = %+v\nwant %+v", s.Record, want)
	}
	if s.Hash != "sha256:dh" || s.Signature != "sig" || s.ManifestLink != nil {
		t.Fatalf("Hash=%q Signature=%q ManifestLink=%v", s.Hash, s.Signature, s.ManifestLink)
	}
	if string(s.Inline["a.txt"]) != "hi" || s.Links["big.bin"].URL != "https://x/big" {
		t.Fatalf("Inline=%v Links=%v", s.Inline, s.Links)
	}
	if string(s.Links["big.bin"].Members["size"]) != `"n/a"` || string(s.Members["layer"]) != "7" {
		t.Fatalf("members not kept: %v %v", s.Links["big.bin"].Members, s.Members)
	}
}

// Spec: §4.7.10 — a null member is absent and a case variant is ignored.
func TestParseLoadResponse_NullAndCaseVariants(t *testing.T) {
	t.Parallel()
	s, err := ParseLoadResponse([]byte(`{"id":"x","ID":5,"sensitivity":null,"FRONTMATTER":1,"resources":{"r":"plain"}}`))
	if err != nil {
		t.Fatalf("ParseLoadResponse: %v", err)
	}
	if s.Record.ID != "x" || s.Record.Sensitivity != "" || s.Record.Resources["r"] != ResourceDigest([]byte("plain")) {
		t.Fatalf("Record = %+v", s.Record)
	}
}

// Spec: §4.7.10 — steps 1 to 4 refuse a mistyped record member, a link with
// no presigned_url, non-canonical base64, and a path served in both maps.
func TestParseLoadResponse_Refusals(t *testing.T) {
	t.Parallel()
	mutate := map[string]func(map[string]any){
		"mistyped id":             func(b map[string]any) { b["id"] = 1 },
		"mistyped base64 flag":    func(b map[string]any) { b["resources_base64"] = "true" },
		"mistyped resources":      func(b map[string]any) { b["resources"] = []any{} },
		"mistyped resource value": func(b map[string]any) { b["resources"] = map[string]any{"a": 1} },
		"non-canonical base64":    func(b map[string]any) { b["resources"] = map[string]any{"a": "aGk"} },
		"mistyped large":          func(b map[string]any) { b["large_resources"] = "x" },
		"mistyped link":           func(b map[string]any) { b["large_resources"] = map[string]any{"l": 1} },
		"empty presigned_url": func(b map[string]any) {
			b["large_resources"] = map[string]any{"l": map[string]any{"presigned_url": "", "content_hash": "h"}}
		},
		"mistyped link hash": func(b map[string]any) {
			b["large_resources"] = map[string]any{"l": map[string]any{"presigned_url": "u", "content_hash": 1}}
		},
		"path in both maps": func(b map[string]any) {
			b["large_resources"] = map[string]any{"a.txt": map[string]any{"presigned_url": "u"}}
		},
		"manifest link without url": func(b map[string]any) { b["manifest_body_url"] = map[string]any{"content_hash": "h"} },
	}
	for name, f := range mutate {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := loadBody()
			f(b)
			if _, err := ParseLoadResponse(mustJSON(t, b)); !errors.Is(err, ErrMalformedRecord) {
				t.Fatalf("ParseLoadResponse = %v, want ErrMalformedRecord", err)
			}
		})
	}
	for name, body := range map[string]string{
		"array body":         `[]`,
		"unpaired surrogate": `{"id":"team/a","frontmatter":"\ud83d"}`,
	} {
		if _, err := ParseLoadResponse([]byte(body)); !errors.Is(err, ErrMalformedRecord) {
			t.Fatalf("%s = %v, want ErrMalformedRecord", name, err)
		}
	}
}

// Spec: §4.7.10 — step 1 accepts a surrogate pair escape, and the record
// frames the UTF-8 encoding of the code point it denotes.
func TestParseLoadResponse_SurrogatePairEscape(t *testing.T) {
	t.Parallel()
	s, err := ParseLoadResponse([]byte(`{"id":"team/a","frontmatter":"\ud83d\ude00"}`))
	if err != nil {
		t.Fatalf("ParseLoadResponse: %v", err)
	}
	if got := []byte(s.Record.Frontmatter); !reflect.DeepEqual(got, []byte{0xF0, 0x9F, 0x98, 0x80}) {
		t.Fatalf("Frontmatter = % X, want F0 9F 98 80", got)
	}
}

// Spec: §4.7.10 — a fetched manifest document goes to skill_raw for a skill
// and to frontmatter for every other type, framed as the fetched bytes.
func TestServed_PlaceManifestDocument(t *testing.T) {
	t.Parallel()
	b := loadBody()
	b["manifest_body_url"] = map[string]any{"presigned_url": "https://x/m", "content_hash": ""}
	delete(b, "skill_raw")
	s, err := ParseLoadResponse(mustJSON(t, b))
	if err != nil {
		t.Fatalf("ParseLoadResponse: %v", err)
	}
	if s.ManifestLink == nil || s.ManifestLink.URL != "https://x/m" {
		t.Fatalf("ManifestLink = %v", s.ManifestLink)
	}
	s.PlaceManifestDocument([]byte("---\n\xff\n---\nb"), "b")
	if s.Record.SkillRaw != "---\n\xff\n---\nb" || s.Record.ManifestBody != "b" {
		t.Fatalf("skill record = %+v", s.Record)
	}
	other := Served{Record: DeliveryRecord{Type: "rule"}}
	other.PlaceManifestDocument([]byte("doc"), "")
	if other.Record.Frontmatter != "doc" || other.Record.SkillRaw != "" {
		t.Fatalf("rule record = %+v", other.Record)
	}
}

// Spec: §4.7.10, §7.6.2 — a batch ok entry frames its own sensitivity and
// artifact_revision, takes each resource hash from its reference, classifies
// a reference with a non-empty presigned_url as a link, reads an absent inline
// as the empty body, and checks each inline body by step 6.
func TestParseBatchResponse(t *testing.T) {
	t.Parallel()
	body := mustJSON(t, []any{
		map[string]any{
			"id": "team/a", "status": "ok", "type": "rule", "version": "1.0.0", "content_hash": "sha256:00",
			"sensitivity": "internal", "artifact_revision": "2025-01-01T00:00:00.000000Z",
			"delivery_hash": "sha256:dh",
			"resources": []any{
				map[string]any{"path": "a", "inline": "aGk=", "inline_base64": true, "content_hash": ResourceDigest([]byte("hi"))},
				map[string]any{"path": "empty", "inline_base64": false, "content_hash": ResourceDigest(nil)},
				map[string]any{"path": "big", "presigned_url": "https://x/big", "inline": "ignored", "content_hash": ""},
			},
		},
		map[string]any{"id": "team/b", "status": "error", "error": map[string]any{"code": "x"}},
	})
	entries, err := ParseBatchResponse(body)
	if err != nil {
		t.Fatalf("ParseBatchResponse: %v", err)
	}
	if len(entries) != 2 || entries[0].Err != nil || entries[0].Status != "ok" || entries[1].Status != "error" {
		t.Fatalf("entries = %+v", entries)
	}
	s := entries[0].Served
	wantRes := map[string]string{"a": ResourceDigest([]byte("hi")), "empty": ResourceDigest(nil), "big": ""}
	if s.Record.Sensitivity != "internal" || s.Record.ArtifactRevision == "" || !reflect.DeepEqual(s.Record.Resources, wantRes) {
		t.Fatalf("Record = %+v", s.Record)
	}
	if string(s.Inline["a"]) != "hi" || s.Links["big"].URL != "https://x/big" {
		t.Fatalf("Inline=%v Links=%v", s.Inline, s.Links)
	}
	if string(entries[1].Served.Members["id"]) != `"team/b"` {
		t.Fatalf("error entry members = %v", entries[1].Served.Members)
	}
}

// Spec: §4.7.10 — a batch body that fails step 1 is refused whole, and an ok
// entry that fails steps 2 to 6 carries its own refusal.
func TestParseBatchResponse_Refusals(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`null`, `{}`, `[1]`, `[{"a":"\udc00"}]`} {
		if _, err := ParseBatchResponse([]byte(body)); !errors.Is(err, ErrMalformedRecord) {
			t.Errorf("ParseBatchResponse(%s) = %v, want ErrMalformedRecord", body, err)
		}
	}
	hash := ResourceDigest([]byte("x"))
	entries := map[string]string{
		"mistyped status":   `{"status":1}`,
		"mistyped member":   `{"status":"ok","sensitivity":2}`,
		"mistyped refs":     `{"status":"ok","resources":{}}`,
		"non-object ref":    `{"status":"ok","resources":[null]}`,
		"no path":           `{"status":"ok","resources":[{"inline":"x"}]}`,
		"mistyped path":     `{"status":"ok","resources":[{"path":1}]}`,
		"repeated path":     `{"status":"ok","resources":[{"path":"p","inline":"x"},{"path":"p","inline":"x"}]}`,
		"repeated link":     `{"status":"ok","resources":[{"path":"p","presigned_url":"u"},{"path":"p","presigned_url":"u"}]}`,
		"mistyped inline":   `{"status":"ok","resources":[{"path":"p","presigned_url":"u","inline":1}]}`,
		"mistyped b64 flag": `{"status":"ok","resources":[{"path":"p","inline_base64":"yes"}]}`,
		"non-canonical":     `{"status":"ok","resources":[{"path":"p","inline":"eA","inline_base64":true}]}`,
		"inline mismatch":   `{"status":"ok","resources":[{"path":"p","inline":"y","content_hash":"` + hash + `"}]}`,
	}
	for name, entry := range entries {
		got, err := ParseBatchResponse([]byte("[" + entry + "]"))
		if err != nil {
			t.Errorf("%s: body refused: %v", name, err)
			continue
		}
		if !errors.Is(got[0].Err, ErrMalformedRecord) {
			t.Errorf("%s: Err = %v, want ErrMalformedRecord", name, got[0].Err)
		}
	}
	got, err := ParseBatchResponse([]byte(`[{"status":"ok","resources":[{"path":"p","inline":"y","content_hash":"` + hash + `"}]}]`))
	if err != nil || !errors.Is(got[0].Err, ErrLinkedHashMismatch) {
		t.Fatalf("inline mismatch = %v, %v, want ErrLinkedHashMismatch", got, err)
	}
}

// Spec: §4.7.10 — the canonical base64 a registry emits round-trips through
// DecodeBase64.
func TestDecodeBase64_RegistryOutput(t *testing.T) {
	t.Parallel()
	raw := []byte{0, 1, 2, 0xfe, 0xff}
	got, err := DecodeBase64(base64.StdEncoding.EncodeToString(raw))
	if err != nil || !reflect.DeepEqual(got, raw) {
		t.Fatalf("DecodeBase64 = %v, %v", got, err)
	}
}
