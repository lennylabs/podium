package sync

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
)

// These tests pin the §13.11.3 rule that a filesystem-source sync keeps the
// previous output of an artifact the unparseable-manifest rule drops, and
// removes the previous output of an artifact any other rule drops.

const (
	keptID    = "tools/a"
	keptOther = "tools/b"
	// keptLayers is a two-layer config: team-shared below personal.
	keptLayers = "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n"
	// brokenSkill is a SKILL.md whose description is a mapping, so it does
	// not parse.
	brokenSkill = "---\nname: a\ndescription:\n  text: A mapping.\n---\n\nhalf-saved body\n"
	// brokenArtifact is an ARTIFACT.md whose frontmatter is not valid YAML.
	brokenArtifact = "---\ntype: mcp-server\nname: [unclosed\n---\n\nbody\n"
)

// plainSkill returns a SKILL.md for name whose body carries marker.
func plainSkill(name, marker string) string {
	return "---\nname: " + name + "\ndescription: " + marker + " skill.\n---\n\n" + marker + " body\n"
}

// writeRegFile writes content to root/rel, creating its directories.
func writeRegFile(t *testing.T, root, rel, content string) {
	t.Helper()
	testharness.WriteTree(t, root, testharness.WriteTreeOption{Path: rel, Content: content})
}

// subtree returns the entries of tree under prefix.
func subtree(tree map[string]string, prefix string) map[string]string {
	out := map[string]string{}
	for p, c := range tree {
		if strings.HasPrefix(p, prefix) {
			out[p] = c
		}
	}
	return out
}

// lockEntries returns the target lock's entries for id.
func lockEntries(t *testing.T, target, id string) []LockArtifact {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: lock=%v err=%v", lock, err)
	}
	var out []LockArtifact
	for _, a := range lock.Artifacts {
		if a.ID == id {
			out = append(out, a)
		}
	}
	return out
}

// onlyDrop fails unless dropped holds exactly one drop, of id, and returns it.
func onlyDrop(t *testing.T, dropped []layer.Drop, id string) layer.Drop {
	t.Helper()
	if len(dropped) != 1 || dropped[0].ArtifactID != id {
		t.Fatalf("Dropped = %+v, want exactly one drop of %s", dropped, id)
	}
	return dropped[0]
}

