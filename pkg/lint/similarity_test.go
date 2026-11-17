package lint

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

// seqTokens returns the tokens from..to-1.
func seqTokens(from, to int) []uint32 {
	out := make([]uint32, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, uint32(i))
	}
	return out
}

// singletonGroups wraps each token slice in a one-member group.
func singletonGroups(sets ...[]uint32) []setGroup {
	out := make([]setGroup, len(sets))
	for i, s := range sets {
		out[i] = setGroup{tokens: s, members: []int{i}}
	}
	return out
}

func mustPairs(t *testing.T, groups []setGroup, th float64) []scoredPair {
	t.Helper()
	pairs, err := similarPairs(context.Background(), groups, th)
	if err != nil {
		t.Fatalf("similarPairs: %v", err)
	}
	return pairs
}

// Spec: §7.3.1 — "Every pair at or above its threshold is reported." A pair
// whose similarity equals the threshold exactly is reported, and a pair one
// token short is not. The 0.55 case fails against filter bounds computed
// without slack, because 0.55*100 is 55.00000000000001 in float64.
func TestSimilarPairs_ThresholdBoundary(t *testing.T) {
	t.Parallel()
	for _, th := range []float64{0.5, 0.55, 0.7, 0.8, 0.85} {
		x := seqTokens(0, 100)
		n := int(th*100 + 0.5)
		at := mustPairs(t, singletonGroups(x, seqTokens(0, n)), th)
		if len(at) != 1 || at[0].inter != n || at[0].union != 100 {
			t.Errorf("t=%v: pair at exactly t not returned: %+v", th, at)
		}
		if short := mustPairs(t, singletonGroups(x, seqTokens(0, n-1)), th); len(short) != 0 {
			t.Errorf("t=%v: pair one token short of t returned: %+v", th, short)
		}
	}
}

// Spec: §7.3.1 — two distinct sets never reach similarity 1, so at a
// threshold of 1.0 the join pairs no distinct groups.
func TestSimilarPairs_NoPairAtOne(t *testing.T) {
	t.Parallel()
	if pairs := mustPairs(t, singletonGroups(seqTokens(0, 50), seqTokens(0, 51)), 1.0); len(pairs) != 0 {
		t.Errorf("distinct groups paired at t=1.0: %+v", pairs)
	}
}

// Spec: §7.3.1 — identical sets form one group with ascending members, an
// empty set joins no group, and sets that differ by one token form two
// groups ordered by their first member.
func TestGroupIdentical(t *testing.T) {
	t.Parallel()
	sets := []tokenSet{
		{owner: 0, tokens: []uint32{3, 4, 5}},
		{owner: 1, tokens: []uint32{1, 2}},
		{owner: 2, tokens: nil},
		{owner: 3, tokens: []uint32{3, 4, 5}},
		{owner: 4, tokens: []uint32{3, 4, 6}},
		{owner: 5, tokens: []uint32{3, 4, 5}},
	}
	got := groupIdentical(sets)
	want := [][]int{{0, 3, 5}, {1}, {4}}
	if len(got) != len(want) {
		t.Fatalf("groups = %+v, want members %v", got, want)
	}
	for i, g := range got {
		if !slices.Equal(g.members, want[i]) {
			t.Errorf("group %d members = %v, want %v", i, g.members, want[i])
		}
		if !slices.Equal(g.tokens, sets[want[i][0]].tokens) {
			t.Errorf("group %d tokens = %v", i, g.tokens)
		}
	}
}

// randomSets builds n sets around a few base sets so that many pairs sit
// near each threshold, with sizes from 1 to 200 tokens.
func randomSets(rng *rand.Rand, n int) [][]uint32 {
	bases := make([][]uint32, 20)
	for i := range bases {
		size := 1 + rng.Intn(200)
		perm := rng.Perm(400)[:size]
		b := make([]uint32, size)
		for k, v := range perm {
			b[k] = uint32(v)
		}
		bases[i] = sortedUnique(b)
	}
	out := make([][]uint32, n)
	for i := range out {
		base := bases[rng.Intn(len(bases))]
		var s []uint32
		for _, tok := range base {
			if rng.Intn(10) != 0 {
				s = append(s, tok)
			}
		}
		for k := rng.Intn(5); k > 0; k-- {
			s = append(s, uint32(rng.Intn(400)))
		}
		if len(s) == 0 {
			s = []uint32{base[0]}
		}
		if len(s) > 200 {
			s = s[:200]
		}
		out[i] = sortedUnique(s)
	}
	return out
}

func asTokenSets(raw [][]uint32) []tokenSet {
	out := make([]tokenSet, len(raw))
	for i, r := range raw {
		out[i] = tokenSet{owner: i, tokens: r}
	}
	return out
}

// Spec: §7.3.1 — "the same input produces the same diagnostics on every
// run." Shuffling the input yields the same pairs once the indexes are
// mapped back to the original sets.
func TestSimilarPairs_OrderIndependent(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(7))
	raw := randomSets(rng, 150)
	key := func(sets []tokenSet, orig func(int) int) map[[2]int]scoredPair {
		groups := groupIdentical(sets)
		out := map[[2]int]scoredPair{}
		for _, p := range mustPairs(t, groups, 0.7) {
			a, b := orig(groups[p.i].members[0]), orig(groups[p.j].members[0])
			if a > b {
				a, b = b, a
			}
			out[[2]int{a, b}] = scoredPair{inter: p.inter, union: p.union}
		}
		return out
	}
	want := key(asTokenSets(raw), func(i int) int { return i })
	perm := rng.Perm(len(raw))
	shuffled := make([][]uint32, len(raw))
	for i, p := range perm {
		shuffled[i] = raw[p]
	}
	got := key(asTokenSets(shuffled), func(i int) int { return perm[i] })
	if len(want) == 0 {
		t.Fatalf("fixture produced no pair at 0.7")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shuffled input changed the pairs: got %d, want %d", len(got), len(want))
	}
}

