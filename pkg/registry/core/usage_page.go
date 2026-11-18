package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lennylabs/podium/pkg/store"
)

// Page sizes of the §8.7 report's artifact list.
const (
	UsageDefaultLimit = 50
	UsageMaxLimit     = 200
)

// usageCursor is a position in the report's artifact order: the counts and ID
// of the last entry a page held. The order is a ranking by counts, so the
// position is the sort key itself; an offset would shift as soon as an
// artifact's counts changed rank.
type usageCursor struct {
	Loads      int64  `json:"l"`
	Syncs      int64  `json:"s"`
	SearchHits int64  `json:"h"`
	ID         string `json:"id"`
}

// before reports whether c sorts before o: loads, syncs, and search hits,
// each descending, then the artifact ID ascending.
//
// Spec: §8.7
func (c usageCursor) before(o usageCursor) bool {
	switch {
	case c.Loads != o.Loads:
		return c.Loads > o.Loads
	case c.Syncs != o.Syncs:
		return c.Syncs > o.Syncs
	case c.SearchHits != o.SearchHits:
		return c.SearchHits > o.SearchHits
	}
	return c.ID < o.ID
}

// encode returns the opaque next_cursor value for c.
func (c usageCursor) encode() string {
	raw, _ := json.Marshal(c) // a struct of integers and a string always marshals
	return base64.RawURLEncoding.EncodeToString(raw)
}

// usageEntry is one artifact of the list with its accumulated rows.
type usageEntry struct {
	m   store.ManifestRecord
	acc *usageAccumulator
	key usageCursor
}

// usagePage is a validated limit and an optional position to start after.
type usagePage struct {
	limit int
	after *usageCursor
}

// parseUsagePage validates q's limit and cursor. A zero limit is the default,
// and a cursor that does not decode to a position is rejected rather than
// read as the start of the list, so a client that mangles one does not loop
// over the first page.
//
// Spec: §8.7
func parseUsagePage(q UsageQuery) (usagePage, error) {
	p := usagePage{limit: q.Limit}
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
	p.after = &c
	return p, nil
}

// slice returns the page of entries, which are in the report's order, and the
// cursor of the page after it, or nil when none remains.
func (p usagePage) slice(entries []usageEntry) ([]usageEntry, *string) {
	start := 0
	if p.after != nil {
		start = sort.Search(len(entries), func(i int) bool { return p.after.before(entries[i].key) })
	}
	end := min(start+p.limit, len(entries))
	if end >= len(entries) {
		return entries[start:], nil
	}
	next := entries[end-1].key.encode()
	return entries[start:end], &next
}
