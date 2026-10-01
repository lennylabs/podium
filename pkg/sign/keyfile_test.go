package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeKeyFile writes body to a key file in a fresh directory and returns its
// path.
func writeKeyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return path
}

// registryKeyFileBody renders a keypair in the two-line format the registry
// writes.
func registryKeyFileBody(priv ed25519.PrivateKey, pub ed25519.PublicKey) string {
	return "private: " + base64.StdEncoding.EncodeToString(priv) + "\n" +
		"public: " + base64.StdEncoding.EncodeToString(pub) + "\n"
}

// Spec: §4.7.9 — a set PODIUM_SIGN_KEY_PATH names the key file, and an unset
// one resolves the standalone default under the home directory.
func TestKeyFilePath(t *testing.T) {
	if got, err := KeyFilePath("/etc/podium/key"); err != nil || got != "/etc/podium/key" {
		t.Fatalf("KeyFilePath(env) = %q, %v; want the env value", got, err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := KeyFilePath("")
	if err != nil {
		t.Fatalf("KeyFilePath(\"\"): %v", err)
	}
	if want := filepath.Join(home, ".podium", "standalone", "registry-signing.key"); got != want {
		t.Errorf("KeyFilePath(\"\") = %q, want %q", got, want)
	}
}

// Spec: §4.7.9 — a home the process cannot resolve is an error, never a
// relative path that would read a file from the working directory.
func TestKeyFilePath_UnresolvableHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got, err := KeyFilePath(""); err == nil {
		t.Fatalf("KeyFilePath(\"\") with no home = %q, want an error", got)
	}
}

// Spec: §4.7.9 — a key file written before verify: lines existed, the
// registry's private: and public: format, reads back with both halves and an
// empty verification-only list.
func TestKeyFile_ReadsBothHalves(t *testing.T) {
	t.Parallel()
	pub, priv := genKey(t)
	path := writeKeyFile(t, registryKeyFileBody(priv, pub))
	kf, err := ReadKeyFile(path)
	if err != nil {
		t.Fatalf("ReadKeyFile: %v", err)
	}
	if !kf.Private.Equal(priv) || !kf.Public.Equal(pub) || len(kf.Verify) != 0 {
		t.Errorf("ReadKeyFile = %+v; want the written keypair and no verify keys", kf)
	}
}

// genKey returns a fresh Ed25519 keypair.
func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

// genPublicKeys returns n fresh Ed25519 public keys.
func genPublicKeys(t *testing.T, n int) []ed25519.PublicKey {
	t.Helper()
	out := make([]ed25519.PublicKey, n)
	for i := range out {
		out[i], _ = genKey(t)
	}
	return out
}

// Spec: §4.7.9 — WriteKeyFile and ReadKeyFile round-trip a file with zero,
// one, and two verify: lines, in order, and the written file is owner-only.
func TestKeyFile_RoundTripVerifyLines(t *testing.T) {
	t.Parallel()
	for n := 0; n <= 2; n++ {
		pub, priv := genKey(t)
		want := KeyFile{Private: priv, Public: pub, Verify: genPublicKeys(t, n)}
		path := filepath.Join(t.TempDir(), "nested", "registry-signing.key")
		if err := WriteKeyFile(path, want); err != nil {
			t.Fatalf("WriteKeyFile(%d verify): %v", n, err)
		}
		got, err := ReadKeyFile(path)
		if err != nil {
			t.Fatalf("ReadKeyFile(%d verify): %v", n, err)
		}
		if !got.Private.Equal(priv) || !got.Public.Equal(pub) || !keysEqual(got.Verify, want.Verify) {
			t.Errorf("round trip with %d verify lines = %+v, want %+v", n, got, want)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("key file mode = %o, want 600", mode)
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if mode := dirInfo.Mode().Perm(); mode != 0o700 {
			t.Errorf("key directory mode = %o, want 700", mode)
		}
	}
}

// keysEqual reports whether a and b hold the same keys in the same order.
func keysEqual(a, b []ed25519.PublicKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// Spec: §4.7.9 — a write over an existing file replaces it through a rename
// and leaves no temporary file in the directory.
func TestKeyFile_WriteReplacesAndLeavesNoTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry-signing.key")
	pub1, priv1 := genKey(t)
	pub2, priv2 := genKey(t)
	if err := WriteKeyFile(path, KeyFile{Private: priv1, Public: pub1}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteKeyFile(path, KeyFile{Private: priv2, Public: pub2, Verify: []ed25519.PublicKey{pub1}}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := ReadKeyFile(path)
	if err != nil || !got.Public.Equal(pub2) || !keysEqual(got.Verify, []ed25519.PublicKey{pub1}) {
		t.Errorf("ReadKeyFile after replace = %+v, %v; want the second keypair with the first on a verify line", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries after two writes, want only the key file", len(entries))
	}
}

// Spec: §4.7.9 — a KeyFile with no public key is not written.
func TestKeyFile_WriteRefusesNoPublic(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "k")
	if err := WriteKeyFile(path, KeyFile{}); err == nil {
		t.Fatal("WriteKeyFile with no public key succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("stat after refused write = %v, want not-exist", err)
	}
}

// Spec: §4.7.9 — a write into a directory that cannot be created is an error
// naming the path.
func TestKeyFile_WriteUncreatableDir(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pub, _ := genKey(t)
	path := filepath.Join(blocker, "sub", "k")
	if err := WriteKeyFile(path, KeyFile{Public: pub}); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("WriteKeyFile under a file = %v, want an error naming %s", err, path)
	}
}

// Spec: §4.7.9 — a consumer's public-only copy parses with a nil Private, and
// its verification key set is the public: key followed by every verify: key.
func TestKeyFile_PublicOnlyVerificationKeys(t *testing.T) {
	t.Parallel()
	keys := genPublicKeys(t, 3)
	body := "public: " + b64(keys[0]) + "\nverify: " + b64(keys[1]) + "\n  verify:   " + b64(keys[2]) + "\n# comment\n"
	path := writeKeyFile(t, body)
	kf, err := ReadKeyFile(path)
	if err != nil || kf.Private != nil {
		t.Fatalf("ReadKeyFile(public-only) = %+v, %v; want a nil Private and no error", kf, err)
	}
	got, err := VerificationKeysFromKeyFile(path)
	if err != nil || !keysEqual(got, keys) {
		t.Errorf("VerificationKeysFromKeyFile = %v, %v; want public then verify keys in order", got, err)
	}
}

// b64 encodes key in standard base64.
func b64(key []byte) string { return base64.StdEncoding.EncodeToString(key) }

// Spec: §4.7.9, §13.12 — a missing or malformed file is an error naming the
// path, which each caller turns into a refusal rather than an unverifying
// provider, and VerificationKeysFromKeyFile never returns an empty set with a
// nil error.
func TestKeyFile_Refusals(t *testing.T) {
	t.Parallel()
	pub, priv := genKey(t)
	otherPub, _ := genKey(t)
	shortKey := b64([]byte("short"))
	cases := []struct {
		name, body, wantSub string
	}{
		{"empty", "", "public:"},
		{"no key lines", "comment: nothing here\n", "public:"},
		{"verify only", "verify: " + b64(pub) + "\n", "public:"},
		{"private only", "private: " + b64(priv) + "\n", "public:"},
		{"undecodable private", "private: !!!\npublic: " + b64(pub) + "\n", "private:"},
		{"undecodable public", "public: !!!\n", "public:"},
		{"undecodable verify", "public: " + b64(pub) + "\nverify: !!!\n", "verify:"},
		{"wrong-size private", "private: " + shortKey + "\npublic: " + b64(pub) + "\n", "private:"},
		{"wrong-size public", "public: " + shortKey + "\n", "public:"},
		{"wrong-size verify", "public: " + b64(pub) + "\nverify: " + shortKey + "\n", "verify:"},
		{"mismatched halves", "private: " + b64(priv) + "\npublic: " + b64(otherPub) + "\n", "public half"},
		{"two public lines", "public: " + b64(pub) + "\npublic: " + b64(otherPub) + "\n", "more than one"},
		{"two private lines", "private: " + b64(priv) + "\nprivate: " + b64(priv) + "\npublic: " + b64(pub) + "\n", "more than one"},
	}
	for _, c := range cases {
		path := writeKeyFile(t, c.body)
		assertKeyFileRefused(t, c.name, path, c.wantSub)
	}
	missing := filepath.Join(t.TempDir(), "absent.key")
	assertKeyFileRefused(t, "missing", missing, "")
	if _, err := ReadKeyFile(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadKeyFile(missing) = %v, want fs.ErrNotExist in the chain", err)
	}
}

// assertKeyFileRefused checks that both readers refuse path with an error
// naming the path and containing wantSub.
func assertKeyFileRefused(t *testing.T, name, path, wantSub string) {
	t.Helper()
	if _, err := ReadKeyFile(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), wantSub) {
		t.Errorf("%s: ReadKeyFile = %v, want an error naming %s and %q", name, err, path, wantSub)
	}
	keys, err := VerificationKeysFromKeyFile(path)
	if err == nil || len(keys) != 0 || !strings.Contains(err.Error(), path) {
		t.Errorf("%s: VerificationKeysFromKeyFile = %v, %v; want no keys and an error naming %s", name, keys, err, path)
	}
}

// Spec: §4.7.9, §6.2 — PODIUM_SIGNATURE_VERIFY_KEY takes one or more
// comma-separated keys with optional surrounding space, and an empty value,
// an empty entry, or an undecodable entry refuses the whole list.
func TestPublicKeysFromList(t *testing.T) {
	t.Parallel()
	keys := genPublicKeys(t, 2)
	if got, err := PublicKeysFromList(b64(keys[0])); err != nil || !keysEqual(got, keys[:1]) {
		t.Errorf("one entry = %v, %v", got, err)
	}
	if got, err := PublicKeysFromList(" " + b64(keys[0]) + " ,\t" + b64(keys[1]) + " "); err != nil || !keysEqual(got, keys) {
		t.Errorf("two spaced entries = %v, %v", got, err)
	}
	for _, bad := range []string{"", b64(keys[0]) + ",", b64(keys[0]) + ", ,", b64(keys[0]) + ",!!!", b64([]byte("short"))} {
		if got, err := PublicKeysFromList(bad); err == nil || got != nil {
			t.Errorf("PublicKeysFromList(%q) = %v, %v; want an error and no keys", bad, got, err)
		}
	}
}

// Spec: §4.7.9, §6.2 — a set PODIUM_SIGNATURE_VERIFY_KEY is authoritative
// even when a key file exists, a malformed list names the variable rather
// than falling through to the file, and an unset variable resolves the key
// file's public: and verify: lines.
func TestVerificationKeys(t *testing.T) {
	t.Parallel()
	keys := genPublicKeys(t, 3)
	path := writeKeyFile(t, "public: "+b64(keys[0])+"\nverify: "+b64(keys[1])+"\n")
	if got, err := VerificationKeys(b64(keys[2]), path); err != nil || !keysEqual(got, keys[2:]) {
		t.Errorf("env set = %v, %v; want the env key alone", got, err)
	}
	if _, err := VerificationKeys(b64(keys[2])+",bad", path); err == nil || !strings.Contains(err.Error(), "PODIUM_SIGNATURE_VERIFY_KEY") {
		t.Errorf("malformed env = %v, want an error naming PODIUM_SIGNATURE_VERIFY_KEY", err)
	}
	if got, err := VerificationKeys("", path); err != nil || !keysEqual(got, keys[:2]) {
		t.Errorf("key-file fallback = %v, %v; want public then verify", got, err)
	}
	missing := filepath.Join(t.TempDir(), "absent.key")
	if _, err := VerificationKeys("", missing); err == nil || !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "PODIUM_SIGN_KEY_PATH") {
		t.Errorf("missing key file = %v, want an error naming the file and PODIUM_SIGN_KEY_PATH", err)
	}
}

// Spec: §4.7.9 — with both variables unset and no resolvable home, the
// resolver reports an error rather than an empty set.
func TestVerificationKeys_UnresolvableHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got, err := VerificationKeys("", ""); err == nil || got != nil {
		t.Errorf("VerificationKeys with no home = %v, %v; want an error", got, err)
	}
}

// Spec: §4.7.9 — a rename that cannot replace the target, here a non-empty
// directory, and a directory that refuses the temporary file are errors
// naming the path, and neither leaves a temporary file behind.
func TestKeyFile_WriteFailures(t *testing.T) {
	t.Parallel()
	pub, _ := genKey(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "registry-signing.key")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteKeyFile(target, KeyFile{Public: pub}); err == nil || !strings.Contains(err.Error(), target) {
		t.Errorf("WriteKeyFile over a directory = %v, want an error naming %s", err, target)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("directory holds %d entries after a failed rename, want the target alone", len(entries))
	}
	if os.Geteuid() == 0 {
		return // root ignores directory permissions
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(locked, "k")
	if err := WriteKeyFile(path, KeyFile{Public: pub}); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("WriteKeyFile into a read-only directory = %v, want an error naming %s", err, path)
	}
}
