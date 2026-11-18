package manifest

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// nonStringValueMaxRunes caps NonStringMetadata.Value. A nested value can be
// a large mapping, and a diagnostic line names it to identify it rather than
// to reproduce it.
const nonStringValueMaxRunes = 120

// UnmarshalYAML decodes a SKILL.md frontmatter mapping. metadata decodes
// through a node, so a sequence or a mapping under one of its keys does not
// fail the decode: a scalar value lands in Metadata as a string and every
// other value is listed in NonStringMetadata. A metadata value that is not a
// mapping, and a kind error in any other field, such as a description that
// is a mapping, still fail it.
//
// Spec: §4.1, §4.3.4
func (s *Skill) UnmarshalYAML(value *yaml.Node) error {
	if root := resolveAlias(value); root.Kind != yaml.MappingNode {
		return &yaml.TypeError{Errors: []string{
			fmt.Sprintf("line %d: cannot unmarshal %s into a SKILL.md frontmatter mapping", value.Line, root.ShortTag()),
		}}
	}
	// skillFields carries Skill's fields without its methods, so the decode
	// below does not call this method again.
	type skillFields Skill
	var aux struct {
		skillFields `yaml:",inline"`
		Metadata    yaml.Node `yaml:"metadata"`
		WhenToUse   yaml.Node `yaml:"when_to_use"`
	}
	if err := value.Decode(&aux); err != nil {
		return err
	}
	md, nested, err := decodeSkillMetadataNode(&aux.Metadata)
	if err != nil {
		return err
	}
	*s = Skill(aux.skillFields)
	s.Metadata, s.NonStringMetadata = md, nested
	return s.decodeTopLevelWhenToUse(&aux.WhenToUse)
}

// decodeTopLevelWhenToUse records Claude Code's top-level when_to_use. A
// scalar lands in WhenToUse as a string, a null or an absent key leaves it
// empty, and a sequence or a mapping lands in WhenToUseNonString, so the
// SKILL.md still parses and lint reports the ignored value.
//
// Spec: §4.3.4
func (s *Skill) decodeTopLevelWhenToUse(n *yaml.Node) error {
	v := resolveAlias(n)
	if n.Kind == 0 || (v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null") {
		return nil
	}
	if v.Kind != yaml.ScalarNode {
		s.WhenToUseNonString = &NonStringMetadata{Key: "when_to_use", Kind: nodeKindName(v), Value: flowValue(v)}
		return nil
	}
	return n.Decode(&s.WhenToUse)
}

// decodeSkillMetadataNode splits the metadata node n into the string-valued
// map Podium resolves fields from and the values that are sequences or
// mappings. yaml.v3 decodes the mapping, so merge keys, alias keys, and the
// repeated-key check apply as they do to any mapping. An absent key or a null
// value yields no metadata.
func decodeSkillMetadataNode(n *yaml.Node) (map[string]string, []NonStringMetadata, error) {
	m := resolveAlias(n)
	if n.Kind == 0 || (m.Kind == yaml.ScalarNode && m.ShortTag() == "!!null") {
		return nil, nil, nil
	}
	if m.Kind != yaml.MappingNode {
		return nil, nil, &yaml.TypeError{Errors: []string{
			fmt.Sprintf("line %d: metadata is not a mapping", n.Line),
		}}
	}
	var values map[string]yaml.Node
	if err := n.Decode(&values); err != nil {
		return nil, nil, err
	}
	md := make(map[string]string, len(values))
	var nested []NonStringMetadata
	w := metadataWalker{seen: map[string]bool{}, visited: map[*yaml.Node]bool{}}
	for _, key := range w.keys(m) {
		v, ok := values[key]
		if !ok {
			// yaml.v3 drops a null key, such as ~, when it decodes into a
			// string-keyed map, as it did before nested values parsed, so
			// the walk lists a key the decode does not hold.
			continue
		}
		vn := resolveAlias(&v)
		if vn.Kind == yaml.ScalarNode {
			var str string
			if err := v.Decode(&str); err != nil {
				return nil, nil, err
			}
			md[key] = str
			continue
		}
		nested = append(nested, NonStringMetadata{Key: key, Kind: nodeKindName(vn), Value: flowValue(vn)})
	}
	return md, nested, nil
}

// metadataWalker holds the state of one walk over a metadata mapping and the
// mappings merged into it. seen holds the keys already listed. visited holds
// the mapping nodes already walked, so a cyclic merge, which the yaml.v3
// decode refuses before the walk runs, could never recurse without end.
type metadataWalker struct {
	seen    map[string]bool
	visited map[*yaml.Node]bool
}

// keys returns the keys of the mapping node m in the order a diagnostic
// lists them: the direct keys in document order, then the keys each merge
// source adds, in order. Keys are alias-resolved, because an alias key's own
// Value is the anchor name rather than the key yaml.v3 decodes. A key already
// listed is skipped, because yaml.v3 decodes a direct key in place of a
// merged one and an earlier merged mapping in place of a later one.
func (w metadataWalker) keys(m *yaml.Node) []string {
	w.visited[m] = true
	var out []string
	var merges []*yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		kn := resolveAlias(m.Content[i])
		if kn.ShortTag() == "!!merge" {
			merges = append(merges, resolveAlias(m.Content[i+1]))
			continue
		}
		if !w.seen[kn.Value] {
			w.seen[kn.Value] = true
			out = append(out, kn.Value)
		}
	}
	for _, mn := range merges {
		sources := []*yaml.Node{mn}
		if mn.Kind == yaml.SequenceNode {
			sources = mn.Content
		}
		for _, src := range sources {
			if src = resolveAlias(src); src.Kind == yaml.MappingNode && !w.visited[src] {
				out = append(out, w.keys(src)...)
			}
		}
	}
	return out
}

// nodeKindName names the kind of a non-scalar node for a diagnostic.
func nodeKindName(n *yaml.Node) string {
	if n.Kind == yaml.SequenceNode {
		return "sequence"
	}
	return "mapping"
}

// flowValue re-encodes n as single-line YAML flow, capped at
// nonStringValueMaxRunes. It copies the tree, because the decoded node
// belongs to the caller, and leaves alias nodes as aliases, so a value that
// refers to itself renders without recursing.
func flowValue(n *yaml.Node) string {
	out, err := yaml.Marshal(flowCopy(n))
	if err != nil {
		// Unreachable for a node yaml.v3 just decoded; the kind still names
		// the value in a diagnostic.
		return nodeKindName(n)
	}
	s := strings.Join(strings.Fields(string(out)), " ")
	if utf8.RuneCountInString(s) > nonStringValueMaxRunes {
		s = string([]rune(s)[:nonStringValueMaxRunes]) + "..."
	}
	return s
}

// flowCopy copies n with every collection in flow style and no comments.
func flowCopy(n *yaml.Node) *yaml.Node {
	c := *n
	c.HeadComment, c.LineComment, c.FootComment = "", "", ""
	if c.Kind == yaml.SequenceNode || c.Kind == yaml.MappingNode {
		c.Style |= yaml.FlowStyle
	}
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c.Content[i] = flowCopy(child)
	}
	return &c
}
