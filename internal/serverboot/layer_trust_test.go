package serverboot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// trustYAMLLayer is a declared git layer of acme/finance with the trust
// policy block policy, indented under the entry.
func trustYAMLLayer(policy string) string {
	return "registry:\n  layers:\n    - id: finance\n      source:\n        git:\n          repo: https://github.com/acme/finance.git\n          ref: podium/signed\n" + policy
}

// loadTrustConfig writes body as registry.yaml and loads the configuration
// with the trust default def.
func loadTrustConfig(t *testing.T, body, def string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("PODIUM_CONFIG_FILE", path)
	t.Setenv(layerTrustDefaultEnv, def)
	return LoadConfig()
}

// Spec: §4.7.11 (Policy rules), §7.3.1 (Layer trust policy) — a declared
// layer whose trust policy breaks a rule refuses the start with
// config.invalid_layer_trust naming the layer and the rule, as does a key a
// declared requirement or signer does not define.
// Matrix: §6.10 (config.invalid_layer_trust)
func TestValidate_DeclaredLayerTrustRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, policy, want string
	}{
		{"require without requirement", "      trust_mode: require\n", "trust_mode require needs a requirement"},
		{"several", "      trust_requirements:\n        - signers: [{issuer: i, identity: alice@acme.com}]\n        - signers: [{issuer: i, identity: alice@acme.com}]\n", "several requirements are not available yet"},
		{"unbound", "      trust_requirements:\n        - signers:\n            - issuer: i\n              identity: https://github.com/acme/.github/.github/workflows/a.yml@refs/heads/main\n", "names no source binding"},
		{"unknown instance", "      trust_requirements:\n        - signers:\n            - {issuer: i, identity: alice@acme.com, instance: acme}\n", `instance "acme"`},
		{"misspelled member", "      trust_requirements:\n        - signers:\n            - {issuer: i, identity: alice@acme.com, source_repositry: x}\n", `trust_requirements[0].signers[0] holds the key "source_repositry" (line 10)`},
		{"requirement key", "      trust_requirements:\n        - signers: [{issuer: i, identity: alice@acme.com}]\n          optional: true\n", `trust_requirements[0] holds the key "optional"`},
		{"not a list", "      trust_requirements: {signers: []}\n", "is not a list"},
		{"bad shape", "      trust_requirements:\n        - signers: notalist\n", "trust_requirements:"},
		{"root on git", "      trust_layer_root: skills\n", "applies to a local layer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadTrustConfig(t, trustYAMLLayer(tc.policy), "").validate()
			if err == nil || !strings.HasPrefix(err.Error(), "config.invalid_layer_trust: declared layer finance: ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate = %v, want config.invalid_layer_trust naming finance and %q", err, tc.want)
			}
		})
	}
}

// Spec: §13.12 — PODIUM_LAYER_TRUST_DEFAULT is none, warn, or require, with
// surrounding whitespace removed and an empty value unset; any other value
// refuses the start with config.invalid_layer_trust, and `podium config
// show --server` reports it.
// Matrix: §6.10 (config.invalid_layer_trust)
func TestLoadConfig_LayerTrustDefault(t *testing.T) {
	for raw, want := range map[string]string{"": "none", " require ": "require", "warn": "warn", "none": "none"} {
		c := loadTrustConfig(t, "", raw)
		if c.layerTrust.mode != want || c.layerTrust.err != nil {
			t.Errorf("%q: mode %q err %v, want %q", raw, c.layerTrust.mode, c.layerTrust.err, want)
		}
	}
	c := loadTrustConfig(t, "", "enforce")
	err := c.validate()
	if err == nil || !strings.HasPrefix(err.Error(), "config.invalid_layer_trust: PODIUM_LAYER_TRUST_DEFAULT") {
		t.Fatalf("validate = %v, want config.invalid_layer_trust", err)
	}
	if errs := c.SettingErrors(); len(errs) != 1 || errs[0] != c.layerTrust.err {
		t.Errorf("SettingErrors = %v", errs)
	}
	for _, s := range c.Settings() {
		if s.Name == "layers.trust_default" && (s.Value != "enforce (invalid)" || s.Source != layerTrustDefaultEnv) {
			t.Errorf("layers.trust_default row = %+v", s)
		}
	}
}

