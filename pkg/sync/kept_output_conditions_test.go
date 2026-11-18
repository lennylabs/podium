package sync

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/materialize"
)

// These tests pin the §13.11.3 conditions under which a sync keeps the
// previous output of an artifact the unparseable-manifest rule drops: the
// earlier run used the same harness adapter, the run's scope and toggles
// select the artifact (the type filter judged by the type its lock entry
// records), and no artifact the run materializes writes a path the kept
// output holds. When a condition fails, the previous output is removed as
// under the remove setting.

// keepLayer is a one-layer registry config.
const keepLayer = "multi_layer: true\nlayer_order:\n  - team\n"

// keepFixture writes a registry with skills tools/a (a SKILL.md and a
// resource) and tools/b, syncs it into a fresh target with opts, breaks
// tools/a's SKILL.md, and returns the registry, the target, and tools/a's
// output from the first sync, keyed by target path.
func keepFixture(t *testing.T, opts Options) (reg, target string, before map[string]string) {
	t.Helper()
	reg, target = t.TempDir(), t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: keepLayer},
		testharness.WriteTreeOption{Path: "team/" + keptID + "/SKILL.md", Content: plainSkill("a", "a-v1")},
		testharness.WriteTreeOption{Path: "team/" + keptID + "/references/guide.md", Content: "guide v1\n"},
		testharness.WriteTreeOption{Path: "team/" + keptOther + "/SKILL.md", Content: plainSkill("b", "b-v1")},
	)
	opts.RegistryPath, opts.Target = reg, target
	mustRun(t, opts)
	before = keptOutputFiles(t, target)
	if len(before) < 2 {
		t.Fatalf("first sync wrote %v for %s, want its SKILL.md and resource", before, keptID)
	}
	writeRegFile(t, reg, "team/"+keptID+"/SKILL.md", brokenSkill)
	return reg, target, before
}

// keptOutputFiles returns the target files the lock attributes to tools/a,
// keyed by target path, with their content.
func keptOutputFiles(t *testing.T, target string) map[string]string {
	t.Helper()
	tree := testharness.ReadTree(t, target)
	out := map[string]string{}
	for _, la := range lockEntries(t, target, keptID) {
		out[la.MaterializedPath] = tree[la.MaterializedPath]
	}
	return out
}

// assertKept fails unless the run kept tools/a's previous output: its drop is
// marked kept, the files are byte-identical to before, and the lock still
// records them.
func assertKept(t *testing.T, target string, dropped []layer.Drop, before map[string]string) {
	t.Helper()
	if d := onlyDrop(t, dropped, keptID); !d.Unparseable() || !d.PreviousOutputKept {
		t.Errorf("drop = %+v, want an unparseable drop marked kept", d)
	}
	tree := testharness.ReadTree(t, target)
	for p, c := range before {
		if tree[p] != c {
			t.Errorf("kept file %s = %q, want %q", p, tree[p], c)
		}
	}
	if got := len(lockEntries(t, target, keptID)); got != len(before) {
		t.Errorf("lock records %d entries for %s, want %d", got, keptID, len(before))
	}
}

// assertRemovedOutput fails unless the run removed tools/a's previous output:
// its drop is not marked kept, none of the paths in gone remains on disk, and
// the lock no longer records tools/a.
func assertRemovedOutput(t *testing.T, target string, dropped []layer.Drop, gone []string) {
	t.Helper()
	if d := onlyDrop(t, dropped, keptID); !d.Unparseable() || d.PreviousOutputKept {
		t.Errorf("drop = %+v, want an unparseable drop not marked kept", d)
	}
	tree := testharness.ReadTree(t, target)
	for _, p := range gone {
		if _, ok := tree[p]; ok {
			t.Errorf("previous output %s survived; tree keys:\n%s", p, sortedTreeKeys(tree))
		}
	}
	if got := lockEntries(t, target, keptID); len(got) != 0 {
		t.Errorf("lock still records %s: %+v", keptID, got)
	}
}

