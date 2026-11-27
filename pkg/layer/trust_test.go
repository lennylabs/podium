package layer_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

const (
	ghIssuer    = "https://token.actions.githubusercontent.com"
	ownWorkflow = "https://github.com/acme/finance/.github/workflows/podium-attest.yml@refs/heads/main"
)

// signer returns a URI-identity signer of the layer's own workflow with no
// binding.
func signer() store.TrustSigner {
	return store.TrustSigner{Issuer: ghIssuer, Identity: ownWorkflow}
}

func policy(mode string, signers ...store.TrustSigner) store.LayerConfig {
	cfg := store.LayerConfig{ID: "finance", SourceType: "git", Repo: "https://github.com/acme/finance", TrustMode: mode}
	if len(signers) > 0 {
		cfg.TrustRequirements = []store.TrustRequirement{{Signers: signers}}
	}
	return cfg
}

// Spec: §4.7.11 (Derived source binding) — a layer URL of the form
// https://<host>/<owner>/<repo>, with an optional .git or /, derives the
// binding; user information, a port, a query, a fragment, an SSH or scp-like
// address, and another path depth derive nothing.
func TestDeriveSourceBinding_URLForms(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		repo, want string
	}{
		{"https://github.com/acme/finance", "https://github.com/acme/finance"},
		{"https://github.com/acme/finance.git", "https://github.com/acme/finance"},
		{"https://github.com/acme/finance/", "https://github.com/acme/finance"},
		{"https://alice@github.com/acme/finance", ""},
		{"https://github.com:443/acme/finance", ""},
		{"https://github.com/acme/finance?x=1", ""},
		{"https://github.com/acme/finance?", ""},
		{"https://github.com/acme/finance#main", ""},
		{"https://github.com/acme/finance#", ""},
		{"ssh://git@github.com/acme/finance.git", ""},
		{"git@github.com:acme/finance.git", ""},
		{"http://github.com/acme/finance", ""},
		{"https://github.com/acme", ""},
		{"https://github.com/acme/finance/sub", ""},
		{"https://github.com//finance", ""},
		{"https://github.com/acme/fin%61nce", ""},
		{"https:///acme/finance", ""},
		{"https://github.com/acme/finance.git/", ""},
		{"https://github.com:/acme/finance", ""},
		{"https://github.com/acme/fin%2Dance", ""},
		{"https://github.com./acme/finance", ""},
	} {
		reqs := []store.TrustRequirement{{Signers: []store.TrustSigner{signer()}}}
		got := layer.DeriveSourceBinding("git", tc.repo, reqs)[0].Signers[0]
		if got.SourceRepository != tc.want {
			t.Errorf("%s: source_repository = %q, want %q", tc.repo, got.SourceRepository, tc.want)
		}
		wantRef := ""
		if tc.want != "" {
			wantRef = "refs/heads/main"
		}
		if got.SourceRepositoryRef != wantRef {
			t.Errorf("%s: source_repository_ref = %q, want %q", tc.repo, got.SourceRepositoryRef, wantRef)
		}
		if reqs[0].Signers[0].SourceRepository != "" {
			t.Fatalf("%s: the input requirement was modified", tc.repo)
		}
	}
}

// Spec: §4.7.11 (Derived source binding) — the derivation applies only to a
// git layer's signer with a URI identity under the repository's workflows
// path that names no source binding and no any_source; it keeps a named ref
// binding, and an identity with no ref derives no ref.
func TestDeriveSourceBinding_SignerForms(t *testing.T) {
	t.Parallel()
	const repo = "https://github.com/acme/finance"
	bound := signer()
	bound.SourceRepositoryOwnerID = "42"
	anySource := signer()
	anySource.AnySource = true
	namedRef := signer()
	namedRef.SourceRepositoryRef = "refs/heads/release"
	anyRef := signer()
	anyRef.AnyRef = true
	otherRepo := store.TrustSigner{Issuer: ghIssuer, Identity: "https://github.com/acme/.github/.github/workflows/a.yml@refs/heads/main"}
	prefixRepo := store.TrustSigner{Issuer: ghIssuer, Identity: "https://github.com/acme/finance-tools/.github/workflows/a.yml@refs/heads/main"}
	noRef := store.TrustSigner{Issuer: ghIssuer, Identity: "https://github.com/acme/finance/.github/workflows/a.yml"}
	emptyRef := store.TrustSigner{Issuer: ghIssuer, Identity: "https://github.com/acme/finance/.github/workflows/a.yml@"}
	email := store.TrustSigner{Issuer: ghIssuer, Identity: "alice@acme.com"}
	reqs := []store.TrustRequirement{{PredicateType: "p", Signers: []store.TrustSigner{bound, anySource, namedRef, anyRef, otherRepo, prefixRepo, noRef, emptyRef, email}}}
	got := layer.DeriveSourceBinding("git", repo, reqs)[0]
	type want struct{ src, ref string }
	wants := []want{{"", ""}, {"", ""}, {repo, "refs/heads/release"}, {repo, ""}, {"", ""}, {"", ""}, {repo, ""}, {repo, ""}, {"", ""}}
	for i, w := range wants {
		if s := got.Signers[i]; s.SourceRepository != w.src || s.SourceRepositoryRef != w.ref {
			t.Errorf("signer %d: (%q, %q), want (%q, %q)", i, s.SourceRepository, s.SourceRepositoryRef, w.src, w.ref)
		}
	}
	if got.PredicateType != "p" {
		t.Errorf("predicate_type = %q", got.PredicateType)
	}
	if local := layer.DeriveSourceBinding("local", repo, reqs)[0].Signers[6]; local.SourceRepository != "" {
		t.Errorf("a local layer derived %q", local.SourceRepository)
	}
	if layer.DeriveSourceBinding("git", repo, nil) != nil {
		t.Errorf("no requirement derived a requirement")
	}
}

