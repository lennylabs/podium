package vectors_test

import (
	"crypto/ed25519"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/sign"
)

// manifestVector is one manifest document and the manifest body the §4.7.10
// step 5 delimiter rule derives from it, both as standard base64.
type manifestVector struct {
	Name           string `json:"name"`
	DocumentBase64 string `json:"document_base64"`
	BodyBase64     string `json:"body_base64"`
}

// keySetVector is one key-list or key-file input and the §4.7.9 verification
// key set it yields, in order, for an ok case.
type keySetVector struct {
	Name    string   `json:"name"`
	Input   string   `json:"input"`
	Outcome string   `json:"outcome"`
	Keys    []string `json:"keys,omitempty"`
}

// manifestVectors builds the manifest_body section from manifest.ManifestBodyOf.
func manifestVectors(t *testing.T) []manifestVector {
	cases := []struct{ name, doc, want string }{
		{"CRLF line endings", "---\r\nname: a\r\n---\r\n\r\nbody\r\n", "body\r\n"},
		{"leading blank lines", "---\nname: a\n---\n\n\nbody\n", "body\n"},
		{"no trailing newline", "---\nname: a\n---\nbody", "body"},
		{"no frontmatter", "body only\n", ""},
		{"closing delimiter followed directly by text", "---\nname: a\n---body\n", "body\n"},
	}
	out := make([]manifestVector, 0, len(cases))
	for _, c := range cases {
		got, err := manifest.ManifestBodyOf([]byte(c.doc))
		if err != nil {
			t.Errorf("manifest_body/%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("manifest_body/%s: body %q, want %q", c.name, got, c.want)
		}
		out = append(out, manifestVector{Name: c.name, DocumentBase64: b64([]byte(c.doc)), BodyBase64: b64([]byte(got))})
	}
	return sortByName(out, func(v manifestVector) string { return v.Name })
}

// keySetCase is one key-set input and the set it declares, nil when refused.
type keySetCase struct {
	name, input string
	want        []ed25519.PublicKey
}

// verifyKeyListVectors builds the verify_key_list section from
// sign.PublicKeysFromList, the parser of PODIUM_SIGNATURE_VERIFY_KEY.
func verifyKeyListVectors(t *testing.T) []keySetVector {
	a, b := publicKey(seedAHex), publicKey(seedBHex)
	cases := []keySetCase{
		{"one key", keyB64(a), []ed25519.PublicKey{a}},
		{"several keys", keyB64(a) + ", " + keyB64(b), []ed25519.PublicKey{a, b}},
		{"empty entry", keyB64(a) + ",," + keyB64(b), nil},
		{"undecodable entry", keyB64(a) + ",not-base64", nil},
	}
	return keySetVectors(t, "verify_key_list", cases, sign.PublicKeysFromList)
}

// keyFileVectors builds the key_file section from sign.ParseKeyFile. The set
// is the public: key followed by each verify: key.
func keyFileVectors(t *testing.T) []keySetVector {
	a, b, c := publicKey(seedAHex), publicKey(seedBHex), publicKey(seedCHex)
	private := "private: " + b64(privateKey(seedCHex)) + "\n"
	cases := []keySetCase{
		{"public line with two verify lines",
			"public: " + keyB64(a) + "\nverify: " + keyB64(b) + "\nverify: " + keyB64(c) + "\n",
			[]ed25519.PublicKey{a, b, c}},
		{"missing public line", "verify: " + keyB64(b) + "\n", nil},
		{"duplicate public line", "public: " + keyB64(a) + "\npublic: " + keyB64(b) + "\n", nil},
		{"private line inconsistent with public line", private + "public: " + keyB64(a) + "\n", nil},
		{"unknown prefix is ignored", "comment: anything\npublic: " + keyB64(a) + "\n", []ed25519.PublicKey{a}},
	}
	parse := func(s string) ([]ed25519.PublicKey, error) {
		kf, err := sign.ParseKeyFile([]byte(s))
		if err != nil {
			return nil, err
		}
		return append([]ed25519.PublicKey{kf.Public}, kf.Verify...), nil
	}
	return keySetVectors(t, "key_file", cases, parse)
}

// keySetVectors runs parse on each case and asserts the declared set. A
// refusal carries config.signature_provider_unavailable, the code a consumer
// whose policy is above never starts with.
func keySetVectors(t *testing.T, section string, cases []keySetCase, parse func(string) ([]ed25519.PublicKey, error)) []keySetVector {
	out := make([]keySetVector, 0, len(cases))
	for _, c := range cases {
		v := keySetVector{Name: c.name, Input: c.input, Outcome: codeUnavailable}
		got, err := parse(c.input)
		switch {
		case c.want == nil && err == nil:
			t.Errorf("%s/%s: Go accepted an input the case declares refused", section, c.name)
		case c.want != nil && err != nil:
			t.Errorf("%s/%s: Go refused an input the case declares ok: %v", section, c.name, err)
		case c.want != nil && !reflect.DeepEqual(got, c.want):
			t.Errorf("%s/%s: key set %v, want %v", section, c.name, keysB64(got), keysB64(c.want))
		case c.want != nil:
			v.Outcome, v.Keys = outcomeOK, keysB64(got)
		}
		out = append(out, v)
	}
	return sortByName(out, func(v keySetVector) string { return v.Name })
}