// Spec: §13.11.3
// Spec: §7.5.1
// Spec: §7.5.3
// A full sync writes tools/a and tools/b, and tools/a's SKILL.md then stops
// parsing. A sync whose scope no longer selects tools/a (an include of tools/b
// alone, an exclude of tools/a, or a type filter the type recorded in tools/a's
// lock entry fails) removes its previous output, in a dry run as well, while
// a scope that still selects it keeps the output. The type filter reads the
// lock entry, which records skill; an entry that records no type fails any
// type filter.
func TestRun_UnparseableKeepFollowsScope(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		scope ScopeFilter
		keep  bool
		// untyped clears the type in tools/a's lock entries before the run.
		untyped bool
	}{
		{name: "include of the sibling", scope: ScopeFilter{Include: []string{keptOther}}},
		{name: "exclude", scope: ScopeFilter{Exclude: []string{keptID}}},
		{name: "type filter the recorded type fails", scope: ScopeFilter{Types: []string{"agent"}}},
		{name: "type filter on an entry with no type", scope: ScopeFilter{Types: []string{"skill"}}, untyped: true},
		{name: "include that still matches", scope: ScopeFilter{Include: []string{"tools/**"}}, keep: true},
		{name: "type filter the recorded type passes", scope: ScopeFilter{Types: []string{"skill"}}, keep: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, target, before := keepFixture(t, Options{})
			for _, la := range lockEntries(t, target, keptID) {
				if la.Type != "skill" {
					t.Fatalf("lock entry %+v records type %q, want skill", la, la.Type)
				}
			}
			if tc.untyped {
				clearLockTypes(t, target, keptID)
			}
			opts := Options{RegistryPath: reg, Target: target, Scope: tc.scope}
			dryOpts := opts
			dryOpts.DryRun = true
			dry := mustRun(t, dryOpts)
			if d := onlyDrop(t, dry.Dropped, keptID); d.PreviousOutputKept != tc.keep {
				t.Errorf("dry-run drop kept = %v, want %v", d.PreviousOutputKept, tc.keep)
			}
			res := mustRun(t, opts)
			if tc.keep {
				assertKept(t, target, res.Dropped, before)
				return
			}
			assertRemovedOutput(t, target, res.Dropped, keys(before))
		})
	}
}

// clearLockTypes rewrites the target lock with no type on id's entries.
func clearLockTypes(t *testing.T, target, id string) {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: lock=%v err=%v", lock, err)
	}
	for i := range lock.Artifacts {
		if lock.Artifacts[i].ID == id {
			lock.Artifacts[i].Type = ""
		}
	}
	if err := WriteLock(target, lock); err != nil {
		t.Fatal(err)
	}
}

// Spec: §13.11.3
// Spec: §7.5.5
// A toggle decides the keep as it decides the materialization set. After
// tools/a's SKILL.md stops parsing, podium sync override --remove tools/a
// records the toggle and removes tools/a's previous output. An artifact that
// only a toggles.add entry brings into the target keeps its output across a
// later override, because the toggle still selects it.
func TestOverride_UnparseableKeepFollowsToggles(t *testing.T) {
	t.Parallel()
	t.Run("remove", func(t *testing.T) {
		t.Parallel()
		reg, target, before := keepFixture(t, Options{})
		res, err := Override(OverrideOptions{Target: target, Remove: []string{keptID}, RegistryPath: reg})
		if err != nil {
			t.Fatalf("Override: %v", err)
		}
		assertRemovedOutput(t, target, res.Dropped, keys(before))
		if rm := res.Lock.Toggles.Remove; len(rm) != 1 || rm[0].ID != keptID {
			t.Errorf("toggles.remove = %+v, want %s", rm, keptID)
		}
	})
	t.Run("add", func(t *testing.T) {
		t.Parallel()
		reg, target := t.TempDir(), t.TempDir()
		testharness.WriteTree(t, reg,
			testharness.WriteTreeOption{Path: ".registry-config", Content: keepLayer},
			testharness.WriteTreeOption{Path: "team/" + keptID + "/SKILL.md", Content: plainSkill("a", "a-v1")},
			testharness.WriteTreeOption{Path: "team/" + keptOther + "/SKILL.md", Content: plainSkill("b", "b-v1")},
			testharness.WriteTreeOption{Path: "team/tools/c/SKILL.md", Content: plainSkill("c", "c-v1")},
		)
		mustRun(t, Options{RegistryPath: reg, Target: target, Scope: ScopeFilter{Include: []string{keptOther}}})
		if _, err := Override(OverrideOptions{Target: target, Add: []string{keptID}, RegistryPath: reg}); err != nil {
			t.Fatalf("Override add: %v", err)
		}
		before := keptOutputFiles(t, target)
		if len(before) == 0 {
			t.Fatalf("override --add wrote nothing for %s", keptID)
		}
		writeRegFile(t, reg, "team/"+keptID+"/SKILL.md", brokenSkill)
		res, err := Override(OverrideOptions{Target: target, Add: []string{"tools/c"}, RegistryPath: reg})
		if err != nil {
			t.Fatalf("Override: %v", err)
		}
		assertKept(t, target, res.Dropped, before)
	})
}

