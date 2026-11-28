package sync

import (
	"fmt"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// AttestationDefaults is the decoded `defaults.attestation` block of a
// sync.yaml (§4.7.11 Policy sources, §7.5.2). Each member is empty when the
// block leaves it unset.
type AttestationDefaults struct {
	// Policy is the default mode the file sets: none, warn, or require.
	Policy string
	// PolicyFile names a policy file. A relative path resolves against the
	// directory of the sync.yaml that holds it (ResolvePath).
	PolicyFile string
	// TrustedRootFile names the public-good trusted root of the rules of
	// PolicyFile. It takes effect from the user's own sync.yaml and the
	// machine file alone.
	TrustedRootFile string
	// RootMaxAge is the maximum age of the refreshed public-good root for
	// the rules of PolicyFile, as a duration. It takes effect from the
	// user's own sync.yaml and the machine file alone.
	RootMaxAge string
	// Overlay is allow or deny, and empty when unset. It takes effect from
	// the user's own sync.yaml and the machine file alone.
	Overlay string
	// LayerRoots maps a layer ID to the root its content was signed under,
	// read by filesystem-source podium sync.
	LayerRoots map[string]string
}

// attestationKeys are the members of the block, in the order an error
// lists them.
var attestationKeys = []string{"policy", "policy_file", "trusted_root_file", "root_max_age", "overlay", "layer_roots"}

// UserOnlyAttestationKeys are the keys that take effect from the user's own
// sync.yaml and the machine file alone. A consumer ignores them in a
// workspace sync.yaml and names them on standard error.
//
// Spec: §4.7.11 (Policy sources)
var UserOnlyAttestationKeys = []string{"trusted_root_file", "root_max_age", "overlay"}

// AttestationDefaults decodes the `defaults.attestation` block. An absent or
// null block is the zero value. A block that is not a mapping, a member of
// another name, a member given twice, and a member of the wrong YAML type
// are refused, so a misspelled key cannot drop a policy without an error.
//
// Spec: §4.7.11 (Policy sources), §7.5.2
func (d Defaults) AttestationDefaults() (AttestationDefaults, error) {
	var out AttestationDefaults
	if d.Attestation.Kind == 0 {
		return out, nil
	}
	var block yamlNode
	// yamlNode keeps any node and returns no error, so this guard is
	// unreachable.
	if err := d.Attestation.Decode(&block); err != nil {
		return out, fmt.Errorf("defaults.attestation: %w", err)
	}
	if block.n == nil {
		return out, nil
	}
	if block.n.Kind != yaml.MappingNode {
		return out, fmt.Errorf("defaults.attestation: want a mapping, got %s", yamlTypeName(block.n))
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(block.n.Content); i += 2 {
		key, val := block.n.Content[i], block.n.Content[i+1]
		if val.Kind == yaml.AliasNode {
			val = val.Alias
		}
		name := key.Value
		if key.Kind != yaml.ScalarNode || !slices.Contains(attestationKeys, name) {
			return out, fmt.Errorf("defaults.attestation: unknown key %q; the keys are %v", name, attestationKeys)
		}
		if seen[name] {
			return out, fmt.Errorf("defaults.attestation: key %q is given twice", name)
		}
		seen[name] = true
		if err := out.set(name, val); err != nil {
			return out, fmt.Errorf("defaults.attestation.%s: %w", name, err)
		}
	}
	return out, nil
}

// set assigns one decoded member.
func (a *AttestationDefaults) set(name string, n *yaml.Node) error {
	if n.ShortTag() == "!!null" {
		return nil
	}
	if name == "layer_roots" {
		if n.Kind != yaml.MappingNode {
			return fmt.Errorf("want a mapping from layer ID to root, got %s", yamlTypeName(n))
		}
		roots := map[string]string{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if v.Kind == yaml.AliasNode {
				v = v.Alias
			}
			if k.ShortTag() != "!!str" || v.ShortTag() != "!!str" {
				return fmt.Errorf("want string keys and values, got %s: %s", yamlTypeName(k), yamlTypeName(v))
			}
			roots[k.Value] = v.Value
		}
		a.LayerRoots = roots
		return nil
	}
	s, err := yamlString(n)
	if err != nil {
		return err
	}
	switch name {
	case "policy":
		a.Policy = *s
	case "policy_file":
		a.PolicyFile = *s
	case "trusted_root_file":
		a.TrustedRootFile = *s
	case "root_max_age":
		a.RootMaxAge = *s
	case "overlay":
		if *s != "allow" && *s != "deny" {
			return fmt.Errorf("want allow or deny, got %q", *s)
		}
		a.Overlay = *s
	}
	return nil
}

// Set reports which keys of the block hold a value.
func (a AttestationDefaults) Set() []string {
	var out []string
	for name, v := range map[string]bool{
		"policy": a.Policy != "", "policy_file": a.PolicyFile != "", "trusted_root_file": a.TrustedRootFile != "",
		"root_max_age": a.RootMaxAge != "", "overlay": a.Overlay != "", "layer_roots": a.LayerRoots != nil,
	} {
		if v {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// ResolvePath resolves a path a sync.yaml names against the directory of
// that file: an absolute path is kept, and a relative one is joined to the
// directory of configPath.
//
// Spec: §4.7.11 (Policy sources)
func ResolvePath(configPath, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(configPath), p)
}
