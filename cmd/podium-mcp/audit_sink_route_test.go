package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
)

// spec: §6.2 / §9 — newAuditSink routes PODIUM_AUDIT_SINK to a file sink
// for a path and to the external-endpoint sink for an http(s) URL; an
// unset var leaves auditing to the registry. F-8.3.1, F-8.3.2.
func TestNewAuditSink_Routing(t *testing.T) {
	t.Run("unset is registry-only", func(t *testing.T) {
		sink, err := newAuditSink(&config{})
		if err != nil {
			t.Fatalf("newAuditSink: %v", err)
		}
		if sink != nil {
			t.Errorf("unset audit sink = %T, want nil", sink)
		}
	})
	t.Run("path selects file sink", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.log")
		sink, err := newAuditSink(&config{auditSink: path, auditSinkSet: true})
		if err != nil {
			t.Fatalf("newAuditSink: %v", err)
		}
		fs, ok := sink.(*audit.FileSink)
		if !ok {
			t.Fatalf("got %T, want *audit.FileSink", sink)
		}
		if fs.Path() != path {
			t.Errorf("path = %q, want %q", fs.Path(), path)
		}
	})
	t.Run("http url selects endpoint sink", func(t *testing.T) {
		const url = "https://siem.acme.com/ingest"
		sink, err := newAuditSink(&config{auditSink: url, auditSinkSet: true})
		if err != nil {
			t.Fatalf("newAuditSink: %v", err)
		}
		es, ok := sink.(*audit.EndpointSink)
		if !ok {
			t.Fatalf("got %T, want *audit.EndpointSink", sink)
		}
		if es.URL() != url {
			t.Errorf("url = %q, want %q", es.URL(), url)
		}
	})
}

// spec: §8.3 / §14.13 — two MCP server processes share one user-wide
// ~/.podium/audit.log. Each writes meta-tool events through its own
// FileSink; a verifier of the shared log accepts the interleaved
// per-writer chains. F-14.13.1 (events are written) + F-14.13.2 (the
// shared chain verifies). The registry is unreachable so only the local
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
	procA.auditMeta(audit.EventArtifactLoaded, "finance/close/forecast")
	procB.auditMeta(audit.EventDomainLoaded, "finance/close")
	procA.auditMeta(audit.EventArtifactLoaded, "finance/close/variance")
	procB.auditMeta(audit.EventArtifactLoaded, "finance/close/forecast")

	reader, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	if err := reader.Verify(context.Background()); err != nil {
		t.Errorf("Verify shared audit log: %v, want nil", err)
	}
}
