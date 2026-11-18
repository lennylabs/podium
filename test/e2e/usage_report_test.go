package e2e

// End-to-end tests for docs/managing/usage.md
// (D-usage). The page documents the §8.7 usage report: counts recorded by the
// compiled registry, flushed on its interval, served by GET /v1/usage, printed
// by podium usage with its window, layer, artifact, and granularity flags, and
// mirrored in the audit stream, with pages and a granularity that long windows
// coarsen; the tests also cover the startup refusal of a usage setting out of
// range.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usageGet sends a GET to the registry with an optional client header and
// returns the status.
func usageGet(t *testing.T, url, client string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client != "" {
		req.Header.Set("X-Podium-Client", client)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// Spec: §8.7, §8.1 — the registry counts loads, syncs, and search hits by
// client kind and harness, flushes them on PODIUM_USAGE_FLUSH_INTERVAL,
// serves them in GET /v1/usage, and records the search results with their
// layers in the artifacts.searched audit record; podium usage prints the
// report and exits 0, 1, and 2 as documented.
func TestUsageReport_BinaryCountsReportAndCLI(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	audit := filepath.Join(home, "audit.log")
	reg := writeRegistry(t, map[string]string{
		"ops/greet/SKILL.md":    skillBodyDesc("greet", "Greet the user at the start of a session."),
		"ops/farewell/SKILL.md": skillBodyDesc("farewell", "Say goodbye to the user at the end of a session."),
	})
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_USAGE_FLUSH_INTERVAL=1s",
		"PODIUM_USAGE_RETENTION_DAYS=1200",
		"PODIUM_AUDIT_LOG_PATH=" + audit,
	}, "serve", "--standalone", "--layer-path", reg)
	for _, c := range []struct{ path, client string }{
		{"/v1/load_artifact?id=ops/greet", "kind=mcp&name=podium-mcp&harness=claude-code"},
		{"/v1/load_artifact?id=ops/greet", "kind=mcp&name=podium-mcp&harness=cursor"},
		{"/v1/load_artifact?id=ops/farewell", "kind=sync&name=podium&harness=claude-code"},
		{"/v1/search_artifacts?query=session", "kind=sdk&name=podium-py"},
	} {
		if st := usageGet(t, srv.BaseURL+c.path, c.client); st != http.StatusOK {
			t.Fatalf("%s: HTTP %d\n%s", c.path, st, srv.log())
		}
	}
	var report struct {
		Totals struct {
			Loads      int64                       `json:"loads"`
			Syncs      int64                       `json:"syncs"`
			SearchHits int64                       `json:"search_hits"`
			ByHarness  map[string]map[string]int64 `json:"by_harness"`
		} `json:"totals"`
		Artifacts []struct {
			ArtifactID string `json:"artifact_id"`
			Loads      int64  `json:"loads"`
			Syncs      int64  `json:"syncs"`
		} `json:"artifacts"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, body := getRaw(t, srv.BaseURL+"/v1/usage")
		if st == http.StatusOK && json.Unmarshal(body, &report) == nil && report.Totals.Loads == 2 && report.Totals.SearchHits == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("usage report never showed the flushed counts: %d %s", st, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if report.Totals.Syncs != 1 || report.Totals.ByHarness["cursor"]["loads"] != 1 || report.Totals.ByHarness["claude-code"]["syncs"] != 1 ||
		len(report.Artifacts) != 2 || report.Artifacts[0].ArtifactID != "ops/greet" || report.Artifacts[0].Loads != 2 {
		t.Errorf("report = %+v", report)
	}

	text := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--granularity", "day", "--days", "2")
	if text.Exit != 0 || !strings.Contains(text.Stdout, "ops/greet") || !strings.Contains(text.Stdout, "2 loads, 1 syncs, 2 search hits") {
		t.Errorf("podium usage exit=%d\n%s%s", text.Exit, text.Stdout, text.Stderr)
	}
	asJSON := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--json", "--artifact", "ops/farewell")
	if asJSON.Exit != 0 || !strings.Contains(asJSON.Stdout, `"artifact_id": "ops/farewell"`) || strings.Contains(asJSON.Stdout, `"artifact_id": "ops/greet"`) {
		t.Errorf("podium usage --json --artifact exit=%d\n%s", asJSON.Exit, asJSON.Stdout)
	}
	var layers struct {
		Layers []struct {
			Layer string `json:"layer"`
		} `json:"layers"`
	}
	if _, body := getRaw(t, srv.BaseURL+"/v1/usage"); json.Unmarshal(body, &layers) != nil || len(layers.Layers) != 1 {
		t.Fatalf("layers: %s", body)
	}
	month := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--layer", layers.Layers[0].Layer, "--granularity", "month")
	if month.Exit != 0 || !strings.Contains(month.Stdout, "PERIOD") || !strings.Contains(month.Stdout, layers.Layers[0].Layer) {
		t.Errorf("podium usage --layer --granularity month exit=%d\n%s%s", month.Exit, month.Stdout, month.Stderr)
	}
	long := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--days", "400", "--granularity", "day")
	if long.Exit != 0 || !strings.Contains(long.Stdout, "periods by week: a 400-day window serves day as week") {
		t.Errorf("podium usage --days 400 --granularity day exit=%d\n%s%s", long.Exit, long.Stdout, long.Stderr)
	}
	var served struct {
		Granularity string `json:"granularity"`
	}
	if _, body := getRaw(t, srv.BaseURL+"/v1/usage?days=1100&granularity=day"); json.Unmarshal(body, &served) != nil || served.Granularity != "month" {
		t.Errorf("a 1,100-day daily request should be served by month: %.200s", body)
	}
	sorted := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--sort", "syncs", "--query", "FARE")
	if sorted.Exit != 0 || !strings.Contains(sorted.Stdout, "ops/farewell") || strings.Contains(sorted.Stdout, "ops/greet") {
		t.Errorf("podium usage --sort syncs --query FARE exit=%d\n%s%s", sorted.Exit, sorted.Stdout, sorted.Stderr)
	}
	typed := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--json", "--type", "agent")
	if typed.Exit != 0 || !strings.Contains(typed.Stdout, `"artifact_count": 0`) {
		t.Errorf("podium usage --type agent over two skills exit=%d\n%s%s", typed.Exit, typed.Stdout, typed.Stderr)
	}
	first := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--limit", "1")
	_, cursor, more := strings.Cut(first.Stdout, "1 of 2 artifacts listed; read the next ones with --cursor ")
	cursor = strings.TrimSpace(cursor)
	if first.Exit != 0 || !more || cursor == "" || !strings.Contains(first.Stdout, "ops/greet") || strings.Contains(first.Stdout, "ops/farewell") {
		t.Fatalf("podium usage --limit 1 exit=%d\n%s%s", first.Exit, first.Stdout, first.Stderr)
	}
	next := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--limit", "1", "--cursor", cursor)
	if next.Exit != 0 || !strings.Contains(next.Stdout, "ops/farewell") || strings.Contains(next.Stdout, "ops/greet") || strings.Contains(next.Stdout, "--cursor") {
		t.Errorf("podium usage --cursor exit=%d\n%s%s", next.Exit, next.Stdout, next.Stderr)
	}
	if res := runPodium(t, "", srv.cliEnv(), "usage", "--registry", srv.BaseURL, "--from", "2000-01-01"); res.Exit != 1 || !strings.Contains(res.Stderr, "registry.invalid_argument") {
		t.Errorf("podium usage --from outside retention exit=%d stderr=%s, want 1 with the envelope", res.Exit, res.Stderr)
	}
	for _, args := range [][]string{{"--days", "0"}, {"--days", "3", "--from", "2026-11-01"}, {"--granularity", "year"}, {"--limit", "0"}, {"--sort", "name"}} {
		if res := runPodium(t, "", srv.cliEnv(), append([]string{"usage", "--registry", srv.BaseURL}, args...)...); res.Exit != 2 {
			t.Errorf("podium usage %v exit=%d, want 2", args, res.Exit)
		}
	}

	raw, err := os.ReadFile(audit)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, `"artifacts.searched"`) {
			continue
		}
		var rec struct {
			Context map[string]string `json:"context"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Context["query"] != "session" {
			continue
		}
		var refs []map[string]string
		if err := json.Unmarshal([]byte(rec.Context["results"]), &refs); err == nil && len(refs) == 2 && refs[0]["layer"] != "" && refs[0]["artifact_id"] != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("no artifacts.searched record carries the two results with their layers:\n%s", raw)
	}
}

