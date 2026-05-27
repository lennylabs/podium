// Filesystem-mode visibility loader.
//
// NewFromFilesystem historically marks every layer as Public. This
// file adds an optional source of per-layer visibility metadata:
// the registry's .podium/layers.yaml file, when present. Layers
// without a visibility block, and layers absent from the YAML,
// fall back to the previous default of Public:true, so existing
// deployments observe no behavior change.

package server

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/layer"
	"gopkg.in/yaml.v3"
)

// layersYAML mirrors the subset of .podium/layers.yaml that
// NewFromFilesystem reads. Other fields (description, owners,
// examples, etc.) are intentionally ignored so the file format
// remains forward-compatible.
type layersYAML struct {
	Layers []layerYAMLEntry `yaml:"layers"`
}

type layerYAMLEntry struct {
	Name       string             `yaml:"name"`
	Visibility *visibilityYAMLDoc `yaml:"visibility"`
}

type visibilityYAMLDoc struct {
	Public       bool     `yaml:"public"`
	Organization bool     `yaml:"organization"`
	Groups       []string `yaml:"groups"`
	Users        []string `yaml:"users"`
}

// loadLayerVisibility reads root/.podium/layers.yaml when present
// and returns a map keyed by layer name to the declared Visibility.
// Layers omitted from the file, or layers present without a
// visibility block, are absent from the map; callers fall back
// to Public:true for those entries.
//
// A missing layers.yaml returns (nil, nil): the caller treats this
// as "no overrides" and keeps the previous default for every layer.
func loadLayerVisibility(root string) (map[string]layer.Visibility, error) {
	yamlPath := filepath.Join(root, ".podium", "layers.yaml")
	body, err := os.ReadFile(yamlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", yamlPath, err)
	}
	var doc layersYAML
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", yamlPath, err)
	}
	out := map[string]layer.Visibility{}
	for _, l := range doc.Layers {
		if l.Visibility == nil {
			continue
		}
		out[l.Name] = layer.Visibility{
			Public:       l.Visibility.Public,
			Organization: l.Visibility.Organization,
			Groups:       append([]string(nil), l.Visibility.Groups...),
			Users:        append([]string(nil), l.Visibility.Users...),
		}
	}
	return out, nil
}
