package serverboot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// emailRequirement is a requirement file body with one email signer of the
// github instance.
const emailRequirement = `{"trust_requirements":[{"signers":[{"issuer":"https://accounts.acme.com","identity":"alice@acme.com","instance":"github"}]}]`

// Spec: §4.7.11 (Trust of bootstrap layers), §13.12 — the requirements file
// of the bootstrap layers: an unset path reads nothing, a valid file yields
// its requirements and roots, and a file that cannot be read, that is not
// JSON in the form, whose requirement breaks a rule, or whose layer_roots
// holds an invalid root refuses with config.invalid_layer_trust.
func TestResolveBootstrapRequirements(t *testing.T) {
	t.Parallel()
	known := func(name string) bool { return name == "github" }
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "requirements.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if b := resolveBootstrapRequirements("  ", known); b.err != nil || b.path != "" {
		t.Fatalf("unset: %+v", b)
	}
	b := resolveBootstrapRequirements(write(emailRequirement+`,"layer_roots":{"one":"skills/team"}}`), known)
	if b.err != nil || len(b.reqs) != 1 || b.roots["one"] != "skills/team" {
		t.Fatalf("valid: %+v", b)
	}
	for name, path := range map[string]string{
		"missing":      filepath.Join(t.TempDir(), "none.json"),
		"not json":     write("x"),
		"no signer":    write(`{"trust_requirements":[{"signers":[]}]}`),
		"bad root":     write(emailRequirement + `,"layer_roots":{"one":"/abs"}}`),
		"root type":    write(emailRequirement + `,"layer_roots":{"one":1}}`),
		"unknown inst": write(strings.Replace(emailRequirement, "github", "staging", 1) + `}`),
	} {
		if b := resolveBootstrapRequirements(path, known); b.err == nil || !strings.HasPrefix(b.err.Error(), "config.invalid_layer_trust: "+layerTrustRequirementsFileEnv) {
			t.Errorf("%s: err %v", name, b.err)
		}
	}
}

// Spec: §4.7.11 (Trust of bootstrap layers) — with the file, a bootstrap
// layer carries its requirements and the root layer_roots names; without it,
// the layer keeps its stored policy, and a resolved mode above none with no
// requirement refuses the start.
func TestBootstrapTrust_Apply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMemoryStoreWithTenant(t)
	reqs := []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: "i", Identity: "alice@acme.com"}}}}
	cfg := store.LayerConfig{TenantID: "default", ID: "one", SourceType: "local"}
	got, err := bootstrapTrust{file: bootstrapRequirements{path: "f", reqs: reqs, roots: map[string]string{"one": "skills"}}}.apply(ctx, st, cfg)
	if err != nil || len(got.TrustRequirements) != 1 || got.TrustLayerRoot != "skills" {
		t.Fatalf("file: %+v %v", got, err)
	}
	if _, err := (bootstrapTrust{defaultMode: "warn"}).apply(ctx, st, cfg); err == nil || !strings.Contains(err.Error(), layerTrustRequirementsFileEnv) {
		t.Fatalf("no file: %v", err)
	}
	stored := cfg
	stored.TrustMode, stored.TrustRequirements = "require", reqs
	if err := st.PutLayerConfig(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if got, err := (bootstrapTrust{defaultMode: "warn"}).apply(ctx, st, cfg); err != nil || got.TrustMode != "require" {
		t.Fatalf("stored policy: %+v %v", got, err)
	}
}

// Spec: §4.7.11 (Startup ingest), §13.10 — an attestation refusal of a
// startup ingest does not abort the start: the bootstrap layer is listed,
// its record holds the refusal, and no artifact is stored; any other error
// is no such refusal.
func TestBootstrapLayerPath_AttestationRefusalContinues(t *testing.T) {
	ctx := context.Background()
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\nlayer_order: [one]\n"},
		testharness.WriteTreeOption{Path: "one/a/ARTIFACT.md", Content: artifactBody},
	)
	att := &ingest.AttestationConfig{Instances: map[string]sign.AttestationInstance{"github": {Name: "github"}}, DefaultMode: "require"}
	reqs := []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: "i", Identity: "alice@acme.com", Instance: "github"}}}}
	layers, err := bootstrapLayerPath(st, "default", root, layer.Visibility{Public: true}, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil,
		bootstrapTrust{file: bootstrapRequirements{path: "f", reqs: reqs}, defaultMode: "require", att: att})
	if err != nil || len(layers) != 1 {
		t.Fatalf("bootstrapLayerPath: %v, layers %v", err, layers)
	}
	lc, err := st.GetLayerConfig(ctx, "default", "one")
	if err != nil {
		t.Fatal(err)
	}
	manifests, _ := st.ListManifests(ctx, "default")
	if lc.Status != ingest.CodeAttestationMissing || len(manifests) != 0 {
		t.Fatalf("record %+v, %d manifests", lc.AttestationRecord, len(manifests))
	}
	if startupRefusal("one", errors.New("disk full")) {
		t.Error("a store error read as an attestation refusal")
	}
}

// Spec: §4.7.11 (Trusted root refresh) — the public-good refresh starts once,
// at a start whose stored layers verify on that instance or at the first
// cycle that does, and the stop ends it; a policy that names another
// instance, or a layer under none, starts nothing.
func TestPublicGoodRefresh_StartsOnce(t *testing.T) {
	t.Parallel()
	var starts, stops atomic.Int32
	p := &publicGoodRefresh{ctx: context.Background(), start: func(context.Context) func() {
		starts.Add(1)
		return func() { stops.Add(1) }
	}}
	p.stop()
	att := &ingest.AttestationConfig{DefaultMode: "none", OnPublicGood: p.trigger}
	st := newMemoryStoreWithTenant(t)
	ctx := context.Background()
	put := func(id, mode, instance string) {
		lc := store.LayerConfig{TenantID: "default", ID: id, SourceType: "local", TrustMode: mode,
			TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Instance: instance}}}}}
		if err := st.PutLayerConfig(ctx, lc); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "require", "github")
	put("b", "", "")
	startRefreshForStoredPolicies(ctx, st, []string{"default", "absent"}, att)
	if starts.Load() != 0 {
		t.Fatalf("started for no public-good policy")
	}
	put("c", "warn", sign.PublicGoodInstance)
	startRefreshForStoredPolicies(ctx, st, []string{"default"}, att)
	p.trigger()
	p.stop()
	if starts.Load() != 1 || stops.Load() != 1 {
		t.Fatalf("starts %d, stops %d", starts.Load(), stops.Load())
	}
	c := &Config{}
	c.layerTrust.mode = "warn"
	c.attestation.limits.MaxBundles = 3
	built, stop := c.ingestAttestation(ctx)
	defer stop()
	if built.DefaultMode != "warn" || built.MaxBundles != 3 || built.OnPublicGood == nil {
		t.Fatalf("config %+v", built)
	}
}