// mustRun runs one sync and fails the test on an error.
func mustRun(t *testing.T, opts Options) *Result {
	t.Helper()
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// Spec: §13.11.3
// Spec: §11
// A target synced with skills tools/a and tools/b keeps tools/a's files and
// lock entries byte-identical when tools/a's SKILL.md stops parsing, while
// tools/b is updated. The drop is still reported, marked as kept, and a dry
// run reports the same mark and writes nothing. A repeated broken sync keeps
// the output again, and the sync after the fix rewrites tools/a.
func TestRun_UnparseableManifestKeepsPreviousOutput(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: keptLayers},
		testharness.WriteTreeOption{Path: "team-shared/" + keptID + "/SKILL.md", Content: plainSkill("a", "a-v1")},
		testharness.WriteTreeOption{Path: "team-shared/" + keptID + "/references/guide.md", Content: "guide v1\n"},
		testharness.WriteTreeOption{Path: "team-shared/" + keptOther + "/SKILL.md", Content: plainSkill("b", "b-v1")},
	)
	opts := Options{RegistryPath: reg, Target: target}
	mustRun(t, opts)
	beforeA := subtree(testharness.ReadTree(t, target), keptID+"/")
	if len(beforeA) < 2 {
		t.Fatalf("first sync wrote %v for %s, want its SKILL.md and resource", beforeA, keptID)
	}
	lockA := lockEntries(t, target, keptID)
	lockBytes, err := os.ReadFile(LockFilePath(target))
	if err != nil {
		t.Fatal(err)
	}

	writeRegFile(t, reg, "team-shared/"+keptID+"/SKILL.md", brokenSkill)
	writeRegFile(t, reg, "team-shared/"+keptOther+"/SKILL.md", plainSkill("b", "b-v2"))

	dry := mustRun(t, Options{RegistryPath: reg, Target: target, DryRun: true})
	if d := onlyDrop(t, dry.Dropped, keptID); !d.Unparseable() || !d.PreviousOutputKept {
		t.Errorf("dry-run drop = %+v, want an unparseable drop marked kept", d)
	}
	if after, _ := os.ReadFile(LockFilePath(target)); string(after) != string(lockBytes) {
		t.Errorf("dry run rewrote the lock")
	}

	for round := 1; round <= 2; round++ {
		res := mustRun(t, opts)
		if d := onlyDrop(t, res.Dropped, keptID); !d.Unparseable() || !d.PreviousOutputKept {
			t.Errorf("round %d: drop = %+v, want an unparseable drop marked kept", round, d)
		}
		if got := artifactIDs(res.Artifacts); !equalStrings(got, []string{keptOther}) {
			t.Errorf("round %d: materialized %v, want only %s", round, got, keptOther)
		}
		tree := testharness.ReadTree(t, target)
		if got := subtree(tree, keptID+"/"); !reflect.DeepEqual(got, beforeA) {
			t.Errorf("round %d: %s output = %v, want the previous output %v", round, keptID, got, beforeA)
		}
		if got := lockEntries(t, target, keptID); !reflect.DeepEqual(got, lockA) {
			t.Errorf("round %d: %s lock entries = %+v, want %+v", round, keptID, got, lockA)
		}
		if b := tree[keptOther+"/SKILL.md"]; !strings.Contains(b, "b-v2 body") {
			t.Errorf("round %d: %s/SKILL.md = %q, want the updated copy", round, keptOther, b)
		}
	}

	writeRegFile(t, reg, "team-shared/"+keptID+"/SKILL.md", plainSkill("a", "a-v2"))
	res := mustRun(t, opts)
	if len(res.Dropped) != 0 {
		t.Errorf("Dropped after the fix = %+v, want none", res.Dropped)
	}
	if a := testharness.ReadTree(t, target)[keptID+"/SKILL.md"]; !strings.Contains(a, "a-v2 body") {
		t.Errorf("%s/SKILL.md after the fix = %q, want the fixed copy", keptID, a)
	}
	if got := lockEntries(t, target, keptID); reflect.DeepEqual(got, lockA) {
		t.Errorf("%s lock entries after the fix are unchanged: %+v", keptID, got)
	}
}

