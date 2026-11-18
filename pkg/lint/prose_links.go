package lint

import (
	"net/url"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// proseParser is a CommonMark parser with no extension, so GFM autolinks of
// bare URLs and other non-CommonMark syntax produce no link. A goldmark
// parser keeps no per-document state outside the context passed to Parse, so
// one value serves concurrent lint runs.
var proseParser = parser.NewParser(
	parser.WithBlockParsers(parser.DefaultBlockParsers()...),
	parser.WithInlineParsers(parser.DefaultInlineParsers()...),
	parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
)

// proseLinkTargets returns the destination of each Markdown inline link and
// image in body, in document order, as CommonMark parses the body. Link syntax
// inside a code span, a fenced or indented code block (nested in a list item
// or a block quote included), and link syntax whose bracket is escaped never
// become link nodes, so the check never sees them.
//
// Reference-style links, `[text][ref]` and its collapsed and shortcut forms
// with a `[ref]: target` definition, are not prose references: §4.4 names the
// inline link and image alone. The parse runs with a context under which no
// label resolves, so the definition is still consumed as CommonMark specifies
// and a reference link stays literal text rather than resolving to a link
// node that would be checked.
//
// Spec: §4.4
func proseLinkTargets(body []byte) []string {
	doc := proseParser.Parse(text.NewReader(body), parser.WithContext(inlineLinksOnly{parser.NewContext()}))
	var out []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest []byte
		switch l := n.(type) {
		case *ast.Link:
			dest = l.Destination
		case *ast.Image:
			dest = l.Destination
		default:
			return ast.WalkContinue, nil
		}
		if target := linkDestination(dest); target != "" {
			out = append(out, target)
		}
		return ast.WalkContinue, nil
	})
	return out
}

// inlineLinksOnly is a parser context under which no link reference
// definition resolves, so no reference-style link becomes a link node
// (proseLinkTargets). The embedded context still records each definition,
// which nothing reads.
type inlineLinksOnly struct {
	parser.Context
}

// Reference reports every label as undefined.
func (inlineLinksOnly) Reference(string) (parser.Reference, bool) { return nil, false }

// linkDestination returns a link's target from the destination bytes goldmark
// records. goldmark has already removed the angle brackets and the link
// title, and it keeps backslash escapes and character references as written,
// so they are resolved here as CommonMark specifies: `my\_notes.md` names
// `my_notes.md`.
func linkDestination(raw []byte) string {
	raw = util.UnescapePunctuations(raw)
	raw = util.ResolveNumericReferences(raw)
	raw = util.ResolveEntityNames(raw)
	return string(raw)
}

// referenceLookupPath returns the path the bundled-file and artifact lookups
// read for a relative prose reference: the target without its fragment (from
// `#`) and its query (from `?`), percent-decoded, so `references/api.md#errors`
// looks up `references/api.md`. A target whose percent-encoding does not
// decode, such as `notes%zz.md`, is looked up as written, without its fragment
// and query, and the lookup then reports it like any other unresolved target.
//
// Spec: §4.4
func referenceLookupPath(target string) string {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	if i := strings.IndexByte(target, '?'); i >= 0 {
		target = target[:i]
	}
	if decoded, err := url.PathUnescape(target); err == nil {
		return decoded
	}
	return target
}
