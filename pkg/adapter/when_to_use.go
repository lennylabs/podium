package adapter

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

// whenToUseKey is the top-level SKILL.md key Claude Code reads as additional
// context for when to invoke a skill (§4.3.4).
const whenToUseKey = "when_to_use"

// withClaudeWhenToUse writes a skill's resolved when_to_use as a top-level
// when_to_use string in b, the SKILL.md a Claude writer materializes, so
// Claude Code reads the situations a skill states in ARTIFACT.md or in
// metadata.when_to_use, which it does not read itself. The situations are
// joined by line feeds into one double-quoted scalar.
//
// An authored top-level key is kept byte for byte, whatever its value: it is
// the text Claude Code is meant to read, and a second key would make the
// frontmatter a mapping with a duplicate key. In a block mapping the line is
// injected at the indentation of its keys, which keeps every other line
// unchanged. No injected line joins a flow mapping, a mapping whose first key
// is explicit, or one that opens with a tag or anchor line without breaking
// the decode, so there the key joins the mapping node and the frontmatter is
// re-encoded, as the user-only control does. A frontmatter that does not
// decode to a mapping is left as it is.
//
// Spec: §4.3.4, §6.7, §7.8
func withClaudeWhenToUse(b []byte, src Source) []byte {
	fm, _, err := manifest.SplitFrontmatter(b)
	if err != nil {
		return b
	}
	var doc yaml.Node
	if yaml.Unmarshal(fm, &doc) != nil || len(doc.Content) == 0 {
		return b
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return b
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == whenToUseKey {
			return b
		}
	}
	a, err := manifest.ResolveArtifact(src.ArtifactBytes, b)
	if err != nil || len(a.WhenToUse) == 0 {
		return b
	}
	value := strings.Join(a.WhenToUse, "\n")
	lines := strings.SplitAfter(string(b), "\n")
	if indent, ok := blockKeyIndent(lines, root); ok {
		return injectFrontmatterLine(b, indent, whenToUseKey, strconv.Quote(value))
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: whenToUseKey},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle})
	return reencodeFrontmatter(lines, closingDelimiterLine(fm), &doc)
}
