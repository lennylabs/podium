package manifest

import (
	"errors"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// ErrUnreadableChain reports that a parent chain block or the child block
// cannot be decoded for the §4.6 executable-field comparison, or that a field
// path the comparison would emit names a chain parent §4.6 withholds.
var ErrUnreadableChain = errors.New("manifest: executable fields: unreadable block")

// InheritedExecutableFields returns the §4.6 executable fields whose merged
// value would not be the value the child authored, as field paths sorted
// byte-wise and de-duplicated. The result is empty exactly when the
// executable values of MergeExtends(parent, child) and of the restored
// undeclared keys equal the values the child authored.
//
// childID is the child's canonical ID, used only to build the set of chain
// parents a field path must not name. parent is the parent's chain-resolved
// artifact, and parentChain holds the parent's authored blocks, root first and
// the parent last. childBlock is the child's authored ARTIFACT.md. The child is
// parsed from childBlock here, for both the typed and the undeclared halves,
// so a caller cannot pass a child that a resolver has already merged: such a
// child carries inherited values as its own and would pass silently.
//
// A field path is hook_action or server_identifier; mcpServers[<name>] for a
// merged entry the child's list does not carry; mcpServers[<name>].transport,
// .command, .args, or .env.<KEY> for a member the child's entry does not set;
// or the bare key name for an undeclared key.
//
// Every error wraps ErrUnreadableChain. A caller turns it into a per-artifact
// ingest.invalid_artifact rejection or drop with
// layer.UnreadableExecutableFieldsReason and does not abort the run. The error
// never wraps ErrUnhidableParent, which stays specific to the read path's
// hidden-parent rule.
//
// The function is pure: it reads no clock, randomness, or I/O, so the server
// ingest path and the filesystem resolver reach the same answer for the same
// inputs (§2.2).
//
// Spec: §4.6
func InheritedExecutableFields(childID string, parent Artifact, parentChain []MergedBlock, childBlock []byte) ([]string, error) {
	child, err := ParseArtifact(childBlock)
	if err != nil {
		return nil, unreadable("decode child block", err)
	}
	childMapping, _, err := mappingOf(childBlock)
	if err != nil {
		return nil, unreadable("decode child block", err)
	}
	authored, err := chainMappings(parentChain)
	if err != nil {
		return nil, unreadable("decode parent chain", err)
	}

	merged := MergeExtends(parent, *child)
	var fields []string
	if merged.HookAction != child.HookAction {
		fields = append(fields, "hook_action")
	}
	if merged.ServerIdentifier != child.ServerIdentifier {
		fields = append(fields, "server_identifier")
	}
	servers, serverNames := inheritedServerFields(merged.MCPServers, child.MCPServers)
	keys := inheritedUndeclaredKeys(append(authored, childMapping), childMapping)

	// Spec: §4.6 withheld. A path built from a parent-authored name skips the
	// read path's reference test, so it is held to that test here. A child
	// whose path would name a chain parent fails every read, so refusing it
	// loses no servable artifact.
	pins := make([]string, 0, len(parentChain)+1)
	for _, block := range parentChain {
		pins = append(pins, block.Extends)
	}
	parents := pinnedParents(childID, append(pins, child.Extends))
	for _, name := range append(serverNames, keys...) {
		if parents.discloses(name) {
			return nil, fmt.Errorf("manifest: executable fields: field path names a chain parent: %w", ErrUnreadableChain)
		}
	}

	fields = append(fields, servers...)
	fields = append(fields, keys...)
	slices.Sort(fields)
	return slices.Compact(fields), nil
}

// unreadable wraps a decode failure in ErrUnreadableChain. The cause is
// formatted with %v so that a mappingOf failure, which can carry
// ErrUnhidableParent, does not leak that sentinel into this error's chain.
func unreadable(op string, err error) error {
	return fmt.Errorf("manifest: executable fields: %s: %v: %w", op, err, ErrUnreadableChain)
}

// inheritedServerFields compares each merged mcpServers entry with the child's
// own entry of the same name and returns the field paths of the members the
// child does not set, together with every name component those paths carry.
//
// The child's list is folded through mergeMCPServers on its own, so a child
// that lists one name twice is compared as the entry the merge produces from
// those two entries.
func inheritedServerFields(merged, child []MCPServerRef) (fields, components []string) {
	own := map[string]MCPServerRef{}
	for _, c := range mergeMCPServers(nil, child) {
		own[c.Name] = c
	}
	for _, m := range merged {
		prefix := "mcpServers[" + m.Name + "]"
		c, ok := own[m.Name]
		if !ok {
			fields = append(fields, prefix)
			components = append(components, m.Name)
			continue
		}
		before := len(fields)
		if m.Transport != c.Transport {
			fields = append(fields, prefix+".transport")
		}
		if m.Command != c.Command {
			fields = append(fields, prefix+".command")
		}
		if !slices.Equal(m.Args, c.Args) {
			fields = append(fields, prefix+".args")
		}
		for key := range m.Env {
			if _, set := c.Env[key]; !set {
				fields = append(fields, prefix+".env."+key)
				components = append(components, key)
			}
		}
		if len(fields) > before {
			components = append(components, m.Name)
		}
	}
	return fields, components
}

// inheritedUndeclaredKeys returns the undeclared keys whose winning value
// across authored, which ends with the child's mapping, is not a node the
// child authored.
//
// Spec: §4.6 omitted fields. A parent value that is an empty scalar sets
// nothing, so it is skipped. A parent `[]` or `{}` is a value the parent set,
// which matches what SerializeMerged serves for the key.
func inheritedUndeclaredKeys(authored []*yaml.Node, childMapping *yaml.Node) []string {
	own := make(map[*yaml.Node]bool, len(childMapping.Content)/2)
	for j := 1; j < len(childMapping.Content); j += 2 {
		own[childMapping.Content[j]] = true
	}
	var keys []string
	for _, key := range undeclaredKeysOf(authored) {
		if own[key.value] || isEmptyValue(key.value) {
			continue
		}
		keys = append(keys, key.name)
	}
	return keys
}
