package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// domainAuditCollector records the (eventType, target, ctx) tuples ingest
// passes to the §8 audit seam so a test can assert which domain.published
// events fired.
type domainAuditEvent struct {
	typ    string
	target string
	ctx    map[string]string
}

func collectDomainAudit(emits *[]domainAuditEvent) ingest.AuditEmitterFunc {
	return func(eventType, target string, ctxFields map[string]string) {
		*emits = append(*emits, domainAuditEvent{eventType, target, ctxFields})
	}
}

func countDomainPublished(emits []domainAuditEvent) int {
	n := 0
	for _, e := range emits {
		if e.typ == "domain.published" {
			n++
		}
	}
	return n
}

// spec: §8.1 — "domain.published | When a DOMAIN.md was added or changed."
// A first ingest of a layer with a DOMAIN.md emits domain.published with the
// canonical domain path as target and the layer recorded in context. F-8.1.5.
func TestIngest_EmitsDomainPublished(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	var emits []domainAuditEvent
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:  "t",
		LayerID:   "L",
		AuditEmit: collectDomainAudit(&emits),
		Files: fstest.MapFS{
			"finance/ap/DOMAIN.md":       &fstest.MapFile{Data: []byte("---\ndescription: AP\n---\n\n# AP\n")},
			"finance/ap/pay/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("pay"))},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	var got *domainAuditEvent
	for i := range emits {
		if emits[i].typ == "domain.published" {
			got = &emits[i]
		}
	}
	if got == nil {
		t.Fatalf("domain.published not emitted; got %+v", emits)
	}
	if got.target != "finance/ap" {
		t.Errorf("target = %q, want finance/ap", got.target)
	}
	if got.ctx["layer"] != "L" {
		t.Errorf("ctx.layer = %q, want L", got.ctx["layer"])
	}
}

// spec: §8.1 — domain.published fires only on a genuine change. Re-ingesting
// a layer whose DOMAIN.md bytes are identical stays quiet, so SIEM consumers
// see one event per real change rather than one per ingest cycle. F-8.1.5.
func TestIngest_DomainPublishedSilentOnUnchangedReingest(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	files := fstest.MapFS{
		"finance/DOMAIN.md":     &fstest.MapFile{Data: []byte("---\ndescription: finance\n---\n")},
		"finance/x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("x"))},
	}
	var emits []domainAuditEvent
	for i := 0; i < 3; i++ {
		if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: "L", Files: files, AuditEmit: collectDomainAudit(&emits),
		}); err != nil {
			t.Fatalf("Ingest #%d: %v", i, err)
		}
	}
	if n := countDomainPublished(emits); n != 1 {
		t.Errorf("domain.published emitted %d times across 3 identical ingests, want 1", n)
	}
}

// spec: §8.1 — a DOMAIN.md whose source changed since the previous ingest
// emits domain.published again, distinguishing a content change from an
// idempotent re-ingest. F-8.1.5.
func TestIngest_DomainPublishedOnContentChange(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	mk := func(desc string) fstest.MapFS {
		return fstest.MapFS{
			"finance/DOMAIN.md":     &fstest.MapFile{Data: []byte("---\ndescription: " + desc + "\n---\n")},
			"finance/x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("x"))},
		}
	}
	var emits []domainAuditEvent
	for _, desc := range []string{"first", "second"} {
		if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: "L", Files: mk(desc), AuditEmit: collectDomainAudit(&emits),
		}); err != nil {
			t.Fatalf("Ingest %s: %v", desc, err)
		}
	}
	if n := countDomainPublished(emits); n != 2 {
		t.Errorf("domain.published emitted %d times across a change, want 2", n)
	}
}

// spec: §4.5.5 / §8.1 — the registry root has no DOMAIN.md, so a root-level
// DOMAIN.md is skipped by ingest and never emits domain.published. F-8.1.5.
func TestIngest_RootDomainMDDoesNotPublish(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	var emits []domainAuditEvent
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", AuditEmit: collectDomainAudit(&emits),
		Files: fstest.MapFS{
			"DOMAIN.md":           &fstest.MapFile{Data: []byte("---\ndescription: root\n---\n")},
			"finance/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("x"))},
		},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if n := countDomainPublished(emits); n != 0 {
		t.Errorf("root DOMAIN.md emitted domain.published %d times, want 0", n)
	}
}
