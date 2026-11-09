package ingest_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// execAgent builds a type:agent ARTIFACT.md at version with an optional
// extends: reference and extra frontmatter lines. The server cases use an
// undeclared key or an mcpServers entry, because the ingest linter refuses a
// hook child that omits hook_action before the §4.6 executable-field rule
// runs.
func execAgent(version, extends, extra string) string {
	src := "---\ntype: agent\nversion: " + version + "\ndescription: agent\nsensitivity: low\n"
	if extends != "" {
		src += "extends: " + extends + "\n"
	}
	return src + extra + "---\n\nagent body\n"
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