// Spec: §4.7.11 (Derived source binding), §7.3.1 (Layer trust policy) — a
// declared policy is seeded with its derived source binding, an instance
// that sigstore.instances configures is admitted, and an unquoted numeric
// identifier is read as written.
func TestBootstrapDeclaredLayers_SeedsTrustPolicy(t *testing.T) {
	dir := t.TempDir()
	writeRoot(t, dir, "github.json", readTestdataRoot(t, "github.trusted_root.json"))
	body := "registry:\n  sigstore:\n    instances:\n      - name: github\n        trusted_root: " + filepath.Join(dir, "github.json") + "\n" +
		strings.TrimPrefix(trustYAMLLayer("      trust_mode: require\n      trust_requirements:\n        - predicate_type: https://slsa.dev/provenance/v1\n          signers:\n"+
			"            - issuer: https://token.actions.githubusercontent.com\n              identity: https://github.com/acme/finance/.github/workflows/podium-attest.yml@refs/heads/main\n"+
			"            - {issuer: i, identity: alice@acme.com, instance: github, source_repository_owner_id: 12345, any_ref: true}\n"), "registry:\n")
	c := loadTrustConfig(t, body, "")
	if err := c.validate(); err != nil {
		t.Fatalf("validate = %v", err)
	}
	st := newMemoryStoreWithTenant(t)
	if _, err := bootstrapDeclaredLayers(st, "default", c, nil, nil, false, collocatedVectorIngest{}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	got, err := st.GetLayerConfig(context.Background(), "default", "finance")
	if err != nil {
		t.Fatal(err)
	}
	want := []store.TrustRequirement{{PredicateType: "https://slsa.dev/provenance/v1", Signers: []store.TrustSigner{
		{Issuer: "https://token.actions.githubusercontent.com", Identity: "https://github.com/acme/finance/.github/workflows/podium-attest.yml@refs/heads/main",
			SourceRepository: "https://github.com/acme/finance", SourceRepositoryRef: "refs/heads/main"},
		{Issuer: "i", Identity: "alice@acme.com", Instance: "github", SourceRepositoryOwnerID: "12345", AnyRef: true},
	}}}
	if !layer.TrustPolicyEqual(got, store.LayerConfig{TrustMode: "require", TrustRequirements: want}) {
		t.Errorf("seeded policy = %q %+v, want require %+v", got.TrustMode, got.TrustRequirements, want)
	}
}

// Spec: §7.3.1 (Layer trust policy) — a start that re-seeds a --layer-path
// layer keeps the trust policy the layer stores, and a failed read of the
// stored layer aborts the start rather than drop the policy.
func TestBootstrapLayerPath_KeepsStoredTrustPolicy(t *testing.T) {
	root := t.TempDir()
	testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "a/ARTIFACT.md", Content: artifactBody})
	st := newMemoryStoreWithTenant(t)
	policy := store.LayerConfig{TrustMode: "warn", TrustLayerRoot: "skills",
		TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: "i", Identity: "alice@acme.com"}}}}}
	layers, err := bootstrapLayerPath(st, "default", root, layer.Visibility{Public: true}, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil)
	if err != nil || len(layers) != 1 {
		t.Fatalf("first start: %v %v", layers, err)
	}
	id := layers[0].ID
	stored, _ := st.GetLayerConfig(context.Background(), "default", id)
	stored.TrustMode, stored.TrustRequirements, stored.TrustLayerRoot = policy.TrustMode, policy.TrustRequirements, policy.TrustLayerRoot
	if err := st.PutLayerConfig(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrapLayerPath(st, "default", root, layer.Visibility{Public: true}, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if got, _ := st.GetLayerConfig(context.Background(), "default", id); !layer.TrustPolicyEqual(got, policy) {
		t.Errorf("policy after the re-seed = %q %+v %q", got.TrustMode, got.TrustRequirements, got.TrustLayerRoot)
	}
	if _, err := keepStoredTrust(context.Background(), failingGetStore{st}, store.LayerConfig{TenantID: "default", ID: id}); err == nil {
		t.Error("a failed read kept nothing and reported no error")
	}
}

// failingGetStore fails every layer read.
type failingGetStore struct{ store.Store }

func (failingGetStore) GetLayerConfig(context.Context, string, string) (store.LayerConfig, error) {
	return store.LayerConfig{}, errors.New("store down")
}

// failingListStore fails every layer list.
type failingListStore struct{ store.Store }

func (failingListStore) ListLayerConfigs(context.Context, string) ([]store.LayerConfig, error) {
	return nil, errors.New("store down")
}

// Spec: §4.7.11 (Policy changes) — at a start the registry names each stored
// layer whose empty mode the trust default resolves to warn or require and
// that has no requirement, in every tenant of a multi-tenant registry, and
// logs nothing under the default none.
func TestLogUnrequiredTrustLayers(t *testing.T) {
	ctx := context.Background()
	st := newMemoryStoreWithTenant(t)
	if err := st.CreateTenant(ctx, store.Tenant{ID: "globex"}); err != nil {
		t.Fatal(err)
	}
	reqs := []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: "i", Identity: "alice@acme.com"}}}}
	for _, l := range []store.LayerConfig{
		{TenantID: "default", ID: "bare", SourceType: "local", LocalPath: "/x"},
		{TenantID: "default", ID: "explicit-none", SourceType: "local", LocalPath: "/x", TrustMode: "none"},
		{TenantID: "default", ID: "has-requirement", SourceType: "local", LocalPath: "/x", TrustRequirements: reqs},
		{TenantID: "globex", ID: "other-bare", SourceType: "local", LocalPath: "/x"},
	} {
		if err := st.PutLayerConfig(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	logUnrequiredTrustLayers(ctx, st, trustLogTenants(ctx, st, true, "default"), "require", logf)
	if len(lines) != 2 || !strings.Contains(lines[0], "layer bare (tenant default) resolves to trust mode require through PODIUM_LAYER_TRUST_DEFAULT") ||
		!strings.Contains(lines[1], "layer other-bare (tenant globex)") {
		t.Errorf("lines = %q", lines)
	}
	lines = nil
	logUnrequiredTrustLayers(ctx, st, trustLogTenants(ctx, st, false, "default"), "none", logf)
	logUnrequiredTrustLayers(ctx, failingListStore{st}, []string{"default"}, "warn", logf)
	if len(lines) != 1 || !strings.Contains(lines[0], "could not list the layers of tenant default") {
		t.Errorf("lines = %q", lines)
	}
}

// failingTenantsStore fails the tenant list.
type failingTenantsStore struct{ store.Store }

func (failingTenantsStore) ListTenants(context.Context) ([]store.Tenant, error) {
	return nil, errors.New("store down")
}

// Spec: §7.3.1 (Layer trust policy) — the seed paths fail closed: a declared
// policy that breaks a rule fails the seed even when validate was skipped, a
// requirement that is no mapping fails its decode, a failed read of a stored
// --layer-path layer aborts the re-seed, and a failed tenant list falls back
// to the bound tenant for the start warning.
func TestLayerTrust_SeedPathsFailClosed(t *testing.T) {
	cfg := &Config{declaredLayers: []yamlLayerEntry{{ID: "finance", TrustMode: "require",
		Source: yamlLayerSource{Local: &yamlLocalSource{Path: declaredLocalTree(t, "policy")}}}}}
	if _, err := bootstrapDeclaredLayers(newMemoryStoreWithTenant(t), "default", cfg, nil, nil, false, collocatedVectorIngest{}); err == nil ||
		!strings.Contains(err.Error(), "config.invalid_layer_trust: declared layer finance") {
		t.Errorf("seed of an invalid declared policy = %v", err)
	}
	if err := loadTrustConfig(t, trustYAMLLayer("      trust_requirements: [notamapping]\n"), "").validate(); err == nil ||
		!strings.Contains(err.Error(), "config.invalid_layer_trust") {
		t.Errorf("a requirement that is no mapping = %v", err)
	}
	if !(&Config{}).trustInstanceKnown("public-good") || (&Config{}).trustInstanceKnown("github") {
		t.Error("trustInstanceKnown without sigstore.instances")
	}
	root := t.TempDir()
	testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "a/ARTIFACT.md", Content: artifactBody})
	if _, err := bootstrapLayerPath(failingGetStore{newMemoryStoreWithTenant(t)}, "default", root, layer.Visibility{Public: true}, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil); err == nil {
		t.Error("a failed read of the stored layer did not abort the re-seed")
	}
	if got := trustLogTenants(context.Background(), failingTenantsStore{newMemoryStoreWithTenant(t)}, true, "default"); len(got) != 1 || got[0] != "default" {
		t.Errorf("tenants after a failed list = %v", got)
	}
}

