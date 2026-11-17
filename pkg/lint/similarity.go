package lint

// Exact set-similarity join shared by the §7.3.1 overlap advisories
// (lint.colliding_descriptions and lint.near_duplicate_content).
//
// Tokens are interned to dense IDs rather than hashed, so two distinct words
// or shingles never share an ID and the similarity of two ID sets equals the
// similarity of the sets they stand for. Identical sets are grouped before the
// join, so a file bundled unchanged in N artifacts costs O(N) rather than
// N(N-1)/2 pair verifications. The join itself is an all-pairs prefix-filtered
// Jaccard join: every pair at or above the threshold is found, and no pair is
// estimated.
//
// Cost: time is proportional to the total number of words plus the posting
// entries the prefix filter probes and the candidate group pairs it verifies;
// memory to the total number of words plus the group pairs at or above the
// threshold. N units that are near-identical but pairwise distinct form N
// groups and up to N(N-1)/2 pairs, which §7.3.1 accepts by reporting every
// pair.
//
// Spec: §7.3.1

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// shingleWidth is the §7.3.1 shingle length in words.
const shingleWidth = 5

// maxListedPeers is the number of peers a diagnostic names before it
// abbreviates the rest to a count (§7.3.1).
const maxListedPeers = 10

// filterSlack widens every prefix and length filter bound. The bounds are
// computed in float64, where 0.55*100 is 55.00000000000001, so an unslackened
// bound would drop a pair whose similarity is exactly the threshold. The slack
// can only admit extra candidates; the exact test in verification decides.
const filterSlack = 1e-9

// interner assigns dense uint32 IDs in first-seen order to normalized words
// and to five-word shingles keyed by their word IDs. A rule builds one per
// Check call and drops it when the call returns.
type interner struct {
	wordIDs    map[string]uint32
	shingleIDs map[[shingleWidth]uint32]uint32
}

func newInterner() *interner {
	return &interner{
		wordIDs:    map[string]uint32{},
		shingleIDs: map[[shingleWidth]uint32]uint32{},
	}
}

// wordTokens returns the §7.3.1 words of s: the space-separated parts of its
// normalized text.
func wordTokens(s string) []string {
	return strings.Fields(normalizeDescription(s))
}

// wordID interns one word.
func (in *interner) wordID(w string) uint32 {
	id, ok := in.wordIDs[w]
	if !ok {
		id = uint32(len(in.wordIDs))
		in.wordIDs[w] = id
	}
	return id
}

// words returns the sorted, deduplicated IDs of ws.
func (in *interner) words(ws []string) []uint32 {
	out := make([]uint32, len(ws))
	for i, w := range ws {
		out[i] = in.wordID(w)
	}
	return sortedUnique(out)
}

// shingles returns the sorted, deduplicated IDs of each run of five
// consecutive words of ws. Fewer than five words yield no shingle.
func (in *interner) shingles(ws []string) []uint32 {
	if len(ws) < shingleWidth {
		return nil
	}
	ids := make([]uint32, len(ws))
	for i, w := range ws {
		ids[i] = in.wordID(w)
	}
	out := make([]uint32, 0, len(ids)-shingleWidth+1)
	for i := 0; i+shingleWidth <= len(ids); i++ {
		var key [shingleWidth]uint32
		copy(key[:], ids[i:i+shingleWidth])
		id, ok := in.shingleIDs[key]
		if !ok {
			id = uint32(len(in.shingleIDs))
			in.shingleIDs[key] = id
		}
		out = append(out, id)
	}
	return sortedUnique(out)
}

func sortedUnique(ids []uint32) []uint32 {
	slices.Sort(ids)
	return slices.Compact(ids)
}

// tokenSet is one compared unit: owner identifies the artifact (a record
// index), and tokens is sorted and deduplicated.
type tokenSet struct {
	owner  int
	tokens []uint32
}

// setGroup holds the sets whose token slices are equal. members are
// ascending indexes into the sets slice; every two members have
// similarity 1.
type setGroup struct {
	tokens  []uint32
	members []int
}

