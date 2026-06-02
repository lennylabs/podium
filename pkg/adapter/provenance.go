package adapter

import (
	"bytes"
	"regexp"
)

// provenanceBlockPattern matches a §4.4.2 imported block:
//
//	<!-- begin imported source="..." -->
//	...body...
//	<!-- end imported -->
//
// The source attribute is captured (group 1) and the body is
// captured verbatim (group 2). Multiline + dotall: the body can
// span lines; we use (?s) so `.` matches `\n` too.
var provenanceBlockPattern = regexp.MustCompile(
	`(?s)<!--\s*begin imported(?:\s+source="([^"]*)")?\s*-->\s*(.*?)\s*<!--\s*end imported\s*-->`,
)

// rewriteProvenanceForClaude propagates §4.4.2 provenance markers into the
// Claude Code <untrusted-data> trust-region convention so the host can apply
// differential trust at read time. Two markers are handled:
//
//   - Inline `<!-- begin imported ... -->` blocks become a per-region
//     <untrusted-data source="X">...</untrusted-data>.
//   - A document-level `source:` that declares a non-authored default
//     provenance (for example `imported`) wraps the remaining authored
//     prose in a single <untrusted-data source="X"> region.
//
// An empty or `authored` source leaves the body's prose trusted, so a body
// with no inline blocks and an authored (or absent) source is returned
// unchanged.
func rewriteProvenanceForClaude(body []byte, source string) []byte {
	out := rewriteImportedBlocks(body)
	if isUntrustedDefaultSource(source) {
		out = wrapProseAsUntrusted(out, source)
	}
	return out
}

// rewriteImportedBlocks rewrites every §4.4.2 inline imported block into a
// Claude Code <untrusted-data source="X">...</untrusted-data> region.
// Returns the original body unchanged when no blocks are present.
func rewriteImportedBlocks(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	if !provenanceBlockPattern.Match(body) {
		return body
	}
	return provenanceBlockPattern.ReplaceAllFunc(body, func(match []byte) []byte {
		groups := provenanceBlockPattern.FindSubmatch(match)
		source := string(groups[1])
		inner := groups[2]
		open := "<untrusted-data"
		if source != "" {
			open += ` source="` + source + `"`
		}
		open += ">"
		out := []byte(open + "\n")
		out = append(out, inner...)
		out = append(out, []byte("\n</untrusted-data>")...)
		return out
	})
}

// isUntrustedDefaultSource reports whether a §4.4.2 document-level source
// marks the prose as untrusted by default. The documented trusted value is
// `authored`; an absent source is trusted too. Any other non-empty value
// (for example `imported`) is treated as untrusted.
func isUntrustedDefaultSource(source string) bool {
	return source != "" && source != "authored"
}

// wrapProseAsUntrusted wraps the prose body of src (everything after the
// leading YAML frontmatter) in a single <untrusted-data source="..."> region.
// The frontmatter is preserved byte for byte. A whitespace-only prose body is
// returned unchanged so an empty document gains no spurious region.
func wrapProseAsUntrusted(src []byte, source string) []byte {
	off := proseOffset(src)
	prefix := src[:off]
	prose := bytes.Trim(src[off:], "\r\n")
	if len(bytes.TrimSpace(prose)) == 0 {
		return src
	}
	var b bytes.Buffer
	b.Write(prefix)
	b.WriteString(`<untrusted-data source="` + source + `">` + "\n")
	b.Write(prose)
	b.WriteString("\n</untrusted-data>\n")
	return b.Bytes()
}

// proseOffset returns the byte index in src where the prose body begins,
// just past the closing `---` fence of the leading YAML frontmatter. Returns
// 0 when src has no frontmatter (the whole input is prose).
func proseOffset(src []byte) int {
	if !bytes.HasPrefix(src, []byte("---\n")) && !bytes.HasPrefix(src, []byte("---\r\n")) {
		return 0
	}
	idx := bytes.Index(src[3:], []byte("\n---"))
	if idx < 0 {
		return 0
	}
	pos := 3 + idx + len("\n---")
	if nl := bytes.IndexByte(src[pos:], '\n'); nl >= 0 {
		return pos + nl + 1
	}
	return len(src)
}
