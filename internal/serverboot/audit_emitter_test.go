package serverboot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// Spec: §8.1 — auditEmitterFor adapts the file-backed sink to the
// core.AuditEmitter shape; the resulting emitter writes the
// event type, caller, target, and context to the sink.
func TestAuditEmitterFor_AppendsToSink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	emit := auditEmitterFor(sink)
	emit(context.Background(), core.AuditEvent{
		Type:    "domain.loaded",
		Caller:  "alice",
		Target:  "team/finance",
		Context: map[string]string{"depth": "1"},
	})

	body, err := readBytes(path)
	if err != nil {
		t.Fatalf("readBytes: %v", err)
	}
	got := string(body)
	for _, want := range []string{"domain.loaded", "alice", "team/finance", "\"depth\":\"1\""} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log missing %q in:\n%s", want, got)
		}
	}
}