// groupIdentical groups the sets whose token slices are equal, compared
// element by element. An empty set joins no group and so never pairs.
// Groups are ordered by their first member.
func groupIdentical(sets []tokenSet) []setGroup {
	order := make([]int, 0, len(sets))
	for i, s := range sets {
		if len(s.tokens) > 0 {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return slices.Compare(sets[order[a]].tokens, sets[order[b]].tokens) < 0
	})
	var groups []setGroup
	for k := 0; k < len(order); {
		end := k + 1
		for end < len(order) && slices.Equal(sets[order[k]].tokens, sets[order[end]].tokens) {
			end++
		}
		// The stable sort keeps equal sets in ascending index order.
		members := slices.Clone(order[k:end])
		groups = append(groups, setGroup{tokens: sets[members[0]].tokens, members: members})
		k = end
	}
	sort.Slice(groups, func(a, b int) bool { return groups[a].members[0] < groups[b].members[0] })
	return groups
}

// scoredPair is one pair of groups at or above the threshold: i < j index
// the groups, and inter and union are the exact intersection and union
// sizes of their token sets.
type scoredPair struct {
	i, j, inter, union int
}

// similarPairs returns every pair of groups whose token sets have Jaccard
// similarity of t or higher, sorted by (i, j). Two distinct groups never
// reach similarity 1, so at t = 1.0 it returns no pair, and identical sets
// are reported through their group alone. It checks ctx between groups and
// returns its error on cancellation.
func similarPairs(ctx context.Context, groups []setGroup, t float64) ([]scoredPair, error) {
	ranked := rankTokens(groups)
	tf := t - filterSlack
	order := make([]int, len(groups))
	for i := range order {
		order[i] = i
	}
	// Processing in ascending size lets each set probe only the smaller or
	// equal sets indexed before it, so the length filter is one-sided.
	sort.SliceStable(order, func(a, b int) bool { return len(ranked[order[a]]) < len(ranked[order[b]]) })

	index := map[uint32][]int{}
	seen := make([]int, len(groups))
	for i := range seen {
		seen[i] = -1
	}
	var pairs []scoredPair
	for _, x := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		xs := ranked[x]
		prefix := prefixLen(len(xs), tf)
		for _, tok := range xs[:prefix] {
			for _, y := range index[tok] {
				if seen[y] == x {
					continue
				}
				seen[y] = x
				ys := ranked[y]
				if tf*float64(len(xs)) > float64(len(ys)) || tf*float64(len(ys)) > float64(len(xs)) {
					continue
				}
				inter := intersectSize(xs, ys)
				union := len(xs) + len(ys) - inter
				if float64(inter)/float64(union) >= t {
					pairs = append(pairs, orderedPair(x, y, inter, union))
				}
			}
			index[tok] = append(index[tok], x)
		}
	}
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a].i != pairs[b].i {
			return pairs[a].i < pairs[b].i
		}
		return pairs[a].j < pairs[b].j
	})
	return pairs, nil
}

func orderedPair(x, y, inter, union int) scoredPair {
	if y < x {
		x, y = y, x
	}
	return scoredPair{i: x, j: y, inter: inter, union: union}
}

// prefixLen is the prefix-filter length |x| - ceil(tf*|x|) + 1, clamped to
// [1, |x|]. Two sets at similarity t or higher share a token within their
// prefixes of this length under the global token order.
func prefixLen(n int, tf float64) int {
	p := n - int(math.Ceil(tf*float64(n))) + 1
	return max(1, min(p, n))
}

