package serverboot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// sinkServer pairs a server with the audit sink its read events flow to, so
// a test can drive a request and read back the emitted §8.1 record. The read
// path emits artifacts.searched unconditionally (§4.7.5), so an empty
// registry suffices.
type sinkServer struct {
	srv  *server.Server
	sink *audit.FileSink
}

func newReadEventServer(t *testing.T, opts ...server.Option) sinkServer {
	t.Helper()
	st := store.NewMemory()
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	reg := core.New(st, "default", nil).WithAudit(auditEmitterFor(sink, audit.NewPIIScrubber(), nil))
	return sinkServer{srv: server.New(reg, opts...), sink: sink}
}

// readRequest is the search request drive sends. An empty query searches for
// "variance"; an empty header value leaves that header off the request.
type readRequest struct {
	traceparent   string
	forwardedUser string
	client        string
	query         string
}

func (s sinkServer) drive(t *testing.T, rr readRequest) string {
	t.Helper()
	ts := httptest.NewServer(s.srv.Handler())
	t.Cleanup(ts.Close)
	query := rr.query
	if query == "" {
		query = "variance"
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/search_artifacts?query="+url.QueryEscape(query), nil)
	for name, v := range map[string]string{
		"traceparent":      rr.traceparent,
		"X-Forwarded-User": rr.forwardedUser,
		audit.ClientHeader: rr.client,
	} {
		if v != "" {
			req.Header.Set(name, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET search_artifacts: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d, want 200", resp.StatusCode)
	}
	// Emission is synchronous within the handler, but poll briefly to absorb
	// any filesystem flush latency on slower CI.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := readBytes(s.sink.Path()); len(b) > 0 {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("audit log empty after search")
	return ""
}

// spec: §8.1 ("Caller identity in audit events") + §8.1 trace id — an
// authenticated read records the caller's email and groups from the OAuth
// identity and the request's W3C trace id, and carries no public-mode
// network block. This exercises the full read path: the server identity
// middleware attaches the §8.1 metadata to the request context, the core
// emitter forwards it, and auditEmitterFor writes it to the sink.
func TestReadEvent_AuthenticatedCallerCarriesEmailGroupsAndTrace(t *testing.T) {
	s := newReadEventServer(t, server.WithIdentityResolver(func(*http.Request) layer.Identity {
		return layer.Identity{
			Sub: "alice", Email: "alice@acme.com", Groups: []string{"eng", "sec"},
			IsAuthenticated: true,
		}
	}))
	got := s.drive(t, readRequest{traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"})
	for _, want := range []string{
		`"type":"artifacts.searched"`,
		`"caller":{"identity":"alice"`,
		`"email":"alice@acme.com"`,
		`"groups":["eng","sec"]`,
		`"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("authenticated read audit missing %s\nlog:\n%s", want, got)
		}
	}
	if strings.Contains(got, "public_mode") || strings.Contains(got, `"network"`) {
		t.Errorf("authenticated read leaked public-mode fields:\n%s", got)
	}
}

// spec: §8.1 / §13.2.2 / §13.10 — a public-mode read records
// caller.identity=system:public, the caller.public_mode flag, and the source
// IP and X-Forwarded-User under caller.network so a SIEM can filter without
// parsing identity strings. A request without a traceparent still carries a
// generated trace id.
func TestReadEvent_PublicCallerCarriesNetworkAndFlag(t *testing.T) {
	s := newReadEventServer(t, server.WithPublicMode())
	got := s.drive(t, readRequest{forwardedUser: "upstream-bob"})
	for _, want := range []string{
		`"type":"artifacts.searched"`,
		`"caller":{"identity":"system:public"`,
		`"public_mode":true`,
		`"network":{"source_ip":"127.0.0.1","forwarded_user":"upstream-bob"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("public read audit missing %s\nlog:\n%s", want, got)
		}
	}
	if strings.Contains(got, "email") || strings.Contains(got, "groups") {
		t.Errorf("public read leaked authenticated fields:\n%s", got)
	}
	if !strings.Contains(got, `"trace_id"`) {
		t.Errorf("public read missing generated trace_id:\n%s", got)
	}
}

// Spec: §7.2.2, §8.1 — a search that names its client in X-Podium-Client
// records the normalized value as caller.client on artifacts.searched, beside
// the PII-scrubbed context.query. caller.client is event metadata, so the §8.2
// query scrub leaves it unchanged while it removes the email from the query.
func TestReadEvent_RecordsCallerClientBesideScrubbedQuery(t *testing.T) {
	s := newReadEventServer(t)
	got := s.drive(t, readRequest{
		client: testClientHeader,
		query:  "variance report for alice@acme.com",
	})
	var ev auditLine
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &ev); err != nil {
		t.Fatalf("decode audit line: %v\nlog:\n%s", err, got)
	}
	if ev.Type != string(audit.EventArtifactsSearched) {
		t.Fatalf("type = %s, want artifacts.searched", ev.Type)
	}
	if !reflect.DeepEqual(ev.Caller.Client, testClient) {
		t.Errorf("caller.client = %v, want %v", ev.Caller.Client, testClient)
	}
	q := ev.Context["query"]
	if q == "" || strings.Contains(q, "alice@acme.com") {
		t.Errorf("context.query = %q, want the query with the email scrubbed", q)
	}
	if strings.Contains(got, "alice@acme.com") {
		t.Errorf("audit line carries the unscrubbed email:\n%s", got)
	}
}
