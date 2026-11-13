package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/lennylabs/podium/internal/testharness/auditharness"
	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §6.2, §8.3 — newAuditSink opens a stream with a file destination for
// a path and a stream with no file destination for an http(s) URL, whose
// receiver gets each record once the stream closes; an unset var leaves
// auditing to the registry.
func TestNewAuditSink_Routing(t *testing.T) {
	t.Run("unset is registry-only", func(t *testing.T) {
		stream, err := newAuditSink(&config{}, nil)
		if err != nil {
			t.Fatalf("newAuditSink: %v", err)
		}
		if stream != nil {
			t.Errorf("unset audit stream = %v, want nil", stream)
		}
	})
	t.Run("path selects a file destination", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.log")
		stream, err := newAuditSink(&config{auditSink: path, auditSinkSet: true}, nil)
		if err != nil {
			t.Fatalf("newAuditSink: %v", err)
		}
		t.Cleanup(stream.Shutdown)
		if stream.File() == nil || stream.File().Path() != path {
			t.Errorf("file destination = %v, want %s", stream.File(), path)
		}
	})
	t.Run("http url selects an HTTP destination", func(t *testing.T) {
		var mu sync.Mutex
		var body []byte
		rcv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			body = append(body, b...)
			mu.Unlock()
		}))
		t.Cleanup(rcv.Close)
		stream, err := newAuditSink(&config{auditSink: rcv.URL, auditSinkSet: true}, nil)
		if err != nil {
			t.Fatalf("newAuditSink: %v", err)
		}
		t.Cleanup(stream.Shutdown)
		if stream.File() != nil {
			t.Errorf("file destination = %v, want nil for an HTTP destination", stream.File())
		}
		if err := stream.Append(context.Background(), audit.Event{Type: audit.EventArtifactLoaded, Caller: "alice"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := stream.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(string(body), `"type":"artifact.loaded"`) {
			t.Errorf("receiver body = %q, want the artifact.loaded record", body)
		}
	})
}

// Spec: §13.12, §6.10 — an unusable PODIUM_AUDIT_QUEUE_MAX_BYTES refuses with
// config.audit_sink_unavailable naming the variable, and the error still
// wraps audit.ErrConfig.
//
// Matrix: §6.10 (config.audit_sink_unavailable)
func TestNewAuditSink_RefusesUnusableQueueBound(t *testing.T) {
	t.Setenv("PODIUM_AUDIT_QUEUE_MAX_BYTES", "-1")
	path := filepath.Join(t.TempDir(), "audit.log")
	stream, err := newAuditSink(&config{auditSink: path, auditSinkSet: true}, nil)
	if stream != nil || err == nil || !strings.HasPrefix(err.Error(), "config.audit_sink_unavailable: ") ||
		!strings.Contains(err.Error(), "PODIUM_AUDIT_QUEUE_MAX_BYTES") || !errors.Is(err, audit.ErrConfig) {
		t.Errorf("newAuditSink = (%v, %v), want a config.audit_sink_unavailable refusal naming PODIUM_AUDIT_QUEUE_MAX_BYTES", stream, err)
	}
}

// Spec: §6.2, §13.12 — an unusable destination list refuses with
// config.audit_sink_unavailable naming PODIUM_AUDIT_SINK, and an unset
// variable never reads the queue bound or the transport variables.
//
// Matrix: §6.10 (config.audit_sink_unavailable)
func TestNewAuditSink_RefusesUnusableList(t *testing.T) {
	t.Setenv("PODIUM_AUDIT_QUEUE_MAX_BYTES", "-1")
	t.Setenv("PODIUM_AUDIT_HEADERS_FILE", filepath.Join(t.TempDir(), "absent-headers"))
	stream, err := newAuditSink(&config{auditSinkSet: false}, nil)
	if stream != nil || err != nil {
		t.Errorf("unset newAuditSink = (%v, %v), want (nil, nil) without reading the queue bound or the headers file", stream, err)
	}
	t.Setenv("PODIUM_AUDIT_QUEUE_MAX_BYTES", "")
	t.Setenv("PODIUM_AUDIT_HEADERS_FILE", "")
	stream, err = newAuditSink(&config{auditSink: "stdout", auditSinkSet: true}, nil)
	if stream != nil || err == nil || !strings.HasPrefix(err.Error(), "config.audit_sink_unavailable: ") ||
		!strings.Contains(err.Error(), "PODIUM_AUDIT_SINK") || !errors.Is(err, audit.ErrConfig) {
		t.Errorf("newAuditSink(stdout) = (%v, %v), want a config.audit_sink_unavailable refusal naming PODIUM_AUDIT_SINK", stream, err)
	}
}