// Spec: §13.11.3
// Output an earlier run wrote for another harness adapter is not kept. A
// target synced with claude-code and then with cursor after tools/a's SKILL.md
// stops parsing loses tools/a's .claude/ files, and the cursor lock records
// no entry for tools/a; the dry run reports the drop unmarked. A second sync
// with the same adapter as the first keeps the output.
func TestRun_UnparseableKeepEndsOnHarnessChange(t *testing.T) {
	t.Parallel()
	t.Run("switch", func(t *testing.T) {
		t.Parallel()
		reg, target, before := keepFixture(t, Options{AdapterID: "claude-code"})
		for p := range before {
			if !strings.HasPrefix(p, ".claude/") {
				t.Fatalf("claude-code wrote %s outside .claude/", p)
			}
		}
		opts := Options{RegistryPath: reg, Target: target, AdapterID: "cursor"}
		dryOpts := opts
		dryOpts.DryRun = true
		if d := onlyDrop(t, mustRun(t, dryOpts).Dropped, keptID); d.PreviousOutputKept {
			t.Errorf("dry-run drop = %+v marked kept across a harness change", d)
		}
		res := mustRun(t, opts)
		assertRemovedOutput(t, target, res.Dropped, keys(before))
		if lock, _ := ReadLock(target); lock == nil || lock.Harness != "cursor" {
			t.Errorf("lock = %+v, want harness cursor", lock)
		}
	})
	t.Run("same adapter", func(t *testing.T) {
		t.Parallel()
		reg, target, before := keepFixture(t, Options{AdapterID: "claude-code"})
		res := mustRun(t, Options{RegistryPath: reg, Target: target, AdapterID: "claude-code"})
		assertKept(t, target, res.Dropped, before)
	})
}

// Spec: §13.11.3
// An artifact that writes a path the kept output holds ends the keep. After
// tools/a's SKILL.md stops parsing, a new skill other/a materializes to the
// same .claude/skills/a/SKILL.md. The sync writes other/a's copy, removes
// tools/a's resource, and the lock records the path once, for other/a. When
// other/a is removed in turn, the next sync deletes the file, and no content
// lingers attributed to tools/a.
func TestRun_UnparseableKeepEndsOnPathTakeover(t *testing.T) {
	t.Parallel()
	reg, target, before := keepFixture(t, Options{AdapterID: "claude-code"})
	const skillPath = ".claude/skills/a/SKILL.md"
	if _, ok := before[skillPath]; !ok {
		t.Fatalf("first sync output %v does not hold %s", keys(before), skillPath)
	}
	writeRegFile(t, reg, "team/other/a/SKILL.md", plainSkill("a", "other-a"))
	opts := Options{RegistryPath: reg, Target: target, AdapterID: "claude-code"}

	res := mustRun(t, opts)
	gone := []string{}
	for p := range before {
		if p != skillPath {
			gone = append(gone, p)
		}
	}
	assertRemovedOutput(t, target, res.Dropped, gone)
	if got := readFileString(t, filepath.Join(target, skillPath)); !strings.Contains(got, "other-a body") {
		t.Errorf("%s = %q, want other/a's copy", skillPath, got)
	}
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: lock=%v err=%v", lock, err)
	}
	var owners []string
	for _, la := range lock.Artifacts {
		if la.MaterializedPath == skillPath {
			owners = append(owners, la.ID)
		}
	}
	if !equalStrings(owners, []string{"other/a"}) {
		t.Errorf("lock entries for %s belong to %v, want other/a alone", skillPath, owners)
	}

	if err := os.RemoveAll(filepath.Join(reg, "team", "other")); err != nil {
		t.Fatal(err)
	}
	res = mustRun(t, opts)
	assertRemovedOutput(t, target, res.Dropped, keys(before))
	if got := lockIDs(t, target); !equalStrings(got, []string{keptOther}) {
		t.Errorf("lock IDs after other/a's removal = %v, want only %s", got, keptOther)
	}
}

