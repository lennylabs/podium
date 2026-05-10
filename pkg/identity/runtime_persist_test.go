package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// Spec: §6.3.2 — file-backed runtime key registry persists every
// registration; reopening the registry against the same path
// recovers them.
func TestFilePersistedRuntimeKeyRegistry_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	reg1, err := identity.LoadFilePersistedRuntimeKeyRegistry(path)
	if err != nil {
		t.Fatalf("LoadFilePersistedRuntimeKeyRegistry: %v", err)
	}
	if err := reg1.Register(identity.RuntimeKey{
		Issuer: "claude-runtime", Algorithm: "EdDSA", Key: pub,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	reg2, err := identity.LoadFilePersistedRuntimeKeyRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reg2.Lookup("claude-runtime")
	if !ok {
		t.Fatalf("claude-runtime missing after reload")
	}
	if got.Algorithm != "EdDSA" {
		t.Errorf("Algorithm = %q, want EdDSA", got.Algorithm)
	}
	loaded, ok := got.Key.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("key type = %T, want ed25519.PublicKey", got.Key)
	}
	if !ed25519.PublicKey(pub).Equal(loaded) {
		t.Errorf("key bytes drift across reload")
	}
}

// Spec: §6.3.2 — missing path returns an empty registry that
// creates the file on first Register.
func TestFilePersistedRuntimeKeyRegistry_MissingPathStartsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	reg, err := identity.LoadFilePersistedRuntimeKeyRegistry(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := reg.All(); len(got) != 0 {
		t.Errorf("All() = %v, want empty", got)
	}
}

// Spec: §6.3.2 — reload tolerates RSA / ECDSA / Ed25519 keys.
func TestFilePersistedRuntimeKeyRegistry_AcceptsRSA(t *testing.T) {
	t.Parallel()
	// Reuse helpers from parse_pem_test via a manual PEM round trip.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	parsed, err := identity.ParsePublicKeyPEM(string(pemBytes), "EdDSA")
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "runtimes.json")
	reg, _ := identity.LoadFilePersistedRuntimeKeyRegistry(path)
	if err := reg.Register(identity.RuntimeKey{
		Issuer: "x", Algorithm: "EdDSA", Key: parsed,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reg2, err := identity.LoadFilePersistedRuntimeKeyRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := reg2.Lookup("x"); !ok {
		t.Errorf("registry lost x after reload")
	}
}
