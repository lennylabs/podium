package filesystem

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/layer"
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
	vis, err := ParseLayerVisibility(data)
	if err != nil {
		return Visibility{}, false, err
	}
	return vis, true, nil
}

// ParseLayerVisibility parses the bytes of a .layer-config file and returns
// the visibility it declares. A file that does not parse maps to
// ErrConfigInvalid (§6.10 config.invalid). It applies no entry rule, which
// each caller applies on its own terms.
func ParseLayerVisibility(data []byte) (Visibility, error) {
	var cfg layerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Visibility{}, fmt.Errorf("%w: %s: %v", ErrConfigInvalid, layerConfigFileName, err)
	}
	return cfg.Visibility, nil
}

// DirectoryVisibility is the visibility a .layer-config declares, as an
// ingest cycle reads it.
type DirectoryVisibility struct {
	// Visibility is the declared visibility. It is empty, and admits no
	// caller, when Malformed is set, and empty when Declared is not.
	Visibility layer.Visibility
	// Declared reports a file that declares a visibility, or one that is
	// malformed; an absent file and an empty visibility block declare none,
	// and the deployment default applies.
	Declared bool
	// Malformed reports a file that does not parse or whose visibility
	// breaks the §4.6 entry rule.
	Malformed bool
}

// ReadDirectoryVisibility reads the .layer-config at the root of fsys, the
// tree of an ingest cycle, as that cycle applies it. A path that is not a
// regular file is absent, as it is for the layer digest. A file that does not
// parse, or whose visibility holds an entry §4.6 does not admit, declares a
// visibility that admits no caller: at a start the same file refuses the
// start, and a later cycle must not widen a layer because the file stopped
// parsing. A read error is returned, so the cycle fails and changes nothing.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer), §4.6
func ReadDirectoryVisibility(fsys fs.FS) (DirectoryVisibility, error) {
	ok, err := regularFile(fsys, LayerConfigFile)
	if err != nil {
		return DirectoryVisibility{}, fmt.Errorf("filesystem: stat %s: %w", LayerConfigFile, err)
	}
	if !ok {
		return DirectoryVisibility{}, nil
	}
	data, err := fs.ReadFile(fsys, LayerConfigFile)
	if err != nil {
		return DirectoryVisibility{}, fmt.Errorf("filesystem: read %s: %w", LayerConfigFile, err)
	}
	malformed := DirectoryVisibility{Declared: true, Malformed: true}
	v, err := ParseLayerVisibility(data)
	if err != nil {
		return malformed, nil
	}
	if layer.CheckEntries("groups", v.Groups) != nil || layer.CheckEntries("users", v.Users) != nil {
		return malformed, nil
	}
	vis := layer.Visibility{Public: v.Public, Organization: v.Organization, Groups: v.Groups, Users: v.Users}
	if !vis.Public && !vis.Organization && len(vis.Groups) == 0 && len(vis.Users) == 0 {
		return DirectoryVisibility{}, nil
	}
	return DirectoryVisibility{Visibility: vis, Declared: true}, nil
}
