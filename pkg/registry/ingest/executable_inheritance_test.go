package ingest_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// execAgent builds a type:agent ARTIFACT.md at version with an optional
// extends: reference and extra frontmatter lines. The server cases use an
// undeclared key, an mcpServers entry, or server_identifier, because the
// ingest linter refuses a hook child that omits hook_action before the §4.6
// executable-field rule runs.
func execAgent(version, extends, extra string) string {
	return execArtifact("agent", version, extends, extra)
}

// execMCPServer is execAgent for a type:mcp-server artifact.
func execMCPServer(version, extends, extra string) string {
	return execArtifact("mcp-server", version, extends, extra)
}

// execArtifact builds an ARTIFACT.md of type typ for execAgent and
// execMCPServer.
func execArtifact(typ, version, extends, extra string) string {
	src := "---\ntype: " + typ + "\nversion: " + version + "\ndescription: " + typ + "\nsensitivity: low\n"
	if extends != "" {
		src += "extends: " + extends + "\n"
	}
	return src + extra + "---\n\n" + typ + " body\n"
}

// ingestSnapshot ingests files under layerID and fails the test on an abort.
func ingestSnapshot(t *testing.T, st store.Store, layerID string, files fstest.MapFS) *ingest.Result {
	t.Helper()
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: layerID, Files: files,
	})
	if err != nil {
		t.Fatalf("ingest %s: %v", layerID, err)
	}
	return res
}

// artifactFile is one ARTIFACT.md entry for a snapshot.
func artifactFile(src string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(src)} }

// assertNotStored fails when id@version reached the store.
func assertNotStored(t *testing.T, st store.Store, id, version string) {
	t.Helper()
	if _, err := st.GetManifest(context.Background(), "tenant-1", id, version); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("%s@%s stored (err=%v), want it refused before the insert", id, version, err)
	}
}

// A child whose pinned parent comes from another layer and that leaves an
// executable field to it is rejected with ingest.invalid_artifact, a reason
// that names the field and no parent, and the details the rule defines. The
// child is never stored.
// Spec: §4.6, §7.3.1
// Matrix: §6.10 (ingest.invalid_artifact)
func TestIngest_ExecutableInheritanceRejectsCrossLayerChild(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", execAgent("1.0.0", "", "x_owner: platform\n"))

	res := ingestOne(t, st, "L2", "finance/child", execAgent("2.0.0", "shared/parent@1.x", ""))
	if res.Accepted != 0 || len(res.Rejected) != 1 {
		t.Fatalf("accepted=%d rejected=%+v, want one rejection", res.Accepted, res.Rejected)
	}
	rj := res.Rejected[0]
	want := "extends: a parent in another layer sets executable fields this artifact does not restate: x_owner; restate each one in this artifact"
	if rj.ArtifactID != "finance/child" || rj.Code != layer.InvalidArtifactCode || rj.Reason != want {
		t.Errorf("rejection = %+v, want finance/child %s %q", rj, layer.InvalidArtifactCode, want)
	}
	if strings.Contains(rj.Reason, "shared/parent") || strings.Contains(rj.Reason, "L1") {
		t.Errorf("reason %q names the parent or its layer", rj.Reason)
	}
	wantDetails := map[string]any{"constraint": "executable_inheritance", "fields": []string{"x_owner"}}
	if !reflect.DeepEqual(rj.Details, wantDetails) {
		t.Errorf("details = %#v, want %#v", rj.Details, wantDetails)
	}
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// A cross-layer child that restates every executable field is accepted, and
// a same-layer child that restates none is accepted, because the rule covers
// only an edge that crosses layers.
// Spec: §4.6
func TestIngest_ExecutableInheritanceAcceptsRestatedAndSameLayer(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", execAgent("1.0.0", "", "x_owner: platform\n"))

	res := ingestOne(t, st, "L2", "finance/child", execAgent("2.0.0", "shared/parent@1.x", "x_owner: platform\n"))
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("restating child: accepted=%d rejected=%+v, want a clean accept", res.Accepted, res.Rejected)
	}

	// The parent ID sorts before the child's, so it is stored before the
	// child resolves its pin within the same snapshot.
	same := ingestSnapshot(t, newStore(t), "L1", fstest.MapFS{
		"a/parent/ARTIFACT.md": artifactFile(execAgent("1.0.0", "", "x_owner: platform\n")),
		"b/child/ARTIFACT.md":  artifactFile(execAgent("2.0.0", "a/parent@1.x", "")),
	})
	if same.Accepted != 2 || len(same.Rejected) != 0 {
		t.Errorf("same-layer child: accepted=%d rejected=%+v, want both accepted", same.Accepted, same.Rejected)
	}
}

