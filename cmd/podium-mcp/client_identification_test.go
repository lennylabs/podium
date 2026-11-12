package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
)

// mcpClientMembers returns the X-Podium-Client members podium-mcp reports
// for a host and a harness. An empty host or hostVersion is left out, as the
// encoder omits empty members.
func mcpClientMembers(host, hostVersion, harness string) map[string]string {
	m := map[string]string{
		"kind": "mcp", "name": "podium-mcp", "version": buildinfo.Version, "harness": harness,
	}
	if host != "" {
		m["host"] = host
	}
	if hostVersion != "" {
		m["host_version"] = hostVersion
	}
	return m
}

// headerMembers parses an X-Podium-Client value into its members. The value
// is form-encoded with sorted keys (§7.2.2), so tests compare members rather
// than the literal string.
func headerMembers(t *testing.T, header string) map[string]string {
	t.Helper()
	q, err := url.ParseQuery(header)
	if err != nil {
		t.Fatalf("parse X-Podium-Client %q: %v", header, err)
	}
	m := map[string]string{}
	for k := range q {
		m[k] = q.Get(k)
	}
	return m
}

// assertClientMembers fails when header does not carry exactly want.
func assertClientMembers(t *testing.T, label, header string, want map[string]string) {
	t.Helper()
	if got := headerMembers(t, header); !reflect.DeepEqual(got, want) {
		t.Errorf("%s X-Podium-Client = %v, want %v", label, got, want)
	}
}

// clientStub is a registry stub that records the X-Podium-Client value of
// every request by path and answers the meta-tool routes with minimal bodies.
type clientStub struct {
	srv *httptest.Server
	mu  sync.Mutex
	// headers maps a request path to the X-Podium-Client values received on
	// it, in arrival order. mu guards it against the handler goroutines.
	headers map[string][]string
}

func newClientStub(t *testing.T) *clientStub {
	t.Helper()
	loadBody := loadArtifactJSON(t, map[string]any{
		"id": "team/x", "type": "context", "version": "1.0.0",
		"frontmatter": "---\ntype: context\n---\n",
	})
	st := &clientStub{headers: map[string][]string{}}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.headers[r.URL.Path] = append(st.headers[r.URL.Path], r.Header.Get(audit.ClientHeader))
		st.mu.Unlock()
		switch r.URL.Path {
		case "/v1/search_artifacts":
			_, _ = w.Write([]byte(`{"total_matched":0,"results":[]}`))
		case "/v1/load_domain":
			_, _ = w.Write([]byte(`{"path":"","subdomains":[]}`))
		case "/v1/load_artifact":
			_, _ = w.Write([]byte(loadBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(st.srv.Close)
	return st
}

// received returns the single X-Podium-Client value the stub received on
// path, failing when the path saw a different number of requests.
func (st *clientStub) received(t *testing.T, path string) string {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.headers[path]) != 1 {
		t.Fatalf("%s received %d requests, want 1", path, len(st.headers[path]))
	}
	return st.headers[path][0]
}

// requestCount returns how many requests the stub received on any path.
func (st *clientStub) requestCount() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for _, hs := range st.headers {
		n += len(hs)
	}
	return n
}

// newAuditedServer builds a test server against registry with a local
// FileSink and returns it with the sink's path.
func newAuditedServer(t *testing.T, registry string) (*mcpServer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, &config{
		registry:     registry,
		harness:      "claude-code",
		verifyPolicy: sign.PolicyNever,
	})
	s.audit = sink
	s.sessionID = "sess-client"
	return s, path
}

// localEvent is the part of a local audit line these tests read.
type localEvent struct {
	Type   string `json:"type"`
	Caller struct {
		Client map[string]string `json:"client"`
	} `json:"caller"`
	Context map[string]string `json:"context"`
}

// localEvents decodes every event the local FileSink at path holds.
func localEvents(t *testing.T, path string) []localEvent {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []localEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev localEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode local event %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

// assertCleanMember fails when v holds a byte below 0x20 or an '@', the
// bytes the §7.2.2 sanitization replaces.
func assertCleanMember(t *testing.T, label, v string) {
	t.Helper()
	if v == "" {
		t.Fatalf("%s is empty", label)
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] == '@' {
			t.Errorf("%s = %q holds byte %#x at %d", label, v, v[i], i)
		}
	}
}

