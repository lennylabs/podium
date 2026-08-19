package main

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

// writeTestPublicKeyPEM writes a PKIX PEM public key and returns its path
// alongside the algorithm that matches it.
func writeTestPublicKeyPEM(t *testing.T, dir, name string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// Spec: §6.3.2 — `podium admin runtime register --keys-file` writes the
// trusted key set the registry reads at startup. The command loads the
// existing file, adds the record, and writes the whole set back, so a
// second registration preserves the first.
func TestAdminRuntimeRegister_WritesKeysFileAndPreservesRecords(t *testing.T) {
	tmp := t.TempDir()
	keysPath := filepath.Join(tmp, "runtimes.json")
	first := writeTestPublicKeyPEM(t, tmp, "first.pem")
	second := writeTestPublicKeyPEM(t, tmp, "second.pem")

	register := func(issuer, keyFile string) int {
		return adminRuntimeRegister([]string{
			"--keys-file", keysPath,
			"--issuer", issuer,
			"--algorithm", "EdDSA",
			"--public-key-file", keyFile,
		})
	}
	if rc := register("alice-runtime", first); rc != 0 {
		t.Fatalf("first register: rc = %d, want 0", rc)
	}
	if _, err := os.Stat(keysPath); err != nil {
		t.Fatalf("keys file not created: %v", err)
	}
	if rc := register("bob-runtime", second); rc != 0 {
		t.Fatalf("second register: rc = %d, want 0", rc)
	}

	reg, err := identity.LoadFilePersistedRuntimeKeyRegistry(keysPath)
	if err != nil {
		t.Fatalf("LoadFilePersistedRuntimeKeyRegistry: %v", err)
	}
	for _, issuer := range []string{"alice-runtime", "bob-runtime"} {
		key, ok := reg.Lookup(issuer)
		if !ok {
			t.Fatalf("keys file missing %s", issuer)
		}
		if key.Algorithm != "EdDSA" {
			t.Errorf("%s algorithm = %q, want EdDSA", issuer, key.Algorithm)
		}
	}
}

// Spec: §6.3.2 — the command parses the PEM against the declared
// algorithm, so a mismatch fails at authoring time rather than aborting
// the registry's next start.
func TestAdminRuntimeRegister_AlgorithmMismatch(t *testing.T) {
	tmp := t.TempDir()
	keysPath := filepath.Join(tmp, "runtimes.json")
	keyFile := writeTestPublicKeyPEM(t, tmp, "key.pem")

	withStderr(t, func() {
		rc := adminRuntimeRegister([]string{
			"--keys-file", keysPath,
			"--issuer", "alice-runtime",
			"--algorithm", "RS256",
			"--public-key-file", keyFile,
		})
		if rc != 1 {
			t.Errorf("rc = %d, want 1", rc)
		}
	})
	if _, err := os.Stat(keysPath); !os.IsNotExist(err) {
		t.Errorf("keys file created despite a rejected key: %v", err)
	}
}

// Spec: §6.3.2 — a keys file that is not valid JSON is a load failure
// rather than a silently discarded key set.
func TestAdminRuntimeRegister_CorruptKeysFile(t *testing.T) {
	tmp := t.TempDir()
	keysPath := filepath.Join(tmp, "runtimes.json")
	if err := os.WriteFile(keysPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	keyFile := writeTestPublicKeyPEM(t, tmp, "key.pem")

	withStderr(t, func() {
		rc := adminRuntimeRegister([]string{
			"--keys-file", keysPath,
			"--issuer", "alice-runtime",
			"--algorithm", "EdDSA",
			"--public-key-file", keyFile,
		})
		if rc != 1 {
			t.Errorf("rc = %d, want 1", rc)
		}
	})
}