// The check runs after the idempotency switch: an unchanged stored child is
// idempotent on reingest even after its parent's range gains a version that
// sets an executable field the child does not restate.
// Spec: §4.6, §7.3.1
func TestIngest_ExecutableInheritanceSkipsIdempotentChild(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	child := execAgent("2.0.0", "shared/parent@1.x", "")
	ingestOne(t, st, "L1", "shared/parent", execAgent("1.0.0", "", ""))
	if res := ingestOne(t, st, "L2", "finance/child", child); res.Accepted != 1 {
		t.Fatalf("child not accepted: %+v", res)
	}
	ingestOne(t, st, "L1", "shared/parent", execAgent("1.1.0", "", "x_owner: platform\n"))

	res := ingestOne(t, st, "L2", "finance/child", child)
	if res.Idempotent != 1 || len(res.Rejected) != 0 {
		t.Errorf("idempotent=%d rejected=%+v, want the stored child left idempotent", res.Idempotent, res.Rejected)
	}

	// The child's next version is a new insert, so it is judged against the
	// parent its range now selects and refused for the field it leaves.
	next := ingestOne(t, st, "L2", "finance/child", execAgent("2.1.0", "shared/parent@1.x", ""))
	if next.Accepted != 0 || len(next.Rejected) != 1 {
		t.Fatalf("next version: accepted=%d rejected=%+v, want one rejection", next.Accepted, next.Rejected)
	}
	assertFieldsRejected(t, next.Rejected[0], "finance/child", "x_owner")
	assertNotStored(t, st, "finance/child", "2.1.0")
}

// chainFaultStore fails GetManifest for one ancestor with err once armed, so
// the parent's own ingest succeeds and only the child's chain walk sees the
// failure.
type chainFaultStore struct {
	store.Store
	ancestorID string
	err        error
}

func (s chainFaultStore) GetManifest(ctx context.Context, tenantID, artifactID, version string) (store.ManifestRecord, error) {
	if artifactID == s.ancestorID {
		return store.ManifestRecord{}, s.err
	}
	return s.Store.GetManifest(ctx, tenantID, artifactID, version)
}

// seedChain stores shared/root (L0) and shared/parent (L1), which extends it.
func seedChain(t *testing.T, st store.Store, parentExtra string) {
	t.Helper()
	if res := ingestOne(t, st, "L0", "shared/root", execAgent("1.0.0", "", "")); res.Accepted != 1 {
		t.Fatalf("root not accepted: %+v", res)
	}
	if res := ingestOne(t, st, "L1", "shared/parent", execAgent("1.0.0", "shared/root@1.x", parentExtra)); res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res)
	}
}

