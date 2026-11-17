package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sync"
)

// errListingOverBudget marks a --config target whose listing report fails the
// target under `check: error`. runMultiTargetSync prints it as the target's
// failure; the `listing:` lines on standard error already name the excess.
var errListingOverBudget = errors.New("listing estimate exceeds the configured budget")

// listingJSON is the §7.5.8 `listing` member of the --json envelope. It
// carries no max_entry_chars member: the spec fixes the member's keys.
type listingJSON struct {
	Harness     string          `json:"harness"`
	BudgetChars int             `json:"budget_chars"`
	CostChars   int             `json:"cost_chars"`
	Entries     int             `json:"entries"`
	Over        bool            `json:"over"`
	Check       string          `json:"check"`
	Oversized   []oversizedJSON `json:"oversized"`
}

// oversizedJSON is one entry of listingJSON.Oversized.
type oversizedJSON struct {
	ID    string `json:"id"`
	Chars int    `json:"chars"`
}

// listingOut converts a listing report to its --json member. A nil report,
// which a run that computes no listing returns, yields nil so the envelope
// carries `listing: null`; oversized is [] when no entry is reported.
//
// Spec: §7.5.8
func listingOut(rep *sync.ListingReport) *listingJSON {
	if rep == nil {
		return nil
	}
	out := &listingJSON{
		Harness:     rep.Harness,
		BudgetChars: rep.BudgetChars,
		CostChars:   rep.CostChars,
		Entries:     rep.Entries,
		Over:        rep.Over,
		Check:       rep.Check,
		Oversized:   []oversizedJSON{},
	}
	for _, o := range rep.Oversized {
		out.Oversized = append(out.Oversized, oversizedJSON{ID: o.ID, Chars: o.Chars})
	}
	return out
}

// reportListing writes the listing report's excess and each oversized entry to
// w, which is standard error so a --json envelope on standard output stays
// valid. The lines compare Podium's estimate with the configured budget and
// assert no harness limit. Under `check: warning` each line carries the
// `warning: ` prefix. It reports whether the report fails the run, which only
// `check: error` does.
//
// Spec: §7.5.8
func reportListing(w io.Writer, rep *sync.ListingReport) bool {
	if !rep.Reported() {
		return false
	}
	prefix := ""
	if rep.Check == sync.ListingCheckWarning {
		prefix = "warning: "
	}
	if rep.Over {
		_, _ = fmt.Fprintf(w, "%slisting: %s listing estimate is %d characters against a configured budget of %d (%d entries)\n",
			prefix, rep.Harness, rep.CostChars, rep.BudgetChars, rep.Entries)
	}
	for _, o := range rep.Oversized {
		_, _ = fmt.Fprintf(w, "%slisting: %s description is %d characters, above max_entry_chars %d\n",
			prefix, o.ID, o.Chars, rep.MaxEntryChars)
	}
	return rep.Failed()
}

// overrideListingBudget resolves the listing budget of a `podium sync
// override` that re-materializes. A nil merged config, which a sync.yaml that
// does not load leaves, gives the §7.5.8 defaults. On an invalid entry it
// prints `error: <err>` and returns false, and the caller exits 2 before
// sync.Override records a toggle.
//
// Spec: §7.5.2, §7.5.8
func overrideListingBudget(merged *sync.MergedConfig, harness string) (sync.EffectiveListingBudget, bool) {
	b, err := merged.ListingBudgetFor(harness, adapter.DefaultRegistry())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return sync.EffectiveListingBudget{}, false
	}
	return b, true
}

// overrideExit reports an override's re-materialization drops and listing on
// standard error and returns 1 when either fails the run, else 0. An override
// that does not re-materialize leaves res.Materialized nil, so it reports no
// listing.
//
// Spec: §13.11.3, §7.5.8
func overrideExit(res *sync.OverrideResult, budget sync.EffectiveListingBudget) int {
	dropped := reportDropped(os.Stderr, res.Dropped)
	overBudget := reportListing(os.Stderr, sync.Listing(res.Materialized, budget))
	if dropped || overBudget {
		return 1
	}
	return 0
}

// listingErr returns errListingOverBudget when the target's listing report
// failed it.
func listingErr(failed bool) error {
	if failed {
		return errListingOverBudget
	}
	return nil
}