// Spec: §13.11.3
// Spec: §11
// Under the remove setting, a sync after tools/a's SKILL.md stops parsing
// deletes tools/a's files and lock entries as any other drop does, and reports
// the drop without the kept mark, in a dry run as well; tools/b is updated.
// An override under remove also removes the previous output.
func TestRun_UnparseableManifestRemoveSetting(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (reg, target string) {
		t.Helper()
		reg, target = t.TempDir(), t.TempDir()
		testharness.WriteTree(t, reg,
			testharness.WriteTreeOption{Path: ".registry-config", Content: keptLayers},
			testharness.WriteTreeOption{Path: "team-shared/" + keptID + "/SKILL.md", Content: plainSkill("a", "a-v1")},
			testharness.WriteTreeOption{Path: "team-shared/" + keptID + "/references/guide.md", Content: "guide v1\n"},
			testharness.WriteTreeOption{Path: "team-shared/" + keptOther + "/SKILL.md", Content: plainSkill("b", "b-v1")},
			testharness.WriteTreeOption{Path: "team-shared/tools/c/SKILL.md", Content: plainSkill("c", "c-v1")},
		)
		mustRun(t, Options{RegistryPath: reg, Target: target, Scope: ScopeFilter{Exclude: []string{"tools/c"}}})
		if len(subtree(testharness.ReadTree(t, target), keptID+"/")) < 2 {
			t.Fatalf("first sync wrote no output for %s", keptID)
		}
		writeRegFile(t, reg, "team-shared/"+keptID+"/SKILL.md", brokenSkill)
		writeRegFile(t, reg, "team-shared/"+keptOther+"/SKILL.md", plainSkill("b", "b-v2"))
		return reg, target
	}
	assertRemoved := func(t *testing.T, target string, dropped []layer.Drop) {
		t.Helper()
		if d := onlyDrop(t, dropped, keptID); !d.Unparseable() || d.PreviousOutputKept {
			t.Errorf("drop = %+v, want an unparseable drop not marked kept", d)
		}
		if got := subtree(testharness.ReadTree(t, target), keptID+"/"); len(got) != 0 {
			t.Errorf("previous output of %s survived: %v", keptID, got)
		}
		for _, id := range lockIDs(t, target) {
			if id == keptID {
				t.Errorf("lock still records %s", keptID)
			}
		}
	}

	t.Run("sync", func(t *testing.T) {
		t.Parallel()
		reg, target := setup(t)
		opts := Options{RegistryPath: reg, Target: target, OnUnparseable: OnUnparseableRemove}
		dryOpts := opts
		dryOpts.DryRun = true
		dry := mustRun(t, dryOpts)
		if d := onlyDrop(t, dry.Dropped, keptID); d.PreviousOutputKept {
			t.Errorf("dry-run drop = %+v marked kept", d)
		}
		if len(subtree(testharness.ReadTree(t, target), keptID+"/")) == 0 {
			t.Fatalf("dry run removed the previous output")
		}
		res := mustRun(t, opts)
		assertRemoved(t, target, res.Dropped)
		if b := testharness.ReadTree(t, target)[keptOther+"/SKILL.md"]; !strings.Contains(b, "b-v2 body") {
			t.Errorf("%s/SKILL.md = %q, want the updated copy", keptOther, b)
		}
	})

	t.Run("override", func(t *testing.T) {
		t.Parallel()
		reg, target := setup(t)
		res, err := Override(OverrideOptions{Target: target, Add: []string{"tools/c"}, RegistryPath: reg, OnUnparseable: OnUnparseableRemove})
		if err != nil {
			t.Fatalf("Override: %v", err)
		}
		assertRemoved(t, target, res.Dropped)
		if _, ok := testharness.ReadTree(t, target)["tools/c/SKILL.md"]; !ok {
			t.Errorf("override did not add tools/c")
		}
	})
}

// Spec: §13.11.3
// An unparseable artifact the lock file does not record gets no files: on a
// fresh target, and on a target whose lock records only its siblings.
func TestRun_UnparseableManifestNeverSyncedGetsNoFiles(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: keptLayers},
		testharness.WriteTreeOption{Path: "team-shared/" + keptOther + "/SKILL.md", Content: plainSkill("b", "b-v1")},
	)
	synced := t.TempDir()
	mustRun(t, Options{RegistryPath: reg, Target: synced})
	writeRegFile(t, reg, "team-shared/"+keptID+"/SKILL.md", brokenSkill)

	for name, target := range map[string]string{"fresh target": t.TempDir(), "synced target": synced} {
		res := mustRun(t, Options{RegistryPath: reg, Target: target})
		if d := onlyDrop(t, res.Dropped, keptID); !d.Unparseable() || d.PreviousOutputKept {
			t.Errorf("%s: drop = %+v, want an unparseable drop not marked kept", name, d)
		}
		if got := subtree(testharness.ReadTree(t, target), keptID+"/"); len(got) != 0 {
			t.Errorf("%s: wrote %v for the never-synced %s", name, got, keptID)
		}
		if got := lockIDs(t, target); !equalStrings(got, []string{keptOther}) {
			t.Errorf("%s: lock IDs = %v, want only %s", name, got, keptOther)
		}
	}
}

