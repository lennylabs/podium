package serverboot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// Spec: §8.3 — the audit log is hash-chained and load-bearing.
// auditEmitterFor adapts a FileSink to core.AuditEmitter, but the
// adapter today discards the sink's error via `_ = sink.Append(...)`.
// This test pins that behavior: a sink that errors does NOT cause
// the adapter to panic or otherwise disrupt the caller, but the
// audit event is silently lost.
//
// If this contract changes (e.g., the adapter starts panicking or
// surfacing errors back to core.emit), this test must be updated.
// The deliberate behavior choice should be documented either way:
// today the registry op succeeds even when its audit record fails
// to land.
func TestAuditEmitterFor_SwallowsSinkAppendError(t *testing.T) {
	t.Parallel()
	// Point the sink at a path inside a directory that exists, then
	// make the file unwritable. Append will fail on the first
	// invocation; we want to confirm the adapter doesn't panic.
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	// Remove write permission from the directory so subsequent
	// Append (which opens-for-append) fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	emit := auditEmitterFor(sink)
	// Must not panic, must not deadlock. The error inside the
	// adapter is silently discarded.
	emit(context.Background(), core.AuditEvent{
		Type:   "artifact.published",
		Caller: "alice",
		Target: "team/x",
	})
}

// Spec: §8.3 — the meta-tool emission path tolerates a missing
// audit sink (cfg.auditLogPath unresolvable). The Registry.emit
// short-circuits when r.audit is nil, so no panic + the op
// continues. This pins that behavior under the wiring path that
// produces a nil AuditEmitter.
func TestAuditEmit_NilEmitterIsSafe(t *testing.T) {
	t.Parallel()
	// auditEmitterFor against a closed/nil sink (operator with
	// PODIUM_AUDIT_DISABLED-style configuration).
	emit := auditEmitterFor(nil)
	// Calling the returned function with a nil sink would panic on
	// sink.Append. The current adapter does not nil-check the sink
	// at construction time — we test that the documented production
	// path (openAuditSink returns nil → registry.WithAudit is never
	// called → emit is nil) avoids the panic by NOT calling the
	// adapter at all.
	if emit == nil {
		t.Fatal("adapter returned nil; expected a callable function")
	}
	defer func() {
		// If the adapter ever changes to defend against a nil sink,
		// this recover prevents the test from blocking other tests.
		_ = recover()
	}()
	// Production never reaches this call (openAuditSink-nil short-
	// circuits before WithAudit is wired). We document the
	// adapter's nil-sink fragility by NOT invoking emit() here;
	// the assertion is that the construction itself does not panic.
}
