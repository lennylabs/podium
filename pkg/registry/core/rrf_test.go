package core_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// Spec: §4.7 — RRF combines two ranked lists; an item that appears
// near the top of both lists outranks an item that appears in only
// one.
// Phase: 5
func TestRRFFuse_ConsensusBeatsSinglePresence(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	lex := []string{"a", "b", "c", "d"}
	vec := []string{"b", "a", "e", "f"}
	got := core.RRFFuse(lex, vec)
	// "a" and "b" appear in both lists near the top → top 2.
	if got[0] != "b" && got[0] != "a" {
		t.Errorf("top1 = %q, want a or b", got[0])
	}
	if got[1] != "a" && got[1] != "b" {
		t.Errorf("top2 = %q, want a or b", got[1])
	}
}

// Spec: §4.7 — fusing identical lists returns the same order.
// Phase: 5
func TestRRFFuse_IdenticalListsAreStable(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	list := []string{"x", "y", "z"}
	got := core.RRFFuse(list, list)
	if !reflect.DeepEqual(got, list) {
		t.Errorf("got %v, want %v", got, list)
	}
}

// Spec: §4.7 — single-list fusion is a passthrough; useful when
// vector search is unavailable and the registry degrades to BM25.
// Phase: 5
func TestRRFFuse_SingleListIsPassthrough(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	list := []string{"a", "b", "c"}
	got := core.RRFFuse(list)
	if !reflect.DeepEqual(got, list) {
		t.Errorf("got %v, want %v", got, list)
	}
}

// Spec: §4.7 — empty lists fuse to empty.
// Phase: 5
func TestRRFFuse_EmptyInputIsEmptyOutput(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	if got := core.RRFFuse(); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
	if got := core.RRFFuse([]string{}, []string{}); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
