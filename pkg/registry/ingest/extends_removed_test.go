package ingest_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// erTenant is the tenant every case in this file ingests into.
const erTenant = "tenant-1"

// erPublic is the caller every core read in this file uses. Every layer the
// cases build is public, so visibility never hides a row.
var erPublic = layer.Identity{IsPublic: true}

// erEpoch orders the rows the cases seed through the store.
var erEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// erDoc returns an ARTIFACT.md of type typ at version ver, with the extra
// frontmatter lines appended and body after the closing delimiter.
func erDoc(typ, ver, extra, body string) string {
	return "---\ntype: " + typ + "\nversion: " + ver + "\ndescription: Fixture " + typ + " artifact.\n" +
		"sensitivity: low\n" + extra + "---\n\n" + body + "\n"
}

// erSkill returns a SKILL.md named name with the extra frontmatter lines.
func erSkill(name, extra string) string {
	return "---\nname: " + name + "\ndescription: Fixture skill " + name + ".\n" + extra + "---\n\nSkill body of " + name + ".\n"
}

// erFS builds a layer snapshot from paths and their contents.
func erFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for p, src := range files {
		out[p] = &fstest.MapFile{Data: []byte(src)}
	}
	return out
}

// erIngest ingests files into layerID and fails the test on an aborted call.
func erIngest(t *testing.T, st store.Store, layerID string, files map[string]string) *ingest.Result {
	t.Helper()
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: erTenant, LayerID: layerID, Files: erFS(files),
	})
	if err != nil {
		t.Fatalf("ingest %s: %v", layerID, err)
	}
	return res
}

// erRow builds a context row whose stored ARTIFACT.md is doc, ingested hour
// hours after erEpoch.
func erRow(id, ver, layerID, doc string, hour int) store.ManifestRecord {
	return store.ManifestRecord{
		TenantID: erTenant, ArtifactID: id, Version: ver, Layer: layerID, Type: "context",
		Description: "Fixture context artifact.", Sensitivity: "low",
		Frontmatter: []byte(doc), Body: []byte("Body of " + id + "@" + ver + ".\n"),
		IngestedAt: erEpoch.Add(time.Duration(hour) * time.Hour),
	}
}

// erSeed seals rec with the content hash ingest computes for the same bytes
// and stores it, bypassing ingest, as a row an earlier release stored.
func erSeed(t *testing.T, st store.Store, rec store.ManifestRecord) store.ManifestRecord {
	t.Helper()
	sealed := storetest.Seal(t, rec, nil, nil)
	if err := st.PutManifest(context.Background(), sealed); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", rec.ArtifactID, rec.Version, err)
	}
	return sealed
}

// erRegistry builds a core registry over st whose public layers are layerIDs.
func erRegistry(st store.Store, layerIDs ...string) *core.Registry {
	layers := make([]layer.Layer, 0, len(layerIDs))
	for i, id := range layerIDs {
		layers = append(layers, layer.Layer{ID: id, Precedence: i + 1, Visibility: layer.Visibility{Public: true}})
	}
	return core.New(st, erTenant, layers)
}

// erRejection returns the rejection of id in res, or fails the test.
func erRejection(t *testing.T, res *ingest.Result, id string) ingest.RejectedArtifact {
	t.Helper()
	for _, rj := range res.Rejected {
		if rj.ArtifactID == id {
			return rj
		}
	}
	t.Fatalf("%s not rejected: %+v", id, res.Rejected)
	return ingest.RejectedArtifact{}
}

// erAssertExtendsRemoved fails unless res rejects id under the §4.6
// no-inheritance rule, with exactly the extends_removed constraint in details.
func erAssertExtendsRemoved(t *testing.T, res *ingest.Result, id string) {
	t.Helper()
	rj := erRejection(t, res, id)
	want := map[string]any{"constraint": layer.ExtendsRemovedConstraint}
	if rj.Code != layer.InvalidArtifactCode || rj.Reason != layer.ExtendsRemovedReason || !reflect.DeepEqual(rj.Details, want) {
		t.Errorf("rejection of %s = %+v, want %s, reason %q, details %v",
			id, rj, layer.InvalidArtifactCode, layer.ExtendsRemovedReason, want)
	}
}

