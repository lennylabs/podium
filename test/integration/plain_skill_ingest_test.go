package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// The plain-skill cases drive a SKILL.md-only package, which carries no
// ARTIFACT.md and declares no metadata.version, through ingest and the HTTP
// read path. Every case runs over ingestConvergenceStores, so memory and
// SQLite always run and Postgres runs when PODIUM_POSTGRES_DSN is set.

const psTenant = "t"

// psDuplicateDetails is the details object of the §7.3.1 rejection of a
// version-less skill whose bytes a stored row of its ID already holds.
var psDuplicateDetails = map[string]any{"constraint": "auto_version_duplicate_content"}

// psSkill is the SKILL.md of the plain skill x. body varies the content hash,
// and a non-empty metaVersion declares metadata.version.
func psSkill(body, metaVersion string) string {
	return psNamedSkill("x", body, metaVersion)
}

// psNamedSkill is psSkill for the skill whose directory is name, which the
// Agent Skills name field must match.
func psNamedSkill(name, body, metaVersion string) string {
	meta := ""
	if metaVersion != "" {
		meta = "metadata:\n  version: \"" + metaVersion + "\"\n"
	}
	return "---\nname: " + name + "\ndescription: " + name + " skill\n" + meta + "---\n\n" + body + "\n"
}

// psFS is a layer snapshot holding id's SKILL.md and, when artifactMD is
// non-empty, its ARTIFACT.md.
func psFS(id, skill, artifactMD string) fstest.MapFS {
	fs := fstest.MapFS{id + "/SKILL.md": &fstest.MapFile{Data: []byte(skill)}}
	if artifactMD != "" {
		fs[id+"/ARTIFACT.md"] = &fstest.MapFile{Data: []byte(artifactMD)}
	}
	return fs
}

// psEnv is one store with its tenant, a set of public layers, and the HTTP
// read path over it.
type psEnv struct {
	st store.Store
	ts *httptest.Server
}

// newPSEnv opens a store from f, creates the tenant, registers each layer as
// public in ascending order, and serves the store through the registry's
// HTTP handler, which admits every row it loads (§13.4).
func newPSEnv(t *testing.T, f ingestConvergenceFactory, layers ...string) *psEnv {
	t.Helper()
	st := f.open(t)
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: psTenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	e := &psEnv{st: st}
	for i, id := range layers {
		e.register(t, id, i+1)
	}
	reg := core.New(st, psTenant, nil).WithAdmission(nil, nil, 0)
	e.ts = httptest.NewServer(server.New(reg, server.WithIdentityResolver(func(*http.Request) layer.Identity {
		return layer.Identity{IsPublic: true}
	})).Handler())
	t.Cleanup(e.ts.Close)
	return e
}

// register stores a public layer config for id at order.
func (e *psEnv) register(t *testing.T, id string, order int) {
	t.Helper()
	if err := e.st.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: psTenant, ID: id, SourceType: "local", Order: order, Public: true,
	}); err != nil {
		t.Fatalf("PutLayerConfig %s: %v", id, err)
	}
}

// ingest runs the pipeline for one snapshot of layerID.
func (e *psEnv) ingest(t *testing.T, layerID string, files fstest.MapFS) *ingest.Result {
	t.Helper()
	res, err := ingest.Ingest(context.Background(), e.st, ingest.Request{TenantID: psTenant, LayerID: layerID, Files: files})
	if err != nil {
		t.Fatalf("Ingest %s: %v", layerID, err)
	}
	return res
}

// wantAccepted fails unless res newly stored exactly id at version v.
func wantAccepted(t *testing.T, res *ingest.Result, id, v string) {
	t.Helper()
	if res.Accepted != 1 || len(res.Rejected) != 0 || len(res.Conflicts) != 0 || len(res.LintFailures) != 0 {
		t.Fatalf("result = %+v, want one accepted record", res)
	}
	if got := res.Ingested; len(got) != 1 || got[0].ArtifactID != id || got[0].Version != v || !got[0].Accepted {
		t.Fatalf("ingested = %+v, want %s@%s accepted", got, id, v)
	}
}