// Spec: §13.11.3
// Spec: §4.6
// A drop under the no-inheritance rule, the removed-type rule, or the
// collision rule removes the artifact's previous output under both
// on_unparseable settings, as does an unparseable copy shadowing a
// lower-precedence copy that still parses, because the lower copy is then the
// artifact this run materializes.
func TestRun_LastingDropsRemovePreviousOutput(t *testing.T) {
	t.Parallel()
	agent := func(typ string) string {
		return "---\ntype: " + typ + "\nversion: 1.0.0\ndescription: a\n---\n\nagent body\n"
	}
	cases := []struct {
		name       string
		before     []testharness.WriteTreeOption
		after      []testharness.WriteTreeOption
		constraint string
		// lowerCopy reports that a lower-precedence copy of tools/a still
		// materializes, so its SKILL.md stays and only the dropped copy's
		// extra resource goes.
		lowerCopy bool
	}{
		{
			name:       "extends",
			before:     collisionSkill("personal/"+keptID, "a", "from-a", ""),
			after:      collisionSkill("personal/"+keptID, "a", "from-a", keptOther),
			constraint: layer.ExtendsRemovedConstraint,
		},
		{
			name:       "removed type",
			before:     []testharness.WriteTreeOption{{Path: "personal/" + keptID + "/ARTIFACT.md", Content: agent("agent")}},
			after:      []testharness.WriteTreeOption{{Path: "personal/" + keptID + "/ARTIFACT.md", Content: agent("command")}},
			constraint: layer.TypeRemovedConstraint,
		},
		{
			name: "collision",
			before: append(collisionSkill("personal/"+keptID, "a", "from-personal", ""),
				testharness.WriteTreeOption{Path: "personal/" + keptID + "/references/extra.md", Content: "extra\n"}),
			after:     collisionSkill("team-shared/"+keptID, "a", "from-base", ""),
			lowerCopy: true,
		},
		{
			name: "unparseable over a parsing lower copy",
			before: append(collisionSkill("personal/"+keptID, "a", "from-personal", ""),
				testharness.WriteTreeOption{Path: "personal/" + keptID + "/references/extra.md", Content: "extra\n"}),
			after: append(collisionSkill("team-shared/"+keptID, "a", "from-base", ""),
				testharness.WriteTreeOption{Path: "personal/" + keptID + "/SKILL.md", Content: brokenSkill}),
			constraint: layer.ManifestUnparseableConstraint,
			lowerCopy:  true,
		},
	}
	for _, policy := range []OnUnparseable{OnUnparseableKeep, OnUnparseableRemove} {
		for _, tc := range cases {
			t.Run(string(policy)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				reg := t.TempDir()
				target := t.TempDir()
				testharness.WriteTree(t, reg, append([]testharness.WriteTreeOption{
					{Path: ".registry-config", Content: keptLayers},
					{Path: "team-shared/" + keptOther + "/SKILL.md", Content: plainSkill("b", "b-v1")},
				}, tc.before...)...)
				mustRun(t, Options{RegistryPath: reg, Target: target, OnUnparseable: policy})
				if len(subtree(testharness.ReadTree(t, target), keptID+"/")) == 0 {
					t.Fatalf("first sync wrote nothing for %s", keptID)
				}

				testharness.WriteTree(t, reg, tc.after...)
				res := mustRun(t, Options{RegistryPath: reg, Target: target, OnUnparseable: policy})
				d := onlyDrop(t, res.Dropped, keptID)
				if d.PreviousOutputKept {
					t.Errorf("drop = %+v marked kept", d)
				}
				if tc.constraint != "" && d.Details["constraint"] != tc.constraint {
					t.Errorf("drop constraint = %v, want %s", d.Details["constraint"], tc.constraint)
				}
				tree := testharness.ReadTree(t, target)
				if !tc.lowerCopy {
					if got := subtree(tree, keptID+"/"); len(got) != 0 {
						t.Errorf("previous output survived: %v", got)
					}
					if got := lockIDs(t, target); !equalStrings(got, []string{keptOther}) {
						t.Errorf("lock IDs = %v, want only %s", got, keptOther)
					}
					return
				}
				if _, ok := tree[keptID+"/references/extra.md"]; ok {
					t.Errorf("the dropped copy's resource survived; tree keys:\n%s", sortedTreeKeys(tree))
				}
				if a := tree[keptID+"/SKILL.md"]; !strings.Contains(a, "from-base body") {
					t.Errorf("%s/SKILL.md = %q, want the lower copy", keptID, a)
				}
				if l := lockLayers(t, target)[keptID]; l != "team-shared" {
					t.Errorf("lock layer for %s = %q, want team-shared", keptID, l)
				}
			})
		}
	}
}

