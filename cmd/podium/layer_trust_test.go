package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

const (
	cliTrustIssuer   = "https://token.actions.githubusercontent.com"
	cliTrustRepo     = "https://github.com/acme/finance"
	cliTrustIdentity = cliTrustRepo + "/.github/workflows/podium-attest.yml@refs/heads/main"
)

// trustCLIServer serves a layer endpoint over a memory store.
func trustCLIServer(t *testing.T) (string, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	ts := httptest.NewServer(server.NewLayerEndpoint(st, "default", server.NewModeTracker()).Handler())
	t.Cleanup(ts.Close)
	return ts.URL, st
}

// Spec: §7.3.1 (Layer CLI) — the trust flags set the policy on register and
// patch it on update: the binding flags apply to every --trust-signer, a
// requirement from the flags replaces the stored one, and
// --clear-trust-requirements removes it.
func TestLayerCmd_TrustFlags(t *testing.T) {
	base, st := trustCLIServer(t)
	stored := func() store.LayerConfig {
		t.Helper()
		cfg, err := st.GetLayerConfig(context.Background(), "default", "finance")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		return cfg
	}
	rc := layerRegister([]string{"--registry", base, "--id", "finance", "--repo", cliTrustRepo, "--ref", "podium/signed",
		"--trust", "require", "--trust-signer", cliTrustIssuer + "=" + cliTrustIdentity,
		"--trust-source-repository-id", "101", "--trust-source-owner-id", "202", "--trust-source-repository-ref", "refs/heads/main"})
	if rc != 0 {
		t.Fatalf("register rc = %d", rc)
	}
	want := store.TrustSigner{Issuer: cliTrustIssuer, Identity: cliTrustIdentity, SourceRepositoryID: "101",
		SourceRepositoryOwnerID: "202", SourceRepositoryRef: "refs/heads/main"}
	if got := stored(); got.TrustMode != "require" || len(got.TrustRequirements) != 1 || got.TrustRequirements[0].Signers[0] != want {
		t.Fatalf("registered policy = %q %+v", got.TrustMode, got.TrustRequirements)
	}

	rc = layerUpdate([]string{"--registry", base, "--id", "finance", "--trust", "warn",
		"--trust-signer", "https://accounts.google.com=alice@acme.com", "--trust-signer", "https://accounts.google.com=bob@acme.com",
		"--trust-any-source", "--trust-any-ref", "--trust-instance", "public-good",
		"--trust-source-owner", "https://github.com/acme", "--trust-source-repository", cliTrustRepo})
	if rc != 1 {
		t.Errorf("any_source beside a source member: rc = %d, want the registry's refusal as 1", rc)
	}
	rc = layerUpdate([]string{"--registry", base, "--id", "finance", "--trust", "warn",
		"--trust-signer", "https://accounts.google.com=alice@acme.com", "--trust-signer", "https://accounts.google.com=bob@acme.com",
		"--trust-instance", "public-good"})
	if rc != 0 {
		t.Fatalf("update rc = %d", rc)
	}
	got := stored()
	if got.TrustMode != "warn" || len(got.TrustRequirements[0].Signers) != 2 || got.TrustRequirements[0].Signers[1].Identity != "bob@acme.com" ||
		got.TrustRequirements[0].Signers[0].Instance != "public-good" {
		t.Fatalf("updated policy = %q %+v", got.TrustMode, got.TrustRequirements)
	}
	if rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--trust", "none", "--clear-trust-requirements"}); rc != 0 {
		t.Fatalf("clear rc = %d", rc)
	}
	if got := stored(); got.TrustMode != "none" || got.TrustRequirements != nil {
		t.Fatalf("cleared policy = %q %+v", got.TrustMode, got.TrustRequirements)
	}
	if rc := layerRegister([]string{"--registry", base, "--id", "local", "--local", t.TempDir(), "--trust-layer-root", "skills/finance",
		"--trust", "warn", "--trust-signer", "https://accounts.google.com=alice@acme.com", "--trust-any-ref"}); rc != 0 {
		t.Fatalf("local register rc = %d", rc)
	}
	if cfg, _ := st.GetLayerConfig(context.Background(), "default", "local"); cfg.TrustLayerRoot != "skills/finance" || !cfg.TrustRequirements[0].Signers[0].AnyRef {
		t.Errorf("local policy = %+v root %q", cfg.TrustRequirements, cfg.TrustLayerRoot)
	}
}

// Spec: §7.3.1 (Layer CLI) — a binding flag with no --trust-signer, a signer
// that is not <issuer>=<identity>, and --clear-trust-requirements beside a
// signer are usage errors that send nothing.
func TestLayerCmd_TrustFlagUsageErrors(t *testing.T) {
	base, st := trustCLIServer(t)
	for _, args := range [][]string{
		{"--trust-source-repository-ref", "refs/heads/main"},
		{"--trust-any-ref"},
		{"--trust-signer", "no-separator"},
		{"--trust-signer", "=alice@acme.com"},
	} {
		full := append([]string{"--registry", base, "--id", "finance", "--repo", cliTrustRepo, "--ref", "main"}, args...)
		if rc := layerRegister(full); rc != 2 {
			t.Errorf("register %v: rc = %d, want 2", args, rc)
		}
	}
	if rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--clear-trust-requirements", "--trust-signer", "i=alice@acme.com"}); rc != 2 {
		t.Errorf("clear beside a signer: rc = %d, want 2", rc)
	}
	if rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--trust-instance", "github"}); rc != 2 {
		t.Errorf("update binding without signer: rc = %d, want 2", rc)
	}
	if layers, _ := st.ListLayerConfigs(context.Background(), "default"); len(layers) != 0 {
		t.Errorf("a usage error stored %d layers", len(layers))
	}
}

// Spec: §7.3.1 (Layer CLI) — `podium layer show <id>` prints the layer object
// with that ID as indented JSON, and exits 1 when the read holds no such
// layer and 2 on a usage error.
func TestLayerShow(t *testing.T) {
	base, st := trustCLIServer(t)
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{TenantID: "default", ID: "finance", SourceType: "git", Repo: cliTrustRepo,
		TrustMode: "require", TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: cliTrustIssuer, Identity: cliTrustIdentity, SourceRepository: cliTrustRepo, SourceRepositoryRef: "refs/heads/main"}}}}}); err != nil {
		t.Fatal(err)
	}
	if rc := layerShow([]string{"--registry", base, "finance"}); rc != 0 {
		t.Errorf("show rc = %d", rc)
	}
	if rc := layerShow([]string{"--registry", base, "absent"}); rc != 1 {
		t.Errorf("show of an absent layer rc = %d, want 1", rc)
	}
	if rc := layerShow([]string{"--registry", base}); rc != 2 {
		t.Errorf("show without an id rc = %d, want 2", rc)
	}
	if rc := layerShow([]string{"--registry", base + "/missing", "finance"}); rc != 1 {
		t.Errorf("show against a failing read rc = %d, want 1", rc)
	}
	var out bytes.Buffer
	body := []byte(`{"layers":[{"id":"other"},{"id":"finance","trust_mode":"require","trust_requirements":[{"signers":[{"issuer":"i","identity":"a@b","source_repository":"r"}]}]}]}`)
	if err := writeLayerObject(&out, body, "finance"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "{\n  \"id\": \"finance\",\n  \"trust_mode\": \"require\",") || !strings.Contains(out.String(), `"source_repository": "r"`) {
		t.Errorf("output = %s", out.String())
	}
	if err := writeLayerObject(&out, []byte("not json"), "finance"); err == nil {
		t.Error("a body that is not JSON was written")
	}
}
