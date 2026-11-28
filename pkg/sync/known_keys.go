package sync

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlKeys returns the YAML member names of a struct type, read from its
// yaml tags, so the list follows the schema as it changes.
func yamlKeys(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// CheckKnownKeys refuses a sync.yaml whose top level, or whose defaults
// block, holds a key the schema does not name. The user's own sync.yaml and
// the machine file carry the protective attestation policy of a consumer, so
// a block written at the top level, under `default:`, or as
// `defaults.attestations:` would otherwise be dropped without an error. A
// body that does not parse is left to the caller's decoder.
//
// Spec: §4.7.11 (User sources of the MCP server, Known keys), §7.5.2
func CheckKnownKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil
	}
	if err := checkMappingKeys(top, "", yamlKeys(reflect.TypeFor[SyncConfig]())); err != nil {
		return err
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "defaults" {
			continue
		}
		d := top.Content[i+1]
		if d.Kind == yaml.AliasNode {
			d = d.Alias
		}
		if d.Kind == yaml.MappingNode {
			if err := checkMappingKeys(d, "defaults.", yamlKeys(reflect.TypeFor[Defaults]())); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkMappingKeys refuses a key of m outside known.
func checkMappingKeys(m *yaml.Node, prefix string, known []string) error {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind != yaml.ScalarNode || !slices.Contains(known, k.Value) {
			return fmt.Errorf("unknown key %q; the keys %sare %s", prefix+k.Value, prefixWord(prefix), strings.Join(known, ", "))
		}
	}
	return nil
}

// prefixWord names the block of a key list in an error message.
func prefixWord(prefix string) string {
	if prefix == "" {
		return "at the top level "
	}
	return "under " + strings.TrimSuffix(prefix, ".") + " "
}
