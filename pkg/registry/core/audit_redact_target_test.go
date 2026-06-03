package core_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// spec: §8.2 — a manifest that names a sensitive frontmatter field (the spec's
// bank_account / ssn example) in audit_redact now surfaces that field into the
// artifact.loaded read-event context, so the redaction directive has a concrete
// target the audit emitter masks. Before this change the named field was never
// emitted, so the directive matched nothing. The recorder captures the
// pre-redaction event; the emitter-side masking is covered in pkg/audit and
// internal/serverboot. F-8.2.1.
func TestAudit_LoadArtifactSurfacesRedactTarget(t *testing.T) {
	t.Parallel()
	const body = "---\n" +
		"type: context\n" +
		"version: 1.0.0\n" +
		"description: payroll record\n" +
		"sensitivity: low\n" +
		"bank_account: \"AC-1234-5678\"\n" +
		"audit_redact:\n  - bank_account\n" +
		"---\n\nbody\n"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: fstest.MapFS{
			"finance/payroll/ARTIFACT.md": &fstest.MapFile{Data: []byte(body)},
		},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	rec := &recorder{}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	}).WithAudit(rec.emit)
	if _, err := reg.LoadArtifact(context.Background(), publicID, "finance/payroll", core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	ev := rec.snapshot()[0]
	if got, ok := ev.Context["bank_account"]; !ok || got != "AC-1234-5678" {
		t.Errorf("context[bank_account] = %q (present=%v), want the manifest value as a redaction target", got, ok)
	}
	found := false
	for _, k := range ev.RedactKeys {
		if k == "bank_account" {
			found = true
		}
	}
	if !found {
		t.Errorf("RedactKeys = %v, want it to contain bank_account", ev.RedactKeys)
	}
}
