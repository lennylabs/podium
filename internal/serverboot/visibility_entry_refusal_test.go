package serverboot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.6 — both startup paths that declare a layer's visibility refuse a
// groups or users entry that is empty or holds a control character with
// config.invalid, naming the layer and the entry's position, and store no
// layer config for the refused layer. The compiled-binary cases live in
// test/e2e/layer_visibility_entries_test.go; these run in process so the
// refusal branches are measured by the in-process coverage profile.
func TestBootstrap_RefusesVisibilityEntries(t *testing.T) {
	t.Run("declared layer", func(t *testing.T) {
		st := newMemoryStoreWithTenant(t)
		cfg := &Config{
			identityProvider: "oidc-jwt",
			declaredLayers: []yamlLayerEntry{{
				ID:         "team-finance",
				Source:     yamlLayerSource{Local: &yamlLocalSource{Path: declaredLocalTree(t, "policy")}},
				Visibility: yamlLayerVisibility{Groups: []string{"acme-finance"}, Users: []string{"alice@acme.com\nbob@acme.com"}},
			}},
		}
		_, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{})
		assertBootstrapEntryRefusal(t, st, "team-finance", err,
			"config.invalid: declared layer team-finance: visibility.users[0] contains a control character")
	})

	t.Run(".layer-config", func(t *testing.T) {
		st := newMemoryStoreWithTenant(t)
		root := t.TempDir()
		testharness.WriteTree(t, root,
			testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
			testharness.WriteTreeOption{Path: "fin/b/ARTIFACT.md", Content: artifactBody},
			testharness.WriteTreeOption{Path: "fin/.layer-config", Content: "visibility:\n  groups: [acme-finance, \"\"]\n"},
		)
		_, err := bootstrapLayerPath(st, "default", root, layer.Visibility{Public: true}, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil)
		assertBootstrapEntryRefusal(t, st, "fin", err,
			"config.invalid: layer fin: .layer-config visibility.groups[1] is empty")
	})
}

// assertBootstrapEntryRefusal asserts that err starts with want and that no
// layer config was stored under id.
func assertBootstrapEntryRefusal(t *testing.T, st store.Store, id string, err error, want string) {
	t.Helper()
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want a refusal starting %q", err, want)
	}
	if _, getErr := st.GetLayerConfig(context.Background(), "default", id); !errors.Is(getErr, store.ErrNotFound) {
		t.Fatalf("GetLayerConfig(%s) after the refusal: err = %v, want ErrNotFound", id, getErr)
	}
}
