package serverboot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §13.2.1 — when the read-only probe flips the mode, it
// must append a registry.read_only_entered audit event so SIEM
// pipelines see the transition. The exit callback writes
// registry.read_only_exited.
func TestReadOnlyCallbacks_AppendAuditEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	enter := readOnlyEnterCallback(sink, "tenant-x", "store_probe_failed")
	exit := readOnlyExitCallback(sink, "tenant-x")
	enter()
	exit()

	body, err := readFile(t, path)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if !strings.Contains(body, "registry.read_only_entered") {
		t.Errorf("audit log missing read_only_entered: %s", body)
	}
	if !strings.Contains(body, "registry.read_only_exited") {
		t.Errorf("audit log missing read_only_exited: %s", body)
	}
	if !strings.Contains(body, "store_probe_failed") {
		t.Errorf("audit log missing reason context: %s", body)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// Spec: §13.2.1 — nil sink is a no-op so the probe still runs in
// deployments that haven't enabled audit logging.
func TestReadOnlyCallbacks_NilSinkIsNoOp(t *testing.T) {
	t.Parallel()
	enter := readOnlyEnterCallback(nil, "tenant", "reason")
	exit := readOnlyExitCallback(nil, "tenant")
	enter()
	exit()
	// Reaching here without panic is the assertion.
}

func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	data, err := readBytes(path)
	return string(data), err
}
