package sync

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
)

// These tests pin the filesystem-source outcome of a cross-layer collision on
// each pkg/sync entry point: Run, Render, and Override. Two registry-side
// layers contribute shared/note, and team-shared also contributes
// shared/other. The composition drops the higher (personal) copy, keeps the
// team-shared copy, and materializes the rest.

const (
	collisionID      = "shared/note"
	collisionOtherID = "shared/other"
)

// collisionSkill returns the ARTIFACT.md and SKILL.md sources of a skill whose
// description and body carry marker, so a test can tell which layer's copy was
// materialized. A non-empty extends adds the extends: field to ARTIFACT.md.
func collisionSkill(dir, name, marker, extends string) []testharness.WriteTreeOption {
	artifact := "---\ntype: skill\nversion: 1.0.0\n"
	if extends != "" {
		artifact += "extends: " + extends + "\n"
	}
	artifact += "description: " + marker + "\n---\n"
	skill := "---\nname: " + name + "\ndescription: " + marker + " skill.\n---\n\n" + marker + " body\n"
	return []testharness.WriteTreeOption{
		{Path: dir + "/ARTIFACT.md", Content: artifact},
		{Path: dir + "/SKILL.md", Content: skill},
	}
}

// collisionRegistry returns a fresh two-layer colliding registry.
func collisionRegistry(t *testing.T) string {
	t.Helper()
	reg := t.TempDir()
	opts := []testharness.WriteTreeOption{{
		Path:    ".registry-config",
		Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
	}}
	opts = append(opts, collisionSkill("team-shared/shared/note", "note", "from-base", "")...)
	opts = append(opts, collisionSkill("team-shared/shared/other", "other", "from-other", "")...)
	opts = append(opts, collisionSkill("personal/shared/note", "note", "from-personal", "")...)
	testharness.WriteTree(t, reg, opts...)
	return reg
}

// assertOneCollision fails unless dropped holds exactly one ingest.collision
// drop of shared/note whose reason names the rename-or-remove remedy, carries
// no extends: wording, and omits the kept layer. A drop carries no layer, so
// each caller pins which copy was dropped through the copy it materializes.
func assertOneCollision(t *testing.T, dropped []layer.Drop) {
	t.Helper()
	if len(dropped) != 1 {
		t.Fatalf("Dropped = %+v, want exactly one collision", dropped)
	}
	got := dropped[0]
	want := layer.Collision{ArtifactID: collisionID, Layer: "personal", ExistingLayer: "team-shared"}.Drop()
	if got != want {
		t.Errorf("Dropped[0] = %+v, want %+v", got, want)
	}
	if !strings.Contains(got.Reason, "rename") || strings.Contains(got.Reason, "extends") {
		t.Errorf("Reason = %q, want the rename-or-remove remedy with no extends: wording", got.Reason)
	}
	if strings.Contains(got.Reason, "team-shared") {
		t.Errorf("Reason = %q names the kept layer", got.Reason)
	}
}