// erAssertNotIngested fails when res lists id among the ingested pairs.
func erAssertNotIngested(t *testing.T, res *ingest.Result, id string) {
	t.Helper()
	for _, ia := range res.Ingested {
		if ia.ArtifactID == id {
			t.Errorf("%s listed as ingested: %+v", id, ia)
		}
	}
}

// erAssertNotStored fails unless id@ver is absent from the store.
func erAssertNotStored(t *testing.T, st store.Store, id, ver string) {
	t.Helper()
	if _, err := st.GetManifest(context.Background(), erTenant, id, ver); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetManifest(%s@%s) err = %v, want store.ErrNotFound", id, ver, err)
	}
}

// erAssertNoDependents fails when any edge points at id.
func erAssertNoDependents(t *testing.T, st store.Store, id string) {
	t.Helper()
	edges, err := st.DependentsOf(context.Background(), erTenant, id)
	if err != nil {
		t.Fatalf("DependentsOf(%s): %v", id, err)
	}
	if len(edges) != 0 {
		t.Errorf("DependentsOf(%s) = %+v, want no edge", id, edges)
	}
}

// erLoad loads id at ver ("" for latest) through reg as a public caller.
func erLoad(reg *core.Registry, id, ver string) (*core.LoadArtifactResult, error) {
	return reg.LoadArtifact(context.Background(), erPublic, id, core.LoadArtifactOptions{Version: ver})
}

// erAssertServes fails unless a load of id at ver serves wantVersion.
func erAssertServes(t *testing.T, reg *core.Registry, id, ver, wantVersion string) {
	t.Helper()
	res, err := erLoad(reg, id, ver)
	if err != nil {
		t.Fatalf("LoadArtifact(%s@%q): %v", id, ver, err)
	}
	if res.Version != wantVersion {
		t.Errorf("LoadArtifact(%s@%q) version = %s, want %s", id, ver, res.Version, wantVersion)
	}
}

// erAssertUnserved fails unless a latest load of id answers registry.not_found.
func erAssertUnserved(t *testing.T, reg *core.Registry, id string) {
	t.Helper()
	if _, err := erLoad(reg, id, ""); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("LoadArtifact(%s) err = %v, want registry.not_found", id, err)
	}
}

// Spec: §4.6, §7.3.1 — an ARTIFACT.md that declares extends: is refused with
// ingest.invalid_artifact and exactly the extends_removed constraint. The
// refused artifact is not stored and writes no dependency edge, so a plain
// copy of the same ID from another layer later collides with nothing.
func TestIngest_ExtendsRemovedRefusal(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res := erIngest(t, st, "team", map[string]string{
		"ops/child/ARTIFACT.md": erDoc("context", "1.0.0", "extends: ops/base\ndelegates_to: [ops/target]\n", "Child body."),
	})
	if res.Accepted != 0 || res.Idempotent != 0 || len(res.Rejected) != 1 {
		t.Fatalf("result = %+v, want the one refusal only", res)
	}
	erAssertExtendsRemoved(t, res, "ops/child")
	erAssertNotIngested(t, res, "ops/child")
	erAssertNotStored(t, st, "ops/child", "1.0.0")
	erAssertNoDependents(t, st, "ops/target")

	plain := erIngest(t, st, "base", map[string]string{
		"ops/child/ARTIFACT.md": erDoc("context", "1.0.0", "", "Plain body."),
	})
	if plain.Accepted != 1 || len(plain.Rejected) != 0 {
		t.Fatalf("plain copy from another layer = %+v, want accepted with no ingest.collision", plain)
	}
}

