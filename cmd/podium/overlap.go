package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/lennylabs/podium/pkg/lint"
)

// overlapResponseJSON is the part of the GET /v1/overlap response the text
// output, the coverage warning, and the exit status read. --json writes the
// response itself.
type overlapResponseJSON struct {
	SemanticOverlap struct {
		Computed *int `json:"computed"`
		Total    *int `json:"total"`
	} `json:"semantic_overlap"`
	Diagnostics []lintDiagnosticJSON `json:"diagnostics"`
}

// incomplete reports whether the response says some visible artifacts have
// no current semantic overlap computation: computed below total. Both are
// null when the check is off, which is not incomplete.
func (r overlapResponseJSON) incomplete() bool {
	c, t := r.SemanticOverlap.Computed, r.SemanticOverlap.Total
	return c != nil && t != nil && *c < *t
}

// overlapCmd calls the §7.3.1 catalog overlap report.
//
//	podium overlap --registry <url> [--threshold <n>] [--json] [--fail-on error|warning]
//
// Spec: §7.3.1
func overlapCmd(args []string) int {
	fs := flag.NewFlagSet("overlap", flag.ContinueOnError)
	setUsage(fs, "Report description overlap across every layer you can see (registry HTTP API).")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	threshold := fs.String("threshold", "", "semantic overlap threshold for this read, from 0.5 to 1.0")
	asJSON := fs.Bool("json", false, "write the response object on standard output")
	failOnFlag := fs.String("fail-on", string(lint.SeverityError), "lowest severity that fails the run: error or warning")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	failOn := lint.Severity(*failOnFlag)
	if failOn != lint.SeverityError && failOn != lint.SeverityWarning {
		fmt.Fprintln(os.Stderr, "error: --fail-on must be error or warning")
		return 2
	}
	q := url.Values{}
	if *threshold != "" {
		if _, ok := lint.ParseSimilarity(*threshold); !ok {
			fmt.Fprintln(os.Stderr, "error: --threshold must be a number from 0.5 to 1.0")
			return 2
		}
		q.Set("threshold", *threshold)
	}
	endpoint := *registry + "/v1/overlap"
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	body, status := doJSON(endpoint, "GET", nil)
	return renderOverlap(os.Stdout, os.Stderr, body, status, *asJSON, failOn)
}

// renderOverlap writes the overlap response and returns the exit status: 1
// for an error envelope or an unreadable body, and otherwise the `podium
// lint` status for the diagnostics under failOn. When computed is below total
// it prints a warning naming both numbers on stderr, under --json too, and
// counts it as a warning for failOn, so a report with pending or failed
// computations is not read as clean.
//
// Spec: §7.3.1
func renderOverlap(stdout, stderr io.Writer, body []byte, status int, asJSON bool, failOn lint.Severity) int {
	if status >= 400 {
		_, _ = fmt.Fprintf(stderr, "overlap failed: HTTP %d\n%s\n", status, body)
		return 1
	}
	var resp overlapResponseJSON
	if err := json.Unmarshal(body, &resp); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: decode overlap response: %v\n", err)
		return 1
	}
	diags := make([]lint.Diagnostic, 0, len(resp.Diagnostics))
	for _, d := range resp.Diagnostics {
		diags = append(diags, lint.Diagnostic{
			ArtifactID: d.ArtifactID,
			Code:       d.Code,
			Severity:   lint.Severity(d.Severity),
			Message:    d.Message,
		})
	}
	exit := lintExitCode(diags, false, failOn)
	if resp.incomplete() {
		c, t := *resp.SemanticOverlap.Computed, *resp.SemanticOverlap.Total
		_, _ = fmt.Fprintf(stderr, "warning: semantic overlap is computed for %d of %d artifacts; the report can miss overlap of the other %d until their computation succeeds\n", c, t, t-c)
		if failOn == lint.SeverityWarning {
			exit = 1
		}
	}
	if asJSON {
		var out bytes.Buffer
		if err := json.Indent(&out, body, "", "  "); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, out.String())
		return exit
	}
	if len(diags) == 0 {
		_, _ = fmt.Fprintln(stdout, "overlap: no issues.")
		return exit
	}
	for _, d := range diags {
		_, _ = fmt.Fprintln(stdout, d.String())
	}
	return exit
}

// adminRebuildOverlapCmd computes the §7.3.1 overlap records of every served
// artifact of the caller's tenant.
//
//	podium admin rebuild-overlap --registry <url>
//
// Spec: §7.3.1
func adminRebuildOverlapCmd(args []string) int {
	fs := flag.NewFlagSet("admin rebuild-overlap", flag.ContinueOnError)
	setUsage(fs, "Recompute the semantic overlap records of every served artifact.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	body, status := doJSON(*registry+"/v1/admin/overlap/rebuild", "POST", nil)
	return renderRebuildOverlap(os.Stdout, os.Stderr, body, status)
}

// renderRebuildOverlap prints the rebuild counts and returns 1 when the
// response is an error envelope or failed is not zero.
//
// Spec: §7.3.1
func renderRebuildOverlap(stdout, stderr io.Writer, body []byte, status int) int {
	if status >= 400 {
		_, _ = fmt.Fprintf(stderr, "rebuild-overlap failed: HTTP %d\n%s\n", status, body)
		return 1
	}
	var res struct {
		Computed int `json:"computed"`
		Failed   int `json:"failed"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: decode rebuild-overlap response: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "computed: %d\nfailed: %d\n", res.Computed, res.Failed)
	if res.Failed != 0 {
		return 1
	}
	return 0
}
