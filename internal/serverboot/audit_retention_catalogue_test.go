package serverboot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// Spec: §8.1/§8.3/§8.4 (F-8.4.7) — registry-owned catalogue events are
// written through the registry AuditEmitter into the registry's FileSink
// (the metadata store persists no audit stream), and the §8.4 retention
// scheduler enforces against that same FileSink. So the 1-year audit-event
// default applies to the registry-owned stream, not just the local MCP log.
func TestRetention_CoversRegistryCatalogueEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	// The registry emits a catalogue event through the same emitter
	// serverboot wires to this sink. This is the registry-owned stream.
	emit := auditEmitterFor(sink, audit.NewPIIScrubber(), nil)
	emit(context.Background(), core.AuditEvent{
		Type:   string(audit.EventArtifactPublished),
		Caller: "alice@acme.com",
		Target: "skill/x@1.0.0",
	})

	// Simulate an older registry-owned catalogue event past the 1-year
	// window by appending it with a backdated timestamp to the same sink.
	old := time.Now().UTC().Add(-400 * 24 * time.Hour)
	if err := sink.Append(context.Background(), audit.Event{
		Type:      audit.EventArtifactPublished,
		Timestamp: old,
		Caller:    "alice@acme.com",
		Target:    "skill/y@0.1.0",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Run the same retention pass the scheduler runs against this sink.
	policies := defaultRetentionPolicies(365 * 24 * time.Hour)
	runRetentionOnce(context.Background(), sink, policies, nil)

	body, err := readBytes(path)
	if err != nil {
		t.Fatalf("readBytes: %v", err)
	}
	got := string(body)
	// The over-age catalogue event is gone; the fresh one survives.
	if strings.Contains(got, "skill/y@0.1.0") {
		t.Errorf("over-age registry catalogue event not aged out:\n%s", got)
	}
	if !strings.Contains(got, "skill/x@1.0.0") {
		t.Errorf("fresh registry catalogue event was dropped:\n%s", got)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("chain invalid after catalogue-event retention: %v", err)
	}
}
