// Package filesystem implements the filesystem-source registry
// described in spec §13.11. A directory becomes a registry;
// subdirectories optionally become layers depending on the
// .registry-config dispatch (§13.11.1, §13.10 --layer-path modes).
package filesystem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Errors returned by Open. Tests assert against them via errors.Is. Codes
// align with the canonical error namespace in spec §6.10.
var (
	// ErrConfigMissing signals that the configured path does not exist.
	ErrConfigMissing = errors.New("filesystem: registry path does not exist")
	// ErrConfigInvalid signals a malformed .registry-config file.
	// Maps to config.invalid in §6.10.
	ErrConfigInvalid = errors.New("filesystem: invalid .registry-config")
	// ErrLayerPathAmbiguous signals that .registry-config sets
	// multi_layer: true but manifest files appear at the top level.
	// Maps to config.layer_path_ambiguous in §13.10 / §6.10.
	ErrLayerPathAmbiguous = errors.New("filesystem: layer path ambiguous (multi_layer: true but manifests at top level)")
)

// Mode is the dispatch result for a filesystem registry path.
type Mode int

// Mode values per §13.10 ("--layer-path modes") and §13.11.1.
const (
	// ModeSingleLayer is the default: <path> is one local-source layer
	// rooted at the path. Triggered when .registry-config is absent or
	// sets multi_layer: false.
	ModeSingleLayer Mode = iota
	// ModeMultiLayer is the filesystem-registry root mode: each
	// subdirectory of <path> is a separate local-source layer.
	// Triggered by .registry-config with multi_layer: true.
	ModeMultiLayer
)

// String returns "single-layer" / "multi-layer".
func (m Mode) String() string {
	switch m {
	case ModeSingleLayer:
		return "single-layer"
	case ModeMultiLayer:
		return "multi-layer"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// Config is the parsed .registry-config file (§13.11.1).
type Config struct {
	MultiLayer bool     `yaml:"multi_layer"`
	LayerOrder []string `yaml:"layer_order,omitempty"`
}

// Layer is a single local-source layer in a filesystem registry.
type Layer struct {
	// ID is the layer identifier. Defaults to the subdirectory's basename
	// in multi-layer mode, or to the registry path's basename in
	// single-layer mode.
	ID string
	// Path is the absolute filesystem path to the layer's root.
	Path string
}

// Registry is an opened filesystem registry: the dispatched mode plus the
// ordered layer list.
type Registry struct {
	Mode   Mode
	Path   string
	Layers []Layer
	Config Config
}

// configFileName is the filename for the per-registry config (§13.11.1).
const configFileName = ".registry-config"

// manifestFileNames are the filenames that signal "manifest is here" (§4.3,
// §4.5.1). Their presence at the top level of a multi_layer: true root
// triggers ErrLayerPathAmbiguous (§13.10).
var manifestFileNames = []string{
	"ARTIFACT.md",
	"SKILL.md",
	"DOMAIN.md",
}

// Open opens the filesystem registry rooted at path, dispatches between
// single-layer and multi-layer mode per §13.10 / §13.11.1, and returns the
// resolved layer list. The dispatch logic:
//
//  1. If <path> does not exist, return ErrConfigMissing.
//  2. If <path>/.registry-config is missing or sets multi_layer: false,
//     return ModeSingleLayer with one layer rooted at <path>.
//  3. If <path>/.registry-config sets multi_layer: true, every subdirectory
//     of <path> becomes a layer; ordering follows layer_order: when set,
//     alphabetical otherwise. If a manifest file appears at the top level,
//     return ErrLayerPathAmbiguous.
//
func Open(path string) (*Registry, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrConfigMissing, abs)
		}
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%w: not a directory: %s", ErrConfigMissing, abs)
	}

	cfg, hasCfg, err := readConfig(abs)
	if err != nil {
		return nil, err
	}
	if !hasCfg || !cfg.MultiLayer {
		return openSingleLayer(abs, cfg), nil
	}
	return openMultiLayer(abs, cfg)
}

func readConfig(root string) (Config, bool, error) {
	data, err := os.ReadFile(filepath.Join(root, configFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("%w: %v", ErrConfigInvalid, err)
	}
	return cfg, true, nil
}

func openSingleLayer(root string, cfg Config) *Registry {
	id := filepath.Base(root)
	return &Registry{
		Mode:   ModeSingleLayer,
		Path:   root,
		Config: cfg,
		Layers: []Layer{{ID: id, Path: root}},
	}
}

func openMultiLayer(root string, cfg Config) (*Registry, error) {
	if err := assertNoTopLevelManifests(root); err != nil {
		return nil, err
	}
	subdirs, err := listSubdirs(root)
	if err != nil {
		return nil, err
	}
	ordered, err := orderLayers(subdirs, cfg.LayerOrder)
	if err != nil {
		return nil, err
	}
	layers := make([]Layer, 0, len(ordered))
	for _, name := range ordered {
		layers = append(layers, Layer{
			ID:   name,
			Path: filepath.Join(root, name),
		})
	}
	return &Registry{
		Mode:   ModeMultiLayer,
		Path:   root,
		Config: cfg,
		Layers: layers,
	}, nil
}

func assertNoTopLevelManifests(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		for _, name := range manifestFileNames {
			if strings.EqualFold(e.Name(), name) {
				found = append(found, e.Name())
			}
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("%w: top-level manifest(s): %s", ErrLayerPathAmbiguous, strings.Join(found, ", "))
	}
	return nil
}

func listSubdirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip dotfile / dot-directory entries (e.g., .git).
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// orderLayers respects layer_order: when set; everything else falls in
// alphabetical order after the explicit list. Layers in layer_order: that
// don't exist on disk are silently dropped (a future phase's lint surfaces
// them as warnings).
func orderLayers(subdirs, explicit []string) ([]string, error) {
	if len(explicit) == 0 {
		return subdirs, nil
	}
	have := map[string]bool{}
	for _, s := range subdirs {
		have[s] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, name := range explicit {
		if !have[name] {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, s := range subdirs {
		if !seen[s] {
			out = append(out, s)
		}
	}
	return out, nil
}
