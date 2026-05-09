package serverboot

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

// Spec: §8.6 — first call to loadOrGenerateAuditSigner with a
// missing path generates a fresh keypair and persists it.
// Subsequent calls return the same keypair so the chain head and
// signer KeyID stay stable across server restarts.
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

// Spec: §8.6 — the persisted key file uses the simple two-line
// format readOrCreateEd25519 implements; an existing key is
// loaded byte-identical.
func TestReadOrCreateEd25519_LoadsExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.key")
	priv1, pub1, err := readOrCreateEd25519(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	priv2, pub2, err := readOrCreateEd25519(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !ed25519.PrivateKey(priv1).Equal(priv2) {
		t.Errorf("private key changed across reloads")
	}
	if !ed25519.PublicKey(pub1).Equal(pub2) {
		t.Errorf("public key changed across reloads")
	}
}
