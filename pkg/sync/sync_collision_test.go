package sync

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
)

// These tests pin the filesystem-source outcome of an unsanctioned
// cross-layer collision on each pkg/sync entry point: Run, Render, and
// Override. Two registry-side layers contribute shared/note, the higher layer
// (personal) declares no extends:, and team-shared also contributes
// shared/other. The composition drops the personal copy, keeps the
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

// writeCollisionRegistry writes the two-layer colliding registry into reg.
// A non-empty extends is declared on the higher-precedence (personal) copy.
func writeCollisionRegistry(t *testing.T, reg, extends string) {
	t.Helper()
	opts := []testharness.WriteTreeOption{{
		Path:    ".registry-config",
		Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
	}}
	opts = append(opts, collisionSkill("team-shared/shared/note", "note", "from-base", "")...)
	opts = append(opts, collisionSkill("team-shared/shared/other", "other", "from-other", "")...)
	opts = append(opts, collisionSkill("personal/shared/note", "note", "from-personal", extends)...)
	testharness.WriteTree(t, reg, opts...)
}

// collisionRegistry returns a fresh colliding registry with no extends:.
func collisionRegistry(t *testing.T) string {
	t.Helper()
	reg := t.TempDir()
	writeCollisionRegistry(t, reg, "")
	return reg
}

// assertOneCollision fails unless dropped holds exactly the personal copy of
// shared/note, kept from team-shared, and its reason names the extends:
// remedy without naming the kept layer.
func assertOneCollision(t *testing.T, dropped []layer.Collision) {
	t.Helper()
	if len(dropped) != 1 {
		t.Fatalf("Dropped = %+v, want exactly one collision", dropped)
	}
	got := dropped[0]
	want := layer.Collision{ArtifactID: collisionID, Layer: "personal", ExistingLayer: "team-shared"}
	if got != want {
		t.Errorf("Dropped[0] = %+v, want %+v", got, want)
	}
	reason := got.Reason()
	if !strings.Contains(reason, "declare extends: "+collisionID) {
		t.Errorf("Reason() = %q, want the extends: remedy", reason)
	}
	if strings.Contains(reason, "team-shared") {
		t.Errorf("Reason() = %q names the kept layer", reason)
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
// A filesystem-source sync drops the higher-precedence copy of an unsanctioned
// collision, keeps the lower copy, materializes every other artifact, and
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
// Matrix: §6.10 (ingest.collision)
// Adding extends: to the higher copy sanctions the collision: the next sync
// drops nothing, materializes the merge, and stale-file cleanup leaves no file
// the first run wrote for the kept copy that the merge no longer produces.
func TestRun_FilesystemCollisionResolvedByExtends(t *testing.T) {
	t.Parallel()
	reg := collisionRegistry(t)
	target := t.TempDir()

	first, err := Run(Options{RegistryPath: reg, Target: target})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	assertOneCollision(t, first.Dropped)

	writeCollisionRegistry(t, reg, collisionID)
	second, err := Run(Options{RegistryPath: reg, Target: target})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(second.Dropped) != 0 {
		t.Fatalf("second Run Dropped = %+v, want none", second.Dropped)
	}

	tree := testharness.ReadTree(t, target)
	if got := tree[collisionID+"/ARTIFACT.md"]; !strings.Contains(got, "description: from-personal") {
		t.Errorf("%s/ARTIFACT.md = %q, want the merged personal description", collisionID, got)
	}
	if got := tree[collisionID+"/SKILL.md"]; !strings.Contains(got, "from-personal body") {
		t.Errorf("%s/SKILL.md = %q, want the personal body", collisionID, got)
	}
	if layer := lockLayers(t, target)[collisionID]; layer != "personal" {
		t.Errorf("lock layer for %s = %q, want personal", collisionID, layer)
	}

	// Every file on disk is either a lock-tracked path or the lock itself, so
	// nothing from the first run survived outside the current materialization.
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: lock=%v err=%v", lock, err)
	}
	tracked := map[string]bool{}
	for _, a := range lock.Artifacts {
		tracked[filepath.ToSlash(a.MaterializedPath)] = true
	}
	for path := range tree {
		if strings.HasPrefix(path, ".podium/") {
			continue
		}
		if !tracked[path] {
			t.Errorf("leftover file %s is not in the lock", path)
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
