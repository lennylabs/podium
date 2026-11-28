package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/attest"
)

// AttestationBlock is the defaults.attestation block of one sync.yaml file
// (§7.5 Author attestation). Each field is empty when the file leaves its key
// unset. Relative paths are resolved against the directory of the file.
type AttestationBlock struct {
	// Path is the sync.yaml file the block was read from.
	Path string
	// Policy is the default mode the file sets.
	Policy attest.Mode
	// PolicyFile is the policy file the file names, made absolute.
	PolicyFile string
	// TrustedRootFile and RootMaxAge are the public-good root settings of
	// the rules of PolicyFile. RootMaxAgeSet reports that the key is set,
	// because a zero age removes the limit.
	TrustedRootFile string
	RootMaxAge      time.Duration
	RootMaxAgeSet   bool
	// Overlay is allow, deny, or empty.
	Overlay string
	// LayerRoots maps a filesystem-source layer ID to its signed root.
	LayerRoots map[string]string
	// PinSigners is the pin_signers setting, false when unset.
	PinSigners bool
}

// attestationKeys are the keys of the defaults.attestation block.
var attestationKeys = map[string]bool{
	"policy": true, "policy_file": true, "trusted_root_file": true, "root_max_age": true,
	"overlay": true, "layer_roots": true, "pin_signers": true,
}

// ReadAttestationBlock reads the defaults.attestation block of the sync.yaml
// at path. A file that does not exist, or that sets no block, returns nil and
// no error. A file that is not YAML, a block that is not a mapping, a key of
// another name or given twice, and a value of another type or outside its
// values are errors that lead with config.attestation_policy_unavailable and
// name the file. The block is decoded apart from the rest of the file, so a
// reader of the other keys is unaffected by it.
//
// Spec: §7.5 (Author attestation)
func ReadAttestationBlock(path string) (*AttestationBlock, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, blockError(path, "cannot read the file: %v", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, blockError(path, "%v", err)
	}
	node := attestationNode(&doc)
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, blockError(path, "defaults.attestation is not a mapping")
	}
	b := &AttestationBlock{Path: path}
	seen := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i].Value, resolveAlias(node.Content[i+1])
		if !attestationKeys[key] || seen[key] {
			return nil, blockError(path, "defaults.attestation: key %q is unknown or given twice", key)
		}
		seen[key] = true
		if isNull(val) {
			continue
		}
		if err := b.set(key, val); err != nil {
			return nil, blockError(path, "defaults.attestation.%s: %v", key, err)
		}
	}
	return b, nil
}

// attestationNode returns the value node of defaults.attestation in a
// decoded document, nil when the file sets none.
func attestationNode(doc *yaml.Node) *yaml.Node {
	if len(doc.Content) == 0 {
		return nil
	}
	root := resolveAlias(doc.Content[0])
	if root.Kind != yaml.MappingNode {
		return nil
	}
	defaults := findConfigKey(root, "defaults")
	if defaults == nil {
		return nil
	}
	defaults = resolveAlias(defaults)
	if defaults.Kind != yaml.MappingNode {
		return nil
	}
	n := findConfigKey(defaults, "attestation")
	if n == nil || isNull(resolveAlias(n)) {
		return nil
	}
	return resolveAlias(n)
}

// set decodes one key of the block.
func (b *AttestationBlock) set(key string, val *yaml.Node) error {
	if key == "layer_roots" {
		return b.setLayerRoots(val)
	}
	if key == "pin_signers" {
		if val.Kind != yaml.ScalarNode || val.Tag != "!!bool" {
			return errors.New("want a boolean")
		}
		return val.Decode(&b.PinSigners)
	}
	if val.Kind != yaml.ScalarNode || val.Tag != "!!str" {
		return errors.New("want a string")
	}
	dir := filepath.Dir(b.Path)
	switch key {
	case "policy":
		m, err := attest.ParseMode(val.Value)
		if err != nil || m == "" {
			return fmt.Errorf("want none, warn, or require, got %q", val.Value)
		}
		b.Policy = m
	case "policy_file":
		b.PolicyFile = absUnder(dir, val.Value)
	case "trusted_root_file":
		b.TrustedRootFile = absUnder(dir, val.Value)
	case "root_max_age":
		d, err := time.ParseDuration(val.Value)
		if err != nil || d < 0 {
			return fmt.Errorf("want a duration of zero or more, got %q", val.Value)
		}
		b.RootMaxAge, b.RootMaxAgeSet = d, true
	case "overlay":
		if val.Value != OverlayAllow && val.Value != OverlayDeny {
			return fmt.Errorf("want allow or deny, got %q", val.Value)
		}
		b.Overlay = val.Value
	}
	return nil
}

// setLayerRoots decodes the layer_roots mapping of layer IDs to roots.
func (b *AttestationBlock) setLayerRoots(val *yaml.Node) error {
	if val.Kind != yaml.MappingNode {
		return errors.New("want a mapping of layer IDs to roots")
	}
	b.LayerRoots = map[string]string{}
	for i := 0; i+1 < len(val.Content); i += 2 {
		k, v := resolveAlias(val.Content[i]), resolveAlias(val.Content[i+1])
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return errors.New("want a mapping of layer IDs to root strings")
		}
		if _, dup := b.LayerRoots[k.Value]; dup {
			return fmt.Errorf("layer %q is given twice", k.Value)
		}
		if _, err := attest.CheckLayerRoot(v.Value); err != nil {
			return fmt.Errorf("layer %q: %w", k.Value, err)
		}
		b.LayerRoots[k.Value] = v.Value
	}
	return nil
}

// resolveAlias follows a YAML alias to the node it names.
func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// isNull reports whether n is a YAML null, which leaves a key unset.
func isNull(n *yaml.Node) bool {
	return n == nil || n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// absUnder resolves p against dir when it is relative.
func absUnder(dir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// blockError is a config.attestation_policy_unavailable error that names
// the file.
func blockError(path, format string, args ...any) error {
	return fmt.Errorf("%s: %s: %s", attest.CodePolicyUnavailable, path, fmt.Sprintf(format, args...))
}
