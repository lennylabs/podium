package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// ruleDomainImportsResolve implements the §4.5.2 "validation"
// promise: every `include:` / `exclude:` pattern in a DOMAIN.md
// must match at least one artifact ID known to the registry; an
// unresolved pattern surfaces as an ingest-time warning, not an
// error (the spec calls out the "expected to be defined in
// another layer later" case).
type ruleDomainImportsResolve struct{}

func (ruleDomainImportsResolve) Code() string        { return "lint.domain_import_unresolved" }

func (r ruleDomainImportsResolve) Check(reg *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	if reg == nil {
		return nil
	}
	ids := make([]string, 0, len(records))
	for _, rec := range records {
		ids = append(ids, rec.ID)
	}
	var out []Diagnostic
	for _, layer := range reg.Layers {
		domains := walkDomainsInLayer(layer)
		for path, dom := range domains {
			for _, pattern := range append([]string(nil), dom.Include...) {
				if pattern == "" || importPatternResolves(pattern, ids) {
					continue
				}
				out = append(out, Diagnostic{
					ArtifactID: path,
					Code:       r.Code(),
					Severity:   SeverityWarning,
					Message: fmt.Sprintf(
						"DOMAIN.md include pattern %q does not match any known artifact",
						pattern),
				})
			}
		}
	}
	return out
}

// ruleDomainImportCycle implements the §4.5.2 "cycle detection"
// promise: two domains importing each other is allowed but
// lint-warned. We compute the include-pattern graph between
// domain paths and walk it; any cycle (length ≥ 2) yields a
// warning naming the participants.
type ruleDomainImportCycle struct{}

func (ruleDomainImportCycle) Code() string        { return "lint.domain_import_cycle" }

func (r ruleDomainImportCycle) Check(reg *filesystem.Registry, _ []filesystem.ArtifactRecord) []Diagnostic {
	if reg == nil {
		return nil
	}
	// Build domain → []domain edges.
	graph := map[string][]string{}
	for _, layer := range reg.Layers {
		for path, dom := range walkDomainsInLayer(layer) {
			for _, pattern := range dom.Include {
				if other := domainForPattern(pattern); other != "" && other != path {
					graph[path] = append(graph[path], other)
				}
			}
		}
	}
	cycles := findCycles(graph)
	out := make([]Diagnostic, 0, len(cycles))
	for _, cyc := range cycles {
		out = append(out, Diagnostic{
			ArtifactID: cyc[0],
			Code:       r.Code(),
			Severity:   SeverityWarning,
			Message:    fmt.Sprintf("DOMAIN.md import cycle: %s", strings.Join(cyc, " -> ")),
		})
	}
	return out
}

// walkDomainsInLayer returns every parsed DOMAIN.md keyed by the
// canonical domain path (relative to the layer root, slash-separated).
// Parse failures are silently skipped — manifest-parse rules cover
// those.
func walkDomainsInLayer(layer filesystem.Layer) map[string]*manifest.Domain {
	out := map[string]*manifest.Domain{}
	_ = filepath.WalkDir(layer.Path, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}
		if d.Name() != "DOMAIN.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		dom, err := manifest.ParseDomain(data)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(layer.Path, filepath.Dir(path))
		if err != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if key == "." {
			key = ""
		}
		out[key] = dom
		return nil
	})
	return out
}

// importPatternResolves reports whether pattern matches at least
// one artifact ID. Honors the §4.5.2 glob syntax (`*` one
// segment, `**` recursive, `{a,b}` alternatives).
func importPatternResolves(pattern string, ids []string) bool {
	for _, id := range ids {
		if globMatchesID(pattern, id) {
			return true
		}
	}
	return false
}

// domainForPattern returns the directory prefix the pattern
// targets: e.g., "finance/ap/payments/*" targets the
// "finance/ap/payments" domain. Returns "" when the pattern is
// degenerate (purely recursive at root).
func domainForPattern(pattern string) string {
	parts := strings.Split(pattern, "/")
	out := []string{}
	for _, p := range parts {
		if strings.ContainsAny(p, "*{") {
			break
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}

// findCycles returns every cycle in the directed graph as an
// ordered list of nodes. Trivial self-loops are excluded.
func findCycles(graph map[string][]string) [][]string {
	var cycles [][]string
	visited := map[string]int{}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	stack := []string{}
	var visit func(node string) bool
	visit = func(node string) bool {
		if visited[node] == gray {
			cycle := append([]string{}, stack...)
			cycle = append(cycle, node)
			cycles = append(cycles, cycle)
			return true
		}
		if visited[node] == black {
			return false
		}
		visited[node] = gray
		stack = append(stack, node)
		for _, n := range graph[node] {
			if visit(n) {
				// continue to surface multiple cycles
			}
		}
		visited[node] = black
		stack = stack[:len(stack)-1]
		return false
	}
	keys := make([]string, 0, len(graph))
	for k := range graph {
		keys = append(keys, k)
	}
	for _, k := range keys {
		visit(k)
	}
	return cycles
}

// globMatchesID is a small reimplementation of the §4.5.2 glob
// matcher — duplicated here from pkg/sync/scope.go so pkg/lint
// does not import pkg/sync (which imports pkg/lint already).
// Supports `*` (one segment), `**` (recursive), `{a,b}`
// (alternatives).
func globMatchesID(pattern, id string) bool {
	for _, alt := range expandAlternatives(pattern) {
		if globMatchSimple(alt, id) {
			return true
		}
	}
	return false
}

func expandAlternatives(pattern string) []string {
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
	choices := strings.Split(pattern[open+1:close], ",")
	out := []string{}
	for _, c := range choices {
		for _, expanded := range expandAlternatives(prefix + c + suffix) {
			out = append(out, expanded)
		}
	}
	return out
}

func globMatchSimple(pattern, target string) bool {
	patSegs := strings.Split(pattern, "/")
	tgtSegs := strings.Split(target, "/")
	return globSegments(patSegs, tgtSegs)
}

func globSegments(pat, tgt []string) bool {
	for i := 0; i < len(pat); i++ {
		seg := pat[i]
		if seg == "**" {
			if i == len(pat)-1 {
				return true
			}
			rest := pat[i+1:]
			for j := 0; j <= len(tgt); j++ {
				if globSegments(rest, tgt[j:]) {
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
