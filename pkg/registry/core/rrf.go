package core

import "sort"

// RRFFuse combines two or more ranked lists into a single ranking
// via Reciprocal Rank Fusion (Cormack/Clarke 2009). Each list is a
// slice of artifact IDs ordered by descending relevance; the same
// ID may appear in multiple lists.
//
// score(d) = Σ 1 / (k + rank_i(d))
//
// where k=60 per Cormack/Clarke. Items absent from a list contribute
// nothing from that list. The fused order is descending score.
//
// RRFFuse is used by §4.7 hybrid retrieval to combine BM25 ranks
// with vector-similarity ranks. The same function works for any
// number of input lists; callers pass two for the canonical hybrid
// case but more is fine.
func RRFFuse(lists ...[]string) []string {
	const k = 60.0
	score := map[string]float64{}
	first := map[string]int{}
	for _, list := range lists {
		for rank, id := range list {
			score[id] += 1.0 / (k + float64(rank+1))
			if _, ok := first[id]; !ok {
				first[id] = rank
			}
		}
	}
	out := make([]string, 0, len(score))
	for id := range score {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := score[out[i]], score[out[j]]
		if si != sj {
			return si > sj
		}
		// Stable tiebreak: lower first-list rank wins, then alpha.
		if first[out[i]] != first[out[j]] {
			return first[out[i]] < first[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}
