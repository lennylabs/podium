package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// usageHarness is the overlap harness with a usage counter wired, two team
// artifacts, and one artifact in the secret layer, which only the eng group
// sees.
func usageHarness(t *testing.T) (*overlapHarness, *core.UsageCounter) {
	t.Helper()
	h := newOverlapHarness(t)
	counter := core.NewUsageCounter(nil)
	h.reg.WithUsageCounts(counter)
	h.write("team", "fin/close", "1.0.0", "Close the monthly books for the finance team.")
	h.write("team", "fin/reconcile", "1.0.0", "Reconcile vendor invoices before the close.")
	h.write("secret", "fin/payroll", "1.0.0", "Run payroll for the finance team each month.")
	h.reingest("team")
	h.reingest("secret")
	return h, counter
}

// usageCall sends one request to the registry API as user, with the client
// header when client is not empty, and returns the status and body.
func usageCall(t *testing.T, h *overlapHarness, method, path, body, user, group, client string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, h.api.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-User", user)
	if group != "" {
		req.Header.Set("X-Test-Group", group)
	}
	if client != "" {
		req.Header.Set("X-Podium-Client", client)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type usageResp struct {
	Totals struct {
		Loads      int64                       `json:"loads"`
		Syncs      int64                       `json:"syncs"`
		SearchHits int64                       `json:"search_hits"`
		ByHarness  map[string]core.UsageCounts `json:"by_harness"`
		ByClient   map[string]core.UsageCounts `json:"by_client"`
	} `json:"totals"`
	Layers []struct {
		Layer string `json:"layer"`
	} `json:"layers"`
	Artifacts []struct {
		ArtifactID string           `json:"artifact_id"`
		Loads      int64            `json:"loads"`
		Series     []map[string]any `json:"series"`
	} `json:"artifacts"`
}

func decodeUsage(t *testing.T, body string) usageResp {
	t.Helper()
	var r usageResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decode usage: %v\n%s", err, body)
	}
	return r
}

// Spec: §8.7, §7.2.2 — loads and searches served over HTTP count under the
// client kind and harness of their X-Podium-Client header, and GET /v1/usage
// reports them over the caller's effective view only: a caller outside the
// secret layer's group sees neither its counts nor its layer.
func TestUsageReport_HTTPCountsByClientAndVisibility(t *testing.T) {
	t.Parallel()
	h, counter := usageHarness(t)
	mcp := "kind=mcp&name=podium-mcp&harness=claude-code"
	for _, c := range []struct{ path, group, client string }{
		{"/v1/load_artifact?id=fin/close", "", mcp},
		{"/v1/load_artifact?id=fin/close", "", "kind=sync&name=podium&harness=cursor"},
		{"/v1/load_artifact?id=fin/payroll", "eng", mcp},
		{"/v1/search_artifacts?query=finance", "eng", "kind=sdk&name=podium-py"},
	} {
		if st, body := usageCall(t, h, http.MethodGet, c.path, "", "alice", c.group, c.client); st != http.StatusOK {
			t.Fatalf("%s: %d %s", c.path, st, body)
		}
	}
	if err := counter.Flush(context.Background(), h.st); err != nil {
		t.Fatal(err)
	}
	st, body := usageCall(t, h, http.MethodGet, "/v1/usage", "", "alice", "eng", "")
	eng := decodeUsage(t, body)
	// The harness's hybrid search returns all three artifacts for the query,
	// and eng sees the team, secret, and hook layers.
	if st != http.StatusOK || eng.Totals.Loads != 2 || eng.Totals.Syncs != 1 || eng.Totals.SearchHits != 3 ||
		eng.Totals.ByHarness["cursor"].Syncs != 1 || eng.Totals.ByClient["sdk"].SearchHits != 3 || len(eng.Layers) != 3 {
		t.Errorf("eng report: %d %s", st, body)
	}
	_, body = usageCall(t, h, http.MethodGet, "/v1/usage", "", "alice", "", "")
	team := decodeUsage(t, body)
	if team.Totals.Loads != 1 || team.Totals.Syncs != 1 || team.Totals.SearchHits != 2 || len(team.Layers) != 2 || len(team.Artifacts) != 2 {
		t.Errorf("team-only report should hold the team and hook layers, two artifacts, and no payroll counts: %s", body)
	}
	for _, l := range team.Layers {
		if l.Layer == "secret" {
			t.Errorf("a caller outside eng sees the secret layer: %s", body)
		}
	}
}

// Spec: §8.7 — POST /v1/usage takes the parameters as a JSON body, repeated
// artifact parameters read several artifacts at once, a granularity adds a
// series, and both methods serve on a read-only registry; an unknown body
// member, a bad days, and another method are refused.
func TestUsageReport_HTTPBatchAndErrors(t *testing.T) {
	t.Parallel()
	h, _ := usageHarness(t)
	_, body := usageCall(t, h, http.MethodPost, "/v1/usage", `{"artifacts":["fin/close","fin/reconcile"],"granularity":"day","days":2}`, "alice", "", "")
	post := decodeUsage(t, body)
	if len(post.Artifacts) != 2 || len(post.Artifacts[0].Series) != 2 {
		t.Errorf("POST batch: %s", body)
	}
	_, getBody := usageCall(t, h, http.MethodGet, "/v1/usage?artifact=fin/close&artifact=fin/reconcile&granularity=day&days=2", "", "alice", "", "")
	if getBody != body {
		t.Errorf("GET with repeated artifacts differs from the POST body form:\nGET  %s\nPOST %s", getBody, body)
	}
	h.mode.Set(server.ModeReadOnly)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		payload := ""
		if method == http.MethodPost {
			payload = `{}`
		}
		if st, b := usageCall(t, h, method, "/v1/usage", payload, "alice", "", ""); st != http.StatusOK {
			t.Errorf("%s on a read-only registry: %d %s", method, st, b)
		}
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/v1/usage", `{"artifact":["x"]}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/usage", `{"days":0}`, http.StatusBadRequest},
		{http.MethodGet, "/v1/usage?days=abc", "", http.StatusBadRequest},
		{http.MethodGet, "/v1/usage?granularity=year", "", http.StatusBadRequest},
		{http.MethodGet, "/v1/usage?limit=0", "", http.StatusBadRequest},
		{http.MethodGet, "/v1/usage?limit=x", "", http.StatusBadRequest},
		{http.MethodGet, "/v1/usage?limit=201", "", http.StatusBadRequest},
		{http.MethodGet, "/v1/usage?cursor=!!", "", http.StatusBadRequest},
		{http.MethodPost, "/v1/usage", `{"limit":0}`, http.StatusBadRequest},
		{http.MethodPost, "/v1/usage", `{"cursor":"!!"}`, http.StatusBadRequest},
		{http.MethodPut, "/v1/usage", "", http.StatusMethodNotAllowed},
	} {
		st, b := usageCall(t, h, c.method, c.path, c.body, "alice", "", "")
		if st != c.want || !strings.Contains(b, "registry.invalid_argument") {
			t.Errorf("%s %s %s: %d %s, want %d registry.invalid_argument", c.method, c.path, c.body, st, b, c.want)
		}
	}
}