// Spec: §7.2.2, §8.1, §8.3 — the accepted initialize's clientInfo becomes
// the host and host_version members of the registry request.
func TestClientIdentification_HostFromInitialize(t *testing.T) {
	t.Parallel()
	st := newClientStub(t)
	s, _ := newAuditedServer(t, st.srv.URL)
	if resp := initWithClient(s, "claude-code", "1.0.33"); resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	_ = s.callTool([]byte(`{"name":"search_artifacts","arguments":{"query":"forecast"}}`))
	assertClientMembers(t, "search_artifacts", st.received(t, "/v1/search_artifacts"),
		mcpClientMembers("claude-code", "1.0.33", "claude-code"))
}

// Spec: §7.2.2, §8.1, §8.3 — a host name carrying control bytes and an '@'
// reaches neither the header nor the local caller.client.host unsanitized.
func TestClientIdentification_HostSanitized(t *testing.T) {
	t.Parallel()
	st := newClientStub(t)
	s, path := newAuditedServer(t, st.srv.URL)
	if resp := initWithClient(s, "evil\u001b[2J\n@x", "1.0"); resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	_ = s.callTool([]byte(`{"name":"search_artifacts","arguments":{"query":"forecast"}}`))

	header := st.received(t, "/v1/search_artifacts")
	assertCleanMember(t, "header host", headerMembers(t, header)["host"])
	events := localEvents(t, path)
	if len(events) != 1 {
		t.Fatalf("got %d local events, want 1", len(events))
	}
	assertCleanMember(t, "local caller.client.host", events[0].Caller.Client["host"])
}

// Spec: §7.2.2 — an initialize that negotiateProtocol refuses, or that the
// §6.9 version floor refuses, records no host, so a later call sends no host
// or host_version member.
func TestClientIdentification_RefusedInitializeRecordsNoHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, protocol, floor string
	}{
		{name: "unsupported protocol", protocol: "2024-01-01"},
		{name: "client below floor", protocol: protocolVersion, floor: "2.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := newClientStub(t)
			s, _ := newAuditedServer(t, st.srv.URL)
			s.cfg.minClientVersion = tc.floor
			params, _ := json.Marshal(map[string]any{
				"protocolVersion": tc.protocol,
				"clientInfo":      map[string]any{"name": "claude-code", "version": "1.0.33"},
			})
			resp := s.handle(rpcRequest{JSONRPC: "2.0", Method: "initialize", Params: params})
			if resp.Error == nil {
				t.Fatalf("initialize was accepted: %+v", resp.Result)
			}
			if s.hostName != "" || s.hostVersion != "" {
				t.Errorf("refused initialize recorded host %q/%q", s.hostName, s.hostVersion)
			}
			_ = s.callTool([]byte(`{"name":"search_artifacts","arguments":{"query":"forecast"}}`))
			assertClientMembers(t, "search_artifacts", st.received(t, "/v1/search_artifacts"),
				mcpClientMembers("", "", "claude-code"))
		})
	}
}

// Spec: §8.1, §8.3, §7.2.2 — the local search, load_domain, and
// artifact.loaded events record the caller.client the registry request
// carried, and none carries a context.source key.
func TestClientIdentification_LocalEventsMatchRequests(t *testing.T) {
	t.Parallel()
	st := newClientStub(t)
	s, path := newAuditedServer(t, st.srv.URL)
	if resp := initWithClient(s, "cursor", "2.0"); resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	_ = s.callTool([]byte(`{"name":"search_artifacts","arguments":{"query":"forecast"}}`))
	_ = s.callTool([]byte(`{"name":"load_domain","arguments":{"path":""}}`))
	_ = s.callTool([]byte(`{"name":"load_artifact","arguments":{"id":"team/x","harness":"codex"}}`))

	events := localEvents(t, path)
	wantOrder := []struct {
		event   audit.EventType
		path    string
		harness string
	}{
		{audit.EventArtifactsSearched, "/v1/search_artifacts", "claude-code"},
		{audit.EventDomainLoaded, "/v1/load_domain", "claude-code"},
		{audit.EventArtifactLoaded, "/v1/load_artifact", "codex"},
	}
	if len(events) != len(wantOrder) {
		t.Fatalf("got %d local events, want %d: %+v", len(events), len(wantOrder), events)
	}
	for i, want := range wantOrder {
		ev := events[i]
		if ev.Type != string(want.event) {
			t.Errorf("event %d type = %q, want %q", i, ev.Type, want.event)
		}
		sent := headerMembers(t, st.received(t, want.path))
		if !reflect.DeepEqual(ev.Caller.Client, sent) {
			t.Errorf("%s caller.client = %v, request carried %v", want.event, ev.Caller.Client, sent)
		}
		if !reflect.DeepEqual(sent, mcpClientMembers("cursor", "2.0", want.harness)) {
			t.Errorf("%s request members = %v", want.path, sent)
		}
		if _, ok := ev.Context["source"]; ok {
			t.Errorf("%s carries context.source: %v", want.event, ev.Context)
		}
	}
}