// rankTokens rewrites each group's tokens as ranks in the global order of
// ascending document frequency and then ID, sorted ascending. The rank is a
// bijection on tokens, so intersection sizes are unchanged, and rare tokens
// come first so the prefixes probe short posting lists.
func rankTokens(groups []setGroup) [][]uint32 {
	df := map[uint32]int{}
	for _, g := range groups {
		for _, tok := range g.tokens {
			df[tok]++
		}
	}
	toks := make([]uint32, 0, len(df))
	for tok := range df {
		toks = append(toks, tok)
	}
	sort.Slice(toks, func(a, b int) bool {
		if df[toks[a]] != df[toks[b]] {
			return df[toks[a]] < df[toks[b]]
		}
		return toks[a] < toks[b]
	})
	rank := make(map[uint32]uint32, len(toks))
	for r, tok := range toks {
		rank[tok] = uint32(r)
	}
	out := make([][]uint32, len(groups))
	for i, g := range groups {
		rs := make([]uint32, len(g.tokens))
		for k, tok := range g.tokens {
			rs[k] = rank[tok]
		}
		slices.Sort(rs)
		out[i] = rs
	}
	return out
}

// intersectSize counts the common elements of two ascending slices.
func intersectSize(a, b []uint32) int {
	n, i, j := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			n++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return n
}

// peer is one set a summary names, with its similarity percentage rounded
// down: 100 * inter / union in integer arithmetic, and 100 within a group.
type peer struct {
	set int
	pct int
}

// groupEdge links a group to a group the join paired it with.
type groupEdge struct {
	group, inter, union int
}

// peerIndex answers, for each set, which sets it overlaps. It is built once
// per Check call from the groups and the pairs of the join.
type peerIndex struct {
	sets    []tokenSet
	groupOf []int
	// byIdent holds each group's members sorted by the caller's identifier.
	byIdent [][]int
	// ownerCount counts each group's members per owner, so a summary can
	// exclude the set's own artifact without scanning the group.
	ownerCount []map[int]int
	// edges lists each group's paired groups by descending similarity.
	edges [][]groupEdge
	ident func(set int) string
}

// newPeerIndex builds the peer index. ident names a set; peers of equal
// similarity are ordered by it.
func newPeerIndex(sets []tokenSet, groups []setGroup, pairs []scoredPair, ident func(set int) string) *peerIndex {
	p := &peerIndex{
		sets:       sets,
		groupOf:    make([]int, len(sets)),
		byIdent:    make([][]int, len(groups)),
		ownerCount: make([]map[int]int, len(groups)),
		edges:      make([][]groupEdge, len(groups)),
		ident:      ident,
	}
	for i := range p.groupOf {
		p.groupOf[i] = -1
	}
	for g, grp := range groups {
		p.ownerCount[g] = map[int]int{}
		for _, m := range grp.members {
			p.groupOf[m] = g
			p.ownerCount[g][sets[m].owner]++
		}
		p.byIdent[g] = slices.Clone(grp.members)
		p.sortByIdent(p.byIdent[g])
	}
	for _, pr := range pairs {
		p.edges[pr.i] = append(p.edges[pr.i], groupEdge{group: pr.j, inter: pr.inter, union: pr.union})
		p.edges[pr.j] = append(p.edges[pr.j], groupEdge{group: pr.i, inter: pr.inter, union: pr.union})
	}
	for _, es := range p.edges {
		sort.SliceStable(es, func(a, b int) bool { return compareSimilarity(es[a], es[b]) > 0 })
	}
	return p
}

// compareSimilarity compares inter/union exactly by cross-multiplication.
func compareSimilarity(a, b groupEdge) int {
	l, r := a.inter*b.union, b.inter*a.union
	switch {
	case l > r:
		return 1
	case l < r:
		return -1
	}
	return 0
}

// sortByIdent orders members by identifier. Both rules supply unique
// identifiers (an artifact ID, or an artifact ID and a unit name), so the
// index tie-break never decides an order the caller observes; it only keeps
// the sort total.
func (p *peerIndex) sortByIdent(members []int) {
	sort.SliceStable(members, func(a, b int) bool {
		ia, ib := p.ident(members[a]), p.ident(members[b])
		if ia != ib {
			return ia < ib
		}
		return members[a] < members[b]
	})
}