// Spec: §7.3.1 — the join is exact: it returns the same pairs as an
// all-pairs scan at every threshold.
func TestSimilarPairs_MatchesBruteForce(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))
	groups := groupIdentical(asTokenSets(randomSets(rng, 200)))
	for _, th := range []float64{0.5, 0.55, 0.7, 0.8, 0.85, 0.9} {
		var want []scoredPair
		for i := range groups {
			for j := i + 1; j < len(groups); j++ {
				inter := intersectSize(groups[i].tokens, groups[j].tokens)
				union := len(groups[i].tokens) + len(groups[j].tokens) - inter
				if float64(inter)/float64(union) >= th {
					want = append(want, scoredPair{i: i, j: j, inter: inter, union: union})
				}
			}
		}
		got := mustPairs(t, groups, th)
		if len(want) == 0 {
			t.Errorf("t=%v: fixture produced no pair", th)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("t=%v: join returned %d pairs, brute force %d", th, len(got), len(want))
		}
	}
}

// Spec: §7.3.1 — a cancelled context stops the join with its error.
func TestSimilarPairs_Cancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := similarPairs(ctx, singletonGroups(seqTokens(0, 10), seqTokens(0, 10)), 0.8)
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// Spec: §7.3.1 — "A unit's shingles are the sequences of five consecutive
// words of its normalized text." Normalization folds case and punctuation
// before interning, and distinct shingles receive distinct IDs.
func TestInterner_FiveWordWindows(t *testing.T) {
	t.Parallel()
	in := newInterner()
	if got := in.shingles(wordTokens("one two three four five six")); len(got) != 2 {
		t.Errorf("6-word text gave %d shingles, want 2", len(got))
	}
	if got := in.shingles(wordTokens("one two three four")); len(got) != 0 {
		t.Errorf("4-word text gave shingles %v", got)
	}
	a := in.words(wordTokens("Close the books."))
	b := in.words(wordTokens("close the books"))
	if !slices.Equal(a, b) || len(a) != 3 {
		t.Errorf("normalized words differ: %v vs %v", a, b)
	}
	seen := map[uint32]string{}
	text := wordTokens("a b c d e f a b c d e g b c d e f a")
	for i := 0; i+shingleWidth <= len(text); i++ {
		window := fmt.Sprint(text[i : i+shingleWidth])
		id := in.shingles(text[i : i+shingleWidth])[0]
		if prev, ok := seen[id]; ok && prev != window {
			t.Errorf("shingles %s and %s share ID %d", prev, window, id)
		}
		seen[id] = window
	}
}

// Spec: §7.3.1 — "A diagnostic names ... the artifacts it overlaps ..., in
// order of descending similarity and then by identifier", abbreviated past
// 10 peers, and two units of one artifact are never peers.
func TestPeerSummary_OrderCountAndOwner(t *testing.T) {
	t.Parallel()
	t.Run("one group of 500", func(t *testing.T) {
		t.Parallel()
		sets := make([]tokenSet, 500)
		for i := range sets {
			sets[i] = tokenSet{owner: i, tokens: []uint32{1, 2, 3}}
		}
		// Identifiers run opposite to the index order.
		ident := func(s int) string { return fmt.Sprintf("id%03d", 499-s) }
		groups := groupIdentical(sets)
		idx := newPeerIndex(sets, groups, nil, ident)
		for _, s := range []int{0, 250, 499} {
			first, total := idx.summary(s)
			if total != 499 || len(first) != maxListedPeers {
				t.Fatalf("set %d: %d peers listed of %d", s, len(first), total)
			}
			for k := 1; k < len(first); k++ {
				if ident(first[k-1].set) >= ident(first[k].set) {
					t.Errorf("set %d: peers out of identifier order at %d", s, k)
				}
			}
			for _, p := range first {
				if p.set == s || p.pct != 100 {
					t.Errorf("set %d: bad peer %+v", s, p)
				}
			}
		}
	})
	t.Run("same owner excluded", func(t *testing.T) {
		t.Parallel()
		sets := []tokenSet{
			{owner: 0, tokens: []uint32{1, 2}},
			{owner: 0, tokens: []uint32{1, 2}},
			{owner: 1, tokens: []uint32{1, 2}},
		}
		idx := newPeerIndex(sets, groupIdentical(sets), nil, func(s int) string { return fmt.Sprint(s) })
		first, total := idx.summary(0)
		if total != 1 || len(first) != 1 || first[0].set != 2 {
			t.Errorf("summary(0) = %+v, %d; want only set 2", first, total)
		}
	})
	t.Run("similarity before identifier", func(t *testing.T) {
		t.Parallel()
		sets := []tokenSet{
			{owner: 0, tokens: seqTokens(0, 10)},
			{owner: 1, tokens: seqTokens(0, 9)},
			{owner: 2, tokens: seqTokens(0, 8)},
		}
		names := []string{"m", "z", "a"}
		groups := groupIdentical(sets)
		pairs := mustPairs(t, groups, 0.5)
		idx := newPeerIndex(sets, groups, pairs, func(s int) string { return names[s] })
		first, total := idx.summary(0)
		got := formatPeers(first, total, func(s int) string { return names[s] })
		if got != "z (90%), a (80%)" {
			t.Errorf("peers = %q, want %q", got, "z (90%), a (80%)")
		}
	})
}