// wantIdempotent fails unless res matched the stored id@v without a write.
func wantIdempotent(t *testing.T, res *ingest.Result, id, v string) {
	t.Helper()
	if res.Accepted != 0 || res.Idempotent != 1 || len(res.Rejected) != 0 || len(res.Conflicts) != 0 {
		t.Fatalf("result = %+v, want one idempotent record", res)
	}
	if got := res.Ingested; len(got) != 1 || got[0].ArtifactID != id || got[0].Version != v || got[0].Accepted {
		t.Fatalf("ingested = %+v, want %s@%s idempotent", got, id, v)
	}
}

// wantRejected fails unless res rejected id alone with code and details and
// reported it neither accepted nor idempotent.
func wantRejected(t *testing.T, res *ingest.Result, id, code string, details map[string]any) {
	t.Helper()
	if res.Accepted != 0 || res.Idempotent != 0 || len(res.Ingested) != 0 {
		t.Fatalf("result = %+v, want nothing accepted or idempotent", res)
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected = %+v, want one entry", res.Rejected)
	}
	r := res.Rejected[0]
	if r.ArtifactID != id || r.Code != code || !reflect.DeepEqual(r.Details, details) {
		t.Errorf("rejected = %+v, want %s with %s and details %v", r, id, code, details)
	}
}

// rows returns every stored row of id, soft-deleted rows included.
func (e *psEnv) rows(t *testing.T, id string) []store.ManifestRecord {
	t.Helper()
	all, err := e.st.ListManifestsIncludingDeleted(context.Background(), psTenant)
	if err != nil {
		t.Fatalf("ListManifestsIncludingDeleted: %v", err)
	}
	var out []store.ManifestRecord
	for _, m := range all {
		if m.ArtifactID == id {
			out = append(out, m)
		}
	}
	return out
}

// row returns the non-deleted stored row id@v.
func (e *psEnv) row(t *testing.T, id, v string) store.ManifestRecord {
	t.Helper()
	m, err := e.st.GetManifest(context.Background(), psTenant, id, v)
	if err != nil {
		t.Fatalf("GetManifest %s@%s: %v", id, v, err)
	}
	return m
}

// load issues GET /v1/load_artifact for id at v (latest when empty) and
// returns the status and the decoded response.
func (e *psEnv) load(t *testing.T, id, v string) (int, server.LoadArtifactResponse) {
	t.Helper()
	q := url.Values{"id": {id}}
	if v != "" {
		q.Set("version", v)
	}
	resp, err := http.Get(e.ts.URL + "/v1/load_artifact?" + q.Encode())
	if err != nil {
		t.Fatalf("load %s: %v", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out server.LoadArtifactResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode load %s: %v (%s)", id, err, raw)
		}
	}
	return resp.StatusCode, out
}

// wantServedPlain fails unless a latest load of id serves version v with the
// synthesized ARTIFACT.md and body.
func (e *psEnv) wantServedPlain(t *testing.T, id, v, body string) {
	t.Helper()
	status, got := e.load(t, id, "")
	if status != http.StatusOK {
		t.Fatalf("load %s = %d, want 200", id, status)
	}
	want, err := manifest.SynthesizedSkillDocument(v)
	if err != nil {
		t.Fatalf("SynthesizedSkillDocument: %v", err)
	}
	if got.Version != v || got.Type != "skill" || got.Frontmatter != string(want) || !strings.Contains(got.ManifestBody, body) {
		t.Errorf("load %s = version %q type %q frontmatter %q body %q, want %s, skill, the synthesized document, and %q",
			id, got.Version, got.Type, got.Frontmatter, got.ManifestBody, v, body)
	}
}

