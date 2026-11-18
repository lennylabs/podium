package main

import (
	"bytes"
	"encoding/json"
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
	From   string `json:"from"`
	To     string `json:"to"`
	Totals struct {
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
}

// usageCmd calls the §8.7 usage report.
//
//	podium usage --registry <url> [--from <date>] [--to <date>] [--days <n>]
//	             [--granularity none|day|week|month]
//	             [--layer <id>]... [--artifact <id>]... [--json]
//
// Spec: §8.7
func usageCmd(args []string) int {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	setUsage(fs, "Report artifact loads, syncs, and search hits per artifact, per layer, and for the catalog (registry HTTP API).")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	from := fs.String("from", "", "first UTC day of the window, as YYYY-MM-DD")
	to := fs.String("to", "", "last UTC day of the window, as YYYY-MM-DD")
	days := fs.String("days", "", "window of the last n UTC days, counting today")
	granularity := fs.String("granularity", "", "series granularity: none, day, week, or month")
	var layers, artifacts stringSliceFlag
	fs.Var(&layers, "layer", "restrict the report to a layer (repeatable)")
	fs.Var(&artifacts, "artifact", "restrict the artifact list to an artifact (repeatable)")
	asJSON := fs.Bool("json", false, "write the response object on standard output")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	q := url.Values{}
	if *days != "" {
		if n, err := strconv.Atoi(*days); err != nil || n < 1 {
			fmt.Fprintln(os.Stderr, "error: --days must be a positive integer")
			return 2
		}
		if *from != "" || *to != "" {
			fmt.Fprintln(os.Stderr, "error: --days cannot be combined with --from or --to")
			return 2
		}
		q.Set("days", *days)
	}
	switch *granularity {
	case "", "none", "day", "week", "month":
	default:
		fmt.Fprintln(os.Stderr, "error: --granularity must be none, day, week, or month")
		return 2
	}
	setIf(q, "granularity", *granularity)
	setIf(q, "from", *from)
	setIf(q, "to", *to)
	q["layer"] = layers
	q["artifact"] = artifacts
	endpoint := *registry + "/v1/usage"
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	body, status := doJSON(endpoint, "GET", nil)
	return renderUsage(os.Stdout, os.Stderr, body, status, *asJSON)
}

// setIf sets key to value when value is not empty.
func setIf(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

// renderUsage writes the usage response and returns the exit status: 1 for an
// error envelope or an unreadable body, and 0 otherwise.
//
// Spec: §8.7
func renderUsage(stdout, stderr io.Writer, body []byte, status int, asJSON bool) int {
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
		_, _ = fmt.Fprintf(stdout, "\n%8s %8s %8s  %s\n", "LOADS", "SYNCS", "SEARCHES", "PERIOD")
		for _, p := range resp.Totals.Series {
			_, _ = fmt.Fprintf(stdout, "%8d %8d %8d  %s to %s\n", p.Loads, p.Syncs, p.SearchHits, p.Start, p.End)
		}
	}
	_, _ = fmt.Fprintf(stdout, "\n%s to %s: %d loads, %d syncs, %d search hits\n",
		resp.From, resp.To, resp.Totals.Loads, resp.Totals.Syncs, resp.Totals.SearchHits)
	return 0
}
