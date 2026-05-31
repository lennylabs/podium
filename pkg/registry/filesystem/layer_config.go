package filesystem

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// layerConfigFileName is the per-layer config read from a layer directory.
// It is optional; when absent the layer carries no explicit visibility and
// downstream consumers apply their default (public for the §13.10 standalone
// bootstrap). The filesystem-source client (`podium sync`, §13.11.3) ignores
// it because that path short-circuits visibility to true; a server pointed at
// the same directory via `--layer-path` honors it.
const layerConfigFileName = ".layer-config"

// Visibility mirrors the §4.6 layer visibility model for a filesystem-source
// layer. It is the declared form that a server bootstrap maps onto its
// runtime layer.Visibility. The four fields combine as a union (§4.6).
type Visibility struct {
	Public       bool     `yaml:"public,omitempty"`
	Organization bool     `yaml:"organization,omitempty"`
	Groups       []string `yaml:"groups,omitempty"`
	Users        []string `yaml:"users,omitempty"`
}

// layerConfig is the parsed .layer-config file.
type layerConfig struct {
	Visibility Visibility `yaml:"visibility"`
}

// readLayerVisibility reads <dir>/.layer-config and returns the declared
// visibility. The second return reports whether the file was present. A
// missing file is not an error: the layer simply has no explicit visibility.
// A malformed file maps to ErrConfigInvalid (§6.10 config.invalid).
func readLayerVisibility(dir string) (Visibility, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, layerConfigFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return Visibility{}, false, nil
		}
		return Visibility{}, false, err
	}
	var cfg layerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Visibility{}, false, fmt.Errorf("%w: %s: %v", ErrConfigInvalid, layerConfigFileName, err)
	}
	return cfg.Visibility, true, nil
}