// Spec: §8.7, §7.6.2, §13.4 — each resolved artifacts:batchLoad item counts
// one load, or one sync under client kind sync, and an unresolved item counts
// nothing; a HEAD and a GET that If-None-Match answers 304 each count one
// load, like the full GET that returned the validator.
func TestUsageReport_HTTPBatchItemsAndRevalidatedLoads(t *testing.T) {
	t.Parallel()
	h, counter := usageHarness(t)
	mcp := "kind=mcp&name=podium-mcp&harness=claude-code"
	batch := `{"ids":["fin/close","fin/reconcile","fin/missing"]}`
	if st, body := usageCall(t, h, http.MethodPost, "/v1/artifacts:batchLoad", batch, "alice", "", mcp); st != http.StatusOK {
		t.Fatalf("batchLoad: %d %s", st, body)
	}
	if st, body := usageCall(t, h, http.MethodPost, "/v1/artifacts:batchLoad", batch, "alice", "", "kind=sync&name=podium&harness=cursor"); st != http.StatusOK {
		t.Fatalf("batchLoad as sync: %d %s", st, body)
	}
	send := func(method, etag string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, h.api.URL+"/v1/load_artifact?id=fin/close", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Test-User", "alice")
		req.Header.Set("X-Podium-Client", mcp)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp
	}
	full := send(http.MethodGet, "")
	etag := full.Header.Get("ETag")
	if full.StatusCode != http.StatusOK || etag == "" {
		t.Fatalf("full GET: %d, ETag %q", full.StatusCode, etag)
	}
	if head := send(http.MethodHead, ""); head.StatusCode != http.StatusOK {
		t.Fatalf("HEAD: %d", head.StatusCode)
	}
	if cond := send(http.MethodGet, etag); cond.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET: %d, want 304", cond.StatusCode)
	}
	if err := counter.Flush(context.Background(), h.st); err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Totals struct {
			Loads int64 `json:"loads"`
			Syncs int64 `json:"syncs"`
		} `json:"totals"`
		Artifacts []struct {
			ArtifactID string `json:"artifact_id"`
			Loads      int64  `json:"loads"`
			Syncs      int64  `json:"syncs"`
		} `json:"artifacts"`
	}
	_, body := usageCall(t, h, http.MethodGet, "/v1/usage", "", "alice", "", "")
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	got := map[string][2]int64{}
	for _, a := range rep.Artifacts {
		got[a.ArtifactID] = [2]int64{a.Loads, a.Syncs}
	}
	if rep.Totals.Loads != 5 || rep.Totals.Syncs != 2 || got["fin/close"] != [2]int64{4, 1} || got["fin/reconcile"] != [2]int64{1, 1} {
		t.Errorf("want fin/close 4 loads 1 sync and fin/reconcile 1 load 1 sync: %s", body)
	}
}