// Spec: §4.6, §7.3.1 — the refusal runs before lint. A hook that declares
// extends: and omits hook_action, which lint.required_field_missing would
// refuse, is reported only as extends_removed: no lint diagnostic is recorded
// and the call does not fail with ingest.lint_failed.
func TestIngest_ExtendsRemovedBeforeLint(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: erTenant, LayerID: "team", Files: erFS(map[string]string{
			"ops/hook/ARTIFACT.md": erDoc("hook", "1.0.0", "hook_event: stop\nextends: ops/base-hook\n", "Hook body."),
		}),
	})
	if err != nil {
		t.Fatalf("Ingest err = %v, want nil and no ingest.lint_failed", err)
	}
	if len(res.LintFailures) != 0 {
		t.Errorf("LintFailures = %+v, want none", res.LintFailures)
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("Rejected = %+v, want the one refusal", res.Rejected)
	}
	erAssertExtendsRemoved(t, res, "ops/hook")
}

// Spec: §4.6, §7.3.1, §4.7.3 — a refused mcp-server leaves the record set
// before the server_identifier index is built, so an agent whose mcpServers
// entry derives that identifier resolves no mcpServers edge to it.
func TestIngest_ExtendsRemovedMCPServerResolvesNoEdge(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res := erIngest(t, st, "team", map[string]string{
		"infra/mcp/warehouse/ARTIFACT.md": erDoc("mcp-server", "1.0.0",
			"server_identifier: npx:@acme/warehouse-mcp\nextends: infra/mcp/base\n", "Server body."),
		"finance/agent/ARTIFACT.md": erDoc("agent", "1.0.0",
			"mcpServers:\n  - name: warehouse\n    command: npx\n    args: [\"-y\", \"@acme/warehouse-mcp\"]\n", "Agent body."),
	})
	erAssertExtendsRemoved(t, res, "infra/mcp/warehouse")
	if res.Accepted != 1 {
		t.Fatalf("Accepted = %d, want the agent: %+v", res.Accepted, res)
	}
	erAssertNoDependents(t, st, "infra/mcp/warehouse")
}

// Spec: §4.6, §7.3.1 — a stored row whose ARTIFACT.md declares extends: is
// retired, and a retired row in one layer is no §4.6 collision for a new
// version of the same ID ingested in another layer.
func TestIngest_RetiredRowIsNoCollision(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	erSeed(t, st, erRow("ops/doc", "1.0.0", "team",
		erDoc("context", "1.0.0", "extends: ops/base\n", "Retired body."), 1))
	res := erIngest(t, st, "base", map[string]string{
		"ops/doc/ARTIFACT.md": erDoc("context", "2.0.0", "", "Plain body."),
	})
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("result = %+v, want ops/doc@2.0.0 accepted with no rejection", res)
	}
}

// Spec: §4.6, §7.3.1 — bytes identical to a stored retired row are refused
// before placement, so they are never an idempotent no-op.
func TestIngest_RetiredRowBytesAreRefused(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	doc := erDoc("context", "1.0.0", "extends: ops/base\n", "Retired body.")
	erSeed(t, st, erRow("ops/doc", "1.0.0", "team", doc, 1))
	res := erIngest(t, st, "team", map[string]string{"ops/doc/ARTIFACT.md": doc})
	if res.Idempotent != 0 || res.Accepted != 0 || len(res.Conflicts) != 0 {
		t.Errorf("result = %+v, want no idempotent, accepted, or conflicting pair", res)
	}
	erAssertNotIngested(t, res, "ops/doc")
	erAssertExtendsRemoved(t, res, "ops/doc")
}

// Spec: §4.6, §7.3.1, §4.7 — a retired row keeps its (id, version). Plain
// bytes at that pair conflict with the stored hash, which the reingest
// endpoint reports as ingest.immutable_violation.
func TestIngest_RetiredRowVersionIsImmutable(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	retired := erSeed(t, st, erRow("ops/doc", "1.0.0", "team",
		erDoc("context", "1.0.0", "extends: ops/base\n", "Retired body."), 1))
	res := erIngest(t, st, "base", map[string]string{
		"ops/doc/ARTIFACT.md": erDoc("context", "1.0.0", "", "Plain body."),
	})
	if len(res.Conflicts) != 1 {
		t.Fatalf("Conflicts = %+v, want one: %+v", res.Conflicts, res)
	}
	c := res.Conflicts[0]
	if c.ArtifactID != "ops/doc" || c.Version != "1.0.0" || c.OldHash != retired.ContentHash || c.NewHash == retired.ContentHash {
		t.Errorf("conflict = %+v, want ops/doc@1.0.0 against the retired hash %s", c, retired.ContentHash)
	}
	if res.Accepted != 0 || len(res.Rejected) != 0 {
		t.Errorf("result = %+v, want only the conflict", res)
	}
}

