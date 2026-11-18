package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/store"
)

// Page sizes of the §8.7 report's artifact list.
const (
	UsageDefaultLimit = 50
	UsageMaxLimit     = 200
)

// UsageMaxQuery is the longest §8.7 query text, in characters.
const UsageMaxQuery = 200

// The §8.7 sort keys and directions of the artifact list.
const (
	UsageSortLoads      = "loads"
	UsageSortSyncs      = "syncs"
	UsageSortSearchHits = "search_hits"
	UsageSortLastLoaded = "last_loaded"
	UsageOrderDesc      = "desc"
	UsageOrderAsc       = "asc"
)

// usageKey is what orders one artifact entry: its counts, its latest day with
// a load ("" for none in the window), and its ID.
type usageKey struct {
	loads, syncs, hits int64
	lastLoaded, id     string
}

// usageOrder is a validated sort key and direction.
type usageOrder struct {
	sort string
	asc  bool
}

// before reports whether a sorts before b: by the sort key in the order's
// direction, with an artifact that has no load placed last under last_loaded
// in either direction, then by the default order for a tie.
//
// Spec: §8.7
func (o usageOrder) before(a, b usageKey) bool {
	switch o.sort {
	case UsageSortSyncs:
		if a.syncs != b.syncs {
			return (a.syncs < b.syncs) == o.asc
		}
	case UsageSortSearchHits:
		if a.hits != b.hits {
			return (a.hits < b.hits) == o.asc
		}
	case UsageSortLastLoaded:
		if a.lastLoaded != b.lastLoaded {
			if a.lastLoaded == "" || b.lastLoaded == "" {
				return b.lastLoaded == ""
			}
			return (a.lastLoaded < b.lastLoaded) == o.asc
		}
	default:
		if a.loads != b.loads {
			return (a.loads < b.loads) == o.asc
		}
	}
	return defaultBefore(a, b)
}

// defaultBefore is the §8.7 default order: loads, syncs, and search hits, each
// descending, then the artifact ID ascending.
func defaultBefore(a, b usageKey) bool {
	switch {
	case a.loads != b.loads:
		return a.loads > b.loads
	case a.syncs != b.syncs:
		return a.syncs > b.syncs
	case a.hits != b.hits:
		return a.hits > b.hits
	}
	return a.id < b.id
}

// usageCursor is the opaque next_cursor: the sort and direction it was issued
// under and the last entry's key. The order is a ranking by counts, so the
// position is the sort key itself; an offset would shift as soon as an
// artifact's counts changed rank. The sort and direction are recorded because
// a key from one order names no position in another.
type usageCursor struct {
	Sort       string `json:"o"`
	Asc        bool   `json:"a,omitempty"`
	Loads      int64  `json:"l"`
	Syncs      int64  `json:"s"`
	SearchHits int64  `json:"h"`
	LastLoaded string `json:"d,omitempty"`
	ID         string `json:"id"`
}

// encodeCursor returns the next_cursor value for the entry k under o.
func encodeCursor(o usageOrder, k usageKey) string {
	raw, _ := json.Marshal(usageCursor{ // a struct of integers, strings, and a bool always marshals
		Sort: o.sort, Asc: o.asc, Loads: k.loads, Syncs: k.syncs, SearchHits: k.hits, LastLoaded: k.lastLoaded, ID: k.id,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// usageEntry is one artifact of the list with its accumulated rows.
type usageEntry struct {
	m   store.ManifestRecord
	acc *usageAccumulator
	key usageKey
}

// usagePage is a validated query, order, limit, and an optional position to
// start after.
type usagePage struct {
	query string
	order usageOrder
	limit int
	after *usageKey
}

// parseUsagePage validates q's query, sort, order, limit, and cursor. A zero
// limit is the default. A cursor that does not decode to a position, or that
// was issued under another sort or order, is rejected rather than read as the
// start of the list, so a client that mangles one does not loop over the
// first page and one that changes the sort does not read a wrong page.
//
// Spec: §8.7
func parseUsagePage(q UsageQuery) (usagePage, error) {
	if utf8.RuneCountInString(q.Query) > UsageMaxQuery {
		return usagePage{}, fmt.Errorf("%w: query must be at most %d characters", ErrInvalidArgument, UsageMaxQuery)
	}
	p := usagePage{query: strings.ToLower(q.Query), limit: q.Limit}
	switch q.Sort {
	case "", UsageSortLoads:
		p.order.sort = UsageSortLoads
	case UsageSortSyncs, UsageSortSearchHits, UsageSortLastLoaded:
		p.order.sort = q.Sort
	default:
		return usagePage{}, fmt.Errorf("%w: sort must be loads, syncs, search_hits, or last_loaded, got %q", ErrInvalidArgument, q.Sort)
	}
	switch q.Order {
	case "", UsageOrderDesc:
	case UsageOrderAsc:
		p.order.asc = true
	default:
		return usagePage{}, fmt.Errorf("%w: order must be desc or asc, got %q", ErrInvalidArgument, q.Order)
	}
	if p.limit == 0 {
		p.limit = UsageDefaultLimit
	}
	if p.limit < 1 || p.limit > UsageMaxLimit {
		return usagePage{}, fmt.Errorf("%w: limit must be an integer from 1 to %d", ErrInvalidArgument, UsageMaxLimit)
	}
	if q.Cursor == "" {
		return p, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	var c usageCursor
	if err != nil || json.Unmarshal(raw, &c) != nil || c.ID == "" {
		return usagePage{}, fmt.Errorf("%w: cursor is not a next_cursor value", ErrInvalidArgument)
	}
	if c.Sort != p.order.sort || c.Asc != p.order.asc {
		return usagePage{}, fmt.Errorf("%w: cursor was issued for another sort or order", ErrInvalidArgument)
	}
	p.after = &usageKey{loads: c.Loads, syncs: c.Syncs, hits: c.SearchHits, lastLoaded: c.LastLoaded, id: c.ID}
	return p, nil
}

// matches reports whether id contains the page's query, ignoring case.
func (p usagePage) matches(id string) bool {
	return p.query == "" || strings.Contains(strings.ToLower(id), p.query)
}

// sortEntries orders entries by the page's order.
func (p usagePage) sortEntries(entries []usageEntry) {
	sort.Slice(entries, func(i, j int) bool { return p.order.before(entries[i].key, entries[j].key) })
}

// slice returns the page of entries, which are in the page's order, and the
// cursor of the page after it, or nil when none remains.
func (p usagePage) slice(entries []usageEntry) ([]usageEntry, *string) {
	start := 0
	if p.after != nil {
		start = sort.Search(len(entries), func(i int) bool { return p.order.before(*p.after, entries[i].key) })
	}
	end := min(start+p.limit, len(entries))
	if end >= len(entries) {
		return entries[start:], nil
	}
	next := encodeCursor(p.order, entries[end-1].key)
	return entries[start:end], &next
}
