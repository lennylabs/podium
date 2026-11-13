package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
// variable never reads the queue bound.
//
// Matrix: §6.10 (config.audit_sink_unavailable)
func TestNewAuditSink_RefusesUnusableList(t *testing.T) {
	t.Setenv("PODIUM_AUDIT_QUEUE_MAX_BYTES", "-1")
	stream, err := newAuditSink(&config{auditSinkSet: false}, nil)
	if stream != nil || err != nil {
		t.Errorf("unset newAuditSink = (%v, %v), want (nil, nil) without reading the queue bound", stream, err)
	}
	t.Setenv("PODIUM_AUDIT_QUEUE_MAX_BYTES", "")
	stream, err = newAuditSink(&config{auditSink: "stdout", auditSinkSet: true}, nil)
	if stream != nil || err == nil || !strings.HasPrefix(err.Error(), "config.audit_sink_unavailable: ") ||
		!strings.Contains(err.Error(), "PODIUM_AUDIT_SINK") || !errors.Is(err, audit.ErrConfig) {
		t.Errorf("newAuditSink(stdout) = (%v, %v), want a config.audit_sink_unavailable refusal naming PODIUM_AUDIT_SINK", stream, err)
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
