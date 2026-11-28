package serverboot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/audit"
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
	several := `{"trust_requirements":[{"signers":[{"issuer":"i","identity":"alice@acme.com"}],"covers_layer":true},` +
		`{"signers":[{"issuer":"i","identity":"scanner@acme.com"}],"predicate_type":"https://acme.com/scan/v1","optional":true}]}`
	if b := resolveBootstrapRequirements(write(several), known); b.err != nil || len(b.reqs) != 2 || !b.reqs[0].CoversLayer || !b.reqs[1].Optional {
		t.Fatalf("several: %+v", b)
	}
	for name, path := range map[string]string{
		"unmarked":     write(strings.Replace(several, `,"covers_layer":true`, "", 1)),
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

// errListLinks fails the link read of the re-evaluation.
type errListLinks struct{ store.Store }

func (errListLinks) ListAttestationLinks(context.Context, string, store.LinkFilter) ([]store.AttestationLink, error) {
	return nil, errors.New("store down")
}

// Spec: §4.7.11 (Re-evaluation), §7.3.1 (Layer trust policy) — the hook a
// policy change runs re-evaluates the layer's links, records the downgrade
// on the audit stream with the caller, and logs a failure without failing.
func TestTrustPolicyChangeHook(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	if err := st.PutManifest(ctx, store.ManifestRecord{TenantID: "t", ArtifactID: "a/x", Version: "1.0.0", ContentHash: "sha256:x",
		Type: "agent", Layer: "L", AttestationOutcome: store.OutcomeVerified}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WriteAttestations(ctx, "t", "", "", store.AttestationWrites{Versions: []store.VersionAttestations{{ArtifactID: "a/x", Version: "1.0.0",
		Links: []store.AttestationLink{{PredicateType: "p", Instance: "github", Digest: "sha256:d", Verdict: store.VerdictVerified, VerifiedUnder: "k", LinkedAt: at}}}}}); err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{}
	lc := store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", TrustMode: "none"}
	trustPolicyChangeHook(st, &ingest.AttestationConfig{}, sink, nil, nil)(ctx, lc, "alice")
	events := sink.events
	if len(events) != 1 || events[0].Type != audit.EventLayerAttestationLinksDowngraded || events[0].Caller != "alice" || events[0].Context["cause"] != "policy_change" {
		t.Fatalf("events %+v", events)
	}
	trustPolicyChangeHook(errListLinks{st}, &ingest.AttestationConfig{}, nil, nil, nil)(ctx, lc, "alice")
}

// errListTenants fails the tenant list.
type errListTenants struct{ store.Store }

func (errListTenants) ListTenants(context.Context) ([]store.Tenant, error) {
	return nil, errors.New("store down")
}

// Spec: §4.7.11 (Re-evaluation) — the hook requests an ingest cycle of the
// changed layer through the queue, which keeps at most one request of a
// layer waiting behind the running one, logs a failure only while the run
// context lives, and lets a shutdown wait for the cycles in flight.
func TestReingestQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	var mu sync.Mutex
	var ran []string
	q := newReingestQueue(ctx, func(_ context.Context, lc store.LayerConfig) error {
		mu.Lock()
		ran = append(ran, lc.ID+"@"+lc.TrustMode)
		first := len(ran) == 1
		mu.Unlock()
		if first {
			<-release
		}
		return errors.New("source down")
	})
	st := store.NewMemory()
	trustPolicyChangeHook(st, &ingest.AttestationConfig{}, nil, nil, q.request)(ctx, store.LayerConfig{TenantID: "t", ID: "L", TrustMode: "none"}, "alice")
	for _, mode := range []string{"warn", "require", "none"} {
		q.request(store.LayerConfig{TenantID: "t", ID: "L", TrustMode: mode})
	}
	close(release)
	if !q.wait(5 * time.Second) {
		t.Fatal("the queue did not drain")
	}
	mu.Lock()
	got := strings.Join(ran, ",")
	mu.Unlock()
	if got != "L@none,L@none" {
		t.Errorf("cycles %s, want the first and the last request", got)
	}
	// A cycle cancelled by the shutdown logs nothing, and wait gives up at
	// its deadline.
	block := make(chan struct{})
	q2 := newReingestQueue(ctx, func(ctx context.Context, _ store.LayerConfig) error {
		<-block
		return ctx.Err()
	})
	q2.request(store.LayerConfig{TenantID: "t", ID: "M"})
	if q2.wait(10 * time.Millisecond) {
		t.Error("wait returned before the cycle ended")
	}
	cancel()
	close(block)
	if !q2.wait(5 * time.Second) {
		t.Error("the cancelled queue did not drain")
	}
}

// Spec: §4.7.11 (Re-evaluation), §13.10 — the start re-evaluates the stored
// layers of each tenant, and a start that cannot read the tenants of a
// multi-tenant registry, or cannot complete the re-evaluation, fails.
func TestReevaluateAtStart(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/x", TrustMode: "none"}); err != nil {
		t.Fatal(err)
	}
	att := &ingest.AttestationConfig{}
	if err := reevaluateAtStart(ctx, st, false, "t", att, &recordingSink{}, nil); err != nil {
		t.Errorf("single tenant: %v", err)
	}
	if err := reevaluateAtStart(ctx, st, true, "", att, nil, nil); err != nil {
		t.Errorf("multi-tenant: %v", err)
	}
	if err := reevaluateAtStart(ctx, errListTenants{st}, true, "", att, nil, nil); err == nil || !strings.Contains(err.Error(), "read the tenants") {
		t.Errorf("unreadable tenants: %v", err)
	}
	if err := reevaluateAtStart(ctx, errListLinks{st}, false, "t", att, nil, nil); err == nil || !strings.Contains(err.Error(), "tenant t") {
		t.Errorf("unreadable links: %v", err)
	}
}