// Spec: §8.7, §13.12 — a usage setting outside its range refuses startup with
// config.invalid naming the variable.
func TestUsageReport_StartupRefusesBadSettings(t *testing.T) {
	t.Parallel()
	for _, kv := range []string{"PODIUM_USAGE_RETENTION_DAYS=0", "PODIUM_USAGE_FLUSH_INTERVAL=10ms"} {
		out := runPodium(t, "", []string{"HOME=" + t.TempDir(), kv}, "serve", "--standalone")
		name := strings.SplitN(kv, "=", 2)[0]
		combined := out.Stderr + out.Stdout
		if out.Exit == 0 || !strings.Contains(combined, "config.invalid: "+name) {
			t.Errorf("serve with %s: exit=%d\n%s", kv, out.Exit, combined)
		}
	}
}

// searchOrder returns the IDs of a search_artifacts call in ranked order.
func searchOrder(t *testing.T, base, query string) []string {
	t.Helper()
	st, body := getRaw(t, base+"/v1/search_artifacts?query="+query)
	var res struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if st != http.StatusOK || json.Unmarshal(body, &res) != nil {
		t.Fatalf("search %q: HTTP %d %s", query, st, body)
	}
	out := make([]string, 0, len(res.Results))
	for _, r := range res.Results {
		out = append(out, r.ID)
	}
	return out
}