// erRetiredSkillRow is a retired row of the skill ops/s at version ver in
// layerID, ingested at at. Its ARTIFACT.md declares extends:.
func erRetiredSkillRow(ver, layerID string, at time.Time) store.ManifestRecord {
	rec := erRow("ops/s", ver, layerID, "---\ntype: skill\nextends: ops/base-skill\n---\n", 0)
	rec.Type = "skill"
	rec.SkillRaw = []byte(erSkill("s", ""))
	rec.IngestedAt = at
	return rec
}

// Spec: §4.6, §7.3.1, §4.7.6 — a retired row never supplies the reused
// registry-assigned version. A skill stored at 0.0.0-auto.0 beside a newer
// retired 0.0.0-auto.1 in the same layer stays unchanged at auto.0 when its
// bytes are ingested again, rather than being refused as duplicate content.
func TestIngest_AutoVersionRevertSkipsRetiredRow(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	files := map[string]string{"ops/s/SKILL.md": erSkill("s", "")}
	first := erIngest(t, st, "team", files)
	if first.Accepted != 1 || len(first.Ingested) != 1 || first.Ingested[0].Version != "0.0.0-auto.0" {
		t.Fatalf("first ingest = %+v, want ops/s@0.0.0-auto.0", first)
	}
	// The retired row is ingested after the served one, so it would be the
	// layer's latest row if a retired row were a reuse candidate.
	erSeed(t, st, erRetiredSkillRow("0.0.0-auto.1", "team", time.Now().Add(time.Hour)))

	res := erIngest(t, st, "team", files)
	if res.Idempotent != 1 || res.Accepted != 0 || len(res.Rejected) != 0 {
		t.Fatalf("revert = %+v, want one idempotent pair", res)
	}
	if got := res.Ingested[0]; got.ArtifactID != "ops/s" || got.Version != "0.0.0-auto.0" || got.Accepted {
		t.Errorf("Ingested = %+v, want unchanged ops/s@0.0.0-auto.0", got)
	}
}

// Spec: §4.6, §7.3.1, §4.7.6 — a retired row counts toward the N derivation,
// because it still occupies its (id, version), so a skill with no declared
// version whose ID holds a retired 0.0.0-auto.1 receives 0.0.0-auto.2.
func TestIngest_AutoVersionCountsRetiredRow(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	erSeed(t, st, erRetiredSkillRow("0.0.0-auto.1", "team", erEpoch))
	res := erIngest(t, st, "team", map[string]string{"ops/s/SKILL.md": erSkill("s", "")})
	if res.Accepted != 1 || len(res.Ingested) != 1 {
		t.Fatalf("result = %+v, want one accepted pair", res)
	}
	if v := res.Ingested[0].Version; v != "0.0.0-auto.2" {
		t.Errorf("assigned version = %s, want 0.0.0-auto.2", v)
	}
}

// Spec: §4.6, §7.3.1, §4.4 — the refused artifact is withheld from the
// catalog lint resolves prose references against, so a same-layer artifact
// whose prose links to it fails lint.prose_reference, while its link to a
// kept sibling resolves.
func TestIngest_ExtendsRemovedProseReference(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res := erIngest(t, st, "team", map[string]string{
		"ops/refused/ARTIFACT.md": erDoc("context", "1.0.0", "extends: ops/base\n", "Refused body."),
		"ops/kept/ARTIFACT.md":    erDoc("context", "1.0.0", "", "Kept body."),
		"ops/sibling/ARTIFACT.md": erDoc("context", "1.0.0", "",
			"See [the refused one](ops/refused) and [the kept one](ops/kept)."),
	})
	erAssertExtendsRemoved(t, res, "ops/refused")
	if len(res.LintFailures) != 1 {
		t.Fatalf("LintFailures = %+v, want the one prose reference", res.LintFailures)
	}
	d := res.LintFailures[0]
	if d.ArtifactID != "ops/sibling" || d.Code != "lint.prose_reference" || !strings.Contains(d.Message, "ops/refused") {
		t.Errorf("diagnostic = %+v, want lint.prose_reference on ops/sibling naming ops/refused", d)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want ops/kept only", res.Accepted)
	}
	erAssertNotStored(t, st, "ops/sibling", "1.0.0")
}