// summary returns the first 10 peers of set s and the total number of its
// peers. The peers are the other members of its group, at similarity 1, and
// the members of every group the join paired with its group, excluding every
// set of s's owner. They are ordered by descending similarity and then by
// identifier.
func (p *peerIndex) summary(s int) ([]peer, int) {
	g := p.groupOf[s]
	if g < 0 {
		// An empty set joins no group. Both rules skip empty sets before
		// the join, so this guards direct callers only.
		return nil, 0
	}
	owner := p.sets[s].owner
	var first []peer
	total := 0
	for _, t := range p.tiers(g) {
		for _, grp := range t.groups {
			total += p.eligible(grp, owner)
		}
		first = p.take(first, t.groups, owner, 100*t.inter/t.union)
	}
	return first, total
}

// rankedPeer is one peer of a set with its exact similarity.
type rankedPeer struct {
	set        int
	similarity float64
}

// all returns every peer of set s in the order summary lists them, each with
// its exact similarity: 1 within s's group and inter/union across groups.
func (p *peerIndex) all(s int) []rankedPeer {
	g := p.groupOf[s]
	if g < 0 {
		return nil
	}
	owner := p.sets[s].owner
	var out []rankedPeer
	for _, t := range p.tiers(g) {
		sim := float64(t.inter) / float64(t.union)
		for _, m := range p.tierMembers(t.groups, owner, -1) {
			out = append(out, rankedPeer{set: m, similarity: sim})
		}
	}
	return out
}

// peerTier is a run of groups paired with one group at an equal similarity,
// inter/union. A group's own tier has inter equal to union.
type peerTier struct {
	groups       []int
	inter, union int
}

// tiers returns group g itself and then its paired groups, gathered into runs
// of equal similarity in descending order.
func (p *peerIndex) tiers(g int) []peerTier {
	out := []peerTier{{groups: []int{g}, inter: 1, union: 1}}
	es := p.edges[g]
	for k := 0; k < len(es); {
		t := peerTier{groups: []int{es[k].group}, inter: es[k].inter, union: es[k].union}
		end := k + 1
		for end < len(es) && compareSimilarity(es[k], es[end]) == 0 {
			t.groups = append(t.groups, es[end].group)
			end++
		}
		out = append(out, t)
		k = end
	}
	return out
}

// eligible counts the members of group g that another owner holds.
func (p *peerIndex) eligible(g, owner int) int {
	return len(p.byIdent[g]) - p.ownerCount[g][owner]
}

// take appends, up to the 10-peer limit, the members of the equal-similarity
// groups in tier that owner does not hold, ordered by identifier.
func (p *peerIndex) take(first []peer, tier []int, owner, pct int) []peer {
	need := maxListedPeers - len(first)
	if need <= 0 {
		return first
	}
	cand := p.tierMembers(tier, owner, need)
	if len(cand) > need {
		cand = cand[:need]
	}
	for _, m := range cand {
		first = append(first, peer{set: m, pct: pct})
	}
	return first
}

// tierMembers returns the members of the groups in tier that owner does not
// hold, ordered by identifier, taking at most perGroup from each group. A
// negative perGroup takes every member.
func (p *peerIndex) tierMembers(tier []int, owner, perGroup int) []int {
	var cand []int
	for _, g := range tier {
		n := 0
		for _, m := range p.byIdent[g] {
			if n == perGroup {
				break
			}
			if p.sets[m].owner == owner {
				continue
			}
			cand = append(cand, m)
			n++
		}
	}
	p.sortByIdent(cand)
	return cand
}

// formatPeers renders "<name> (<pct>%)" for each listed peer, joined by
// commas, followed by "and <n> more" when total exceeds the listed count.
func formatPeers(first []peer, total int, name func(set int) string) string {
	parts := make([]string, 0, len(first)+1)
	for _, pr := range first {
		parts = append(parts, fmt.Sprintf("%s (%d%%)", name(pr.set), pr.pct))
	}
	if rest := total - len(first); rest > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", rest))
	}
	return strings.Join(parts, ", ")
}