// Spec: §7.3.1 (Layer trust policy) — a declared requirement or signer
// reached through a YAML alias is checked as one written in place, so a
// misspelled member behind an alias aborts the start instead of being
// dropped; a merge key is refused as a key; a misspelled layer-level trust
// key (trust-mode, Trust_Mode) aborts the start with
// config.invalid_layer_trust.
// Matrix: §6.10 (config.invalid_layer_trust)
func TestValidate_DeclaredTrustAliasesAndLayerKeys(t *testing.T) {
	layerHead := "  layers:\n    - id: finance\n      source:\n        git:\n          repo: https://github.com/acme/finance.git\n          ref: podium/signed\n"
	for _, tc := range []struct{ name, body, want string }{
		{"aliased signer",
			"registry:\n  x-anchors:\n    signer: &s {issuer: i, identity: alice@acme.com, instance_name: github}\n" + layerHead +
				"      trust_mode: require\n      trust_requirements:\n        - signers: [*s]\n",
			`trust_requirements[0].signers[0] holds the key "instance_name"`},
		{"aliased requirement",
			"registry:\n  x-anchors:\n    req: &r {signers: [{issuer: i, identity: alice@acme.com}], predicate_typo: https://slsa.dev/provenance/v1}\n" + layerHead +
				"      trust_mode: require\n      trust_requirements:\n        - *r\n",
			`trust_requirements[0] holds the key "predicate_typo"`},
		{"aliased signer list",
			"registry:\n  x-anchors:\n    signers: &l [{issuer: i, identity: alice@acme.com, source_repositry: x}]\n" + layerHead +
				"      trust_requirements:\n        - signers: *l\n",
			`holds the key "source_repositry"`},
		{"merge key",
			"registry:\n  x-anchors:\n    base: &b {issuer: i}\n" + layerHead +
				"      trust_requirements:\n        - signers: [{<<: *b, identity: alice@acme.com}]\n",
			`holds the key "<<"`},
		{"dashed layer key", "registry:\n" + layerHead + "      trust-mode: require\n", `the key "trust-mode" (line 8) is not a trust policy member`},
		{"case layer key", "registry:\n" + layerHead + "      Trust_Mode: require\n", `the key "Trust_Mode"`},
		{"entry merge key",
			"registry:\n  x-anchors:\n    c: &c {trust_mod: require}\n" + layerHead + "      <<: *c\n",
			`the key "trust_mod" (line 3)`},
		{"entry merge key list",
			"registry:\n  x-anchors:\n    a: &a {ref_note: x}\n    b: &b {base: &n {trust-layer-root: x}}\n    c: &c {<<: *n}\n" + layerHead + "      <<: [*a, *c]\n",
			`the key "trust-layer-root"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadTrustConfig(t, tc.body, "").validate()
			if err == nil || !strings.HasPrefix(err.Error(), "config.invalid_layer_trust: declared layer finance: ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate = %v, want config.invalid_layer_trust with %q", err, tc.want)
			}
		})
	}
	// An alias to a well-formed signer is admitted and seeded.
	body := "registry:\n  x-anchors:\n    signer: &s {issuer: i, identity: alice@acme.com}\n" + layerHead +
		"      trust_mode: warn\n      trust_requirements:\n        - signers: [*s]\n"
	if err := loadTrustConfig(t, body, "").validate(); err != nil {
		t.Errorf("a well-formed aliased signer: %v", err)
	}
}

// Spec: §7.3.1 (Layer trust policy) — the endpoint's declared-layer
// predicate names the declared IDs of the bound tenant alone.
func TestIsDeclaredLayer(t *testing.T) {
	c := &Config{declaredLayers: []yamlLayerEntry{{ID: "finance"}}}
	is := c.isDeclaredLayer("default")
	if !is("default", "finance") || is("default", "other") || is("globex", "finance") {
		t.Error("isDeclaredLayer")
	}
}

// Spec: §7.3.1 (Layer trust policy) — entryKeys follows merge keys to a
// bounded depth and refuses a deeper chain, whose merged keys the decode
// would still apply.
func TestEntryKeys_BoundsMergeDepth(t *testing.T) {
	inner := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "trust_mod"}, {Kind: yaml.ScalarNode, Value: "x"}}}
	chain := func(n int) *yaml.Node {
		m := inner
		for range n {
			m = &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "<<"}, m}}
		}
		return m
	}
	if keys, err := entryKeys(chain(maxMergeDepth+1), 0); err == nil || len(keys) != 0 {
		t.Errorf("entryKeys past the depth bound = %d keys, err %v; want a refusal", len(keys), err)
	}
	if keys, err := entryKeys(chain(maxMergeDepth), 0); err != nil || len(keys) != 1 || keys[0].Value != "trust_mod" {
		t.Errorf("entryKeys at the depth bound = %v, err %v", keys, err)
	}
}
