package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
)

// usageCountsJSON is the three §8.7 counters of a report entry.
type usageCountsJSON struct {
	Loads      int64 `json:"loads"`
	Syncs      int64 `json:"syncs"`
	SearchHits int64 `json:"search_hits"`
}

// usageResponseJSON is the part of the GET /v1/usage response the text output
// reads. --json writes the response itself.
type usageResponseJSON struct {
	Days        int    `json:"days"`
	From        string `json:"from"`
	To          string `json:"to"`
	Granularity string `json:"granularity"`
	Totals      struct {
		usageCountsJSON
		Series []struct {
			Start string `json:"start"`
			End   string `json:"end"`
			usageCountsJSON
		} `json:"series"`
	} `json:"totals"`
	Layers []struct {
		Layer string `json:"layer"`
		usageCountsJSON
	} `json:"layers"`
	Artifacts []struct {
		ArtifactID string  `json:"artifact_id"`
		LastLoaded *string `json:"last_loaded"`
		usageCountsJSON
	} `json:"artifacts"`
	ArtifactCount int     `json:"artifact_count"`
	NextCursor    *string `json:"next_cursor"`
}

// usageFlags holds the podium usage flags that become query parameters.
type usageFlags struct {
	from, to, days, granularity, limit, cursor string
	layers, artifacts                          stringSliceFlag
}

// usageCmd calls the §8.7 usage report.
//
//	podium usage --registry <url> [--from <date>] [--to <date>] [--days <n>]
//	             [--granularity none|day|week|month]
//	             [--layer <id>]... [--artifact <id>]...
//	             [--limit <n>] [--cursor <cursor>] [--json]
//
// Spec: §8.7
func usageCmd(args []string) int {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	setUsage(fs, "Report artifact loads, syncs, and search hits per artifact, per layer, and for the catalog (registry HTTP API).")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	var f usageFlags
	fs.StringVar(&f.from, "from", "", "first UTC day of the window, as YYYY-MM-DD")
	fs.StringVar(&f.to, "to", "", "last UTC day of the window, as YYYY-MM-DD")
	fs.StringVar(&f.days, "days", "", "window of the last n UTC days, counting today")
	fs.StringVar(&f.granularity, "granularity", "", "series granularity: none, day, week, or month")
	fs.Var(&f.layers, "layer", "restrict the report to a layer (repeatable)")
	fs.Var(&f.artifacts, "artifact", "restrict the artifact list to an artifact (repeatable)")
	fs.StringVar(&f.limit, "limit", "", "most artifacts to list, 1 to 200 (default 50)")
	fs.StringVar(&f.cursor, "cursor", "", "next_cursor value of an earlier report, to list the following artifacts")
	asJSON := fs.Bool("json", false, "write the response object on standard output")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	q, err := f.query()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		return 2
	}
	endpoint := *registry + "/v1/usage"
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	body, status := doJSON(endpoint, "GET", nil)
	return renderUsage(os.Stdout, os.Stderr, body, status, f.granularity, *asJSON)
}

// query validates the flags §8.7 lists as usage errors and returns them as
// query parameters.
func (f usageFlags) query() (url.Values, error) {
	q := url.Values{}
	if f.days != "" {
		if !positiveInt(f.days) {
			return nil, errors.New("--days must be a positive integer")
		}
		if f.from != "" || f.to != "" {
			return nil, errors.New("--days cannot be combined with --from or --to")
		}
		q.Set("days", f.days)
	}
	if f.limit != "" && !positiveInt(f.limit) {
		return nil, errors.New("--limit must be a positive integer")
	}
	switch f.granularity {
	case "", "none", "day", "week", "month":
	default:
		return nil, errors.New("--granularity must be none, day, week, or month")
	}
	setIf(q, "granularity", f.granularity)
	setIf(q, "from", f.from)
	setIf(q, "to", f.to)
	setIf(q, "limit", f.limit)
	setIf(q, "cursor", f.cursor)
	q["layer"] = f.layers
	q["artifact"] = f.artifacts
	return q, nil
}

// positiveInt reports whether s is a positive decimal integer.
func positiveInt(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}

// setIf sets key to value when value is not empty.
func setIf(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

// renderUsage writes the usage response and returns the exit status: 1 for an
// error envelope or an unreadable body, and 0 otherwise. requested is the
// --granularity value, so the output can say when the report coarsened it.
//
// Spec: §8.7
func renderUsage(stdout, stderr io.Writer, body []byte, status int, requested string, asJSON bool) int {
	if status >= 400 {
		_, _ = fmt.Fprintf(stderr, "usage failed: HTTP %d\n%s\n", status, body)
		return 1
	}
	var resp usageResponseJSON
	if err := json.Unmarshal(body, &resp); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: decode usage response: %v\n", err)
		return 1
	}
	if asJSON {
		var out bytes.Buffer
		if err := json.Indent(&out, body, "", "  "); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, out.String())
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "%8s %8s %8s  %-10s  %s\n", "LOADS", "SYNCS", "SEARCHES", "LAST LOAD", "ARTIFACT")
	for _, a := range resp.Artifacts {
		last := "-"
		if a.LastLoaded != nil {
			last = *a.LastLoaded
		}
		_, _ = fmt.Fprintf(stdout, "%8d %8d %8d  %-10s  %s\n", a.Loads, a.Syncs, a.SearchHits, last, a.ArtifactID)
	}
	_, _ = fmt.Fprintf(stdout, "\n%8s %8s %8s  %s\n", "LOADS", "SYNCS", "SEARCHES", "LAYER")
	for _, l := range resp.Layers {
		_, _ = fmt.Fprintf(stdout, "%8d %8d %8d  %s\n", l.Loads, l.Syncs, l.SearchHits, l.Layer)
	}
	if len(resp.Totals.Series) > 0 {
		if requested != "" && resp.Granularity != requested {
			_, _ = fmt.Fprintf(stdout, "\nperiods by %s: a %d-day window serves %s as %s\n", resp.Granularity, resp.Days, requested, resp.Granularity)
		}
		_, _ = fmt.Fprintf(stdout, "\n%8s %8s %8s  %s\n", "LOADS", "SYNCS", "SEARCHES", "PERIOD")
		for _, p := range resp.Totals.Series {
			_, _ = fmt.Fprintf(stdout, "%8d %8d %8d  %s to %s\n", p.Loads, p.Syncs, p.SearchHits, p.Start, p.End)
		}
	}
	_, _ = fmt.Fprintf(stdout, "\n%s to %s: %d loads, %d syncs, %d search hits\n",
		resp.From, resp.To, resp.Totals.Loads, resp.Totals.Syncs, resp.Totals.SearchHits)
	if resp.NextCursor != nil {
		_, _ = fmt.Fprintf(stdout, "%d of %d artifacts listed; read the next ones with --cursor %s\n",
			len(resp.Artifacts), resp.ArtifactCount, *resp.NextCursor)
	}
	return 0
}
