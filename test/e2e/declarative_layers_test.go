package e2e

// End-to-end coverage for the §4.6 declarative `layers:` registry-config list
// (F-4.6.8). A registry.yaml that declares a local-source admin layer with a
// visibility block boots a standalone server that ingests the layer and
// exposes it through the §7.3.1 layer-management API.

import (
	"os"
	"path/filepath"
	"testing"
)

// Spec: §4.6 (F-4.6.8) — a declared local-source layer in registry.yaml is
// ingested at startup (its artifacts are searchable) and is registered so the
// /v1/layers management surface reports it with the declared source and
// visibility.
func TestDeclarativeLayers_LocalLayerBootsAndServes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	// The declared local layer's artifact tree.
	layerRoot := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: pay vendor invoices\nsensitivity: low\n---\n\nbody\n",
	})

	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"registry:\n" +
		"  layers:\n" +
		"    - id: org-defaults\n" +
		"      source:\n" +
		"        local:\n" +
		"          path: " + layerRoot + "\n" +
		"      visibility:\n" +
		"        public: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	srv := startServerArgs(t,
		[]string{"HOME=" + home, "PODIUM_CONFIG_FILE=" + cfgPath, "PODIUM_INGEST_OFFLINE=true"},
		"serve", "--standalone")

	// The declared layer's artifact is searchable out of the box.
	var search struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	getJSON(t, srv.BaseURL+"/v1/search_artifacts?scope=finance", &search)
	found := false
	for _, r := range search.Results {
		if r.ID == "finance/ap/pay-invoice" {
			found = true
		}
	}
	if !found {
		t.Errorf("declared local layer artifact not searchable: %+v", search.Results)
	}

	// The layer is registered (store.LayerConfig has no JSON tags, so fields
	// serialize under their Go names).
	var layers struct {
		Layers []struct {
			ID         string `json:"ID"`
			SourceType string `json:"SourceType"`
			Public     bool   `json:"Public"`
		} `json:"layers"`
	}
	getJSON(t, srv.BaseURL+"/v1/layers", &layers)
	seen := false
	for _, l := range layers.Layers {
		if l.ID != "org-defaults" {
			continue
		}
		seen = true
		if l.SourceType != "local" {
			t.Errorf("SourceType = %q, want local", l.SourceType)
		}
		if !l.Public {
			t.Errorf("Public = false, want true (declared visibility block)")
		}
	}
	if !seen {
		t.Errorf("declared layer org-defaults missing from /v1/layers: %+v", layers.Layers)
	}
}
