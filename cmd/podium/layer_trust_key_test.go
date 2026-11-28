package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/store"
)

// writeKeyFile writes the PEM public key of a fresh signing key to dir and
// returns the path and the PEM text.
func writeKeyFile(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	pub := sh.PublicKeyPEM(t, sh.NewSigningKey(t).Public())
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(pub), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, pub
}

// Spec: §7.3.1 (Layer CLI), §4.7.11 (Key-based signers) — --trust-public-key
// adds a key-based signer with the file's text as public_key, beside the
// signers --trust-signer names, which alone take the binding flags; an
// update with it replaces the stored requirement.
func TestLayerCmd_TrustPublicKey(t *testing.T) {
	base, st := trustCLIServer(t)
	dir := t.TempDir()
	keyA, pubA := writeKeyFile(t, dir, "a.pub")
	keyB, pubB := writeKeyFile(t, dir, "b.pub")
	stored := func() store.LayerConfig {
		t.Helper()
		cfg, err := st.GetLayerConfig(context.Background(), "default", "finance")
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	if rc := layerRegister([]string{"--registry", base, "--id", "finance", "--repo", cliTrustRepo, "--ref", "podium/signed",
		"--trust", "require", "--trust-public-key", keyA}); rc != 0 {
		t.Fatalf("register rc = %d", rc)
	}
	if got := stored(); len(got.TrustRequirements) != 1 || got.TrustRequirements[0].Signers[0] != (store.TrustSigner{PublicKey: pubA}) {
		t.Fatalf("registered %+v", got.TrustRequirements)
	}
	rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--trust-public-key", keyA, "--trust-public-key", keyB,
		"--trust-signer", "https://accounts.google.com=alice@acme.com", "--trust-instance", "public-good"})
	if rc != 0 {
		t.Fatalf("update rc = %d", rc)
	}
	want := []store.TrustSigner{{Issuer: "https://accounts.google.com", Identity: "alice@acme.com", Instance: "public-good"}, {PublicKey: pubA}, {PublicKey: pubB}}
	if got := stored().TrustRequirements[0].Signers; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("updated signers %+v", got)
	}
}

// Spec: §7.3.1 (Layer CLI) — a binding flag with only --trust-public-key,
// --clear-trust-requirements or --trust-requirements-file beside it are
// usage errors, a key file the command cannot read exits 1, and a key the
// registry refuses exits 1, each storing nothing.
func TestLayerCmd_TrustPublicKeyErrors(t *testing.T) {
	base, st := trustCLIServer(t)
	dir := t.TempDir()
	key, _ := writeKeyFile(t, dir, "a.pub")
	p384 := filepath.Join(dir, "p384.pub")
	pub384 := sh.PublicKeyPEM(t, mustP384(t))
	if err := os.WriteFile(p384, []byte(pub384), 0o600); err != nil {
		t.Fatal(err)
	}
	reqFile := filepath.Join(dir, "req.json")
	if err := os.WriteFile(reqFile, []byte(`{"trust_requirements":[{"signers":[{"issuer":"i","identity":"a@b"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := []string{"--registry", base, "--id", "finance", "--repo", cliTrustRepo, "--ref", "main", "--trust", "require"}
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"--trust-public-key", key, "--trust-instance", "github"}, 2},
		{[]string{"--trust-public-key", key, "--trust-requirements-file", reqFile}, 2},
		{[]string{"--trust-public-key", filepath.Join(dir, "absent.pub")}, 1},
		{[]string{"--trust-public-key", p384}, 1},
	} {
		if rc := layerRegister(append(append([]string{}, reg...), tc.args...)); rc != tc.want {
			t.Errorf("register %v: rc = %d, want %d", tc.args, rc, tc.want)
		}
	}
	if rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--clear-trust-requirements", "--trust-public-key", key}); rc != 2 {
		t.Errorf("clear beside a key: rc = %d, want 2", rc)
	}
	if layers, _ := st.ListLayerConfigs(context.Background(), "default"); len(layers) != 0 {
		t.Errorf("a refused command stored %d layers", len(layers))
	}
}

// Spec: §7.3.1 (Layer CLI) — `podium layer show` names the next step of a
// layer whose key-signed layer subject is ahead of the registry's clock.
func TestWriteAttestationNotes_SignedTimeAhead(t *testing.T) {
	var buf bytes.Buffer
	writeAttestationNotes(&buf, []byte(`{"id":"finance","attestation_status":"ingest.attestation_stale","attestation_details":{"constraint":"signed_time_ahead"}}`))
	if !strings.Contains(buf.String(), "PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE") {
		t.Errorf("notes %q", buf.String())
	}
}

// mustP384 returns the public key of a fresh P-384 key, which no key-based
// signer takes.
func mustP384(t *testing.T) any {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k.Public()
}