// A cross-layer parent whose stored chain is missing an ancestor rejects the
// child with a reason that names no parent.
// Spec: §4.6 hidden parents (withheld)
func TestIngest_ExecutableInheritanceUnresolvedChainRejects(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	seedChain(t, st, "")
	gone := chainFaultStore{Store: st, ancestorID: "shared/root", err: store.ErrNotFound}

	res, err := ingest.Ingest(context.Background(), gone, ingest.Request{
		TenantID: "tenant-1", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": artifactFile(execAgent("2.0.0", "shared/parent@1.x", "")),
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].Reason != "extends: parent chain does not resolve" ||
		res.Rejected[0].Code != layer.InvalidArtifactCode || res.Rejected[0].Details != nil {
		t.Fatalf("rejected = %+v, want one chain-unresolved rejection without details", res.Rejected)
	}
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// A store failure other than absence during the chain walk aborts the cycle
// with a message that names the child alone, and the store error stays
// reachable through the chain.
// Spec: §4.6 hidden parents (withheld)
func TestIngest_ExecutableInheritanceChainReadFailureAborts(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	seedChain(t, st, "")
	boom := errors.New("connection reset")
	failing := chainFaultStore{Store: st, ancestorID: "shared/root", err: boom}

	_, err := ingest.Ingest(context.Background(), failing, ingest.Request{
		TenantID: "tenant-1", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": artifactFile(execAgent("2.0.0", "shared/parent@1.x", "")),
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Ingest error = %v, want it to wrap the store failure", err)
	}
	if got, want := err.Error(), "ingest: resolve extends chain for finance/child: store read failed"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// A field path whose name would disclose a chain ancestor rejects the child
// with the unreadable-fields reason, which names no field and no parent.
// Spec: §4.6 hidden parents (withheld)
func TestIngest_ExecutableInheritanceDisclosingPathRejects(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	seedChain(t, st, "mcpServers:\n  - name: shared/root\n    command: run\n")

	res := ingestOne(t, st, "L2", "finance/child", execAgent("2.0.0", "shared/parent@1.x", ""))
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected = %+v, want one rejection", res.Rejected)
	}
	rj := res.Rejected[0]
	if rj.Reason != layer.UnreadableExecutableFieldsReason || rj.Code != layer.InvalidArtifactCode || rj.Details != nil {
		t.Errorf("rejection = %+v, want the unreadable-fields reason without details", rj)
	}
	if strings.Contains(rj.Reason, "shared/root") {
		t.Errorf("reason %q names the ancestor", rj.Reason)
	}
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// assertFieldsRejected fails unless rj refuses id under the §4.6
// executable-field rule with exactly fields as its details.
func assertFieldsRejected(t *testing.T, rj ingest.RejectedArtifact, id string, fields ...string) {
	t.Helper()
	ie := &layer.ExecutableInheritance{ArtifactID: id, Fields: fields}
	if rj.ArtifactID != id || rj.Code != layer.InvalidArtifactCode || rj.Reason != ie.Reason() {
		t.Errorf("rejection = %+v, want %s %s %q", rj, id, layer.InvalidArtifactCode, ie.Reason())
	}
	want := map[string]any{"constraint": layer.ExecutableInheritanceConstraint, "fields": fields}
	if !reflect.DeepEqual(rj.Details, want) {
		t.Errorf("details = %#v, want %#v", rj.Details, want)
	}
}

// loadMerged serves id at version (latest when empty) through the read path,
// which folds the stored extends: chain per the §4.6 field-semantics table.
func loadMerged(t *testing.T, st store.Store, id, version string) *core.LoadArtifactResult {
	t.Helper()
	reg := core.New(st, "tenant-1", nil)
	got, err := reg.LoadArtifact(context.Background(), layer.Identity{Sub: "alice", IsAuthenticated: true}, id,
		core.LoadArtifactOptions{Version: version})
	if err != nil {
		t.Fatalf("LoadArtifact %s@%s: %v", id, version, err)
	}
	return got
}

// An mcp-server child whose cross-layer parent sets server_identifier and
// that omits it is rejected with the field in details and a reason that names
// neither the parent, its layer, nor its version. A rejection from another
// extends: rule carries no details.
// Spec: §4.6, §7.3.1
// Matrix: §6.10 (ingest.invalid_artifact)
func TestIngest_ExecutableInheritanceRejectsServerIdentifier(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/server", execMCPServer("1.0.0", "", "server_identifier: npx:@acme/srv\n"))

	res := ingestOne(t, st, "L2", "finance/server", execMCPServer("2.0.0", "shared/server@1.x", ""))
	if res.Accepted != 0 || len(res.Rejected) != 1 {
		t.Fatalf("accepted=%d rejected=%+v, want one rejection", res.Accepted, res.Rejected)
	}
	rj := res.Rejected[0]
	assertFieldsRejected(t, rj, "finance/server", "server_identifier")
	for _, withheld := range []string{"shared/server", "L1", "1.0.0"} {
		if strings.Contains(rj.Reason, withheld) {
			t.Errorf("reason %q contains %q", rj.Reason, withheld)
		}
	}
	assertNotStored(t, st, "finance/server", "2.0.0")

	mismatch := ingestOne(t, st, "L2", "finance/agent", execAgent("1.0.0", "shared/server@1.x", ""))
	if len(mismatch.Rejected) != 1 || mismatch.Rejected[0].Code != layer.InvalidArtifactCode ||
		!strings.Contains(mismatch.Rejected[0].Reason, "does not match parent") || mismatch.Rejected[0].Details != nil {
		t.Errorf("type mismatch rejected = %+v, want one invalid_artifact rejection without details", mismatch.Rejected)
	}
}

// A cross-layer child that restates server_identifier with a value of its own
// is accepted: the rule requires the child to state the field and leaves the
// value to the child.
// Spec: §4.6
func TestIngest_ExecutableInheritanceAcceptsDifferentRestatedValue(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/server", execMCPServer("1.0.0", "", "server_identifier: npx:@acme/srv\n"))

	res := ingestOne(t, st, "L2", "finance/server", execMCPServer("2.0.0", "shared/server@1.x", "server_identifier: npx:@acme/finance-srv\n"))
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want a clean accept", res.Accepted, res.Rejected)
	}
}

// A same-layer child that omits server_identifier is accepted, and the merged
// load serves the value it inherits from the parent.
// Spec: §4.6
func TestIngest_ExecutableInheritanceSameLayerServesParentField(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	// The parent ID sorts before the child's, so it is stored first.
	res := ingestSnapshot(t, st, "L1", fstest.MapFS{
		"a/server/ARTIFACT.md": artifactFile(execMCPServer("1.0.0", "", "server_identifier: npx:@acme/srv\n")),
		"b/server/ARTIFACT.md": artifactFile(execMCPServer("2.0.0", "a/server@1.x", "")),
	})
	if res.Accepted != 2 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want both accepted", res.Accepted, res.Rejected)
	}
	got := loadMerged(t, st, "b/server", "2.0.0")
	if !strings.Contains(string(got.Frontmatter), "npx:@acme/srv") {
		t.Errorf("merged frontmatter = %q, want the parent's server_identifier", got.Frontmatter)
	}
}

// The rule compares the child against everything its cross-layer parent's
// chain sets, so a field the parent inherits within its own layer still has to
// be restated.
// Spec: §4.6
func TestIngest_ExecutableInheritanceFieldFromGrandparent(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/base", execMCPServer("1.0.0", "", "server_identifier: npx:@acme/srv\n"))
	if res := ingestOne(t, st, "L1", "shared/mid", execMCPServer("1.0.0", "shared/base@1.x", "")); res.Accepted != 1 {
		t.Fatalf("same-layer intermediate: accepted=%d rejected=%+v, want it accepted", res.Accepted, res.Rejected)
	}

	res := ingestOne(t, st, "L2", "finance/server", execMCPServer("2.0.0", "shared/mid@1.x", ""))
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected = %+v, want one rejection", res.Rejected)
	}
	assertFieldsRejected(t, res.Rejected[0], "finance/server", "server_identifier")
}

// A same-ID overlay whose pin resolves to the lower layer's copy crosses
// layers, so it is refused for an undeclared key it leaves to that copy, and
// the read path keeps serving the lower copy.
// Spec: §4.6
func TestIngest_ExecutableInheritanceSameIDOverlayRejects(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "x", execAgent("1.0.0", "", "acme_hook: run.sh\n"))

	res := ingestOne(t, st, "L2", "x", execAgent("1.1.0", "x", ""))
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected = %+v, want one rejection", res.Rejected)
	}
	assertFieldsRejected(t, res.Rejected[0], "x", "acme_hook")
	assertNotStored(t, st, "x", "1.1.0")
	if got := loadMerged(t, st, "x", ""); got.Version != "1.0.0" {
		t.Errorf("served x@%s, want 1.0.0", got.Version)
	}
}

