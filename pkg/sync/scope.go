package sync

import (
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// ScopeFilter narrows a record set per §7.5.1: --include, --exclude, --type.
// Patterns use the same glob syntax as DOMAIN.md include: (§4.5.2): "*"
// matches one segment, "**" matches recursively, brace alternation works.
type ScopeFilter struct {
	Include []string
	Exclude []string
	Types   []string
}

// IsEmpty reports whether the filter would match every input.
func (f ScopeFilter) IsEmpty() bool {
	return len(f.Include) == 0 && len(f.Exclude) == 0 && len(f.Types) == 0
}

// Apply runs the filter over the records and returns those that pass.
// When Include is non-empty, only records matching at least one include
// pattern survive; Exclude is then applied; Types is then applied.
func (f ScopeFilter) Apply(records []filesystem.ArtifactRecord) []filesystem.ArtifactRecord {
	if f.IsEmpty() {
		return records
	}
	out := make([]filesystem.ArtifactRecord, 0, len(records))
	for _, rec := range records {
		if !matchesAny(rec.ID, f.Include) && len(f.Include) > 0 {
			continue
		}
		if matchesAny(rec.ID, f.Exclude) {
			continue
		}
		if len(f.Types) > 0 && !containsType(f.Types, string(rec.Artifact.Type)) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func matchesAny(id string, patterns []string) bool {
	for _, p := range patterns {
		if matchGlob(p, id) {
			return true
		}
	}
	return false
}

func containsType(allowed []string, ty string) bool {
	for _, a := range allowed {
		if a == ty {
			return true
		}
	}
	return false
}

// matchGlob is a small glob matcher supporting "*", "**", and {a,b}
// alternation. It splits the pattern on slashes, expands brace
// alternation, then matches segment-by-segment with "**" matching zero
// or more segments and "*" matching exactly one.
func matchGlob(pattern, target string) bool {
	for _, p := range expandBraces(pattern) {
		if doGlobMatch(splitSegments(p), splitSegments(target)) {
			return true
		}
	}
	return false
}

func splitSegments(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func doGlobMatch(pattern, target []string) bool {
	if len(pattern) == 0 {
		return len(target) == 0
	}
	if pattern[0] == "**" {
		// Trailing ** requires at least one segment so that "finance/**"
		// does not match the bare "finance" path. ** in the middle of
		// a pattern still matches zero segments so "finance/**/pay"
		// matches "finance/pay".
		minMatch := 0
		if len(pattern) == 1 {
			minMatch = 1
		}
		for i := minMatch; i <= len(target); i++ {
			if doGlobMatch(pattern[1:], target[i:]) {
				return true
			}
		}
		return false
	}
	if len(target) == 0 {
		return false
	}
	if !singleSegmentMatch(pattern[0], target[0]) {
		return false
	}
	return doGlobMatch(pattern[1:], target[1:])
}

func singleSegmentMatch(pattern, segment string) bool {
	matched, err := filepath.Match(pattern, segment)
	if err != nil {
		return false
	}
	return matched
}

// expandBraces returns all literal expansions of a brace-style pattern.
// {a,b}/x expands to ["a/x", "b/x"]. Nested braces are not supported.
func expandBraces(pattern string) []string {
	open := strings.Index(pattern, "{")
	if open < 0 {
		return []string{pattern}
	}
	close := strings.Index(pattern[open:], "}")
	if close < 0 {
		return []string{pattern}
	}
	close += open
	prefix := pattern[:open]
	suffix := pattern[close+1:]
	options := strings.Split(pattern[open+1:close], ",")
	out := make([]string, 0, len(options))
	for _, opt := range options {
		expanded := expandBraces(prefix + opt + suffix)
		out = append(out, expanded...)
	}
	return out
}
