package serverboot

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
)

// Spec: §4.6 / §13.10 — defaultBootstrapVisibility returns public
// only for a no-identity-provider standalone (or public mode); once an
// identity provider is configured it honors PODIUM_DEFAULT_LAYER_VISIBILITY so
// bootstrap layers are not exposed to every caller.
func TestDefaultBootstrapVisibility_ByMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  *Config
		want layer.Visibility
	}{
		{"no idp standalone", &Config{identityProvider: ""}, layer.Visibility{Public: true}},
		{"public mode overrides idp", &Config{publicMode: true, identityProvider: "oidc"}, layer.Visibility{Public: true}},
		{"idp + private default", &Config{identityProvider: "oidc", defaultLayerVisibility: "private"}, layer.Visibility{}},
		{"idp + unset default", &Config{identityProvider: "oidc", defaultLayerVisibility: ""}, layer.Visibility{}},
		{"idp + organization default", &Config{identityProvider: "oidc", defaultLayerVisibility: "organization"}, layer.Visibility{Organization: true}},
		{"idp + public default", &Config{identityProvider: "oidc", defaultLayerVisibility: "public"}, layer.Visibility{Public: true}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := defaultBootstrapVisibility(c.cfg)
			if got.Public != c.want.Public || got.Organization != c.want.Organization {
				t.Errorf("defaultBootstrapVisibility = %+v, want %+v", got, c.want)
			}
		})
	}
}

// Spec: §4.6 — a PODIUM_LAYER_PATH bootstrap layer in a deployment
// with an identity provider and the default private visibility is NOT marked
// public, so it is not exposed to every caller. The pre-fix bootstrap
// hardcoded Public:true regardless of the deployment auth mode.
func TestBootstrapLayerPath_IdPDeploymentNotPublic(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "x/ARTIFACT.md", Content: artifactBody})

	cfg := &Config{identityProvider: "oidc", defaultLayerVisibility: "private"}
	layers, err := bootstrapLayerPath(st, "default", root, defaultBootstrapVisibility(cfg), 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %v, want 1", layers)
	}
	if layers[0].Visibility.Public {
		t.Errorf("in-memory layer marked Public; an IdP deployment must not expose a bootstrap layer to all callers")
	}
	cfgs, err := st.ListLayerConfigs(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	if len(cfgs) != 1 {
		t.Fatalf("LayerConfigs = %v, want 1", cfgs)
	}
	if cfgs[0].Public {
		t.Errorf("persisted LayerConfig.Public = true; want false for IdP + private default")
	}
}

// Spec: §13.10 / §13.12 — a zero-config standalone (no identity provider, no
// PODIUM_DEFAULT_LAYER_VISIBILITY) keeps the public default so the visibility
// evaluator (which short-circuits to true when there is no identity) and the
// stored config agree. LoadConfig resolves the unset default to "public".
func TestBootstrapLayerPath_ZeroConfigStandaloneStaysPublic(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "x/ARTIFACT.md", Content: artifactBody})

	// Mirror LoadConfig's resolution: a no-IdP standalone with no override
	// resolves the default visibility to "public".
	cfg := &Config{identityProvider: "", defaultLayerVisibility: "public"}
	layers, err := bootstrapLayerPath(st, "default", root, defaultBootstrapVisibility(cfg), 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	if len(layers) != 1 || !layers[0].Visibility.Public {
		t.Errorf("zero-config standalone bootstrap layer should be public: %+v", layers)
	}
}

// Spec: §13.12 — PODIUM_DEFAULT_LAYER_VISIBILITY is the fallback
// "applied when a layer is registered without an explicit setting", and a
// bootstrap layer is exactly that. An operator who sets it to
// private|organization for a multi-user standalone must get the restricted
// visibility on the bootstrap layers, not an unconditional public layer that
// leaks content the operator intended to restrict.
func TestBootstrapLayerPath_StandaloneHonorsVisibilityOverride(t *testing.T) {
	cases := []struct {
		name             string
		defaultVis       string
		wantPublic       bool
		wantOrganization bool
	}{
		{"private override", "private", false, false},
		{"organization override", "organization", false, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			st := newMemoryStoreWithTenant(t)
			root := t.TempDir()
			testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "x/ARTIFACT.md", Content: artifactBody})

			cfg := &Config{identityProvider: "", defaultLayerVisibility: c.defaultVis}
			layers, err := bootstrapLayerPath(st, "default", root, defaultBootstrapVisibility(cfg), 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil)
			if err != nil {
				t.Fatalf("bootstrapLayerPath: %v", err)
			}
			if len(layers) != 1 {
				t.Fatalf("layers = %v, want 1", layers)
			}
			if layers[0].Visibility.Public != c.wantPublic || layers[0].Visibility.Organization != c.wantOrganization {
				t.Errorf("in-memory visibility = %+v, want public=%v organization=%v",
					layers[0].Visibility, c.wantPublic, c.wantOrganization)
			}
			cfgs, err := st.ListLayerConfigs(context.Background(), "default")
			if err != nil {
				t.Fatalf("ListLayerConfigs: %v", err)
			}
			if len(cfgs) != 1 {
				t.Fatalf("LayerConfigs = %v, want 1", cfgs)
			}
			if cfgs[0].Public != c.wantPublic || cfgs[0].Organization != c.wantOrganization {
				t.Errorf("persisted LayerConfig public=%v organization=%v, want public=%v organization=%v",
					cfgs[0].Public, cfgs[0].Organization, c.wantPublic, c.wantOrganization)
			}
		})
	}
}
