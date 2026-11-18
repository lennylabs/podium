package core

import (
	"fmt"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// The §8.7 series granularities. GranularityNone, the default, carries no
// series.
const (
	GranularityNone  = "none"
	GranularityDay   = "day"
	GranularityWeek  = "week"
	GranularityMonth = "month"
)

// The longest windows §8.7 serves at day and week granularity; a longer
// window coarsens the series to the next granularity, so a series stays a
// few hundred periods long whatever the window.
const (
	UsageMaxDailyDays  = 365
	UsageMaxWeeklyDays = 3 * 365
)

// servedGranularity returns the granularity a window of days serves for the
// requested one: none for the default, and day coarsened to week and week to
// month past their longest windows, in sequence, so day over a window longer
// than the weekly limit is served as month.
//
// Spec: §8.7
func servedGranularity(requested string, days int) string {
	g := requested
	if g == "" {
		g = GranularityNone
	}
	if g == GranularityDay && days > UsageMaxDailyDays {
		g = GranularityWeek
	}
	if g == GranularityWeek && days > UsageMaxWeeklyDays {
		g = GranularityMonth
	}
	return g
}

// validGranularity reports whether g is a §8.7 granularity; the empty string
// is the default, none.
func validGranularity(g string) bool {
	switch g {
	case "", GranularityNone, GranularityDay, GranularityWeek, GranularityMonth:
		return true
	}
	return false
}

// periodKey names the period a UTC day falls in at granularity: the day
// itself, its ISO week, or its calendar month.
func periodKey(day, granularity string) string {
	switch granularity {
	case GranularityWeek:
		d, err := time.Parse(store.UsageDayLayout, day)
		if err != nil {
			// Unreachable: the window holds only days it formatted.
			return day
		}
		y, wk := d.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, wk)
	case GranularityMonth:
		return day[:7]
	}
	return day
}

// usageSeries splits the window into periods at granularity, oldest first,
// and sums each period's days from daily. Consecutive days that share a period
// form one entry, so the first and last periods are cut to the window. It
// returns nil for the none granularity.
//
// Spec: §8.7
func usageSeries(w usageWindow, granularity string, daily map[string]*UsageCounts) []UsagePeriod {
	if granularity == "" || granularity == GranularityNone {
		return nil
	}
	var out []UsagePeriod
	prev := ""
	for _, day := range w.days {
		k := periodKey(day, granularity)
		if k != prev || len(out) == 0 {
			out = append(out, UsagePeriod{Start: day})
			prev = k
		}
		p := &out[len(out)-1]
		p.End = day
		if c := daily[day]; c != nil {
			p.Loads += c.Loads
			p.Syncs += c.Syncs
			p.SearchHits += c.SearchHits
		}
	}
	return out
}