// Spec: §13.11.3
// Spec: §6.7
// A kept artifact's entry in a shared config-merge file survives: beside a
// sibling that still writes the file, and when it is the file's only
// contributor and the file is reconciled as a prior-only path.
func TestRun_UnparseableManifestKeepsConfigMergeEntry(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\nlayer_order:\n  - team\n"},
		testharness.WriteTreeOption{Path: "team/srv/alpha/ARTIFACT.md", Content: mcpServerSrc("alpha", "Alpha server.")},
		testharness.WriteTreeOption{Path: "team/srv/beta/ARTIFACT.md", Content: mcpServerSrc("beta", "Beta server.")},
		testharness.WriteTreeOption{Path: "team/" + keptOther + "/SKILL.md", Content: plainSkill("b", "b-v1")},
	)
	opts := Options{RegistryPath: reg, Target: target, AdapterID: "claude-code"}
	mustRun(t, opts)
	lockAlpha := lockEntries(t, target, "srv/alpha")
	mcp := filepath.Join(target, ".mcp.json")

	writeRegFile(t, reg, "team/srv/alpha/ARTIFACT.md", brokenArtifact)
	writeRegFile(t, reg, "team/srv/beta/ARTIFACT.md", strings.Replace(mcpServerSrc("beta", "Beta server."), "@acme/beta", "@acme/beta-two", 1))
	res := mustRun(t, opts)
	if d := onlyDrop(t, res.Dropped, "srv/alpha"); !d.PreviousOutputKept {
		t.Errorf("drop = %+v, want it marked kept", d)
	}
	got := readFileString(t, mcp)
	for _, want := range []string{"@acme/alpha", "@acme/beta-two"} {
		if !strings.Contains(got, want) {
			t.Errorf(".mcp.json missing %s beside a sibling:\n%s", want, got)
		}
	}
	if got := lockEntries(t, target, "srv/alpha"); !reflect.DeepEqual(got, lockAlpha) {
		t.Errorf("srv/alpha lock entries = %+v, want %+v", got, lockAlpha)
	}

	if err := os.RemoveAll(filepath.Join(reg, "team", "srv", "beta")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, opts)
	got = readFileString(t, mcp)
	if !strings.Contains(got, "@acme/alpha") || strings.Contains(got, "beta") {
		t.Errorf(".mcp.json as the kept artifact's only file = %s, want alpha alone", got)
	}
}

// readFileString reads path and fails the test on an error.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Spec: §13.11.3
// Spec: §7.5.5
// podium sync override re-materializes through Run, so an override over a
// broken SKILL.md keeps the previous output and reports the kept drop.
func TestOverride_UnparseableManifestKeepsPreviousOutput(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: keptLayers},
		testharness.WriteTreeOption{Path: "team-shared/" + keptID + "/SKILL.md", Content: plainSkill("a", "a-v1")},
		testharness.WriteTreeOption{Path: "team-shared/" + keptOther + "/SKILL.md", Content: plainSkill("b", "b-v1")},
		testharness.WriteTreeOption{Path: "team-shared/tools/c/SKILL.md", Content: plainSkill("c", "c-v1")},
	)
	mustRun(t, Options{RegistryPath: reg, Target: target, Scope: ScopeFilter{Exclude: []string{"tools/c"}}})
	beforeA := subtree(testharness.ReadTree(t, target), keptID+"/")
	writeRegFile(t, reg, "team-shared/"+keptID+"/SKILL.md", brokenSkill)

	res, err := Override(OverrideOptions{Target: target, Add: []string{"tools/c"}, RegistryPath: reg})
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if d := onlyDrop(t, res.Dropped, keptID); !d.PreviousOutputKept {
		t.Errorf("drop = %+v, want it marked kept", d)
	}
	tree := testharness.ReadTree(t, target)
	if got := subtree(tree, keptID+"/"); !reflect.DeepEqual(got, beforeA) {
		t.Errorf("%s output = %v, want %v", keptID, got, beforeA)
	}
	if _, ok := tree["tools/c/SKILL.md"]; !ok {
		t.Errorf("override did not add tools/c; tree keys:\n%s", sortedTreeKeys(tree))
	}
}
