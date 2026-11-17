package sync

import (
	"path"
	"sort"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
)

// ListingReport is the §7.5.8 estimate of the listing a `kind: workspace`
// target contributes to its harness, compared with the configured budget.
// MaxEntryChars echoes the configured per-entry maximum (0 when unset) so the
// CLI can name it; the §7.5.8 `--json` member does not carry it.
type ListingReport struct {
	Harness       string
	BudgetChars   int
	CostChars     int
	Entries       int
	MaxEntryChars int
	Over          bool
	Check         string
	Oversized     []OversizedEntry
}

// OversizedEntry is one listing entry whose description has more code points
// than max_entry_chars.
type OversizedEntry struct {
	ID    string
	Chars int
}

// Listing computes the §7.5.8 listing report of a run. It is a pure function
// of the run's Result: it never enters Run, so the check cannot change what a
// sync writes, and a filesystem-source and a server-source run that resolve
// the same set report the same listing.
//
// It returns nil when res is nil, under `check: off`, for the `none` adapter,
// for a §7.4 offline-first run that left the existing output in place, and
// when no type in b.Types counts for the adapter. Otherwise the entries are
// the artifacts whose listing type is in b.Types and counts for the adapter,
// other than a user-invoked skill, and the cost is the sum over them of the
// code points of the last identifier segment, the code points of the
// description, and b.EntryOverheadChars. A user-invoked skill is never an
// entry: opencode and gemini write it as a command file, and the other
// adapters add the harness's user-only control, so leaving it out keeps the
// estimate a lower bound.
//
// Spec: §7.5.8, §4.3.4
func Listing(res *Result, b EffectiveListingBudget) *ListingReport {
	if res == nil || b.Check == ListingCheckOff || res.Adapter == "none" || res.Offline {
		return nil
	}
	counted := map[string]bool{}
	for _, t := range b.Types {
		if typeCounts(t, res.Adapter) {
			counted[t] = true
		}
	}
	if len(counted) == 0 {
		return nil
	}
	rep := &ListingReport{
		Harness:       res.Adapter,
		BudgetChars:   b.MaxChars,
		MaxEntryChars: b.MaxEntryChars,
		Check:         b.Check,
	}
	for _, a := range res.Artifacts {
		if !counted[a.ListingType] || a.UserInvoked {
			continue
		}
		descChars := utf8.RuneCountInString(a.ListingDescription)
		rep.Entries++
		rep.CostChars += utf8.RuneCountInString(path.Base(a.ID)) + descChars + b.EntryOverheadChars
		if b.MaxEntryChars > 0 && descChars > b.MaxEntryChars {
			rep.Oversized = append(rep.Oversized, OversizedEntry{ID: a.ID, Chars: descChars})
		}
	}
	sort.Slice(rep.Oversized, func(i, j int) bool { return rep.Oversized[i].ID < rep.Oversized[j].ID })
	rep.Over = rep.CostChars > rep.BudgetChars
	return rep
}

// typeCounts reports whether artifact type t counts as a listing entry for
// adapterID. A type counts unless the §6.7.1 matrix grades the adapter ✗ for
// it, reading a rule from the `rule_mode: always` row, a hook from the
// `hook_event` row, and every other type from its type row. A type or an
// adapter the matrix does not grade counts.
//
// Spec: §7.5.8, §6.7.1
func typeCounts(t, adapterID string) bool {
	c := adapter.Capability{Field: "type", Value: t}
	switch manifest.ArtifactType(t) {
	case manifest.TypeRule:
		c = adapter.Capability{Field: "rule_mode", Value: "always"}
	case manifest.TypeHook:
		c = adapter.Capability{Field: "hook_event"}
	}
	level, ok := adapter.Cell(c, adapterID)
	return !ok || level != adapter.SupportUnsupported
}

// Reported reports whether the run writes a listing report: the cost exceeds
// the budget or an entry exceeds max_entry_chars. It is false on a nil report.
func (r *ListingReport) Reported() bool {
	return r != nil && (r.Over || len(r.Oversized) > 0)
}

// Failed reports whether the report fails the run, which happens only under
// `check: error`. It is false on a nil report.
//
// Spec: §7.5.8
func (r *ListingReport) Failed() bool {
	return r.Reported() && r.Check == ListingCheckError
}