// Spec: §7.3.1, §4.7.6, §4.7.10, §13.4 — a version-less plain skill is
// assigned 0.0.0-auto.<N> from the stored rows: the first ingest assigns 0,
// unchanged bytes are idempotent, changed bytes take the next N, and a
// revert to stored bytes is refused so no two versions share a content hash.
// The stored manifest is empty, a load admits the row and serves the
// synthesized ARTIFACT.md, and a tombstoned row's N is never issued again.
func TestPlainSkillIngest_AssignsVersionsAndServesTheSynthesizedDocument(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			e := newPSEnv(t, f, "L")
			first, changed := psSkill("first", ""), psSkill("changed", "")

			wantAccepted(t, e.ingest(t, "L", psFS("x", first, "")), "x", "0.0.0-auto.0")
			wantIdempotent(t, e.ingest(t, "L", psFS("x", first, "")), "x", "0.0.0-auto.0")
			if fm := e.row(t, "x", "0.0.0-auto.0").Frontmatter; len(fm) != 0 {
				t.Errorf("stored Frontmatter = %q, want empty", fm)
			}
			e.wantServedPlain(t, "x", "0.0.0-auto.0", "first")

			wantAccepted(t, e.ingest(t, "L", psFS("x", changed, "")), "x", "0.0.0-auto.1")

			// A revert to the first bytes would give them a second version.
			wantRejected(t, e.ingest(t, "L", psFS("x", first, "")), "x", layer.InvalidArtifactCode, psDuplicateDetails)
			if n := len(e.rows(t, "x")); n != 2 {
				t.Errorf("stored rows = %d after the refused revert, want 2", n)
			}
			e.wantServedPlain(t, "x", "0.0.0-auto.1", "changed")

			if status, got := e.load(t, "x", "0.0.0-auto.1"); status != http.StatusOK || got.Version != "0.0.0-auto.1" {
				t.Errorf("load @0.0.0-auto.1 = %d %q, want 200 at that version", status, got.Version)
			}
			if status, _ := e.load(t, "x", "0.0.x"); status != http.StatusNotFound {
				t.Errorf("load @0.0.x = %d, want 404: a range never selects the reserved form", status)
			}

			// An authored version, then a revert to the bytes of an assigned
			// one, is refused, and latest keeps serving the authored version.
			wantAccepted(t, e.ingest(t, "L", psFS("x", psSkill("authored", "1.0.0"), "")), "x", "1.0.0")
			wantRejected(t, e.ingest(t, "L", psFS("x", changed, "")), "x", layer.InvalidArtifactCode, psDuplicateDetails)
			if status, got := e.load(t, "x", ""); status != http.StatusOK || got.Version != "1.0.0" {
				t.Errorf("latest = %d %q, want 200 at 1.0.0", status, got.Version)
			}

			// A tombstoned row's N counts toward the next assignment.
			deleted := time.Now().UTC()
			e.putRow(t, storetest.Seal(t, store.ManifestRecord{
				TenantID: psTenant, ArtifactID: "x", Version: "0.0.0-auto.5", Layer: "L", Type: "skill",
				SkillRaw: []byte(psSkill("tombstoned", "")), IngestedAt: deleted, DeletedAt: &deleted,
			}, nil, nil))
			wantAccepted(t, e.ingest(t, "L", psFS("x", psSkill("after the tombstone", ""), "")), "x", "0.0.0-auto.6")
		})
	}
}

// putRow writes a sealed row directly to the store.
func (e *psEnv) putRow(t *testing.T, m store.ManifestRecord) {
	t.Helper()
	if err := e.st.PutManifest(context.Background(), m); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", m.ArtifactID, m.Version, err)
	}
}

// Spec: §7.3.1, §8.4 — unregistering a layer tombstones its rows, and the
// same bytes reingested into the re-registered layer are refused: reusing the
// tombstoned version would leave the accepted skill unserved, and a fresh N
// would give the bytes a second version that a restore would serve beside the
// first. Changed bytes take the next N.
func TestPlainSkillIngest_ReregisteredLayerRefusesTombstonedContent(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			e := newPSEnv(t, f, "L")
			ctx := context.Background()
			skill := psSkill("kept", "")
			wantAccepted(t, e.ingest(t, "L", psFS("x", skill, "")), "x", "0.0.0-auto.0")
			if err := e.st.DeleteLayerConfig(ctx, psTenant, "L"); err != nil {
				t.Fatalf("DeleteLayerConfig: %v", err)
			}
			e.register(t, "L", 1)

			wantRejected(t, e.ingest(t, "L", psFS("x", skill, "")), "x", layer.InvalidArtifactCode, psDuplicateDetails)
			if rows := e.rows(t, "x"); len(rows) != 1 || rows[0].DeletedAt == nil {
				t.Errorf("rows = %+v, want only the tombstoned row", rows)
			}
			wantAccepted(t, e.ingest(t, "L", psFS("x", psSkill("changed", ""), "")), "x", "0.0.0-auto.1")
		})
	}
}

