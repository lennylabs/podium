package adapter

import (
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

// rewriteProvenanceForClaude rewrites every §4.4.2 imported block
// into a Claude Code <untrusted-data source="X">...</untrusted-data>
// region so the host can apply differential trust at read time.
// Returns the original body unchanged when no blocks are present.
func rewriteProvenanceForClaude(body []byte) []byte {
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
