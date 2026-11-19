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

func (failingSoftDelete) SoftDeleteArtifacts(context.Context, string, string, []string, time.Time, string) error {
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

// removedSkill is a plain Agent Skill with no declared version, so ingest
// assigns its version.
func removedSkill(desc string) string {
	return "---\nname: greet\ndescription: " + desc + "\n---\n\nGreet the user.\n"
}

// Spec: §7.3.1 (Removed artifacts), §4.7.6 — a skill with no declared version
// whose package returns unchanged after its removal is restored at the version
// the registry assigned it, rather than rejected as duplicate content.
func TestIngest_RemovedAutoVersionedSkillRestores(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	reg := erRegistry(st, "team")
	skill := map[string]string{"ops/greet/SKILL.md": removedSkill("Greet the user at the start of every session")}
	erIngest(t, st, "team", skill)
	erAssertServes(t, reg, "ops/greet", "", "0.0.0-auto.0")
	if res := erIngest(t, st, "team", map[string]string{}); len(res.Removed) != 1 {
		t.Fatalf("Removed = %v, want [ops/greet]", res.Removed)
	}
	erAssertUnserved(t, reg, "ops/greet")
	res := erIngest(t, st, "team", skill)
	if len(res.Rejected) != 0 || len(res.Conflicts) != 0 {
		t.Fatalf("restoring cycle: rejected=%+v conflicts=%+v, want none", res.Rejected, res.Conflicts)
	}
	erAssertServes(t, reg, "ops/greet", "", "0.0.0-auto.0")
}

// Spec: §7.3.1 (Removed artifacts) — a cycle of another layer that ingests a
// removed version, the same bytes included, is rejected as an immutability
// violation, and the version stays unserved; it was reported accepted.
func TestIngest_RemovedVersionInAnotherLayerIsRefused(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	reg := erRegistry(st, "team", "other")
	moved := map[string]string{"ops/gone/ARTIFACT.md": removedAgent("Runbook for restarting the ingest worker", "1.0.0")}
	erIngest(t, st, "team", moved)
	if res := erIngest(t, st, "team", map[string]string{}); len(res.Removed) != 1 {
		t.Fatalf("Removed = %v, want [ops/gone]", res.Removed)
	}
	res := erIngest(t, st, "other", moved)
	if res.Accepted != 0 || len(res.Conflicts) != 1 || res.Conflicts[0].ArtifactID != "ops/gone" {
		t.Fatalf("other layer's cycle: accepted=%d conflicts=%+v, want one conflict on ops/gone", res.Accepted, res.Conflicts)
	}
	// The conflict names the removed row as the holder, with its hash, which
	// equals the new one because the bytes are the same.
	if c := res.Conflicts[0]; !c.Removed || c.OldHash == "" || c.OldHash != c.NewHash {
		t.Errorf("conflict = %+v, want Removed with OldHash equal to NewHash", c)
	}
	erAssertUnserved(t, reg, "ops/gone")
}

// Spec: §7.3.1 (Removed artifacts), §4.1, §4.6 — a retired row, of a removed
// type or declaring extends:, is served by no read path, so a cycle whose
// snapshot no longer holds its package does not report it as removed and
// leaves the row stored and untombstoned.
func TestIngest_RetiredRowIsNotRemoved(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	command := erRow("ops/old", "1.0.0", "team", erDoc("command", "1.0.0", "", "Old body."), 1)
	command.Type = "command"
	erSeed(t, st, command)
	erSeed(t, st, erRow("ops/child", "1.0.0", "team", erDoc("agent", "1.0.0", "extends: ops/base\n", "Child body."), 2))
	res := erIngest(t, st, "team", map[string]string{
		"ops/kept/ARTIFACT.md": removedAgent("Checklist for the weekly on-call handover", "1.0.0"),
	})
	all, err := st.ListManifestsIncludingDeleted(context.Background(), erTenant)
	if err != nil {
		t.Fatal(err)
	}
	var tombstoned []string
	for _, m := range all {
		if m.DeletedAt != nil {
			tombstoned = append(tombstoned, m.ArtifactID)
		}
	}
	if len(res.Removed) != 0 || len(tombstoned) != 0 || len(all) != 3 {
		t.Errorf("removed=%v tombstoned=%v stored=%d, want no removal and 3 stored rows", res.Removed, tombstoned, len(all))
	}
}
