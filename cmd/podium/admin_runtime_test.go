package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §6.3.2 — `podium admin runtime register` POSTs a PEM key
// to the server's runtime endpoint; the registry then trusts the
// issuer for injected-session-token verification.
func TestAdminRuntimeRegister_End2End(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	keyFile := filepath.Join(t.TempDir(), "runtime.pem")
	if err := os.WriteFile(keyFile, pemBytes, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := identity.NewRuntimeKeyRegistry()
	endpoint := server.NewRuntimeKeyEndpoint(reg, server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	rc := adminRuntimeRegister([]string{
		"--registry", ts.URL,
		"--issuer", "claude-runtime",
		"--algorithm", "EdDSA",
		"--public-key-file", keyFile,
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if _, ok := reg.Lookup("claude-runtime"); !ok {
		t.Errorf("registry missing claude-runtime")
	}
}

// Spec: §6.3.2 — missing flags surface as an argument error.
func TestAdminRuntimeRegister_MissingFlags(t *testing.T) {
	rc := adminRuntimeRegister([]string{"--registry", "http://localhost"})
	if rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
}
