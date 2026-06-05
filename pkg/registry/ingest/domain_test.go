package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// spec: §4.7 "Domain embeddings" — ingest composes each DOMAIN.md's
// projection and upserts its embedding into the domain index so
// search_domains has a semantic ranker. A DOMAIN.md with no projectable
// text (include-only) is not embedded.
func TestIngest_EmbedsDomainProjection(t *testing.T) {
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	fsys := fstest.MapFS{
		"finance/ap/DOMAIN.md":         &fstest.MapFile{Data: []byte("---\ndescription: AP ops\ndiscovery:\n  keywords:\n    - reconciliation\n---\n# AP\nbody\n")},
		"finance/ap/imports/DOMAIN.md": &fstest.MapFile{Data: []byte("---\ninclude:\n  - x/*\n---\n")},
		"finance/ap/pay/ARTIFACT.md":   &fstest.MapFile{Data: []byte(contextArtifact("pay"))},
	}
	embedded := map[string]bool{}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: fsys,
		Embedder: func(_ context.Context, text string) ([]float32, error) {
			return []float32{1, 0}, nil
		},
		DomainVectorPut: func(_ context.Context, tenantID, path string, _ []float32) error {
			embedded[path] = true
			return nil
		},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !embedded["finance/ap"] {
		t.Errorf("finance/ap DOMAIN.md was not embedded: %v", embedded)
	}
	if embedded["finance/ap/imports"] {
		t.Errorf("include-only DOMAIN.md should not be embedded (empty projection): %v", embedded)
	}
}

// spec: §4.5.1 — ingest persists each DOMAIN.md as a
// store.DomainRecord keyed by the canonical domain path, so load_domain
// can read domain composition. A root-level DOMAIN.md is skipped (the
// registry root has no DOMAIN.md per §4.5.5).
func TestIngest_PersistsDomainMD(t *testing.T) {
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	fsys := fstest.MapFS{
		"DOMAIN.md":                          &fstest.MapFile{Data: []byte("---\ndescription: root\n---\n")},
		"finance/ap/DOMAIN.md":               &fstest.MapFile{Data: []byte("---\ndescription: AP\n---\n\n# AP\n")},
		"finance/ap/pay-invoice/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("pay"))},
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: fsys,
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	domains, err := st.ListDomains(context.Background(), "t")
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 1 {
		t.Fatalf("ListDomains = %d records, want 1 (root DOMAIN.md skipped): %+v", len(domains), domains)
	}
	d := domains[0]
	if d.Path != "finance/ap" || d.Layer != "L" {
		t.Errorf("record = {path:%q layer:%q}, want {finance/ap L}", d.Path, d.Layer)
	}
	if len(d.Raw) == 0 {
		t.Error("raw DOMAIN.md bytes were not persisted")
	}
}

// spec: §4.5.1 — re-ingesting a layer replaces its DOMAIN.md
// record rather than accumulating duplicates, even though the manifest
// content is immutable.
func TestIngest_DomainMDReingestReplaces(t *testing.T) {
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
	for _, desc := range []string{"first", "second"} {
		if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: "L", Files: mk(desc),
		}); err != nil {
			t.Fatalf("Ingest %s: %v", desc, err)
		}
	}
	domains, _ := st.ListDomains(context.Background(), "t")
	if len(domains) != 1 {
		t.Fatalf("want 1 domain record after reingest, got %d", len(domains))
	}
	if got := string(domains[0].Raw); got != "---\ndescription: second\n---\n" {
		t.Errorf("raw = %q, want the second ingest's bytes", got)
	}
}
