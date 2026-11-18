package ingest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

func removedAgent(desc, version string) string {
	return "---\ntype: agent\nversion: " + version + "\ndescription: " + desc + "\n---\n\nBody of " + desc + ".\n"
}

// Spec: §7.3.1 (Removed artifacts) — a completed cycle whose snapshot no
// longer contains an artifact's package soft-deletes every version the layer
// contributed and reports it as removed, so neither a load nor a search
// serves it; an artifact whose package the snapshot contains is not removed,
// including one the cycle drops.
func TestIngest_RemovedArtifactIsSoftDeleted(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	reg := erRegistry(st, "team")
	erIngest(t, st, "team", map[string]string{
		"ops/gone/ARTIFACT.md": removedAgent("Runbook for restarting the ingest worker", "1.0.0"),
		"ops/kept/ARTIFACT.md": removedAgent("Checklist for the weekly on-call handover", "1.0.0"),
		"ops/bad/ARTIFACT.md":  removedAgent("Template for incident review notes", "1.0.0"),
	})
	erIngest(t, st, "team", map[string]string{
		"ops/gone/ARTIFACT.md": removedAgent("Runbook for restarting the ingest worker", "2.0.0"),
		"ops/kept/ARTIFACT.md": removedAgent("Checklist for the weekly on-call handover", "1.0.0"),
		"ops/bad/ARTIFACT.md":  removedAgent("Template for incident review notes", "1.0.0"),
	})

	if !searchFinds(t, reg, "ops/gone") {
		t.Fatal("search does not find ops/gone before its removal")
	}

	// ops/gone leaves the source; ops/bad stays but no longer parses.
	res := erIngest(t, st, "team", map[string]string{
		"ops/kept/ARTIFACT.md": removedAgent("Checklist for the weekly on-call handover", "1.0.0"),
		"ops/bad/ARTIFACT.md":  "not a manifest",
	})
	if len(res.Removed) != 1 || res.Removed[0] != "ops/gone" {
		t.Fatalf("Removed = %v, want [ops/gone]", res.Removed)
	}
	erAssertUnserved(t, reg, "ops/gone")
	if _, err := erLoad(reg, "ops/gone", "1.0.0"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("pinned load of a removed version: err = %v, want registry.not_found", err)
	}
	erAssertServes(t, reg, "ops/kept", "", "1.0.0")
	erAssertServes(t, reg, "ops/bad", "", "1.0.0")

	if searchFinds(t, reg, "ops/gone") {
		t.Error("search returned the removed artifact")
	}
}

// searchFinds reports whether a search for the ingest-worker runbook returns id.
func searchFinds(t *testing.T, reg *core.Registry, id string) bool {
	t.Helper()
	found, err := reg.SearchArtifacts(context.Background(), layer.Identity{IsPublic: true}, core.SearchArtifactsOptions{Query: "ingest worker runbook"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range found.Results {
		if d.ID == id {
			return true
		}
	}
	return false
}

// Spec: §7.3.1 (Removed artifacts) — restoring the package with the same
// content restores the version as served, the same version with different
// content is an immutability violation, and a cycle that cannot read part of
// its snapshot removes nothing.
func TestIngest_RemovedArtifactRestoresAndConflicts(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	reg := erRegistry(st, "team")
	gone := map[string]string{"ops/gone/ARTIFACT.md": removedAgent("Runbook for restarting the ingest worker", "1.0.0")}
	kept := map[string]string{"ops/kept/ARTIFACT.md": removedAgent("Checklist for the weekly on-call handover", "1.0.0")}
	both := map[string]string{}
	for k, v := range gone {
		both[k] = v
	}
	for k, v := range kept {
		both[k] = v
	}
	erIngest(t, st, "team", both)
	if res := erIngest(t, st, "team", kept); len(res.Removed) != 1 {
		t.Fatalf("Removed = %v, want [ops/gone]", res.Removed)
	}

	changed := map[string]string{"ops/gone/ARTIFACT.md": removedAgent("A different runbook body entirely", "1.0.0")}
	if res := erIngest(t, st, "team", changed); len(res.Conflicts) != 1 {
		t.Errorf("same version, new content: result = %+v, want one immutability conflict", res)
	}
	erAssertUnserved(t, reg, "ops/gone")

	if res := erIngest(t, st, "team", both); len(res.Removed) != 0 {
		t.Errorf("restoring cycle removed %v", res.Removed)
	}
	erAssertServes(t, reg, "ops/gone", "", "1.0.0")

	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: erTenant, LayerID: "team", Files: denyFS{MapFS: erFS(both), deny: "ops"},
	}); err == nil {
		t.Fatal("ingest of an unreadable snapshot succeeded")
	}
	erAssertServes(t, reg, "ops/gone", "", "1.0.0")
	erAssertServes(t, reg, "ops/kept", "", "1.0.0")
}

// failingSoftDelete is a store whose tombstone write fails.
type failingSoftDelete struct{ store.Store }

func (failingSoftDelete) SoftDeleteArtifacts(context.Context, string, string, []string, time.Time) error {
	return errors.New("store down")
}

// Spec: §7.3.1 (Removed artifacts) — a cycle whose tombstone write fails
// returns the failure instead of reporting the artifact as removed, and the
// artifact keeps serving.
func TestIngest_RemovedArtifactStoreFailure(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	reg := erRegistry(st, "team")
	erIngest(t, st, "team", map[string]string{"ops/gone/ARTIFACT.md": removedAgent("Runbook for restarting the ingest worker", "1.0.0")})
	_, err := ingest.Ingest(context.Background(), failingSoftDelete{st}, ingest.Request{
		TenantID: erTenant, LayerID: "team", Files: erFS(map[string]string{}),
	})
	if err == nil || !strings.Contains(err.Error(), "store down") {
		t.Fatalf("ingest err = %v, want the store failure", err)
	}
	erAssertServes(t, reg, "ops/gone", "", "1.0.0")
}
