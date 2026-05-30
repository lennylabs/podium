package domain

import "strings"

// Match reports whether the §4.5.2 glob pattern matches the canonical
// artifact ID. Glob syntax: `*` matches one path segment, `**` matches
// zero or more segments (recursive), `{a,b,c}` matches any alternative.
// This is the canonical matcher for DOMAIN.md include:/exclude:
// resolution; the lint and sync packages mirror it for their own
// ingest-time and scope checks.
func Match(pattern, id string) bool {
	for _, alt := range expandAlternatives(pattern) {
		if matchSegments(strings.Split(alt, "/"), strings.Split(id, "/")) {
			return true
		}
	}
	return false
}

// MatchAny reports whether any pattern in patterns matches id.
func MatchAny(patterns []string, id string) bool {
	for _, p := range patterns {
		if p != "" && Match(p, id) {
			return true
		}
	}
	return false
}

// ResolveImports computes the §4.5.2 import set: every ID in ids that
// matches an include pattern, with anything matching an exclude pattern
// removed (exclude is applied after include). The result preserves the
// input order of ids. Empty include yields no imports.
func ResolveImports(include, exclude, ids []string) []string {
	out := make([]string, 0)
	for _, id := range ids {
		if MatchAny(include, id) && !MatchAny(exclude, id) {
			out = append(out, id)
		}
	}
	return out
}

// FallbackDescription synthesizes the §4.5.5 description fallback for a
// domain with no DOMAIN.md description: the directory basename,
// de-slugged (hyphens and underscores become spaces) and title-cased.
// For example "finance/accounts-payable" yields "Accounts Payable".
func FallbackDescription(path string) string {
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	words := strings.Fields(base)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// expandAlternatives expands a single `{a,b,c}` group into its
// alternatives, recursing so multiple groups all expand. A pattern with
// no group returns itself.
func expandAlternatives(pattern string) []string {
	open := strings.Index(pattern, "{")
	if open < 0 {
		return []string{pattern}
	}
	closeIdx := strings.Index(pattern[open:], "}")
	if closeIdx < 0 {
		return []string{pattern}
	}
	closeIdx += open
	prefix := pattern[:open]
	suffix := pattern[closeIdx+1:]
	var out []string
	for _, choice := range strings.Split(pattern[open+1:closeIdx], ",") {
		out = append(out, expandAlternatives(prefix+choice+suffix)...)
	}
	return out
}

// matchSegments matches pattern segments against target segments,
// honoring `*` (one segment) and `**` (zero or more segments).
func matchSegments(pat, tgt []string) bool {
	for i := 0; i < len(pat); i++ {
		seg := pat[i]
		if seg == "**" {
			if i == len(pat)-1 {
				return true
			}
			rest := pat[i+1:]
			for j := 0; j <= len(tgt); j++ {
				if matchSegments(rest, tgt[j:]) {
					return true
				}
			}
			return false
		}
		if i >= len(tgt) {
			return false
		}
		if seg != "*" && seg != tgt[i] {
			return false
		}
	}
	return len(pat) == len(tgt)
}
