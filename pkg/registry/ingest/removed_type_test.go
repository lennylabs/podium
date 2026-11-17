package ingest_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// erAssertTypeRemoved fails unless res rejects id under the §4.1 removed-type
// rule for typ, with exactly the type_removed constraint in details.
func erAssertTypeRemoved(t *testing.T, res *ingest.Result, id, typ string) {
	t.Helper()
	rj := erRejection(t, res, id)
	want := map[string]any{"constraint": layer.TypeRemovedConstraint}
	reason := layer.TypeRemovedReason(typ)
	if rj.Code != layer.InvalidArtifactCode || rj.Reason != reason || !reflect.DeepEqual(rj.Details, want) {
		t.Errorf("rejection of %s = %+v, want %s, reason %q, details %v",
			id, rj, layer.InvalidArtifactCode, reason, want)
	}
}

// Spec: §4.1, §7.3.1 — an ARTIFACT.md of a removed type is refused with
// ingest.invalid_artifact and exactly the type_removed constraint, is not
// stored, and leaves a valid sibling accepted. An ARTIFACT.md that also
// declares extends: reports extends_removed, because the §4.6 check runs
// first.
func TestIngest_RemovedTypeRejected(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res := erIngest(t, st, "team", map[string]string{
		"ops/cmd/ARTIFACT.md":     erDoc("command", "1.0.0", "", "Command body."),
		"ops/ref/ARTIFACT.md":     erDoc("context", "1.0.0", "", "Reference body."),
		"ops/both/ARTIFACT.md":    erDoc("context", "1.0.0", "extends: ops/base\n", "Both body."),
		"ops/sibling/ARTIFACT.md": erDoc("agent", "1.0.0", "", "Sibling body."),
	})
	if res.Accepted != 1 || len(res.Rejected) != 3 {
		t.Fatalf("result = %+v, want the sibling accepted and three refusals", res)
	}
	erAssertTypeRemoved(t, res, "ops/cmd", "command")
	erAssertTypeRemoved(t, res, "ops/ref", "context")
	erAssertExtendsRemoved(t, res, "ops/both")
	for _, id := range []string{"ops/cmd", "ops/ref", "ops/both"} {
		erAssertNotIngested(t, res, id)
		erAssertNotStored(t, st, id, "1.0.0")
	}
}

// Spec: §4.1, §7.3.1, §4.7.6 — a stored row of a removed type never supplies
// the reused registry-assigned version. A skill stored at 0.0.0-auto.0 beside
// a newer command row of the same ID in the same layer stays unchanged at
// auto.0 when its bytes are ingested again, and the command row is served on
// no read path.
func TestIngest_AutoVersionRevertSkipsRemovedTypeRow(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	files := map[string]string{"ops/s/SKILL.md": erSkill("s", "")}
	first := erIngest(t, st, "team", files)
	if first.Accepted != 1 || len(first.Ingested) != 1 || first.Ingested[0].Version != "0.0.0-auto.0" {
		t.Fatalf("first ingest = %+v, want ops/s@0.0.0-auto.0", first)
	}
	row := erRow("ops/s", "0.0.0-auto.1", "team", "---\ntype: command\nversion: 0.0.0-auto.1\n---\n", 0)
	row.Type = "command"
	row.IngestedAt = time.Now().Add(time.Hour)
	erSeed(t, st, row)

	res := erIngest(t, st, "team", files)
	if res.Idempotent != 1 || res.Accepted != 0 || len(res.Rejected) != 0 {
		t.Fatalf("revert = %+v, want one idempotent pair", res)
	}
	if got := res.Ingested[0]; got.ArtifactID != "ops/s" || got.Version != "0.0.0-auto.0" || got.Accepted {
		t.Errorf("Ingested = %+v, want unchanged ops/s@0.0.0-auto.0", got)
	}
	erAssertServes(t, erRegistry(st, "team"), "ops/s", "", "0.0.0-auto.0")
}

// Spec: §4.1, §4.6 — a stored row whose type §4.1 removed is retired on the
// core read path, so an ID whose every row has a removed type answers
// registry.not_found.
func TestRetiredRow_RemovedTypeUnserved(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"command", "context"} {
		st := newStore(t)
		row := erRow("ops/old", "1.0.0", "team", erDoc(typ, "1.0.0", "", "Old body."), 1)
		row.Type = typ
		erSeed(t, st, row)
		erAssertUnserved(t, erRegistry(st, "team"), "ops/old")
	}
}
