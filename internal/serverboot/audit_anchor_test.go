package serverboot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §8.6 — first call to loadOrGenerateAuditSigner with a
// missing path generates a fresh keypair and persists it.
// Subsequent calls return the same keypair so the chain head and
// signer key_id stay stable across server restarts.
func TestLoadOrGenerateAuditSigner_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.key")

	signer1, err := loadOrGenerateAuditSigner(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("key file not created: %v", err)
	}

	signer2, err := loadOrGenerateAuditSigner(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	a, ok := signer1.(interface{ ID() string })
	if !ok {
		t.Fatalf("signer1 lacks ID()")
	}
	b, ok := signer2.(interface{ ID() string })
	if !ok {
		t.Fatalf("signer2 lacks ID()")
	}
	if a.ID() != b.ID() {
		t.Errorf("signer ID changed across reloads: %s vs %s", a.ID(), b.ID())
	}
}

// Spec: §8.6 — the persisted key file uses the registry key-file format
// readOrCreateKeyFile reads and writes; an existing key is loaded
// byte-identical, and a generated file is owner-only.
func TestReadOrCreateKeyFile_LoadsExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.key")
	kf1, err := readOrCreateKeyFile(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	kf2, err := readOrCreateKeyFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !kf1.Private.Equal(kf2.Private) {
		t.Errorf("private key changed across reloads")
	}
	if !kf1.Public.Equal(kf2.Public) {
		t.Errorf("public key changed across reloads")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("generated key file mode = %o, want 600", mode)
	}
}

// Spec: §8.6, §13.12 — readOrCreateKeyFile generates only for an absent file.
// A malformed file and a public-only file are refused and left unchanged, so a
// damaged key is never silently replaced by a fresh one.
func TestReadOrCreateKeyFile_RefusesWithoutGenerating(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	for name, body := range map[string]string{
		"malformed":   "public: !!!\n",
		"public only": "public: " + base64.StdEncoding.EncodeToString(pub) + "\n",
	} {
		path := filepath.Join(t.TempDir(), "audit.key")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readOrCreateKeyFile(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: readOrCreateKeyFile = %v, want an error naming %s", name, err, path)
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Errorf("%s: key file rewritten to %q", name, got)
		}
	}
}

// Spec: §8.6 — the anchor signer ignores verify: lines, because the §8.6 key
// is not rotated through a verification key set.
func TestLoadOrGenerateAuditSigner_IgnoresVerifyLines(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	extra, _, _ := ed25519.GenerateKey(rand.Reader)
	path := filepath.Join(t.TempDir(), "audit.key")
	if err := sign.WriteKeyFile(path, sign.KeyFile{Private: priv, Public: pub, Verify: []ed25519.PublicKey{extra}}); err != nil {
		t.Fatal(err)
	}
	signer, err := loadOrGenerateAuditSigner(path)
	if err != nil {
		t.Fatalf("loadOrGenerateAuditSigner: %v", err)
	}
	if rk := signer.(sign.RegistryManagedKey); len(rk.Trusted) != 0 || !rk.PublicKey.Equal(pub) {
		t.Errorf("audit signer = %+v; want the file's keypair and no trusted keys", rk)
	}
}

// Spec: §13.12 — an absent key file whose directory refuses the write is an
// error, and the registry loader surfaces a malformed key file rather than
// replacing it.
func TestReadOrCreateKeyFile_GenerateAndLoaderFailures(t *testing.T) {
	if os.Geteuid() != 0 {
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		if _, err := readOrCreateKeyFile(filepath.Join(locked, "k")); err == nil {
			t.Error("readOrCreateKeyFile into a read-only directory succeeded")
		}
	}
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := os.WriteFile(path, []byte("public: !!!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrGenerateRegistrySigner(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("loadOrGenerateRegistrySigner(malformed) = %v, want an error naming %s", err, path)
	}
}

// Spec: §4.7.9 — the registry signer carries the key file's verify: lines as
// its verification-only keys, so a row the retired key signed still verifies.
func TestLoadOrGenerateRegistrySigner_ReadsVerifyLines(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	retiredPub, retiredPriv, _ := ed25519.GenerateKey(rand.Reader)
	path := filepath.Join(t.TempDir(), "registry-signing.key")
	if err := sign.WriteKeyFile(path, sign.KeyFile{Private: priv, Public: pub, Verify: []ed25519.PublicKey{retiredPub}}); err != nil {
		t.Fatal(err)
	}
	provider, err := loadOrGenerateRegistrySigner(path)
	if err != nil {
		t.Fatalf("loadOrGenerateRegistrySigner: %v", err)
	}
	hash := "sha256:" + strings.Repeat("ab", 32)
	envelope, err := (sign.RegistryManagedKey{PrivateKey: retiredPriv}).Sign(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Verify(context.Background(), hash, envelope); err != nil {
		t.Errorf("verify of a retired-key envelope: %v", err)
	}
}
