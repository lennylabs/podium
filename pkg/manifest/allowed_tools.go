package manifest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AllowedTools is the SKILL.md `allowed-tools` field. The Agent Skills
// standard defines it as a space-separated string (`Bash(git:*) Read`), and
// Claude Code also accepts a YAML sequence, so decoding accepts both. Podium
// does not interpret the value: adapters copy the authored SKILL.md bytes, and
// the decoded list exists so a malformed value is reported rather than
// silently dropped.
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

// splitToolList splits a space-separated tool list. Separators inside
// parentheses belong to the tool pattern and do not split it.
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