// lockIDs returns the sorted artifact IDs the target's sync.lock lists.
func lockIDs(t *testing.T, target string) []string {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock == nil {
		t.Fatalf("sync.lock missing under %s", target)
	}
	seen := map[string]bool{}
	var ids []string
	for _, a := range lock.Artifacts {
		if !seen[a.ID] {
			seen[a.ID] = true
			ids = append(ids, a.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// lockLayers maps each artifact ID in the target's sync.lock to its layer.
func lockLayers(t *testing.T, target string) map[string]string {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: lock=%v err=%v", lock, err)
	}
	out := map[string]string{}
	for _, a := range lock.Artifacts {
		out[a.ID] = a.Layer
	}
	return out
}

// Spec: §4.6
// Spec: §13.11.3
// Matrix: §6.10 (ingest.collision)
// A filesystem-source sync drops the higher-precedence copy of a collision, keeps the lower copy, materializes every other artifact, and
// writes a lock that omits the dropped copy.
func TestRun_FilesystemCollisionDropsAndContinues(t *testing.T) {
	t.Parallel()
	reg := collisionRegistry(t)
	target := t.TempDir()

	res, err := Run(Options{RegistryPath: reg, Target: target})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertOneCollision(t, res.Dropped)

	tree := testharness.ReadTree(t, target)
	note := tree[collisionID+"/SKILL.md"]
	if !strings.Contains(note, "from-base body") || strings.Contains(note, "from-personal") {
		t.Errorf("%s/SKILL.md = %q, want the team-shared copy", collisionID, note)
	}
	if _, ok := tree[collisionOtherID+"/SKILL.md"]; !ok {
		t.Errorf("%s not materialized; tree keys:\n%s", collisionOtherID, sortedTreeKeys(tree))
	}

	if got, want := lockIDs(t, target), []string{collisionID, collisionOtherID}; !equalStrings(got, want) {
		t.Errorf("lock IDs = %v, want %v", got, want)
	}
	if layer := lockLayers(t, target)[collisionID]; layer != "team-shared" {
		t.Errorf("lock layer for %s = %q, want team-shared (the dropped copy is personal)", collisionID, layer)
	}
}

// Spec: §4.6
// Spec: §13.11.3
// Matrix: §6.10 (ingest.collision)
// A dry run reports the same drop and writes neither the target nor the lock.
func TestRun_FilesystemCollisionDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	reg := collisionRegistry(t)
	target := filepath.Join(t.TempDir(), "target")

	res, err := Run(Options{RegistryPath: reg, Target: target, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertOneCollision(t, res.Dropped)
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dry run created the target directory (stat err = %v)", err)
	}
	if _, err := os.Stat(LockFilePath(target)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dry run created the lock (stat err = %v)", err)
	}
}

// Spec: §4.6
// Spec: §13.11.3
// A filesystem-source sync refuses an ARTIFACT.md that declares extends:
// under the §4.6 no-inheritance rule. Result.Dropped holds the
// extends_removed drop, the target and the lock omit the artifact, and every
// other artifact materializes. Run sets neither standard error nor an exit
// code, so the CLI end-to-end test pins those.
func TestRun_FilesystemExtendsDeclarationDropped(t *testing.T) {
	t.Parallel()
	const legacyID = "shared/legacy"
	reg := t.TempDir()
	opts := []testharness.WriteTreeOption{{
		Path:    ".registry-config",
		Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
	}}
	opts = append(opts, collisionSkill("team-shared/"+collisionOtherID, "other", "from-other", "")...)
	opts = append(opts, collisionSkill("personal/"+legacyID, "legacy", "from-legacy", collisionOtherID)...)
	testharness.WriteTree(t, reg, opts...)
	target := t.TempDir()

	res, err := Run(Options{RegistryPath: reg, Target: target})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []layer.Drop{layer.ExtendsRemovedDrop(legacyID)}; !reflect.DeepEqual(res.Dropped, want) {
		t.Errorf("Dropped = %+v, want %+v", res.Dropped, want)
	}
	if got, want := lockIDs(t, target), []string{collisionOtherID}; !equalStrings(got, want) {
		t.Errorf("lock IDs = %v, want %v", got, want)
	}
	tree := testharness.ReadTree(t, target)
	if _, ok := tree[collisionOtherID+"/SKILL.md"]; !ok {
		t.Errorf("%s not materialized; tree keys:\n%s", collisionOtherID, sortedTreeKeys(tree))
	}
	for path := range tree {
		if strings.HasPrefix(path, legacyID+"/") {
			t.Errorf("refused artifact materialized at %s", path)
		}
	}
}

// Spec: §4.6
// Spec: §13.11.3
// Matrix: §6.10 (ingest.collision)
// A marketplace render over a filesystem source composes through FetchRecords
// and reports the same drop on RenderResult.Dropped, rendering the kept copy.
func TestRender_FilesystemCollisionReportsDropped(t *testing.T) {
	t.Parallel()
	reg := collisionRegistry(t)
	workdir := t.TempDir()

	res, err := Render(context.Background(), RenderOptions{
		OutputID:  "acme-agents",
		Registry:  reg,
		Workdir:   workdir,
		Harnesses: []string{"codex"},
		Plugins:   []PluginFilter{{Name: "shared-pack", Include: []string{"shared/**"}}},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertOneCollision(t, res.Dropped)

	tree := testharness.ReadTree(t, workdir)
	note := tree["codex/shared-pack/skills/note/SKILL.md"]
	if !strings.Contains(note, "from-base body") {
		t.Errorf("rendered note = %q, want the team-shared copy; tree keys:\n%s", note, sortedTreeKeys(tree))
	}
	if _, ok := tree["codex/shared-pack/skills/other/SKILL.md"]; !ok {
		t.Errorf("other not rendered; tree keys:\n%s", sortedTreeKeys(tree))
	}
}

// Spec: §4.6
// Spec: §13.11.3
// Matrix: §6.10 (ingest.collision)
// An override that re-materializes reports the drops of that composition, and
// the drop does not roll back the toggle. The first sync narrows the scope to
// the colliding ID, so adding shared/other is a real toggle rather than the
// §7.5.5 redundant-add no-op. A dry-run override composes nothing and reports
// no drop.
func TestOverride_FilesystemCollisionReportsDropped(t *testing.T) {
	t.Parallel()
	reg := collisionRegistry(t)
	target := t.TempDir()

	first, err := Run(Options{
		RegistryPath: reg,
		Target:       target,
		Scope:        ScopeFilter{Include: []string{collisionID}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertOneCollision(t, first.Dropped)
	if got := lockIDs(t, target); !equalStrings(got, []string{collisionID}) {
		t.Fatalf("first sync lock IDs = %v, want only %s", got, collisionID)
	}

	dry, err := Override(OverrideOptions{
		Target:       target,
		Add:          []string{collisionOtherID},
		RegistryPath: reg,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("Override (dry run): %v", err)
	}
	if dry.Dropped != nil {
		t.Errorf("dry-run Dropped = %+v, want nil", dry.Dropped)
	}

	res, err := Override(OverrideOptions{
		Target:       target,
		Add:          []string{collisionOtherID},
		RegistryPath: reg,
	})
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	assertOneCollision(t, res.Dropped)
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none (the add is a real toggle)", res.Warnings)
	}

	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: lock=%v err=%v", lock, err)
	}
	if len(lock.Toggles.Add) != 1 || lock.Toggles.Add[0].ID != collisionOtherID {
		t.Errorf("toggles.add = %+v, want [%s]", lock.Toggles.Add, collisionOtherID)
	}
	tree := testharness.ReadTree(t, target)
	if _, ok := tree[collisionOtherID+"/SKILL.md"]; !ok {
		t.Errorf("%s not materialized after override; tree keys:\n%s", collisionOtherID, sortedTreeKeys(tree))
	}
	if note := tree[collisionID+"/SKILL.md"]; !strings.Contains(note, "from-base body") {
		t.Errorf("%s/SKILL.md = %q, want the team-shared copy", collisionID, note)
	}
}