// An undeclared key set to an empty scalar is omitted under the §4.6
// omitted-field rule, so it inherits and is refused. The same key set to []
// is a value of its own and counts as restated.
// Spec: §4.6
func TestIngest_ExecutableInheritanceUndeclaredKeyEmptyVersusList(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", execAgent("1.0.0", "", "acme_hooks:\n  pre: x\n"))

	res := ingestSnapshot(t, st, "L2", fstest.MapFS{
		"finance/empty/ARTIFACT.md": artifactFile(execAgent("1.0.0", "shared/parent@1.x", "acme_hooks: \"\"\n")),
		"finance/list/ARTIFACT.md":  artifactFile(execAgent("1.0.0", "shared/parent@1.x", "acme_hooks: []\n")),
	})
	if res.Accepted != 1 || len(res.Rejected) != 1 {
		t.Fatalf("accepted=%d rejected=%+v, want finance/list accepted and finance/empty rejected", res.Accepted, res.Rejected)
	}
	assertFieldsRejected(t, res.Rejected[0], "finance/empty", "acme_hooks")
	if _, err := st.GetManifest(context.Background(), "tenant-1", "finance/list", "1.0.0"); err != nil {
		t.Errorf("finance/list not stored: %v", err)
	}
}

// A stored version is never re-judged: a child the rule would refuse, written
// to the store directly, is idempotent on an unchanged reingest, and changed
// bytes at the same version are an immutability conflict rather than an
// executable-field rejection.
// Spec: §4.6, §7.3.1
func TestIngest_ExecutableInheritanceStoredViolatorIsNotRejudged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	child := execAgent("2.0.0", "shared/parent@1.x", "")

	// A first store yields the record ingest computes for the child, so the
	// row written below carries the content hash a reingest compares against.
	scratch := newStore(t)
	ingestOne(t, scratch, "L1", "shared/parent", execAgent("1.0.0", "", ""))
	ingestOne(t, scratch, "L2", "finance/child", child)
	rec, err := scratch.GetManifest(ctx, "tenant-1", "finance/child", "2.0.0")
	if err != nil {
		t.Fatalf("GetManifest scratch child: %v", err)
	}

	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", execAgent("1.0.0", "", "x_owner: platform\n"))
	if err := st.PutManifest(ctx, rec); err != nil {
		t.Fatalf("PutManifest violating child: %v", err)
	}

	same := ingestOne(t, st, "L2", "finance/child", child)
	if same.Idempotent != 1 || len(same.Rejected) != 0 {
		t.Errorf("unchanged: idempotent=%d rejected=%+v, want idempotent with no rejection", same.Idempotent, same.Rejected)
	}
	changed := ingestOne(t, st, "L2", "finance/child", execAgent("2.0.0", "shared/parent@1.x", "tags: [edited]\n"))
	if len(changed.Conflicts) != 1 || len(changed.Rejected) != 0 {
		t.Errorf("changed: conflicts=%+v rejected=%+v, want one conflict and no rejection", changed.Conflicts, changed.Rejected)
	}
}

