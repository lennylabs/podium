// Package dependency implements the cross-type dependency graph and
// reverse index from spec §4.7.3 plus the impact-analysis surface
// from §4.7.5. Edges span types: extends, delegates_to, mcpServers.
package dependency

import "sort"

// EdgeKind enumerates the §4.7.3 edge types.
type EdgeKind string

// EdgeKind values.
const (
	EdgeExtends     EdgeKind = "extends"
	EdgeDelegatesTo EdgeKind = "delegates_to"
	EdgeMCPServer   EdgeKind = "mcpServers"
)

// Edge is one (from -> to) relation, with the discriminating kind.
type Edge struct {
	From string
	To   string
	Kind EdgeKind
}

// Graph is an adjacency-list structure with both forward and reverse
// indexes. Concurrent writes are not allowed; callers serialize.
type Graph struct {
	out map[string][]Edge // out[from] = edges leaving from
	in  map[string][]Edge // in[to] = edges arriving at to
}

// NewGraph returns an empty Graph.
func NewGraph() *Graph {
	return &Graph{
		out: map[string][]Edge{},
		in:  map[string][]Edge{},
	}
}

// AddEdge inserts an edge.
func (g *Graph) AddEdge(e Edge) {
	g.out[e.From] = append(g.out[e.From], e)
	g.in[e.To] = append(g.in[e.To], e)
}

// DependentsOf returns every edge whose To is artifactID. Result is
// sorted by From / Kind so output is deterministic.
func (g *Graph) DependentsOf(artifactID string) []Edge {
	out := append([]Edge{}, g.in[artifactID]...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// ImpactSet computes the transitive set of artifact IDs that depend on
// the seed, traversing in-edges. Used by `podium vuln explain` and the
// admin impact-analysis CLI (§4.7.5).
func (g *Graph) ImpactSet(seed string) []string {
	seen := map[string]bool{seed: true}
	queue := []string{seed}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.in[cur] {
			if seen[e.From] {
				continue
			}
			seen[e.From] = true
			queue = append(queue, e.From)
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		if id == seed {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