// Spec: §7.3.1, §8.4 — a soft-deleted row of another layer is no §4.6
// collision and still counts toward N and the duplicate set; a purge of the
// tombstoned rows releases their bytes, which then ingest at the purged N.
func TestPlainSkillIngest_SoftDeletedCrossLayerRowAndPurge(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			held := psSkill("held by L", "")
			setup := func(t *testing.T) *psEnv {
				t.Helper()
				e := newPSEnv(t, f, "L", "M")
				wantAccepted(t, e.ingest(t, "L", psFS("x", held, "")), "x", "0.0.0-auto.0")
				if err := e.st.DeleteLayerConfig(context.Background(), psTenant, "L"); err != nil {
					t.Fatalf("DeleteLayerConfig: %v", err)
				}
				return e
			}

			t.Run("other bytes take the next N", func(t *testing.T) {
				e := setup(t)
				wantAccepted(t, e.ingest(t, "M", psFS("x", psSkill("M's own", ""), "")), "x", "0.0.0-auto.1")
			})
			t.Run("held bytes are released by a purge", func(t *testing.T) {
				e := setup(t)
				wantRejected(t, e.ingest(t, "M", psFS("x", held, "")), "x", layer.InvalidArtifactCode, psDuplicateDetails)
				if _, err := e.st.PurgeExpiredLayerDeletions(context.Background(), time.Now().Add(time.Hour)); err != nil {
					t.Fatalf("PurgeExpiredLayerDeletions: %v", err)
				}
				wantAccepted(t, e.ingest(t, "M", psFS("x", held, "")), "x", "0.0.0-auto.0")
				e.wantServedPlain(t, "x", "0.0.0-auto.0", "held by L")
			})
		})
	}
}

// Spec: §7.3.1, §4.7.6 — concurrent ingests of different bytes for one
// version-less skill in one layer commit two distinct N, because a commit
// that finds the assigned version taken re-reads the rows and re-derives N.
// It runs on file-backed SQLite, whose pooled connections commit
// concurrently, and is -race clean.
func TestPlainSkillIngest_ConcurrentIngestsCommitDistinctVersions(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		if f.name != "sqlite" {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			e := newPSEnv(t, f, "L")
			results := make([]*ingest.Result, 2)
			errs := make([]error, 2)
			ready := make(chan struct{})
			var wg sync.WaitGroup
			for i := range results {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-ready
					results[i], errs[i] = ingest.Ingest(context.Background(), e.st, ingest.Request{
						TenantID: psTenant, LayerID: "L",
						Files: psFS("x", psSkill("racer "+string(rune('a'+i)), ""), ""),
					})
				}()
			}
			close(ready)
			wg.Wait()
			versions := map[string]bool{}
			for i, res := range results {
				if errs[i] != nil {
					t.Fatalf("Ingest %d: %v", i, errs[i])
				}
				if len(res.Conflicts) != 0 || res.Accepted != 1 {
					t.Fatalf("result %d = %+v, want one accepted record and no conflict", i, res)
				}
				versions[res.Ingested[0].Version] = true
			}
			if !versions["0.0.0-auto.0"] || !versions["0.0.0-auto.1"] {
				t.Errorf("versions = %v, want 0.0.0-auto.0 and 0.0.0-auto.1", versions)
			}
		})
	}
}

// psOverlay is team's same-ID overlay of x: a hand-written ARTIFACT.md that
// extends x, sets two tags, and declares no version.
const psOverlay = "---\ntype: skill\nextends: x\ntags: [alpha, beta]\n---\n"

