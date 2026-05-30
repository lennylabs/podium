package serverboot

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
)

// Spec: §4.6 / §13.10 (F-4.6.9) — defaultBootstrapVisibility returns public
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

// Spec: §4.6 (F-4.6.9) — a PODIUM_LAYER_PATH bootstrap layer in a deployment
// with an identity provider and the default private visibility is NOT marked
// public, so it is not exposed to every caller. The pre-fix bootstrap
// hardcoded Public:true regardless of the deployment auth mode.
func TestBootstrapLayerPath_IdPDeploymentNotPublic(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "x/ARTIFACT.md", Content: artifactBody})

	cfg := &Config{identityProvider: "oidc", defaultLayerVisibility: "private"}
	layers, err := bootstrapLayerPath(st, "default", root, defaultBootstrapVisibility(cfg), 0)
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

// Spec: §13.10 (F-4.6.9) — the no-identity-provider standalone keeps the
// public default so the visibility evaluator (which short-circuits to true
// when there is no identity) and the stored config agree.
func TestBootstrapLayerPath_StandaloneStaysPublic(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: "x/ARTIFACT.md", Content: artifactBody})

	cfg := &Config{identityProvider: "", defaultLayerVisibility: "private"}
	layers, err := bootstrapLayerPath(st, "default", root, defaultBootstrapVisibility(cfg), 0)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	if len(layers) != 1 || !layers[0].Visibility.Public {
		t.Errorf("standalone (no IdP) bootstrap layer should be public: %+v", layers)
	}
}
