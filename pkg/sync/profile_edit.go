package sync

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// editProfileYAML applies the §7.5.7 profile edits to <target>/.podium/sync.yaml
// by mutating a yaml.Node tree in place. Comments and formatting on keys the
// edit does not touch survive the round-trip; only the edited include/exclude
// sequences are rewritten. When the file does not exist, it is created with the
// named profile and an empty `defaults:` block (§7.5.7).
func editProfileYAML(opts ProfileEditOptions) (*ProfileEditResult, error) {
	path := ConfigPath(opts.Target)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("sync.yaml: %w", err)
		}
	}
	root := documentRoot(&doc)
	created := len(data) == 0
	if created {
		// §7.5.7: a fresh file gets an empty defaults: block.
		setMapValue(root, "defaults", &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
	}

	profiles := findMapValue(root, "profiles")
	if profiles == nil || profiles.Kind != yaml.MappingNode {
		profiles = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapValue(root, "profiles", profiles)
	}
	prof := findMapValue(profiles, opts.Profile)
	if prof == nil || prof.Kind != yaml.MappingNode {
		prof = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapValue(profiles, opts.Profile, prof)
	}

	for _, p := range opts.AddInclude {
		addSeqValue(prof, "include", p)
	}
	removeSeqValue(prof, "include", opts.RemoveInclude)
	for _, p := range opts.AddExclude {
		addSeqValue(prof, "exclude", p)
	}
	removeSeqValue(prof, "exclude", opts.RemoveExclude)

	// Decode the edited profile node for the returned result.
	var decoded Profile
	if err := prof.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("sync.yaml: decode profile %q: %w", opts.Profile, err)
	}
	res := &ProfileEditResult{Profile: decoded}
	if opts.DryRun {
		return res, nil
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(opts.Target, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	res.Wrote = true
	return res, nil
}

// documentRoot returns the top-level mapping node of a parsed document,
// initializing an empty document into a mapping when needed.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content = []*yaml.Node{root}
		return root
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		root.Kind = yaml.MappingNode
		root.Tag = "!!map"
		root.Content = nil
	}
	return root
}

// findMapValue returns the value node for key in a mapping node, or nil.
func findMapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMapValue sets key to val in a mapping node, replacing an existing entry
// or appending a new key/value pair.
func setMapValue(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		val)
}

// addSeqValue appends val to the sequence under key (creating it when absent),
// skipping a value that is already present.
func addSeqValue(parent *yaml.Node, key, val string) {
	seq := findMapValue(parent, key)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setMapValue(parent, key, seq)
	}
	for _, n := range seq.Content {
		if n.Value == val {
			return
		}
	}
	seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val})
}

// removeSeqValue drops every entry equal to one of vals from the sequence
// under key.
func removeSeqValue(parent *yaml.Node, key string, vals []string) {
	if len(vals) == 0 {
		return
	}
	seq := findMapValue(parent, key)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	drop := make(map[string]bool, len(vals))
	for _, v := range vals {
		drop[v] = true
	}
	kept := seq.Content[:0]
	for _, n := range seq.Content {
		if !drop[n.Value] {
			kept = append(kept, n)
		}
	}
	seq.Content = kept
}