// Spec: §8.1, §8.3, §7.2.2 — an overlay load_artifact sends no registry
// request and still records caller.client on its local event.
func TestClientIdentification_OverlayLoadRecordsClient(t *testing.T) {
	t.Parallel()
	st := newClientStub(t)
	s, path := newAuditedServer(t, st.srv.URL)
	if resp := initWithClient(s, "cursor", "2.0"); resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	s.setOverlay([]filesystem.ArtifactRecord{{
		ID:            "personal/hello/greet",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"),
		AuthoredBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"),
		SkillBytes:    []byte("---\nname: greet\n---\nbody\n"),
		Artifact:      &manifest.Artifact{Type: manifest.TypeSkill, Version: "1.0.0", Body: "body"},
	}}, nil)

	_ = s.callTool([]byte(`{"name":"load_artifact","arguments":{"id":"personal/hello/greet","harness":"codex"}}`))

	if n := st.requestCount(); n != 0 {
		t.Errorf("overlay load sent %d registry requests, want 0", n)
	}
	events := localEvents(t, path)
	if len(events) != 1 || events[0].Type != string(audit.EventArtifactLoaded) {
		t.Fatalf("local events = %+v, want one artifact.loaded", events)
	}
	if want := mcpClientMembers("cursor", "2.0", "codex"); !reflect.DeepEqual(events[0].Caller.Client, want) {
		t.Errorf("overlay caller.client = %v, want %v", events[0].Caller.Client, want)
	}
	if _, ok := events[0].Context["source"]; ok {
		t.Errorf("overlay event carries context.source: %v", events[0].Context)
	}
}

// Spec: §7.2.2 — X-Podium-Client goes on control-plane requests only: a SigV4
// presigned link, an /objects link, and the /readyz and /healthz probes carry
// none. The search request is the positive control that the stub observes
// the header when it is sent.
func TestClientIdentification_NoHeaderOnLinksAndProbes(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	sent := map[string]bool{} // path -> whether X-Podium-Client was present
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		_, sent[r.URL.Path] = r.Header[audit.ClientHeader]
		mu.Unlock()
		switch r.URL.Path {
		case "/readyz":
			_, _ = w.Write([]byte(`{"mode":"ready"}`))
		case "/healthz":
			_, _ = w.Write([]byte(`{"mode":"ready"}`))
		case "/v1/search_artifacts":
			_, _ = w.Write([]byte(`{"total_matched":0,"results":[]}`))
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	t.Cleanup(ts.Close)
	s := newTestServer(t, &config{
		registry:     ts.URL,
		sessionToken: "session-jwt",
		tenantID:     "acme",
		harness:      "claude-code",
		verifyPolicy: sign.PolicyNever,
	})

	sigV4URL := ts.URL + "/blob?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AK%2F20260604%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Signature=deadbeef"
	for _, link := range []string{sigV4URL, ts.URL + "/objects/sha256:abc"} {
		if _, status, err := s.getLargeResource(link); err != nil || status != http.StatusOK {
			t.Fatalf("getLargeResource %s: status=%d err=%v", link, status, err)
		}
	}
	if connected, mode := s.probeReadyMode(); !connected || mode != "ready" {
		t.Fatalf("probeReadyMode = %v, %q", connected, mode)
	}
	_ = s.probeHealthzPublic()
	if _, err := s.fetchJSON("/v1/search_artifacts", map[string]any{"query": "x"}); err != nil {
		t.Fatalf("fetchJSON: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range []string{"/blob", "/objects/sha256:abc", "/readyz", "/healthz"} {
		got, seen := sent[p]
		if !seen {
			t.Errorf("%s received no request", p)
		}
		if got {
			t.Errorf("%s received X-Podium-Client", p)
		}
	}
	if !sent["/v1/search_artifacts"] {
		t.Error("control-plane search carried no X-Podium-Client")
	}
}