// Spec: §4.7.11 (Policy rules) — each rule refuses with an error wrapping
// ErrTrustPolicy that names the member, and a policy that meets every rule
// passes.
func TestCheckTrustPolicy_Rules(t *testing.T) {
	t.Parallel()
	derived := func(cfg store.LayerConfig) store.LayerConfig {
		cfg.TrustRequirements = layer.DeriveSourceBinding(cfg.SourceType, cfg.Repo, cfg.TrustRequirements)
		return cfg
	}
	known := layer.TrustInstances(func(n string) bool { return n == "github" })
	ok := func(mut func(*store.LayerConfig)) store.LayerConfig {
		cfg := derived(policy("require", signer()))
		mut(&cfg)
		return cfg
	}
	twoReqs := ok(func(c *store.LayerConfig) { c.TrustRequirements = append(c.TrustRequirements, c.TrustRequirements[0]) })
	for _, tc := range []struct {
		name string
		cfg  store.LayerConfig
		want string
	}{
		{"mode", policy("enforce"), `trust_mode "enforce"`},
		{"several", twoReqs, "several requirements are not available yet"},
		{"require without requirement", policy("require"), "trust_mode require needs a requirement"},
		{"warn without requirement", policy("warn"), "trust_mode warn needs a requirement"},
		{"no signer", ok(func(c *store.LayerConfig) { c.TrustRequirements[0].Signers = nil }), "trust_requirements[0] names no signer"},
		{"no issuer", ok(func(c *store.LayerConfig) { c.TrustRequirements[0].Signers[0].Issuer = "" }), "signers[0]: the signer names no issuer"},
		{"no identity", ok(func(c *store.LayerConfig) { c.TrustRequirements[0].Signers[0].Identity = "" }), "names no identity"},
		{"URI without binding", policy("require", store.TrustSigner{Issuer: ghIssuer, Identity: "https://github.com/acme/.github/.github/workflows/a.yml@refs/heads/main"}), "names no source binding"},
		{"binding without ref", policy("require", store.TrustSigner{Issuer: ghIssuer, Identity: ownWorkflow, SourceRepositoryID: "1"}), "names no source_repository_ref"},
		{"any_source with member", ok(func(c *store.LayerConfig) { c.TrustRequirements[0].Signers[0].AnySource = true }), "sets any_source and names a source binding member"},
		{"any_ref with ref", ok(func(c *store.LayerConfig) { c.TrustRequirements[0].Signers[0].AnyRef = true }), "sets any_ref and names source_repository_ref"},
		{"unknown instance", ok(func(c *store.LayerConfig) { c.TrustRequirements[0].Signers[0].Instance = "acme" }), `instance "acme"`},
		{"root on git", ok(func(c *store.LayerConfig) { c.TrustLayerRoot = "skills" }), "trust_layer_root applies to a local layer"},
	} {
		err := layer.CheckTrustPolicy(tc.cfg, known, "")
		if !errors.Is(err, layer.ErrTrustPolicy) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want ErrTrustPolicy with %q", tc.name, err, tc.want)
		}
	}
	for _, cfg := range []store.LayerConfig{
		policy(""), policy("none"),
		derived(policy("require", signer())),
		derived(policy("warn", store.TrustSigner{Issuer: ghIssuer, Identity: "alice@acme.com", Instance: "github"})),
		policy("none", store.TrustSigner{Issuer: ghIssuer, Identity: "https://x.example/w", AnySource: true, Instance: "public-good"}),
		policy("require", store.TrustSigner{Issuer: ghIssuer, Identity: "https://x.example/w", SourceRepositoryOwnerID: "7", AnyRef: true}),
	} {
		if err := layer.CheckTrustPolicy(cfg, known, ""); err != nil {
			t.Errorf("CheckTrustPolicy(%+v) = %v, want nil", cfg.TrustRequirements, err)
		}
	}
	if err := layer.CheckTrustPolicy(ok(func(c *store.LayerConfig) { c.TrustRequirements[0].Signers[0].Instance = "github" }), nil, ""); err == nil {
		t.Errorf("a nil instance set admitted github")
	}
}

