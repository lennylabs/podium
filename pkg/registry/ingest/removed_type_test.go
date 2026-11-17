package ingest_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/typeprovider"
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
// first. The refused artifact is withheld from the catalog lint resolves
// prose references against, so a sibling skill whose body links to it fails
// lint.prose_reference while its link to the kept sibling resolves.
func TestIngest_RemovedTypeRejected(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res := erIngest(t, st, "team", map[string]string{
		"ops/cmd/ARTIFACT.md":     erDoc("command", "1.0.0", "", "Command body."),
		"ops/ref/ARTIFACT.md":     erDoc("context", "1.0.0", "", "Reference body."),
		"ops/both/ARTIFACT.md":    erDoc("context", "1.0.0", "extends: ops/base\n", "Both body."),
		"ops/sibling/ARTIFACT.md": erDoc("agent", "1.0.0", "", "Sibling body."),
		"ops/linker/SKILL.md": "---\nname: linker\ndescription: Fixture skill linker.\n---\n\n" +
			"See [the command](ops/cmd) and [the sibling](ops/sibling).\n",
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
	if len(res.LintFailures) != 1 {
		t.Fatalf("LintFailures = %+v, want the one prose reference", res.LintFailures)
	}
	d := res.LintFailures[0]
	if d.ArtifactID != "ops/linker" || d.Code != "lint.prose_reference" ||
		!strings.Contains(d.Message, "ops/cmd") || strings.Contains(d.Message, "ops/sibling") {
		t.Errorf("diagnostic = %+v, want lint.prose_reference on ops/linker naming ops/cmd alone", d)
	}
}

// commandProvider is a deployment TypeProvider that registers the removed
// type command. It validates nothing, so only the §4.1 refusal can stop a
// command artifact.
type commandProvider struct{}

func (commandProvider) ID() string                  { return "test:command" }
func (commandProvider) Type() manifest.ArtifactType { return "command" }
func (commandProvider) Validate(context.Context, *manifest.Artifact) []typeprovider.Diagnostic {
	return nil
}

// Spec: §4.1, §7.3.1, §9 — the removed-type refusal does not consult the
// TypeProvider registry, so a deployment that registers a provider for
// command still has every command artifact refused with type_removed.
// Registering the provider in typeprovider.Default is the deployment's only
// registration path. The registration persists for the package's test binary,
// which no other case observes, because every command artifact is refused
// before lint reads the registry.
func TestIngest_RemovedTypeUnderAnyTypeProvider(t *testing.T) {
	t.Parallel()
	if _, ok := typeprovider.Default.Get("command"); !ok {
		if err := typeprovider.Default.Register(commandProvider{}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	if err := typeprovider.Default.Require("command"); err != nil {
		t.Fatalf("Require(command) = %v, want a registered provider", err)
	}
	st := newStore(t)
	res := erIngest(t, st, "team", map[string]string{
		"ops/cmd/ARTIFACT.md": erDoc("command", "1.0.0", "", "Command body."),
	})
	if res.Accepted != 0 || len(res.Rejected) != 1 {
		t.Fatalf("result = %+v, want the command refused", res)
	}
	erAssertTypeRemoved(t, res, "ops/cmd", "command")
	erAssertNotStored(t, st, "ops/cmd", "1.0.0")
}

// Spec: §4.1, §4.3.4, §4.7.6 — a package converted by hand from a stored
// command row to a user-invoked skill at the next minor version is accepted
// in the same layer and served as latest, while the command row stays
// retired on an exact-version load.
func TestIngest_ConvertedSkillAtNextMinorServes(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	erSeed(t, st, erRetiredRow("command", "ops/quick-variance", "1.0.0", "team", 1))
	res := erIngest(t, st, "team", map[string]string{
		"ops/quick-variance/SKILL.md": erSkill("quick-variance",
			"metadata:\n  version: \"1.1.0\"\n  invocation: \"user\"\n"),
	})
	if res.Accepted != 1 || len(res.Rejected) != 0 || len(res.Conflicts) != 0 || len(res.LintFailures) != 0 {
		t.Fatalf("result = %+v, want the converted skill accepted", res)
	}
	reg := erRegistry(st, "team")
	erAssertServes(t, reg, "ops/quick-variance", "", "1.1.0")
	loaded, err := erLoad(reg, "ops/quick-variance", "")
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	art, err := manifest.ResolveArtifact(loaded.Frontmatter, loaded.SkillRaw)
	if err != nil || !art.UserInvoked() || loaded.Type != string(manifest.TypeSkill) {
		t.Errorf("served artifact = %+v (%v), type %s; want a user-invoked skill", art, err, loaded.Type)
	}
	if _, err := erLoad(reg, "ops/quick-variance", "1.0.0"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("LoadArtifact(ops/quick-variance@1.0.0) err = %v, want registry.not_found for the retired command row", err)
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
