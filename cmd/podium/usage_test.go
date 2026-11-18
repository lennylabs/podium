package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const usageFixture = `{"days":2,"from":"2026-11-17","to":"2026-11-18","granularity":"day",
"totals":{"loads":3,"syncs":1,"search_hits":2,"by_client":{},"by_harness":{},
 "series":[{"start":"2026-11-17","end":"2026-11-17","loads":1,"syncs":0,"search_hits":0},{"start":"2026-11-18","end":"2026-11-18","loads":2,"syncs":1,"search_hits":2}]},
"layers":[{"layer":"team","loads":3,"syncs":1,"search_hits":2}],
"artifacts":[{"artifact_id":"ops/greet","loads":3,"syncs":0,"search_hits":2,"last_loaded":"2026-11-18"},
 {"artifact_id":"ops/farewell","loads":0,"syncs":1,"search_hits":0,"last_loaded":null}]}`

// Spec: §8.7 — podium usage prints one line per artifact, one per layer, one
// per period of the catalog series, and the window totals; --json writes the
// response; an error envelope or an unreadable body exits 1.
func TestRenderUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := renderUsage(&out, &errOut, []byte(usageFixture), 200, "", false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{
		"       3        0        2  2026-11-18  ops/greet",
		"       0        1        0  -           ops/farewell",
		"       3        1        2  team",
		"       2        1        2  2026-11-18 to 2026-11-18",
		"2026-11-17 to 2026-11-18: 3 loads, 1 syncs, 2 search hits",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := renderUsage(&out, &errOut, []byte(usageFixture), 200, "", true); code != 0 || !strings.Contains(out.String(), `"artifact_id": "ops/greet"`) {
		t.Errorf("--json exit %d:\n%s", code, out.String())
	}
	for _, c := range []struct {
		body   string
		status int
	}{
		{`{"code":"registry.invalid_argument","message":"bad days"}`, 400},
		{`not json`, 200},
	} {
		errOut.Reset()
		if code := renderUsage(&out, &errOut, []byte(c.body), c.status, "", false); code != 1 || errOut.Len() == 0 {
			t.Errorf("%s: exit %d stderr %q, want 1 with a message", c.body, code, errOut.String())
		}
	}
}

// Spec: §8.7 — a missing --registry, a --days or --limit that is not a positive integer,
// --days with --from or --to, and an unknown --granularity are usage errors
// with exit status 2, reported before any request.
func TestUsageCmd_UsageErrors(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "")
	for _, args := range [][]string{
		{"--days", "3"},
		{"--registry", "http://127.0.0.1:1", "--days", "0"},
		{"--registry", "http://127.0.0.1:1", "--days", "x"},
		{"--registry", "http://127.0.0.1:1", "--days", "3", "--to", "2026-11-01"},
		{"--registry", "http://127.0.0.1:1", "--granularity", "year"},
		{"--registry", "http://127.0.0.1:1", "--limit", "0"},
		{"--registry", "http://127.0.0.1:1", "--limit", "x"},
		{"--registry", "http://127.0.0.1:1", "--bogus"},
	} {
		if code := usageCmd(args); code != 2 {
			t.Errorf("usage %v: exit %d, want 2", args, code)
		}
	}
}

// Spec: §8.7 — podium usage passes its window, granularity, page, and
// repeated layer and artifact flags as query parameters and prints the report.
func TestUsageCmd_PassesParameters(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		_, _ = w.Write([]byte(usageFixture))
	}))
	defer srv.Close()
	code := usageCmd([]string{"--registry", srv.URL, "--from", "2026-11-01", "--to", "2026-11-18", "--granularity", "week",
		"--layer", "team", "--artifact", "ops/greet", "--artifact", "ops/farewell", "--limit", "20", "--cursor", "abc"})
	want := "artifact=ops%2Fgreet&artifact=ops%2Ffarewell&cursor=abc&from=2026-11-01&granularity=week&layer=team&limit=20&to=2026-11-18"
	if code != 0 || got != want {
		t.Errorf("exit %d query %q, want 0 and %q", code, got, want)
	}
	if code := usageCmd([]string{"--registry", srv.URL, "--days", "7"}); code != 0 || got != "days=7" {
		t.Errorf("--days: exit %d query %q", code, got)
	}
}

// Spec: §8.7 — when the response carries a next_cursor, podium usage ends the
// text output with the number of artifacts listed and the --cursor value that
// reads the next page, and prints no such line on the last page.
func TestRenderUsage_NextCursorLine(t *testing.T) {
	var out, errOut bytes.Buffer
	paged := strings.Replace(usageFixture, `"artifacts":[`, `"artifact_count":7,"next_cursor":"eyJpZCI6Im9wcy9mYXJld2VsbCJ9","artifacts":[`, 1)
	if code := renderUsage(&out, &errOut, []byte(paged), 200, "", false); code != 0 ||
		!strings.Contains(out.String(), "2 of 7 artifacts listed; read the next ones with --cursor eyJpZCI6Im9wcy9mYXJld2VsbCJ9") {
		t.Errorf("exit %d:\n%s", code, out.String())
	}
	out.Reset()
	if renderUsage(&out, &errOut, []byte(usageFixture), 200, "", false); strings.Contains(out.String(), "--cursor") {
		t.Errorf("last page prints a cursor line:\n%s", out.String())
	}
}

// Spec: §8.7 — when the report coarsened the requested granularity, podium
// usage names the served granularity before the periods, and prints no such
// line when the report served the request as asked.
func TestRenderUsage_CoarsenedGranularityLine(t *testing.T) {
	var out, errOut bytes.Buffer
	weekly := strings.Replace(usageFixture, `"days":2,`, `"days":400,`, 1)
	weekly = strings.Replace(weekly, `"granularity":"day"`, `"granularity":"week"`, 1)
	if code := renderUsage(&out, &errOut, []byte(weekly), 200, "day", false); code != 0 ||
		!strings.Contains(out.String(), "periods by week: a 400-day window serves day as week") {
		t.Errorf("exit %d:\n%s", code, out.String())
	}
	out.Reset()
	if renderUsage(&out, &errOut, []byte(usageFixture), 200, "day", false); strings.Contains(out.String(), "periods by") {
		t.Errorf("an uncoarsened report prints the line:\n%s", out.String())
	}
}
