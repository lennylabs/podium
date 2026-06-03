package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

// scopePreview mirrors the §3.5 GET /v1/scope/preview response: aggregate
// counts for the caller's effective view, with no per-artifact metadata.
type scopePreview struct {
	Layers        []string       `json:"layers"`
	ArtifactCount int            `json:"artifact_count"`
	ByType        map[string]int `json:"by_type"`
	BySensitivity map[string]int `json:"by_sensitivity"`
}

// scopePreviewDisabled is returned by fetchScopePreview when the tenant
// gate expose_scope_preview is false (the §3.5 403 config.scope_preview_disabled
// response). Callers surface it as a disabled notice rather than an error.
type scopePreviewDisabledError struct{}

func (scopePreviewDisabledError) Error() string { return "config.scope_preview_disabled" }

// isScopePreviewDisabled reports whether err is the §3.5 tenant-gate refusal.
func isScopePreviewDisabled(err error) bool {
	_, ok := err.(scopePreviewDisabledError)
	return ok
}

// fetchScopePreview calls §3.5 GET /v1/scope/preview against a server-source
// registry, attaching the resolved caller credential so layer composition and
// visibility filtering apply to this identity (matching the MCP and SDK
// paths). A 403 config.scope_preview_disabled maps to scopePreviewDisabledError; any
// other non-2xx or transport failure is returned verbatim.
//
// spec: §3.5 — the consumer paths (MCP server, SDK, podium sync) and the
// podium status CLI all surface this preview.
func fetchScopePreview(registry string) (*scopePreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registry+"/v1/scope/preview", nil)
	if err != nil {
		return nil, err
	}
	if tok := readCLIToken(registry); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, scopePreviewDisabledError{}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	var p scopePreview
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("decode scope preview: %w", err)
	}
	return &p, nil
}

// runScopePreview fetches and prints the §3.5 scope preview for a
// server-source registry. With asJSON it emits the raw preview object;
// otherwise it prints the human-readable count block. A disabled tenant
// gate is reported on stderr with a non-zero exit so scripts can detect it.
func runScopePreview(registry string, asJSON bool) int {
	preview, err := fetchScopePreview(registry)
	switch {
	case isScopePreviewDisabled(err):
		fmt.Fprintln(os.Stderr, "error: scope preview is disabled for this tenant (expose_scope_preview: false)")
		return 1
	case err != nil:
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if asJSON {
		out, _ := json.MarshalIndent(preview, "", "  ")
		fmt.Println(string(out))
		return 0
	}
	fmt.Println("scope preview:")
	printScopePreview(os.Stdout, preview)
	return 0
}

// printScopePreview renders a §3.5 scope preview as a stable, human-readable
// block. Map keys are sorted so repeated invocations print identically.
func printScopePreview(w io.Writer, p *scopePreview) {
	fmt.Fprintf(w, "  artifacts:        %d\n", p.ArtifactCount)
	if len(p.Layers) > 0 {
		fmt.Fprintf(w, "  layers:           %v\n", p.Layers)
	}
	printCountMap(w, "by type", p.ByType)
	printCountMap(w, "by sensitivity", p.BySensitivity)
}

// printCountMap prints a count map with sorted keys under a label, so output
// is deterministic regardless of Go map iteration order.
func printCountMap(w io.Writer, label string, m map[string]int) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(w, "  %s:\n", label)
	for _, k := range keys {
		fmt.Fprintf(w, "      %-12s %d\n", k, m[k])
	}
}
