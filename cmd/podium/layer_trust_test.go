package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
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

// Spec: §7.3.1 (Layer CLI), §4.7.11 (Requirements file) — `podium layer
// show <id> --requirements` writes the layer's requirements, with the
// derived members, as a requirements file that `podium attest check`
// reads, and exits 1 for a layer with no requirement.
func TestLayerShow_Requirements(t *testing.T) {
	base, st := trustCLIServer(t)
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{TenantID: "default", ID: "finance", SourceType: "git", Repo: cliTrustRepo,
		TrustMode: "require", TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: cliTrustIssuer, Identity: cliTrustIdentity, SourceRepository: cliTrustRepo, SourceRepositoryRef: "refs/heads/main"}}}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutLayerConfig(context.Background(), store.LayerConfig{TenantID: "default", ID: "open", SourceType: "git", Repo: cliTrustRepo}); err != nil {
		t.Fatal(err)
	}
	if rc := layerShow([]string{"--registry", base, "finance", "--requirements"}); rc != 0 {
		t.Errorf("show --requirements rc = %d", rc)
	}
	if rc := layerShow([]string{"--registry", base, "--requirements", "open"}); rc != 1 {
		t.Errorf("show --requirements of a layer with no requirement rc = %d, want 1", rc)
	}
	var out bytes.Buffer
	body := []byte(`{"layers":[{"id":"finance","trust_requirements":[{"signers":[{"issuer":"i","identity":"https://acme.test/w","source_repository":"r","source_repository_ref":"refs/heads/main","instance":"github"}]}]}]}`)
	if err := writeLayerRequirements(&out, body, "finance"); err != nil {
		t.Fatal(err)
	}
	f, err := layer.ParseRequirementsFile(out.Bytes())
	if err != nil || len(f.TrustRequirements) != 1 || f.TrustRequirements[0].Signers[0].Instance != "github" {
		t.Fatalf("file = %s, %v", out.String(), err)
	}
	if err := layer.CheckRequirements(f.TrustRequirements, func(n string) bool { return n == "github" }); err != nil {
		t.Errorf("the written file breaks a rule: %v", err)
	}
	for name, b := range map[string]string{
		"absent layer":   `{"layers":[]}`,
		"unknown member": `{"layers":[{"id":"finance","trust_requirements":[{"signers":[],"bogus":1}]}]}`,
		"null":           `{"layers":[{"id":"finance","trust_requirements":null}]}`,
	} {
		if err := writeLayerRequirements(&out, []byte(b), "finance"); err == nil {
			t.Errorf("%s: written", name)
		}
	}
}