// Spec: §3.3, §8.7 — the compiled registry ranks search results that tie on
// retrieval by loads: a load raises its artifact at once, and after a graceful
// restart on the same database the stored loads keep the order with no new
// load, because the ranking reads the stored counts rather than process
// memory.
func TestLearnFromUsage_RankingReadsStoredLoadsAcrossRestart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	reg := writeRegistry(t, map[string]string{
		"tools/aaa/SKILL.md": skillBodyDesc("aaa", "Shared helper for session tasks."),
		"tools/bbb/SKILL.md": skillBodyDesc("bbb", "Shared helper for session tasks."),
	})
	env := []string{"HOME=" + home, "PODIUM_USAGE_FLUSH_INTERVAL=1s"}
	srv := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	if got := searchOrder(t, srv.BaseURL, "helper"); len(got) != 2 || got[0] != "tools/aaa" {
		t.Fatalf("control order = %v, want tools/aaa first on the alphabetical tie\n%s", got, srv.log())
	}
	for range 3 {
		if st := usageGet(t, srv.BaseURL+"/v1/load_artifact?id=tools/bbb", "kind=mcp&name=podium-mcp&harness=claude-code"); st != http.StatusOK {
			t.Fatalf("load tools/bbb: HTTP %d", st)
		}
	}
	if got := searchOrder(t, srv.BaseURL, "helper"); len(got) != 2 || got[0] != "tools/bbb" {
		t.Errorf("order after loads = %v, want tools/bbb first", got)
	}
	stopProc(srv.cmd)
	again := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	if got := searchOrder(t, again.BaseURL, "helper"); len(got) != 2 || got[0] != "tools/bbb" {
		t.Errorf("order after a restart = %v, want tools/bbb first on its stored loads\n%s", got, again.log())
	}
}
