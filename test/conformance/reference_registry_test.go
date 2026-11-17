// Package conformance holds the cross-cutting suites that any built-in
// or community implementation runs against. Phase 19 ships the example
// artifact registry test that verifies the testdata/registries/reference
// fixture loads end-to-end through the production library code.
package conformance

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sync"
)

// referencePath returns the absolute path to testdata/registries/reference
// derived from this test file's location, so the test runs from any cwd.
func referencePath(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "testdata", "registries", "reference")
}

// Spec: §11 Verification — the example artifact registry covers every
// first-class type, every visibility mode, and every adapter target;
// it loads end-to-end with no errors.
func TestReferenceRegistry_OpensAndWalks(t *testing.T) {
	t.Parallel()
	reg, err := filesystem.Open(referencePath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reg.Mode != filesystem.ModeMultiLayer {
		t.Errorf("Mode = %s, want multi-layer", reg.Mode)
	}
	wantLayers := []string{"org-defaults", "_shared", "team-finance", "personal"}
	if len(reg.Layers) != len(wantLayers) {
		t.Fatalf("got %d layers, want %d", len(reg.Layers), len(wantLayers))
	}
	for i, want := range wantLayers {
		if reg.Layers[i].ID != want {
			t.Errorf("Layers[%d] = %q, want %q", i, reg.Layers[i].ID, want)
		}
	}

	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(records) == 0 {
		t.Fatalf("walked zero artifacts; reference registry should have content")
	}
}

// Spec: §11 — `podium sync` against the reference registry produces
// a valid materialized tree.
func TestReferenceRegistry_SyncsThroughNoneAdapter(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	res, err := sync.Run(sync.Options{
		RegistryPath: referencePath(t),
		Target:       target,
		AdapterID:    "none",
	})
	if err != nil {
		t.Fatalf("sync.Run: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Errorf("no artifacts materialized")
	}
}

// Spec: §11 — claude-code adapter handles the reference registry's
// type-skill mix, including reference and user-invoked skills, without errors.
func TestReferenceRegistry_SyncsThroughClaudeCode(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	res, err := sync.Run(sync.Options{
		RegistryPath: referencePath(t),
		Target:       target,
		AdapterID:    "claude-code",
	})
	if err != nil {
		t.Fatalf("sync.Run: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Errorf("no artifacts materialized")
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got[".claude/skills/welcome/SKILL.md"]; !ok {
		t.Errorf("expected .claude/skills/welcome/SKILL.md in target, got: %v", keys(got))
	}
}

// convertedFixture is one reference package FIX-1 converted by hand from a
// removed type to a skill: its layer, its layer-relative ID, the version
// the conversion published, and whether it is user-invoked.
type convertedFixture struct {
	layer, id, version string
	userInvoked        bool
}

// convertedFixtures are the reference packages that were type: context or
// type: command before 0.7.0. The command is the user-invoked skill, and each
// conversion published the next minor version.
var convertedFixtures = []convertedFixture{
	{"personal", "notes/journal", "0.2.0", false},
	{"personal", "notes/glossary-notes", "0.3.0", false},
	{"team-finance", "commands/quick-variance", "1.1.0", true},
	{"org-defaults", "company-glossary", "1.1.0", false},
}

// Spec: §4.1, §4.3.4, §11 — the reference registry holds no record of a
// removed type, and each package converted from one is a plain skill with no
// ARTIFACT.md that resolves to its published version and invocation. The
// ingest linter reports no error and no lint.skill_artifact_field warning for
// them.
func TestReferenceRegistry_ConvertedFixtures(t *testing.T) {
	t.Parallel()
	reg, err := filesystem.Open(referencePath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	byKey := map[string]filesystem.ArtifactRecord{}
	for _, rec := range records {
		if manifest.IsRemovedType(rec.Type()) {
			t.Errorf("%s/%s has the removed type %q", rec.Layer.ID, rec.ID, rec.Type())
		}
		byKey[rec.Layer.ID+"/"+rec.ID] = rec
	}
	converted := map[string]bool{}
	for _, f := range convertedFixtures {
		rec, ok := byKey[f.layer+"/"+f.id]
		if !ok {
			t.Errorf("%s/%s: no record", f.layer, f.id)
			continue
		}
		converted[rec.ID] = true
		assertConvertedFixture(t, rec, f)
	}
	for _, d := range lint.NewIngestLinter(true).Lint(context.Background(), reg, records) {
		if !converted[d.ArtifactID] {
			continue
		}
		if d.Severity == lint.SeverityError || d.Code == "lint.skill_artifact_field" {
			t.Errorf("%s: %s %s: %s", d.ArtifactID, d.Severity, d.Code, d.Message)
		}
	}
}

// assertConvertedFixture checks one converted package: a skill whose
// directory holds no ARTIFACT.md, at the published version, with the
// published invocation.
func assertConvertedFixture(t *testing.T, rec filesystem.ArtifactRecord, f convertedFixture) {
	t.Helper()
	if rec.Type() != manifest.TypeSkill {
		t.Errorf("%s: type = %q, want skill", f.id, rec.Type())
	}
	dir := filepath.Join(rec.Layer.Path, filepath.FromSlash(f.id))
	if _, err := os.Stat(filepath.Join(dir, "ARTIFACT.md")); !os.IsNotExist(err) {
		t.Errorf("%s: ARTIFACT.md stat err = %v, want the package to hold none", f.id, err)
	}
	if rec.AuthoredBytes != nil {
		t.Errorf("%s: AuthoredBytes = %q, want none for a plain skill", f.id, rec.AuthoredBytes)
	}
	if rec.Artifact.Version != f.version || rec.VersionAssigned {
		t.Errorf("%s: version = %q (assigned %v), want %s", f.id, rec.Artifact.Version, rec.VersionAssigned, f.version)
	}
	if got := rec.Artifact.UserInvoked(); got != f.userInvoked {
		t.Errorf("%s: UserInvoked() = %v, want %v", f.id, got, f.userInvoked)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
