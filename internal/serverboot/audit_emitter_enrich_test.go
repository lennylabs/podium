package serverboot

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func (s sinkServer) drive(t *testing.T, traceparent, forwardedUser string) string {
	t.Helper()
	ts := httptest.NewServer(s.srv.Handler())
	t.Cleanup(ts.Close)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/search_artifacts?query=variance", nil)
	if traceparent != "" {
		req.Header.Set("traceparent", traceparent)
	}
	if forwardedUser != "" {
		req.Header.Set("X-Forwarded-User", forwardedUser)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET search_artifacts: %v", err)
	}
	resp.Body.Close()
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
// F-8.1.1, F-8.1.6.
func TestReadEvent_AuthenticatedCallerCarriesEmailGroupsAndTrace(t *testing.T) {
	s := newReadEventServer(t, server.WithIdentityResolver(func(*http.Request) layer.Identity {
		return layer.Identity{
			Sub: "alice", Email: "alice@acme.com", Groups: []string{"eng", "sec"},
			IsAuthenticated: true,
		}
	}))
	got := s.drive(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "")
	for _, want := range []string{
		`"type":"artifacts.searched"`,
		`"caller":"alice"`,
		`"caller_email":"alice@acme.com"`,
		`"caller_groups":["eng","sec"]`,
		`"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("authenticated read audit missing %s\nlog:\n%s", want, got)
		}
	}
	if strings.Contains(got, "caller_public_mode") || strings.Contains(got, "caller_network") {
		t.Errorf("authenticated read leaked public-mode fields:\n%s", got)
	}
}

// spec: §8.1 — a public-mode read records caller.identity=system:public, the
// caller_public_mode flag, and the source IP and X-Forwarded-User in
// caller.network so a SIEM can filter without parsing identity strings.
// A request without a traceparent still carries a generated trace id.
// F-8.1.1, F-8.1.6.
func TestReadEvent_PublicCallerCarriesNetworkAndFlag(t *testing.T) {
	s := newReadEventServer(t, server.WithPublicMode())
	got := s.drive(t, "", "upstream-bob")
	for _, want := range []string{
		`"type":"artifacts.searched"`,
		`"caller":"system:public"`,
		`"caller_public_mode":true`,
		`"source_ip":"127.0.0.1"`,
		`"forwarded_user":"upstream-bob"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("public read audit missing %s\nlog:\n%s", want, got)
		}
	}
	if strings.Contains(got, "caller_email") || strings.Contains(got, "caller_groups") {
		t.Errorf("public read leaked authenticated fields:\n%s", got)
	}
	if !strings.Contains(got, `"trace_id"`) {
		t.Errorf("public read missing generated trace_id:\n%s", got)
	}
}