// A cross-layer parent whose stored chain loops rejects a new child with the
// chain-unresolved reason, and the cycle does not abort the ingest call.
// Spec: §4.6 hidden parents (withheld)
func TestIngest_ExecutableInheritanceStoredCycleRejects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore(t)
	for _, row := range []store.ManifestRecord{
		{ArtifactID: "shared/a", ExtendsPin: "shared/b@1.0.0"},
		{ArtifactID: "shared/b", ExtendsPin: "shared/a@1.0.0"},
	} {
		row.TenantID, row.Version, row.Type, row.Layer = "tenant-1", "1.0.0", "agent", "L1"
		row.ContentHash, row.Sensitivity = "sha256:"+strings.Repeat("0", 64), "low"
		if err := st.PutManifest(ctx, row); err != nil {
			t.Fatalf("PutManifest %s: %v", row.ArtifactID, err)
		}
	}

	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": artifactFile(execAgent("2.0.0", "shared/a@1.0.0", "")),
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].Reason != "extends: parent chain does not resolve" ||
		res.Rejected[0].Code != layer.InvalidArtifactCode || res.Rejected[0].Details != nil {
		t.Fatalf("rejected = %+v, want one chain-unresolved rejection without details", res.Rejected)
	}
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// An mcpServers entry restated by name alone does not restate the fields the
// parent's entry sets, so the field path names the entry and the field.
// Spec: §4.6
func TestIngest_ExecutableInheritanceNameOnlyMCPServerEntry(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", execAgent("1.0.0", "", "mcpServers:\n  - name: jira\n    command: run\n"))

	res := ingestOne(t, st, "L2", "finance/child", execAgent("2.0.0", "shared/parent@1.x", "mcpServers:\n  - name: jira\n"))
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected = %+v, want one rejection", res.Rejected)
	}
	assertFieldsRejected(t, res.Rejected[0], "finance/child", "mcpServers[jira].command")
}

