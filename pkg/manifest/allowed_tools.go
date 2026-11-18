package manifest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AllowedTools is the SKILL.md `allowed-tools` field. The Agent Skills
// standard defines it as a space-separated string (`Bash(git:*) Read`),
// published skills also separate the entries with commas, and a YAML sequence
// of strings is accepted too, so decoding accepts each form. Podium stores the
// authored bytes and does not interpret the value: adapters copy the authored
// SKILL.md, and nothing reads the decoded list. A value of any other kind,
// such as a mapping, fails the decode, which makes the SKILL.md unparseable
// (§4.1).
//
// Spec: §4.3.4
type AllowedTools []string

// UnmarshalYAML decodes a sequence of strings or a scalar string. A scalar is
// split on white space and commas outside parentheses, so a tool pattern such
// as `Bash(git log:*)` stays one entry. A null decodes to an empty list. A
// mapping, or a sequence holding a non-scalar, is a decode error.
//
// Spec: §4.3.4
func (a *AllowedTools) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!null" {
			*a = nil
			return nil
		}
		*a = splitToolList(node.Value)
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}
		*a = list
		return nil
	default:
		return fmt.Errorf("line %d: allowed-tools is a string or a list of strings", node.Line)
	}
}

// splitToolList splits a tool list on white space and on commas, so it
// accepts the space-separated list the standard defines and the
// comma-separated lists published skills use. Separators inside parentheses
// belong to the tool pattern and do not split it.
//
// Spec: §4.3.4
func splitToolList(s string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '(':
			depth++
			cur.WriteRune(r)
		case r == ')':
			if depth > 0 {
				depth--
			}
			cur.WriteRune(r)
		case depth == 0 && (r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}