// Spec: §7.3.1 (Layer CLI) — --trust-requirements-file sends the file's
// requirements, so a policy of several requirements round-trips through
// `podium layer show --requirements`; --trust-require-type sets the listed
// types and --clear-trust-require-types removes them; an update that would
// replace several stored requirements with one built from --trust-signer
// exits 1 and sends nothing, as does a failed read of the layer.
func TestLayerCmd_RequirementsFileAndRequireTypes(t *testing.T) {
	base, st := trustCLIServer(t)
	stored := func() store.LayerConfig {
		t.Helper()
		cfg, err := st.GetLayerConfig(context.Background(), "default", "finance")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		return cfg
	}
	file := filepath.Join(t.TempDir(), "requirements.json")
	body := `{"trust_requirements":[` +
		`{"signers":[{"issuer":"` + cliTrustIssuer + `","identity":"` + cliTrustIdentity + `"}],"covers_layer":true},` +
		`{"signers":[{"issuer":"https://accounts.google.com","identity":"scanner@acme.com"}],"predicate_type":"https://acme.com/scan/v1","optional":true}],` +
		`"layer_roots":{"x":"y"}}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rc := layerRegister([]string{"--registry", base, "--id", "finance", "--repo", cliTrustRepo, "--ref", "podium/signed",
		"--trust", "warn", "--trust-requirements-file", file, "--trust-require-type", "hook", "--trust-require-type", "mcp-server"})
	if rc != 0 {
		t.Fatalf("register rc = %d", rc)
	}
	got := stored()
	if len(got.TrustRequirements) != 2 || !got.TrustRequirements[0].CoversLayer || !got.TrustRequirements[1].Optional ||
		got.TrustRequirements[0].Signers[0].SourceRepository != cliTrustRepo || strings.Join(got.TrustRequireTypes, ",") != "hook,mcp-server" {
		t.Fatalf("registered policy = %+v types %v", got.TrustRequirements, got.TrustRequireTypes)
	}
	// The signer flags would replace two requirements with one.
	if rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--trust-signer", "i=alice@acme.com"}); rc != 1 {
		t.Errorf("signer update over several requirements: rc = %d, want 1", rc)
	}
	if rc := layerUpdate([]string{"--registry", base + "/missing", "--id", "finance", "--trust-signer", "i=alice@acme.com"}); rc != 1 {
		t.Errorf("signer update with a failed read: rc = %d, want 1", rc)
	}
	if rc := layerUpdate([]string{"--registry", base, "--id", "absent", "--trust-signer", "i=alice@acme.com"}); rc != 1 {
		t.Errorf("signer update of a layer the read does not return: rc = %d, want 1", rc)
	}
	odd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"layers":[{"id":"finance","trust_requirements":"several"}]}`))
	}))
	t.Cleanup(odd.Close)
	if rc := layerUpdate([]string{"--registry", odd.URL, "--id", "finance", "--trust-signer", "i=alice@acme.com"}); rc != 1 {
		t.Errorf("signer update over a layer object that does not decode: rc = %d, want 1", rc)
	}
	if got := stored(); len(got.TrustRequirements) != 2 {
		t.Fatalf("a refused update changed the policy: %+v", got.TrustRequirements)
	}
	if rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--clear-trust-require-types"}); rc != 0 {
		t.Fatalf("clear types rc = %d", rc)
	}
	if got := stored(); got.TrustRequireTypes != nil || len(got.TrustRequirements) != 2 {
		t.Fatalf("after clearing the types: %+v %v", got.TrustRequirements, got.TrustRequireTypes)
	}
	// A one-requirement policy from a file can then be replaced by the flags.
	one := filepath.Join(t.TempDir(), "one.json")
	if err := os.WriteFile(one, []byte(`{"trust_requirements":[{"signers":[{"issuer":"i","identity":"alice@acme.com"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--trust-requirements-file", one}); rc != 0 {
		t.Fatalf("file update rc = %d", rc)
	}
	if rc := layerUpdate([]string{"--registry", base, "--id", "finance", "--trust-signer", "i=bob@acme.com"}); rc != 0 {
		t.Fatalf("signer update over one requirement rc = %d", rc)
	}
	if got := stored(); len(got.TrustRequirements) != 1 || got.TrustRequirements[0].Signers[0].Identity != "bob@acme.com" {
		t.Fatalf("replaced policy = %+v", got.TrustRequirements)
	}
}

// Spec: §7.3.1 (Layer CLI) — --trust-requirements-file beside a signer or a
// binding flag, and --clear-trust-requirements or --clear-trust-require-types
// beside the flag it clears, are usage errors; a file the command cannot read
// or that the file rules refuse exits 1. None sends a request.
func TestLayerCmd_RequirementsFileErrors(t *testing.T) {
	base, st := trustCLIServer(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(good, []byte(`{"trust_requirements":[{"signers":[{"issuer":"i","identity":"a@b"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(`{"trust_requirements":[],"trust_mode":"require"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := []string{"--registry", base, "--id", "finance", "--repo", cliTrustRepo, "--ref", "main"}
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"--trust-requirements-file", good, "--trust-signer", "i=a@b"}, 2},
		{[]string{"--trust-requirements-file", good, "--trust-any-ref"}, 2},
		{[]string{"--trust-requirements-file", filepath.Join(dir, "absent.json")}, 1},
		{[]string{"--trust-requirements-file", bad}, 1},
	} {
		if rc := layerRegister(append(append([]string{}, reg...), tc.args...)); rc != tc.want {
			t.Errorf("register %v: rc = %d, want %d", tc.args, rc, tc.want)
		}
	}
	for _, args := range [][]string{
		{"--trust-requirements-file", good, "--clear-trust-requirements"},
		{"--trust-require-type", "hook", "--clear-trust-require-types"},
	} {
		if rc := layerUpdate(append([]string{"--registry", base, "--id", "finance"}, args...)); rc != 2 {
			t.Errorf("update %v: rc = %d, want 2", args, rc)
		}
	}
	if layers, _ := st.ListLayerConfigs(context.Background(), "default"); len(layers) != 0 {
		t.Errorf("a refused command stored %d layers", len(layers))
	}
}

// Spec: §7.3.1 (Layer CLI, Patch semantics on the update body) — an update
// with --trust-signer sends the requirements it read as
// expected_trust_requirements, so a second requirement that another writer
// stores between the read and the update makes the registry refuse the
// update, and the command exits 1 with the scanner requirement kept.
func TestLayerCmd_SignerUpdateRaceIsRefused(t *testing.T) {
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatal(err)
	}
	h := server.NewLayerEndpoint(st, "default", server.NewModeTracker()).Handler()
	raced := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/layers" || raced {
			return
		}
		raced = true
		cfg, err := st.GetLayerConfig(context.Background(), "default", "finance")
		if err != nil {
			t.Error(err)
			return
		}
		cfg.TrustRequirements[0].CoversLayer = true
		cfg.TrustRequirements = append(cfg.TrustRequirements, store.TrustRequirement{PredicateType: "https://acme.com/scan/v1",
			Signers: []store.TrustSigner{{Issuer: "https://accounts.google.com", Identity: "scanner@acme.com", AnySource: true, AnyRef: true}}})
		if err := st.PutLayerConfig(context.Background(), cfg); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(ts.Close)
	if rc := layerRegister([]string{"--registry", ts.URL, "--id", "finance", "--repo", cliTrustRepo, "--ref", "podium/signed",
		"--trust", "require", "--trust-signer", cliTrustIssuer + "=" + cliTrustIdentity}); rc != 0 {
		t.Fatalf("register rc = %d", rc)
	}
	rc := layerUpdate([]string{"--registry", ts.URL, "--id", "finance", "--trust-signer", cliTrustIssuer + "=" + cliTrustIdentity})
	cfg, _ := st.GetLayerConfig(context.Background(), "default", "finance")
	if !raced || rc != 1 || len(cfg.TrustRequirements) != 2 {
		t.Fatalf("raced %v, rc %d, %d requirements", raced, rc, len(cfg.TrustRequirements))
	}
}
