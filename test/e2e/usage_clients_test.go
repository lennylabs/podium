package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Spec: §7.2.2, §8.7 — through the binaries: a server-source podium sync
// counts its loads as syncs under client cli and its harness; a caller that
// names itself counts under its own name and harness; a request with no
// client value counts under unknown for both; and the CLI's own reads count
// as loads under cli.
func TestUsageClients_SyncCustomAndUnknown(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"ops/greet/SKILL.md": skillBodyDesc("greet", "Greet the user at the start of a session."),
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_USAGE_FLUSH_INTERVAL=1s"},
		"serve", "--standalone", "--layer-path", reg)

	sync := runPodium(t, "", append(srvSyncEnv(srv), "HOME="+t.TempDir()),
		"sync", "--registry", srv.BaseURL, "--target", t.TempDir(), "--harness", "claude-code")
	cliWantExit(t, sync, 0, "podium sync")
	show := runPodium(t, "", nil, "artifact", "show", "ops/greet", "--registry", srv.BaseURL)
	cliWantExit(t, show, 0, "podium artifact show")
	for _, client := range []string{"name=acme-evals&harness=cursor", ""} {
		if st := usageGet(t, srv.BaseURL+"/v1/load_artifact?id=ops/greet", client); st != http.StatusOK {
			t.Fatalf("load with client %q: HTTP %d", client, st)
		}
	}

	var report struct {
		Totals struct {
			Syncs     int64                       `json:"syncs"`
			ByClient  map[string]map[string]int64 `json:"by_client"`
			ByHarness map[string]map[string]int64 `json:"by_harness"`
		} `json:"totals"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, body := getRaw(t, srv.BaseURL+"/v1/usage")
		if st == http.StatusOK && json.Unmarshal(body, &report) == nil &&
			report.Totals.Syncs == 1 && report.Totals.ByClient["unknown"]["loads"] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("usage report never showed the flushed counts: %d %s", st, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	c, h := report.Totals.ByClient, report.Totals.ByHarness
	if c["cli"]["syncs"] != 1 || c["cli"]["loads"] != 1 || c["acme-evals"]["loads"] != 1 || c["unknown"]["loads"] != 1 {
		t.Errorf("by_client = %v, want cli with 1 sync and 1 load, acme-evals with 1 load, and unknown with 1 load", c)
	}
	if _, ok := c["sync"]; ok {
		t.Errorf("by_client carries a sync client: %v", c)
	}
	if h["claude-code"]["syncs"] != 1 || h["cursor"]["loads"] != 1 || h["unknown"]["loads"] != 2 {
		t.Errorf("by_harness = %v, want claude-code with 1 sync, cursor with 1 load, and unknown with 2 loads", h)
	}
}
