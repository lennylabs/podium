package dependency

import (
	"testing"
)

// Spec: §4.7.3 — DependentsOf returns every edge ending at the artifact,
// covering extends, delegates_to, and mcpServers edges.
func TestDependentsOf_AllEdgeKinds(t *testing.T) {
	t.Parallel()
	g := NewGraph()
	g.AddEdge(Edge{From: "child-skill", To: "parent", Kind: EdgeExtends})
	g.AddEdge(Edge{From: "agent1", To: "parent", Kind: EdgeDelegatesTo})
	g.AddEdge(Edge{From: "skill-with-mcp", To: "parent", Kind: EdgeMCPServer})
	g.AddEdge(Edge{From: "irrelevant", To: "other", Kind: EdgeExtends})

	got := g.DependentsOf("parent")
	if len(got) != 3 {
		t.Fatalf("got %d edges, want 3: %+v", len(got), got)
	}
	for _, want := range []string{"agent1", "child-skill", "skill-with-mcp"} {
		found := false
		for _, e := range got {
			if e.From == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing dependent %q in %+v", want, got)
		}
	}
}

// Spec: §4.7.3 / §4.7.5 — ImpactSet traverses in-edges transitively so
// admin impact analysis surfaces the full reverse-dependency tree.
func TestImpactSet_Transitive(t *testing.T) {
	t.Parallel()
	g := NewGraph()
	g.AddEdge(Edge{From: "a", To: "b", Kind: EdgeExtends})
	g.AddEdge(Edge{From: "b", To: "c", Kind: EdgeExtends})
	g.AddEdge(Edge{From: "d", To: "b", Kind: EdgeDelegatesTo})

	got := g.ImpactSet("c")
	want := []string{"a", "b", "d"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] got %q, want %q", i, got[i], w)
		}
	}
}