// Spec: §13.11.3
// A config-merge file that is missing, or whose index records no entry for
// the kept artifact, holds no keyed entry another artifact can take over, so
// a fragment for the same file does not end the keep. A fragment that records
// the kept artifact's own entry path under another ID does.
func TestKeepRun_EntryTakenOver(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	writeRegFile(t, target, ".mcp.json", `{"mcpServers":{"alpha":{}},"x-podium":{"srv/beta":["mcpServers","beta"],"srv/alpha":["mcpServers","alpha"]}}`)
	run := keepRun{target: target}
	frag := [][]byte{[]byte(`{"mcpServers":{"alpha":{}},"x-podium":{"srv2/alpha":["mcpServers","alpha"]}}`)}
	if run.entryTakenOver("srv/alpha", "missing.json", frag) {
		t.Errorf("a missing file reported a takeover")
	}
	if run.entryTakenOver("srv/gamma", ".mcp.json", frag) {
		t.Errorf("an ID the index does not record reported a takeover")
	}
	if !run.entryTakenOver("srv/alpha", ".mcp.json", frag) {
		t.Errorf("a fragment writing srv/alpha's entry under srv2/alpha reported no takeover")
	}
}

// Spec: §13.11.3
// Spec: §6.7
// A shared config-merge file is not a path any one artifact holds whole, so a
// sibling that writes another entry into it leaves a kept entry in place
// (TestRun_UnparseableManifestKeepsConfigMergeEntry). A keyed config-map entry
// is keyed by the artifact's name, though, and an artifact that writes the
// same entry takes it over: after srv/alpha stops parsing, srv2/alpha, also
// named alpha, writes mcpServers.alpha. The sync does not keep srv/alpha, the
// entry and its index record belong to srv2/alpha, and removing srv2/alpha
// removes the entry.
func TestRun_UnparseableKeepEndsOnConfigMergeEntryTakeover(t *testing.T) {
	t.Parallel()
	reg := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: keepLayer},
		testharness.WriteTreeOption{Path: "team/srv/alpha/ARTIFACT.md", Content: mcpServerSrc("alpha", "Alpha server.")},
		testharness.WriteTreeOption{Path: "team/srv/beta/ARTIFACT.md", Content: mcpServerSrc("beta", "Beta server.")},
	)
	opts := Options{RegistryPath: reg, Target: target, AdapterID: "claude-code"}
	mustRun(t, opts)
	mcp := filepath.Join(target, ".mcp.json")

	writeRegFile(t, reg, "team/srv/alpha/ARTIFACT.md", brokenArtifact)
	writeRegFile(t, reg, "team/srv2/alpha/ARTIFACT.md",
		strings.Replace(mcpServerSrc("alpha", "Second alpha."), "@acme/alpha", "@acme/alpha-two", 1))
	res := mustRun(t, opts)
	if d := onlyDrop(t, res.Dropped, "srv/alpha"); d.PreviousOutputKept {
		t.Errorf("drop = %+v marked kept although srv2/alpha writes its entry", d)
	}
	doc, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "@acme/alpha-two") || strings.Contains(string(doc), "@acme/alpha\"") {
		t.Errorf(".mcp.json = %s, want srv2/alpha's entry in place of srv/alpha's", doc)
	}
	want := map[string][]string{"srv/beta": {"mcpServers", "beta"}, "srv2/alpha": {"mcpServers", "alpha"}}
	if got := materialize.IndexedEntries(doc); !reflect.DeepEqual(got, want) {
		t.Errorf(".mcp.json index = %v, want %v", got, want)
	}
	if got := lockIDs(t, target); !equalStrings(got, []string{"srv/beta", "srv2/alpha"}) {
		t.Errorf("lock IDs = %v, want srv/beta and srv2/alpha", got)
	}

	if err := os.RemoveAll(filepath.Join(reg, "team", "srv2")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, opts)
	if got := readFileString(t, mcp); strings.Contains(got, "alpha") {
		t.Errorf(".mcp.json after srv2/alpha's removal = %s, want no alpha entry", got)
	}
}