// A field path built from a grandparent-authored name that is the
// grandparent's own ID would disclose it, so the child is refused with the
// unreadable-fields reason and no details, the cycle is not aborted, and
// neither the reason nor the serialized rejection names the grandparent (D-8).
// Both modes give the same constant reason (D-12).
// Spec: §4.6 hidden parents (withheld)
func TestIngest_ExecutableInheritanceDisclosingGrandparentNameWithheld(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	const a0 = "shared/base"
	ingestOne(t, st, "L1", a0, execAgent("1.0.0", "", a0+": run.sh\nmcpServers:\n  - name: "+a0+"\n    command: run\n"))
	if res := ingestOne(t, st, "L1", "shared/mid", execAgent("1.0.0", a0+"@1.x", "")); res.Accepted != 1 {
		t.Fatalf("same-layer intermediate: accepted=%d rejected=%+v, want it accepted", res.Accepted, res.Rejected)
	}

	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": artifactFile(execAgent("2.0.0", "shared/mid@1.x", "")),
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected = %+v, want one rejection", res.Rejected)
	}
	rj := res.Rejected[0]
	if rj.Reason != layer.UnreadableExecutableFieldsReason || rj.Code != layer.InvalidArtifactCode || rj.Details != nil {
		t.Errorf("rejection = %+v, want the unreadable-fields reason without details", rj)
	}
	wire, err := json.Marshal(rj)
	if err != nil {
		t.Fatalf("marshal rejection: %v", err)
	}
	if strings.Contains(rj.Reason, a0) || strings.Contains(string(wire), a0) {
		t.Errorf("rejection %s names %s", wire, a0)
	}
	assertNotStored(t, st, "finance/child", "2.0.0")
}

// A same-ID overlay whose unpinned reference selects an earlier version from
// its own layer resolves a within-layer edge, so the server accepts it and
// serves the earlier version's value (D-5). TEST-3 (b3) records that a
// filesystem sync of the same layout, which holds one record per ID per layer,
// drops the overlay instead.
// Spec: §4.6, §4.7.6
func TestIngest_ExecutableInheritanceSameLayerOverlayPinAccepted(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	// The clock orders the versions in ingest time, so `latest` selects the
	// most recent one rather than winning a timestamp tie on semver.
	clk := clock.NewFrozen(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ingestOneAt(t, st, clk, "L1", "x", execAgent("1.0.0", "", "acme_hook: run.sh\n"))
	clk.Advance(time.Hour)
	if res := ingestOneAt(t, st, clk, "L2", "x", execAgent("1.1.0", "x", "acme_hook: team.sh\n")); res.Accepted != 1 {
		t.Fatalf("restating overlay: accepted=%d rejected=%+v, want it accepted", res.Accepted, res.Rejected)
	}
	clk.Advance(time.Hour)

	res := ingestOneAt(t, st, clk, "L2", "x", execAgent("1.2.0", "x", ""))
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want the same-layer pin accepted", res.Accepted, res.Rejected)
	}
	rec, err := st.GetManifest(context.Background(), "tenant-1", "x", "1.2.0")
	if err != nil {
		t.Fatalf("GetManifest x@1.2.0: %v", err)
	}
	if rec.ExtendsPin != "x@1.1.0" {
		t.Errorf("ExtendsPin = %q, want x@1.1.0", rec.ExtendsPin)
	}
	if fm := string(loadMerged(t, st, "x", "1.2.0").Frontmatter); !strings.Contains(fm, "acme_hook: team.sh") {
		t.Errorf("merged frontmatter = %q, want acme_hook: team.sh", fm)
	}
}