// Spec: §4.7.11 (Policy rules) — trust_layer_root on a local layer is a root
// without a leading or trailing /, without an empty, ".", or ".." segment,
// and without a character the subject name rule excludes.
func TestCheckTrustPolicy_LayerRoot(t *testing.T) {
	t.Parallel()
	for root, ok := range map[string]bool{
		"":               true,
		"skills":         true,
		"skills/finance": true,
		"/skills":        false,
		"skills/":        false,
		"/":              false,
		"a//b":           false,
		"a/./b":          false,
		"a/../b":         false,
		"..":             false,
		"a b":            false,
		`a\b`:            false,
	} {
		cfg := store.LayerConfig{ID: "L", SourceType: "local", TrustLayerRoot: root}
		if err := layer.CheckTrustPolicy(cfg, nil, ""); (err == nil) != ok {
			t.Errorf("trust_layer_root %q: err = %v, want ok=%t", root, err, ok)
		}
	}
}

// Spec: §4.7.11 (Layer trust policy) — the empty mode resolves to the
// default, then to none.
func TestResolveTrustMode(t *testing.T) {
	t.Parallel()
	for _, tc := range [][3]string{{"warn", "require", "warn"}, {"", "require", "require"}, {"", "", "none"}} {
		if got := layer.ResolveTrustMode(tc[0], tc[1]); got != tc[2] {
			t.Errorf("ResolveTrustMode(%q, %q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
	}
}

// Spec: §7.3.1 (The layer lifecycle event follows what the write changed) —
// two policies are equal when every member is, and a nil requirement list
// equals an empty one.
func TestTrustPolicyEqual(t *testing.T) {
	t.Parallel()
	a := policy("require", signer())
	b := policy("require", signer())
	if !layer.TrustPolicyEqual(a, b) {
		t.Fatal("equal policies compared unequal")
	}
	if !layer.TrustPolicyEqual(store.LayerConfig{}, store.LayerConfig{TrustRequirements: []store.TrustRequirement{}}) {
		t.Error("nil and empty requirement lists compared unequal")
	}
	for _, mut := range []func(*store.LayerConfig){
		func(c *store.LayerConfig) { c.TrustMode = "warn" },
		func(c *store.LayerConfig) { c.TrustLayerRoot = "x" },
		func(c *store.LayerConfig) { c.TrustRequirements = nil },
		func(c *store.LayerConfig) { c.TrustRequirements[0].PredicateType = "p" },
		func(c *store.LayerConfig) { c.TrustRequirements[0].Signers[0].AnyRef = true },
	} {
		c := policy("require", signer())
		mut(&c)
		if layer.TrustPolicyEqual(a, c) {
			t.Errorf("a changed policy compared equal: %+v", c)
		}
	}
}

// Spec: §4.7.11 (Policy rules) — at the endpoints the no-requirement rule
// reads the resolved mode: an empty mode that the default raises to warn or
// require needs a requirement, and the refusal names the default.
func TestCheckTrustPolicy_ResolvedDefault(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode, def string
		reqs      bool
		want      string
	}{
		{"", "require", false, "resolves to require through PODIUM_LAYER_TRUST_DEFAULT"},
		{"", "warn", false, "resolves to warn through PODIUM_LAYER_TRUST_DEFAULT"},
		{"", "none", false, ""},
		{"none", "require", false, ""},
		{"", "require", true, ""},
	} {
		cfg := policy(tc.mode)
		if tc.reqs {
			cfg = policy(tc.mode, store.TrustSigner{Issuer: ghIssuer, Identity: "alice@acme.com"})
		}
		err := layer.CheckTrustPolicy(cfg, nil, tc.def)
		if (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
			t.Errorf("mode %q default %q reqs %t: err = %v, want %q", tc.mode, tc.def, tc.reqs, err, tc.want)
		}
	}
}

// Spec: §4.7.11 (Layer trust policy) — a signer that names public-good and
// one that names no instance compare equal.
func TestTrustPolicyEqual_InstanceNormalized(t *testing.T) {
	t.Parallel()
	a := policy("require", store.TrustSigner{Issuer: ghIssuer, Identity: "alice@acme.com"})
	b := policy("require", store.TrustSigner{Issuer: ghIssuer, Identity: "alice@acme.com", Instance: "public-good"})
	c := policy("require", store.TrustSigner{Issuer: ghIssuer, Identity: "alice@acme.com", Instance: "github"})
	if !layer.TrustPolicyEqual(a, b) || layer.TrustPolicyEqual(a, c) {
		t.Errorf("equal(empty, public-good) = %t, equal(empty, github) = %t", layer.TrustPolicyEqual(a, b), layer.TrustPolicyEqual(a, c))
	}
}

// Spec: §7.3.1 (Layer trust policy) — a name reads as a trust member when,
// folded to lower case with - read as _, it begins with "trust".
func TestIsTrustLikeName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{"trust_mod": true, "trust-mode": true, "Trust_Mode": true, "TRUSTED": true, "ref": false, "entrust": false} {
		if got := layer.IsTrustLikeName(name); got != want {
			t.Errorf("IsTrustLikeName(%q) = %t, want %t", name, got, want)
		}
	}
}