// Spec: §7.3.1, §13.11.3, §4.6 — two layers contribute one ID: base a plain
// skill and team a same-ID overlay that declares no version. N is derived
// across both layers' rows, and the assigned-version overlay pins its parent
// as an authored one does: its first row pins base's row, and after a change
// to its bytes it pins its own previous row, the most recently ingested row
// of the ID. A load serves the merged document at the child row's version.
func TestPlainSkillIngest_TwoLayersShareTheAssignedSequence(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			e := newPSEnv(t, f, "base", "team")
			baseFS := psFS("x", psSkill("base prose", ""), "")
			teamFS := psFS("x", psSkill("team prose", ""), psOverlay)

			wantAccepted(t, e.ingest(t, "base", baseFS), "x", "0.0.0-auto.0")
			wantAccepted(t, e.ingest(t, "team", teamFS), "x", "0.0.0-auto.1")
			wantIdempotent(t, e.ingest(t, "base", baseFS), "x", "0.0.0-auto.0")
			wantIdempotent(t, e.ingest(t, "team", teamFS), "x", "0.0.0-auto.1")

			wantAccepted(t, e.ingest(t, "team", psFS("x", psSkill("team prose, edited", ""), psOverlay)), "x", "0.0.0-auto.2")

			status, got := e.load(t, "x", "")
			if status != http.StatusOK {
				t.Fatalf("load x = %d, want 200", status)
			}
			a, err := manifest.ParseArtifact([]byte(got.Frontmatter))
			if err != nil {
				t.Fatalf("ParseArtifact(merged): %v\n%s", err, got.Frontmatter)
			}
			if got.Version != "0.0.0-auto.2" || a.Version != "0.0.0-auto.2" {
				t.Errorf("served version %q, merged version %q, want 0.0.0-auto.2", got.Version, a.Version)
			}
		})
	}
}

// Spec: §4.6, §7.3.1 — a third layer's version-less plain skill at an ID
// another layer already contributes is a §4.6 collision when no overlay row
// of the ID is stored. Once team's overlay row is stored the §4.6 check admits
// it, and a package byte-identical to base's plain skill is then refused as
// duplicate content. Neither stores a row for the third layer.
func TestPlainSkillIngest_ThirdLayerCollisionAndDuplicate(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			e := newPSEnv(t, f, "base", "team", "other")
			baseFS := psFS("x", psSkill("base prose", ""), "")
			wantAccepted(t, e.ingest(t, "base", baseFS), "x", "0.0.0-auto.0")

			res := e.ingest(t, "other", psFS("x", psSkill("other prose", ""), ""))
			if len(res.Rejected) != 1 || res.Rejected[0].Code != "ingest.collision" || res.Accepted != 0 {
				t.Errorf("other's ingest = %+v, want one ingest.collision rejection", res)
			}

			wantAccepted(t, e.ingest(t, "team", psFS("x", psSkill("team prose", ""), psOverlay)), "x", "0.0.0-auto.1")
			wantRejected(t, e.ingest(t, "other", baseFS), "x", layer.InvalidArtifactCode, psDuplicateDetails)
			for _, m := range e.rows(t, "x") {
				if m.Layer == "other" {
					t.Errorf("a row for other is stored: %s@%s", m.ArtifactID, m.Version)
				}
			}
		})
	}
}

// Spec: §7.3.1, §4.7.6 — a both-files skill whose ARTIFACT.md is removed
// keeps its metadata.version, so the reingest changes the bytes of a stored
// version and reports the §4.7 immutability conflict, and latest still serves
// the original row with its authored ARTIFACT.md.
func TestPlainSkillIngest_RemovedArtifactManifestConflicts(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			e := newPSEnv(t, f, "L")
			const authored = "---\ntype: skill\nversion: 1.0.0\n---\n"
			skill := psNamedSkill("y", "both files", "1.0.0")
			wantAccepted(t, e.ingest(t, "L", psFS("y", skill, authored)), "y", "1.0.0")

			res := e.ingest(t, "L", psFS("y", skill, ""))
			if len(res.Conflicts) != 1 || res.Conflicts[0].ArtifactID != "y" || res.Conflicts[0].Version != "1.0.0" || res.Accepted != 0 {
				t.Errorf("reingest = %+v, want one conflict on y@1.0.0", res)
			}
			status, got := e.load(t, "y", "")
			if status != http.StatusOK || got.Version != "1.0.0" || got.Frontmatter != authored {
				t.Errorf("latest = %d %q %q, want 200 at 1.0.0 with the authored ARTIFACT.md", status, got.Version, got.Frontmatter)
			}
		})
	}
}
