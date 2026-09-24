package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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

// Spec: §4.7.9 — both halves read back from a file in the registry's format.
func TestKeyFile_ReadsBothHalves(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	path := writeKeyFile(t, registryKeyFileBody(priv, pub))
	gotPub, err := PublicKeyFromKeyFile(path)
	if err != nil || !gotPub.Equal(pub) {
		t.Fatalf("PublicKeyFromKeyFile = %v, %v; want the written public key", gotPub, err)
	}
	gotPriv, err := PrivateKeyFromKeyFile(path)
	if err != nil || !gotPriv.Equal(priv) {
		t.Fatalf("PrivateKeyFromKeyFile = %v, %v; want the written private key", gotPriv, err)
	}
}

// Spec: §4.7.9 — each reader takes only the half it names, so a file carrying
// the public line alone serves a verifier and refuses a signer.
func TestKeyFile_PublicOnlyFile(t *testing.T) {
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	path := writeKeyFile(t, "public: "+base64.StdEncoding.EncodeToString(pub)+"\n")
	if _, err := PublicKeyFromKeyFile(path); err != nil {
		t.Fatalf("PublicKeyFromKeyFile: %v", err)
	}
	if _, err := PrivateKeyFromKeyFile(path); err == nil || !strings.Contains(err.Error(), "private:") {
		t.Errorf("PrivateKeyFromKeyFile on a public-only file = %v, want an error naming the private line", err)
	}
}

// Spec: §4.7.9 — a missing or malformed file is an error naming the path,
// which each caller turns into a refusal rather than an unverifying provider.
func TestKeyFile_MalformedAndMissing(t *testing.T) {
	t.Parallel()
	shortKey := base64.StdEncoding.EncodeToString([]byte("short"))
	cases := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"no lines", "comment: nothing here\n"},
		{"undecodable", "private: !!!\npublic: !!!\n"},
		{"wrong size", "private: " + shortKey + "\npublic: " + shortKey + "\n"},
	}
	for _, c := range cases {
		path := writeKeyFile(t, c.body)
		if _, err := PublicKeyFromKeyFile(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: PublicKeyFromKeyFile = %v, want an error naming %s", c.name, err, path)
		}
		if _, err := PrivateKeyFromKeyFile(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: PrivateKeyFromKeyFile = %v, want an error naming %s", c.name, err, path)
		}
	}
	missing := filepath.Join(t.TempDir(), "absent.key")
	if _, err := PublicKeyFromKeyFile(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("PublicKeyFromKeyFile(missing) = %v, want an error naming the path", err)
	}
	if _, err := PrivateKeyFromKeyFile(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("PrivateKeyFromKeyFile(missing) = %v, want an error naming the path", err)
	}
}
