package vectors_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// The vector keys derive from fixed seeds, the secret keys of RFC 8032
// section 7.1 tests 1, 2, and 3, so every regeneration signs with the same
// keys and the SDK suites can sign their own fixtures with seedA.
const (
	seedAHex = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	seedBHex = "4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb"
	seedCHex = "c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7"
)

// privateKey returns the Ed25519 private key of a hex seed constant.
func privateKey(seedHex string) ed25519.PrivateKey {
	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		panic("vectors: seed constant is not hex: " + err.Error())
	}
	return ed25519.NewKeyFromSeed(seed)
}

// publicKey returns the Ed25519 public key of a hex seed constant.
func publicKey(seedHex string) ed25519.PublicKey {
	return privateKey(seedHex).Public().(ed25519.PublicKey)
}

// b64 returns the standard base64 encoding of b.
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// keyB64 returns the standard base64 encoding of a public key.
func keyB64(pub ed25519.PublicKey) string { return b64(pub) }

// keysB64 encodes each key of a key set.
func keysB64(keys []ed25519.PublicKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = keyB64(k)
	}
	return out
}

// digest is the §4.7.10 resource content hash of body.
func digest(body string) string { return version.ResourceDigest([]byte(body)) }

// signHash signs a delivery hash with the seedA key and returns the envelope.
func signHash(t testing.TB, hash string) string {
	t.Helper()
	env, err := sign.RegistryManagedKey{PrivateKey: privateKey(seedAHex)}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("sign %s: %v", hash, err)
	}
	return env
}

// str encodes s as a JSON string without HTML escaping, the form the
// registry's encoding/json output takes for the characters the vectors use.
func str(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		panic("vectors: encode string: " + err.Error())
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// nest returns a JSON value of n nested arrays around the number 0, so a
// member holding it adds n levels to the depth of its object.
func nest(n int) string {
	return strings.Repeat("[", n) + "0" + strings.Repeat("]", n)
}

// bigInt is an unknown member value: an integer literal of 5000 digits,
// longer than Python's default integer-conversion limit.
var bigInt = "1" + strings.Repeat("0", 4999)

// member is one member of a JSON object whose key and value are already
// encoded as JSON text. Holding the text lets a case repeat a name, vary its
// letter case, or write an escape the encoder never emits.
type member struct{ key, value string }

// object is an ordered JSON object built member by member.
type object []member

// set replaces the value of the first member named name, or appends the
// member when the object has none.
func (o object) set(name, value string) object {
	key := str(name)
	out := append(object(nil), o...)
	for i := range out {
		if out[i].key == key {
			out[i].value = value
			return out
		}
	}
	return append(out, member{key, value})
}

// plusRawKey appends a member whose key is raw JSON text, such as a key
// holding an escape the encoder never writes.
func (o object) plusRawKey(key, value string) object {
	return append(append(object(nil), o...), member{key, value})
}

// without drops every member named name.
func (o object) without(name string) object {
	key := str(name)
	out := object{}
	for _, m := range o {
		if m.key != key {
			out = append(out, m)
		}
	}
	return out
}

// before prepends a member, so a later member of the same name follows it.
func (o object) before(name, value string) object {
	return append(object{{str(name), value}}, o...)
}

// plus appends a member even when one of the same name exists.
func (o object) plus(name, value string) object {
	return append(append(object(nil), o...), member{str(name), value})
}

// String encodes the object compactly, as the registry does.
func (o object) String() string {
	parts := make([]string, len(o))
	for i, m := range o {
		parts[i] = m.key + ":" + m.value
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// array encodes values as a compact JSON array.
func array(values ...string) string { return "[" + strings.Join(values, ",") + "]" }

// stringMap encodes a path-keyed map of strings as a JSON object in path
// order, so the output does not depend on map iteration.
func stringMap(m map[string]string) string {
	o := object{}
	for _, k := range sortedKeys(m) {
		o = o.set(k, str(m[k]))
	}
	return o.String()
}
