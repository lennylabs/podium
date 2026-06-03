package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// spec: §13.10 (F-13.10.2) — the sandbox-profile ingest gate threads from the
// orchestrator options through to the ingest pipeline. With enforcement on, an
// artifact whose sandbox_profile the local host cannot honor is rejected with
// ingest.sandbox_profile_unenforceable; with enforcement off the same artifact
// is accepted because sandbox_profile is informational in standalone.
func TestSandboxProfileIngestGate(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "eng", "widget"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\ntype: context\nversion: 1.0.0\ndescription: A sandboxed widget.\nsensitivity: low\nsandbox_profile: seccomp-strict\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "eng", "widget", "ARTIFACT.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	run := func(enforce bool) *ingest.Result {
		st := store.NewMemory()
		if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		lc := store.LayerConfig{ID: "L", TenantID: "t", SourceType: "local", LocalPath: dir}
		res, err := ingest.SourceIngestWithOptions(context.Background(), st, source.Local{}, lc, ingest.SourceIngestOptions{
			EnforceSandboxProfile:      enforce,
			EnforceableSandboxProfiles: []string{"unrestricted"},
		})
		if err != nil {
			t.Fatalf("SourceIngestWithOptions(enforce=%v): %v", enforce, err)
		}
		return res
	}

	rejected := run(true)
	if rejected.Accepted != 0 || len(rejected.Rejected) != 1 {
		t.Fatalf("enforce on: Accepted=%d Rejected=%+v, want 0 accepted / 1 rejected", rejected.Accepted, rejected.Rejected)
	}
	if got := rejected.Rejected[0].Code; got != "ingest.sandbox_profile_unenforceable" {
		t.Errorf("reject code = %q, want ingest.sandbox_profile_unenforceable", got)
	}

	accepted := run(false)
	if accepted.Accepted != 1 {
		t.Fatalf("enforce off: Accepted=%d Rejected=%+v, want 1 accepted (informational)", accepted.Accepted, accepted.Rejected)
	}
}