// Spec: §8.7 — GET and POST page the artifact list the same way: limit bounds
// a page, next_cursor from one method continues the list through the other,
// and the last page carries a null next_cursor.
func TestUsageReport_HTTPPages(t *testing.T) {
	t.Parallel()
	h, _ := usageHarness(t)
	type page struct {
		Artifacts []struct {
			ArtifactID string `json:"artifact_id"`
		} `json:"artifacts"`
		ArtifactCount int     `json:"artifact_count"`
		NextCursor    *string `json:"next_cursor"`
	}
	read := func(method, path, body string) page {
		t.Helper()
		st, raw := usageCall(t, h, method, path, body, "alice", "", "")
		var p page
		if st != http.StatusOK || json.Unmarshal([]byte(raw), &p) != nil {
			t.Fatalf("%s %s %s: %d %s", method, path, body, st, raw)
		}
		return p
	}
	first := read(http.MethodGet, "/v1/usage?limit=1", "")
	if len(first.Artifacts) != 1 || first.ArtifactCount != 2 || first.NextCursor == nil {
		t.Fatalf("first page: %+v", first)
	}
	second := read(http.MethodPost, "/v1/usage", `{"limit":1,"cursor":"`+*first.NextCursor+`"}`)
	if len(second.Artifacts) != 1 || second.NextCursor != nil || second.Artifacts[0].ArtifactID == first.Artifacts[0].ArtifactID {
		t.Errorf("second page: %+v after %+v", second, first)
	}
}

// Spec: §8.7 — GET with repeated type parameters and POST with a types member
// restrict the artifact list the same way, and an unknown type lists nothing.
func TestUsageReport_HTTPTypeFilter(t *testing.T) {
	t.Parallel()
	h, _ := usageHarness(t)
	_, get := usageCall(t, h, http.MethodGet, "/v1/usage?type=skill&type=agent", "", "alice", "", "")
	_, post := usageCall(t, h, http.MethodPost, "/v1/usage", `{"types":["skill","agent"]}`, "alice", "", "")
	if get != post {
		t.Errorf("GET and POST type filters differ:\nGET  %s\nPOST %s", get, post)
	}
	if rep := decodeUsage(t, get); len(rep.Artifacts) == 0 {
		t.Errorf("type=skill&type=agent lists no artifact: %s", get)
	}
	if _, none := usageCall(t, h, http.MethodGet, "/v1/usage?type=no-such-type", "", "alice", "", ""); len(decodeUsage(t, none).Artifacts) != 0 {
		t.Errorf("an unknown type lists artifacts: %s", none)
	}
}
