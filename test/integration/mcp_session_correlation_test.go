package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// Spec: §5 / §3.3 — the bridge advertises sessionCorrelation and
// backs it: it threads one per-process session_id through every meta-tool
// call to the registry. This drives the real binary against a recording
// registry stub and checks the session_id the registry actually receives
// is non-empty and identical across calls.
func TestPodiumMCP_AdvertisesAndThreadsSession(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := map[string]string{} // path -> session_id
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.URL.Query().Get("session_id")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/search_artifacts":
			_, _ = w.Write([]byte(`{"query":"x","total_matched":0,"results":[]}`))
		case "/v1/load_artifact":
			_, _ = w.Write([]byte(`{"id":"finance/a","version":"1.0.0","content_hash":"sha256:x","type":"context"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(reg.Close)

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+reg.URL, "PODIUM_CACHE_DIR="+t.TempDir())
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "initialize", ID: 1},
		{Method: "tools/call", ID: 2, Params: map[string]any{
			"name": "search_artifacts", "arguments": map[string]any{"query": "x"}}},
		{Method: "tools/call", ID: 3, Params: map[string]any{
			"name": "load_artifact", "arguments": map[string]any{"id": "finance/a"}}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, stdout.String())
	}

	// initialize advertises sessionCorrelation: true (§5).
	dec := json.NewDecoder(&stdout)
	var init struct {
		Result struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	if err := dec.Decode(&init); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if sc, _ := init.Result.Capabilities["sessionCorrelation"].(bool); !sc {
		t.Errorf("sessionCorrelation = %v, want true", init.Result.Capabilities["sessionCorrelation"])
	}

	mu.Lock()
	defer mu.Unlock()
	searchSess := seen["/v1/search_artifacts"]
	loadSess := seen["/v1/load_artifact"]
	if searchSess == "" {
		t.Errorf("registry saw no session_id on search_artifacts")
	}
	if loadSess == "" {
		t.Errorf("registry saw no session_id on load_artifact")
	}
	if searchSess != loadSess {
		t.Errorf("session_id differs across calls: search=%q load=%q", searchSess, loadSess)
	}
}

// Spec: §4.7.6 / §5 — end to end through the real binary against
// the real registry: a bridge process pins `latest` for its session, so a
// version ingested mid-session does not change what that process loads,
// while a fresh process sees the new latest. Proves the bridge-threaded
// session_id is honored by the registry's session-consistent resolution.
func TestPodiumMCP_SessionPinsLatestEndToEnd(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t0 := time.Now().UTC()
	// ContentHash is the canonical hash of the served frontmatter so the
	// bridge's §6.6 step 2 content-hash check accepts the load.
	put := func(ver string, at time.Time) {
		fm := "---\ntype: context\nversion: " + ver +
			"\ndescription: A reusable finance context for the team.\nsensitivity: low\n---\n"
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "default", ArtifactID: "finance/a", Version: ver,
			ContentHash: "sha256:" + version.ContentHash([]byte(fm)), Type: "context",
			Sensitivity: "low", Layer: "L", IngestedAt: at, Frontmatter: []byte(fm),
		}); err != nil {
			t.Fatalf("PutManifest %s: %v", ver, err)
		}
	}
	put("1.0.0", t0)
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)

	bin := buildMCP(t)

	// Process A: interactive. Pins v1, then sees v1 again after v2 ingest.
	a := newBridgeProc(t, bin, ts.URL)
	if v := a.loadVersion(t, "finance/a"); v != "1.0.0" {
		t.Fatalf("A first load = %q, want 1.0.0", v)
	}
	put("2.0.0", t0.Add(time.Hour)) // newer version mid-session
	if v := a.loadVersion(t, "finance/a"); v != "1.0.0" {
		t.Errorf("A second load = %q, want 1.0.0 (session pinned)", v)
	}
	a.close()

	// Process B: a fresh session pins to the current latest, 2.0.0.
	b := newBridgeProc(t, bin, ts.URL)
	if v := b.loadVersion(t, "finance/a"); v != "2.0.0" {
		t.Errorf("B load = %q, want 2.0.0 (fresh session sees new latest)", v)
	}
	b.close()
}

// bridgeProc drives one long-lived podium-mcp process over stdin/stdout.
type bridgeProc struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Scanner
	id  int
}

func newBridgeProc(t *testing.T, bin, registryURL string) *bridgeProc {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+registryURL, "PODIUM_CACHE_DIR="+t.TempDir())
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	p := &bridgeProc{cmd: cmd, in: in, out: sc}
	t.Cleanup(p.close)
	return p
}

// loadVersion sends one load_artifact request and returns the resolved
// version, with a hard read deadline so a hung bridge fails the test
// rather than blocking forever (anti-hang).
func (p *bridgeProc) loadVersion(t *testing.T, id string) string {
	t.Helper()
	p.id++
	req := map[string]any{
		"jsonrpc": "2.0", "id": p.id, "method": "tools/call",
		"params": map[string]any{"name": "load_artifact", "arguments": map[string]any{"id": id}},
	}
	body, _ := json.Marshal(req)
	if _, err := p.in.Write(append(body, '\n')); err != nil {
		t.Fatalf("write request: %v", err)
	}
	line := make(chan string, 1)
	go func() {
		if p.out.Scan() {
			line <- p.out.Text()
			return
		}
		line <- ""
	}()
	select {
	case l := <-line:
		if l == "" {
			t.Fatalf("no response from bridge (id=%d)", p.id)
		}
		var resp struct {
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal([]byte(l), &resp); err != nil {
			t.Fatalf("decode response %q: %v", l, err)
		}
		v, _ := resp.Result["version"].(string)
		return v
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for bridge response (id=%d)", p.id)
		return ""
	}
}

func (p *bridgeProc) close() {
	_ = p.in.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
}