// appendAndClose appends one event to stream and closes it within budget,
// then seals it, as the process exit does.
func appendAndClose(t *testing.T, stream *audit.Stream, budget time.Duration) {
	t.Helper()
	if err := stream.Append(context.Background(), audit.Event{Type: audit.EventArtifactLoaded, Caller: "alice"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	_ = stream.Close(ctx)
	stream.Seal()
}

// hasLoadedRecord reports whether the receiver accepted an artifact.loaded
// record.
func hasLoadedRecord(t *testing.T, rcv *auditharness.Receiver) bool {
	t.Helper()
	_, ok := rcv.WaitForRecord(t, 0, auditharness.OfType(string(audit.EventArtifactLoaded)))
	return ok
}

// Spec: §6.2, §8.3 — the --audit-sink flag and the audit-sink config-file
// key carry the same destination list as PODIUM_AUDIT_SINK: a file and an
// HTTP receiver.
func TestNewAuditSink_FlagAndConfigKeyTakeAList(t *testing.T) {
	cases := map[string]func(t *testing.T, list string) []string{
		"flag": func(_ *testing.T, list string) []string { return []string{"--audit-sink", list} },
		"config key": func(t *testing.T, list string) []string {
			path := filepath.Join(t.TempDir(), "podium-mcp.yaml")
			if err := os.WriteFile(path, []byte("audit-sink: "+list+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{"--config", path}
		},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			rcv := auditharness.NewReceiver(t)
			path := filepath.Join(t.TempDir(), "a.log")
			cfg := &config{}
			if err := applyFlagsAndConfig(cfg, args(t, path+","+rcv.URL())); err != nil {
				t.Fatalf("applyFlagsAndConfig: %v", err)
			}
			stream, err := newAuditSink(cfg, nil)
			if err != nil {
				t.Fatalf("newAuditSink: %v", err)
			}
			t.Cleanup(stream.Shutdown)
			if stream.File() == nil || stream.File().Path() != path {
				t.Errorf("file destination = %v, want %s", stream.File(), path)
			}
			appendAndClose(t, stream, 5*time.Second)
			if !hasLoadedRecord(t, rcv) {
				t.Error("the receiver holds no record after Close")
			}
		})
	}
}

// fakeObserver records the stream's delivery counts.
type fakeObserver struct {
	mu      sync.Mutex
	queued  []string
	dropped []string
}

func (o *fakeObserver) AddQueued(kind string, delta int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queued = append(o.queued, fmt.Sprintf("%s %d", kind, delta))
}

func (o *fakeObserver) Dropped(kind, reason string, n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dropped = append(o.dropped, fmt.Sprintf("%s %s %d", kind, reason, n))
}

// Spec: §6.2, §13.12 — newAuditSink passes the four transport variables and
// the queue bound to audit.Open. A receiver that only the CA file trusts and
// that requires the client certificate gets the record with the headers
// file's header. An unreadable headers file, and a key file without its
// certificate, each refuse. A 64-byte queue bound drops the record as
// queue_full, which the 64 MiB default would admit.
func TestNewAuditSink_PassesTransportSettings(t *testing.T) {
	ca := auditharness.NewCA(t)
	rcv := auditharness.NewTLSReceiver(t, ca.MutualServerTLS(t))
	files := ca.WriteTransportFiles(t, t.TempDir(), "Authorization: Bearer zq7kx\n")
	files.Setenv(t)

	stream, err := newAuditSink(&config{auditSink: rcv.URL(), auditSinkSet: true}, nil)
	if err != nil {
		t.Fatalf("newAuditSink: %v", err)
	}
	t.Cleanup(stream.Shutdown)
	appendAndClose(t, stream, 10*time.Second)
	reqs := rcv.Requests()
	if len(reqs) == 0 || !hasLoadedRecord(t, rcv) {
		t.Fatalf("the mutual-TLS receiver holds no record; %d request(s)", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer zq7kx" {
		t.Errorf("Authorization = %q, want the headers file's value", got)
	}

	refusals := map[string]func(t *testing.T){
		"unreadable headers file": func(t *testing.T) {
			t.Setenv("PODIUM_AUDIT_HEADERS_FILE", filepath.Join(t.TempDir(), "absent-headers"))
		},
		"key without certificate": func(t *testing.T) {
			t.Setenv("PODIUM_AUDIT_HEADERS_FILE", "")
			t.Setenv("PODIUM_AUDIT_TLS_CERT_FILE", "")
		},
	}
	for name, configure := range refusals {
		t.Run(name, func(t *testing.T) {
			configure(t)
			stream, err := newAuditSink(&config{auditSink: rcv.URL(), auditSinkSet: true}, nil)
			if stream != nil || !errors.Is(err, audit.ErrConfig) {
				t.Errorf("newAuditSink = (%v, %v), want (nil, an audit.ErrConfig error)", stream, err)
			}
		})
	}

	t.Run("queue bound", func(t *testing.T) {
		for _, name := range []string{"PODIUM_AUDIT_HEADERS_FILE", "PODIUM_AUDIT_TLS_CA_FILE", "PODIUM_AUDIT_TLS_CERT_FILE", "PODIUM_AUDIT_TLS_KEY_FILE"} {
			t.Setenv(name, "")
		}
		t.Setenv("PODIUM_AUDIT_QUEUE_MAX_BYTES", "64")
		plain := auditharness.NewReceiver(t)
		obs := &fakeObserver{}
		stream, err := newAuditSink(&config{auditSink: plain.URL(), auditSinkSet: true}, obs)
		if err != nil {
			t.Fatalf("newAuditSink: %v", err)
		}
		t.Cleanup(stream.Shutdown)
		appendAndClose(t, stream, 5*time.Second)
		obs.mu.Lock()
		queued, dropped := obs.queued, obs.dropped
		obs.mu.Unlock()
		if len(queued) != 0 || len(dropped) != 1 || dropped[0] != "http queue_full 1" {
			t.Errorf("observer queued %v and dropped %v, want no queue count and one http queue_full drop", queued, dropped)
		}
		if n := len(plain.Lines()); n != 0 {
			t.Errorf("the receiver holds %d line(s), want 0", n)
		}
	})
}

// Spec: §6.2, §8.3 — the local stream's OTLP resource names the bridge by
// service.name podium-mcp.
func TestNewAuditSink_OTLPServiceName(t *testing.T) {
	rcv := auditharness.NewReceiver(t)
	stream, err := newAuditSink(&config{auditSink: "otlp+" + rcv.URL(), auditSinkSet: true}, nil)
	if err != nil {
		t.Fatalf("newAuditSink: %v", err)
	}
	t.Cleanup(stream.Shutdown)
	appendAndClose(t, stream, 5*time.Second)
	reqs := rcv.Requests()
	if len(reqs) == 0 {
		t.Fatal("the OTLP receiver got no request")
	}
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(reqs[0].Body, &req); err != nil {
		t.Fatalf("decode export request: %v", err)
	}
	var name string
	for _, kv := range req.GetResourceLogs()[0].GetResource().GetAttributes() {
		if kv.GetKey() == "service.name" {
			name = kv.GetValue().GetStringValue()
		}
	}
	if name != "podium-mcp" {
		t.Errorf("service.name = %q, want podium-mcp", name)
	}
}

// Spec: §6.2 — with PODIUM_AUDIT_SINK unset, newServer leaves the audit sink
// a nil interface and the stream nil, so the audit helpers return at their
// nil guards.
func TestNewServer_UnsetAuditSinkLeavesNilInterface(t *testing.T) {
	srv, err := newServer(&config{cacheDir: t.TempDir()})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	if srv.audit != nil {
		t.Errorf("srv.audit = %#v, want a nil interface", srv.audit)
	}
	if srv.auditStream != nil {
		t.Errorf("srv.auditStream = %v, want nil", srv.auditStream)
	}
	srv.auditMeta(audit.EventDomainLoaded, "finance", "")
	srv.auditLoadArtifact("finance/close", "", "")
	srv.auditSearch(audit.EventArtifactsSearched, "q", "")
}

// Spec: §6.2, §13.8 — with no metrics listener, newServer passes a nil
// observer rather than a typed-nil metric set, so the first count does not
// panic and the record reaches the receiver.
func TestNewServer_NoMetricsAddrPassesNilObserver(t *testing.T) {
	rcv := auditharness.NewReceiver(t)
	srv, err := newServer(&config{cacheDir: t.TempDir(), auditSink: rcv.URL(), auditSinkSet: true})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	t.Cleanup(srv.auditStream.Shutdown)
	if srv.metrics != nil {
		t.Fatalf("srv.metrics = %v, want nil with no metrics address", srv.metrics)
	}
	if err := srv.audit.Append(context.Background(), audit.Event{Type: audit.EventArtifactLoaded, Caller: "alice"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.auditStream.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !hasLoadedRecord(t, rcv) {
		t.Error("the receiver holds no record after Close")
	}
}

// spec: §8.3 / §14.13 — two MCP server processes share one user-wide
// ~/.podium/audit.log. Each writes meta-tool events through its own
// FileSink; a verifier of the shared log accepts the interleaved
// per-writer chains, where events are written and the
// shared chain verifies. The registry is unreachable so only the local
// audit path runs.
func TestMCPSharedAuditLog_ConcurrentWritersVerify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")

	sinkA, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("sink A: %v", err)
	}
	sinkB, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("sink B: %v", err)
	}
	procA := &mcpServer{cfg: &config{}, audit: sinkA, sessionID: "claude-code"}
	procB := &mcpServer{cfg: &config{}, audit: sinkB, sessionID: "cursor"}

	// Interleave meta-tool audit writes from the two processes.
	procA.auditMeta(audit.EventArtifactLoaded, "finance/close/forecast", "")
	procB.auditMeta(audit.EventDomainLoaded, "finance/close", "")
	procA.auditMeta(audit.EventArtifactLoaded, "finance/close/variance", "")
	procB.auditMeta(audit.EventArtifactLoaded, "finance/close/forecast", "")

	reader, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	if err := reader.Verify(context.Background()); err != nil {
		t.Errorf("Verify shared audit log: %v, want nil", err)
	}
}
