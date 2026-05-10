package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/serverboot"
)

// Spec: §6.3.2 — when PODIUM_RUNTIME_KEYS_PATH is set, the
// registry persists runtime registrations to JSON. A second
// server boot reads them back and the JWT verifier trusts the
// issuer immediately.
func TestServe_RuntimeKeysPersistAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	keysPath := filepath.Join(tmp, "runtimes.json")
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	keyFile := filepath.Join(tmp, "runtime.pem")
	if err := os.WriteFile(keyFile, pemBytes, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	configFile := filepath.Join(tmp, "missing.yaml")
	startServer := func(port int) {
		t.Setenv("PODIUM_BIND", fmt.Sprintf("127.0.0.1:%d", port))
		t.Setenv("PODIUM_REGISTRY_STORE", "memory")
		t.Setenv("PODIUM_OBJECT_STORE", "none")
		t.Setenv("PODIUM_CONFIG_FILE", configFile)
		t.Setenv("PODIUM_FILESYSTEM_ROOT", tmp)
		t.Setenv("PODIUM_VECTOR_BACKEND", "")
		t.Setenv("PODIUM_RUNTIME_KEYS_PATH", keysPath)
		go func() { _ = serverboot.Run() }()
	}

	port1 := freePort(t)
	startServer(port1)
	waitForHealthz(t, port1)

	rc := adminRuntimeRegister([]string{
		"--registry", fmt.Sprintf("http://127.0.0.1:%d", port1),
		"--issuer", "claude-runtime",
		"--algorithm", "EdDSA",
		"--public-key-file", keyFile,
	})
	if rc != 0 {
		t.Fatalf("first register: rc = %d", rc)
	}
	if _, err := os.Stat(keysPath); err != nil {
		t.Fatalf("keys file not created: %v", err)
	}

	// Second boot reads the persisted file.
	port2 := freePort(t)
	startServer(port2)
	waitForHealthz(t, port2)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/admin/runtime", port2))
	if err != nil {
		t.Fatalf("GET /v1/admin/runtime: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Runtimes []struct {
			Issuer    string `json:"issuer"`
			Algorithm string `json:"algorithm"`
		} `json:"runtimes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Runtimes) != 1 || body.Runtimes[0].Issuer != "claude-runtime" {
		t.Errorf("Runtimes = %+v, want claude-runtime preserved", body.Runtimes)
	}
}

func waitForHealthz(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server on port %d never came up", port)
}
