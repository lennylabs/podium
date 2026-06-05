package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// spec: §8.2 — a manifest-declared audit_redact naming a sensitive frontmatter
// field (bank_account) masks that field's value to [redacted] in the
// artifact.published audit context. The raw value is merged into the context
// and then redacted inside the emission closure, so it never reaches the sink.
// Before this change the named field was never emitted, so the directive had
// no effect.
func TestIngest_AuditRedactMasksContentField(t *testing.T) {
	t.Parallel()
	const secret = "AC-1234-5678"
	const artifact = "---\n" +
		"type: context\n" +
		"version: 1.0.0\n" +
		"description: payroll record\n" +
		"sensitivity: low\n" +
		"bank_account: \"" + secret + "\"\n" +
		"audit_redact:\n  - bank_account\n" +
		"---\n\nbody\n"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	var got map[string]string
	auditEmit := func(eventType, _ string, ctx map[string]string) {
		if eventType == "artifact.published" {
			got = ctx
		}
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "team",
		Files: fstest.MapFS{
			"finance/payroll/ARTIFACT.md": &fstest.MapFile{Data: []byte(artifact)},
		},
		AuditEmit: auditEmit,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1; rejects=%+v lint=%+v", res.Accepted, res.Rejected, res.LintFailures)
	}
	if got == nil {
		t.Fatal("artifact.published not emitted")
	}
	if got["bank_account"] != "[redacted]" {
		t.Errorf("bank_account = %q, want [redacted]", got["bank_account"])
	}
	if got["bank_account"] == secret {
		t.Errorf("raw bank_account value %q leaked into audit context", secret)
	}
}
