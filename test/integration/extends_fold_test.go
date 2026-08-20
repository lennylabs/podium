package integration

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// foldParentBytes is a type:agent parent carrying the fields the §4.6 fold
// reads plus three the fold must leave alone (name, when_to_use, and
// replaced_by have no column in either SQL store).
const foldParentBytes = "---\n" +
	"type: agent\n" +
	"version: 1.0.0\n" +
	"name: parent-agent\n" +
	"description: parent desc\n" +
	"replaced_by: finance/successor\n" +
	"when_to_use:\n  - parent case\n" +
	"tags: [a, shared]\n" +
	"sensitivity: high\n" +
	"search_visibility: direct-only\n" +
	"---\n\nparent body\n"

// foldChildBytes extends foldParentBytes and omits description, name, and
// replaced_by.
const foldChildBytes = "---\n" +
	"type: agent\n" +
	"version: 2.0.0\n" +
	"extends: shared/parent@1.x\n" +
	"when_to_use:\n  - child case\n" +
	"tags: [b]\n" +
	"sensitivity: low\n" +
	"---\n\nchild body\n"

// ingestFoldLayer ingests one ARTIFACT.md under layerID and fails on any
// rejection.
func ingestFoldLayer(t *testing.T, st store.Store, layerID, id, src string) {
	t.Helper()
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: layerID, Files: fstest.MapFS{
			id + "/ARTIFACT.md": &fstest.MapFile{Data: []byte(src)},
		},
	})
	if err != nil {
		t.Fatalf("ingest %s: %v", id, err)
	}
	if res.Accepted != 1 {
		t.Fatalf("ingest %s: accepted=%d rejected=%+v", id, res.Accepted, res.Rejected)
	}
}

// Spec: §4.6 "Omitted fields" — the columns ingest folds are
// the ones every store backend persists, so the resolved description, tags,
// sensitivity, and search visibility survive a round trip through the
// memory store, file-backed SQLite, and Postgres alike.
// Spec: §2.2 — the shared Go library is the single behavioral surface, so
// the deployment modes agree on what is indexed for an extends child.
func TestIngest_ExtendsChildFoldSurvivesStoreRoundTrip(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			st := f.open(t)
			ctx := context.Background()
			if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
				t.Fatalf("CreateTenant: %v", err)
			}

			ingestFoldLayer(t, st, "L1", "shared/parent", foldParentBytes)
			ingestFoldLayer(t, st, "L2", "finance/child", foldChildBytes)

			rec, err := st.GetManifest(ctx, "t", "finance/child", "2.0.0")
			if err != nil {
				t.Fatalf("GetManifest: %v", err)
			}
			if rec.Description != "parent desc" {
				t.Errorf("Description = %q, want %q", rec.Description, "parent desc")
			}
			if got, want := strings.Join(rec.Tags, ","), "a,shared,b"; got != want {
				t.Errorf("Tags = %q, want %q", got, want)
			}
			if rec.Sensitivity != "high" {
				t.Errorf("Sensitivity = %q, want high", rec.Sensitivity)
			}
			if rec.SearchVisibility != "direct-only" {
				t.Errorf("SearchVisibility = %q, want direct-only", rec.SearchVisibility)
			}
			if f.name != "memory" {
				// Neither SQL store has a column for when_to_use, name, or
				// replaced_by, so only the memory arm can observe that the
				// fold left them alone.
				return
			}
			if got := strings.Join(rec.WhenToUse, ","); got != "child case" {
				t.Errorf("WhenToUse = %q, want the child's own entry only", got)
			}
			if rec.Name != "" || rec.ReplacedBy != "" {
				t.Errorf("Name = %q, ReplacedBy = %q, want both unfolded", rec.Name, rec.ReplacedBy)
			}
		})
	}
}
