package serverboot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 (Removed artifacts) — the startup ingest of a `--layer-path`
// layer and of a declared `local` layer claims the stored layer as every
// cycle does, so a cycle of the same layer that claimed it earlier, on
// another replica, is superseded and removes nothing. The `--layer-path`
// ingest stamps last_ingested_at under its claim.
func TestBootstrapIngests_ClaimTheLayer(t *testing.T) {
	ctx := context.Background()
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\nlayer_order: [one]\n"},
		testharness.WriteTreeOption{Path: "one/a/ARTIFACT.md", Content: artifactBody},
	)
	declaredPath := declaredLocalTree(t, "policy")
	for _, id := range []string{"one", "org-defaults"} {
		if err := st.PutLayerConfig(ctx, store.LayerConfig{TenantID: "default", ID: id, SourceType: "local"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.ClaimLayerIngest(ctx, "default", id, "earlier-"+id); err != nil {
			t.Fatal(err)
		}
	}
	before := time.Now().UTC()
	if _, err := bootstrapLayerPath(st, "default", root, layer.Visibility{Public: true}, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil, bootstrapTrust{}); err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	cfg := &Config{declaredLayers: []yamlLayerEntry{{
		ID:     "org-defaults",
		Source: yamlLayerSource{Local: &yamlLocalSource{Path: declaredPath}},
	}}}
	if _, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{}); err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	for _, id := range []string{"one", "org-defaults"} {
		if err := st.SoftDeleteArtifacts(ctx, "default", id, nil, time.Now(), "earlier-"+id); !errors.Is(err, store.ErrIngestSuperseded) {
			t.Errorf("%s: the earlier claim checks %v, want ErrIngestSuperseded after the startup ingest", id, err)
		}
	}
	lc, err := st.GetLayerConfig(ctx, "default", "one")
	if err != nil {
		t.Fatal(err)
	}
	if lc.LastIngestedAt == nil || lc.LastIngestedAt.Before(before) {
		t.Errorf("last_ingested_at = %v, want stamped by the startup ingest", lc.LastIngestedAt)
	}
}