// Spec: §4.6, §7.3.1, §4.3.4 — an extends key in a skill's SKILL.md is not a
// Podium field, and the no-inheritance rule does not refuse it. A plain skill
// and a skill with an ARTIFACT.md overlay that omits the key are both
// accepted, stored, and served, because the refusal reads the authored
// ARTIFACT.md and retirement reads the stored frontmatter, never SKILL.md.
func TestIngest_SkillMDExtendsKeyIsServed(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res := erIngest(t, st, "team", map[string]string{
		"ops/plain/SKILL.md":       erSkill("plain", "extends: other/skill\n"),
		"ops/overlaid/SKILL.md":    erSkill("overlaid", "extends: other/skill\n"),
		"ops/overlaid/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nsensitivity: low\n---\n\n",
	})
	if len(res.Rejected) != 0 || len(res.LintFailures) != 0 {
		t.Fatalf("Rejected = %+v, LintFailures = %+v, want neither", res.Rejected, res.LintFailures)
	}
	want := map[string]string{"ops/plain": "0.0.0-auto.0", "ops/overlaid": "1.0.0"}
	got := map[string]string{}
	for _, ia := range res.Ingested {
		if ia.Accepted {
			got[ia.ArtifactID] = ia.Version
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("accepted = %v, want %v", got, want)
	}
	reg := erRegistry(st, "team")
	for id, ver := range want {
		if _, err := st.GetManifest(context.Background(), erTenant, id, ver); err != nil {
			t.Errorf("GetManifest(%s@%s): %v", id, ver, err)
		}
		erAssertServes(t, reg, id, "", ver)
	}
}

// erTwoLayerFiles are the sources of the D-7 two-layer case, keyed by name.
var erTwoLayerFiles = struct {
	yBase, yOther, z, s string
}{
	yBase:  erDoc("context", "1.0.0", "", "Base copy of ops/y."),
	yOther: erDoc("context", "3.0.0", "", "Other copy of ops/y."),
	z:      erDoc("context", "1.0.0", "", "Body of ops/z."),
	s:      erSkill("s", ""),
}

// erTwoLayerStore registers base, team, and other, ingests ops/z and the
// version-less skill ops/s into other, and then seeds the ops/y rows a 0.5.x
// server left in three layers: a plain 1.0.0 in base, a retired 2.0.0 in team
// that declares extends: ops/y, and a plain 3.0.0 in other, in that ingest
// order.
func erTwoLayerStore(t *testing.T) *store.Memory {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: erTenant, Name: erTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for i, id := range []string{"base", "team", "other"} {
		if err := st.PutLayerConfig(ctx, store.LayerConfig{
			TenantID: erTenant, ID: id, Order: i + 1, SourceType: "local", LocalPath: "/srv/" + id, Public: true,
		}); err != nil {
			t.Fatalf("PutLayerConfig(%s): %v", id, err)
		}
	}
	res := erIngest(t, st, "other", map[string]string{
		"ops/z/ARTIFACT.md": erTwoLayerFiles.z,
		"ops/s/SKILL.md":    erTwoLayerFiles.s,
	})
	if res.Accepted != 2 {
		t.Fatalf("ingest ops/z and ops/s = %+v, want both accepted", res)
	}
	now := time.Now().UTC()
	rows := []store.ManifestRecord{
		erRow("ops/y", "1.0.0", "base", erTwoLayerFiles.yBase, 0),
		erRow("ops/y", "2.0.0", "team", erDoc("context", "2.0.0", "extends: ops/y\n", "Team overlay of ops/y."), 0),
		erRow("ops/y", "3.0.0", "other", erTwoLayerFiles.yOther, 0),
	}
	for i, rec := range rows {
		rec.IngestedAt = now.Add(time.Duration(i+1) * time.Hour)
		erSeed(t, st, rec)
	}
	return st
}

// erAssertCollisionOnly fails unless res rejects ops/y with ingest.collision
// and records no idempotent or accepted pair.
func erAssertCollisionOnly(t *testing.T, res *ingest.Result, what string) {
	t.Helper()
	if rj := erRejection(t, res, "ops/y"); rj.Code != layer.CollisionCode {
		t.Errorf("%s: code = %q, want %s", what, rj.Code, layer.CollisionCode)
	}
	if res.Idempotent != 0 || res.Accepted != 0 {
		t.Errorf("%s: result = %+v, want no idempotent or accepted pair", what, res)
	}
}

// Spec: §4.6, §7.3.1, §4.7.6, §8.4 — the non-retired rows of an ID that two
// registry-side layers contributed keep serving, with latest the most
// recently ingested of them, and every later ingest of the ID from either
// layer, unchanged bytes included, is an ingest.collision. Unregistering one
// layer soft-deletes its rows and its other artifacts, a registration of the
// same source before the §8.4 purge serves none of them, and after the purge
// the same source ingests and serves again.
func TestIngest_TwoLayerIDsAfterRetirement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := erTwoLayerStore(t)
	reg := erRegistry(st, "base", "team", "other", "other2")
	erAssertServes(t, reg, "ops/y", "", "3.0.0")

	baseFiles := map[string]string{"ops/y/ARTIFACT.md": erTwoLayerFiles.yBase}
	erAssertCollisionOnly(t, erIngest(t, st, "base", baseFiles), "base re-ingest")
	erAssertCollisionOnly(t, erIngest(t, st, "other", map[string]string{
		"ops/y/ARTIFACT.md": erTwoLayerFiles.yOther,
	}), "other re-ingest")

	if err := st.DeleteLayerConfig(ctx, erTenant, "other"); err != nil {
		t.Fatalf("DeleteLayerConfig(other): %v", err)
	}
	res := erIngest(t, st, "base", baseFiles)
	if res.Idempotent != 1 || len(res.Rejected) != 0 {
		t.Errorf("base re-ingest after unregistering other = %+v, want idempotent with no rejection", res)
	}
	erAssertServes(t, reg, "ops/y", "", "1.0.0")
	erAssertUnserved(t, reg, "ops/z")
	erAssertUnserved(t, reg, "ops/s")

	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: erTenant, ID: "other2", Order: 4, SourceType: "local", LocalPath: "/srv/other", Public: true,
	}); err != nil {
		t.Fatalf("PutLayerConfig(other2): %v", err)
	}
	reregistered := map[string]string{
		"ops/z/ARTIFACT.md": erTwoLayerFiles.z,
		"ops/s/SKILL.md":    erTwoLayerFiles.s,
	}
	early := erIngest(t, st, "other2", reregistered)
	rj := erRejection(t, early, "ops/s")
	wantDup := map[string]any{"constraint": "auto_version_duplicate_content"}
	if rj.Code != layer.InvalidArtifactCode || !reflect.DeepEqual(rj.Details, wantDup) {
		t.Errorf("pre-purge ops/s rejection = %+v, want %s with details %v", rj, layer.InvalidArtifactCode, wantDup)
	}
	erAssertUnserved(t, reg, "ops/z")
	erAssertUnserved(t, reg, "ops/s")

	if _, err := st.PurgeExpiredLayerDeletions(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("PurgeExpiredLayerDeletions: %v", err)
	}
	late := erIngest(t, st, "other2", reregistered)
	if late.Accepted != 2 || len(late.Rejected) != 0 {
		t.Fatalf("post-purge ingest = %+v, want both accepted", late)
	}
	erAssertServes(t, reg, "ops/z", "", "1.0.0")
	erAssertServes(t, reg, "ops/s", "", "0.0.0-auto.0")
	erAssertServes(t, reg, "ops/y", "", "1.0.0")
}
