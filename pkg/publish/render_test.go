package publish

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sync"
)

// These tests exercise the §7.8 render pipeline against a filesystem-source
// fixture registry (no live server): plugin assignment by declaration order, the
// PluginDescriptor wiring into per-harness subtrees, the once-per-plugin manifest
// entry for a multi-artifact plugin, the multi-harness repository layout, and
// idempotent re-render with stale-file cleanup and the change set.

const skillArtifact = `---
type: skill
version: 1.0.0
---
`

// fixtureRegistry writes a small multi-layer filesystem registry with finance
// and security artifacts and returns its path. The canonical ID strips the
// leading layer directory, so team-finance/finance/ap/pay-invoice has the
// canonical ID finance/ap/pay-invoice.
func fixtureRegistry(t *testing.T) string {
	t.Helper()
	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},

		// finance/ap/pay-invoice (skill)
		testharness.WriteTreeOption{Path: "team-finance/finance/ap/pay-invoice/ARTIFACT.md", Content: skillArtifact},
		testharness.WriteTreeOption{Path: "team-finance/finance/ap/pay-invoice/SKILL.md", Content: skillSrc("pay-invoice")},

		// finance/close/run-variance (skill) — second artifact in finance-pack
		testharness.WriteTreeOption{Path: "team-finance/finance/close/run-variance/ARTIFACT.md", Content: skillArtifact},
		testharness.WriteTreeOption{Path: "team-finance/finance/close/run-variance/SKILL.md", Content: skillSrc("run-variance")},

		// finance/experimental/draft (skill) — excluded by finance-pack exclude
		testharness.WriteTreeOption{Path: "team-finance/finance/experimental/draft/ARTIFACT.md", Content: skillArtifact},
		testharness.WriteTreeOption{Path: "team-finance/finance/experimental/draft/SKILL.md", Content: skillSrc("draft")},

		// security/baseline/lockdown (skill) — security-baseline plugin
		testharness.WriteTreeOption{Path: "team-security/security/baseline/lockdown/ARTIFACT.md", Content: skillArtifact},
		testharness.WriteTreeOption{Path: "team-security/security/baseline/lockdown/SKILL.md", Content: skillSrc("lockdown")},

		// notes/journal (skill) — selected by no plugin, must be dropped
		testharness.WriteTreeOption{Path: "personal/notes/journal/ARTIFACT.md", Content: skillArtifact},
		testharness.WriteTreeOption{Path: "personal/notes/journal/SKILL.md", Content: skillSrc("journal")},
	)
	return reg
}

func skillSrc(name string) string {
	return "---\nname: " + name + "\ndescription: A " + name + " skill.\n---\n\nBody for " + name + ".\n"
}

// financePlugins returns the standard plugin list: a finance-pack that includes
// finance/** and excludes the experimental subtree, then a security-baseline.
func financePlugins() []PluginFilter {
	return []PluginFilter{
		{Name: "finance-pack", Include: []string{"finance/**"}, Exclude: []string{"finance/experimental/**"}},
		{Name: "security-baseline", Include: []string{"security/baseline/**"}},
	}
}

func renderOpts(t *testing.T, reg, workdir string, harnesses []string) RenderOptions {
	t.Helper()
	return RenderOptions{
		OutputID:  "acme-agents",
		Registry:  reg,
		Workdir:   workdir,
		Harnesses: harnesses,
		Plugins:   financePlugins(),
	}
}

// Spec: §7.8 — a multi-harness output renders one repository with each format's
// manifest at its fixed root location and per-harness plugin content under
// <harness>/<plugin>/....
func TestRender_MultiHarnessLayout(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	res, err := Render(context.Background(), renderOpts(t, reg, workdir, []string{"claude-code", "codex", "cursor"}))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !res.Changed {
		t.Errorf("first render must report Changed=true")
	}

	tree := testharness.ReadTree(t, workdir)
	for _, want := range []string{
		// Root manifests at distinct fixed locations.
		".claude-plugin/marketplace.json",
		".agents/plugins/marketplace.json",
		".cursor-plugin/marketplace.json",
		// Per-harness, per-plugin content for finance-pack.
		"claude/finance-pack/.claude-plugin/plugin.json",
		"claude/finance-pack/skills/pay-invoice/SKILL.md",
		"claude/finance-pack/skills/run-variance/SKILL.md",
		"codex/finance-pack/.codex-plugin/plugin.json",
		"codex/finance-pack/skills/pay-invoice/SKILL.md",
		"cursor/finance-pack/.cursor-plugin/plugin.json",
		// security-baseline across harnesses.
		"claude/security-baseline/skills/lockdown/SKILL.md",
		"codex/security-baseline/skills/lockdown/SKILL.md",
	} {
		if _, ok := tree[want]; !ok {
			t.Errorf("missing %q in render tree; got:\n%s", want, sortedTreeKeys(tree))
		}
	}

	// The excluded experimental artifact and the unselected notes/journal
	// artifact must not appear in any subtree.
	for path := range tree {
		if strings.Contains(path, "draft") {
			t.Errorf("excluded finance/experimental/draft leaked into the tree at %q", path)
		}
		if strings.Contains(path, "journal") {
			t.Errorf("unselected notes/journal leaked into the tree at %q", path)
		}
	}
}

// Spec: §7.8 — a plugin that bundles several artifacts contributes one
// per-plugin manifest entry rather than one per artifact, because the
// OpMergeJSON merge concatenates same-key arrays without deduplication.
func TestRender_OncePerPluginManifestEntry(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	if _, err := Render(context.Background(), renderOpts(t, reg, workdir, []string{"claude-code"})); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// finance-pack holds two artifacts (pay-invoice, run-variance); the
	// marketplace manifest must list finance-pack once.
	data, err := os.ReadFile(filepath.Join(workdir, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	}
	var manifest struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name string `json:"name"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("marketplace.json is not valid JSON: %v\n%s", err, data)
	}
	if manifest.Name != "acme-agents" {
		t.Errorf("marketplace name = %q, want acme-agents", manifest.Name)
	}
	counts := map[string]int{}
	for _, p := range manifest.Plugins {
		counts[p.Name]++
	}
	if counts["finance-pack"] != 1 {
		t.Errorf("finance-pack listed %d times, want exactly 1: %s", counts["finance-pack"], data)
	}
	if counts["security-baseline"] != 1 {
		t.Errorf("security-baseline listed %d times, want exactly 1: %s", counts["security-baseline"], data)
	}
}

// Spec: §7.8 — plugin assignment evaluates the plugin filters in declaration
// order, so an artifact selected by an earlier plugin is not also placed in a
// later one. Two overlapping plugins must claim an artifact only for the first.
func TestRender_AssignmentByDeclarationOrder(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)

	records, err := sync.FetchRecords(sync.Options{RegistryPath: reg})
	if err != nil {
		t.Fatalf("fetch records: %v", err)
	}

	// Two plugins both match finance/ap/**; declaration order decides.
	plugins := []PluginFilter{
		{Name: "first", Include: []string{"finance/ap/**"}},
		{Name: "second", Include: []string{"finance/**"}},
	}
	got := assignPlugins(records, plugins)

	byID := map[string]string{}
	for _, a := range got {
		byID[a.record.ID] = a.plugin.Name
	}
	if byID["finance/ap/pay-invoice"] != "first" {
		t.Errorf("pay-invoice assigned to %q, want first (declaration order)", byID["finance/ap/pay-invoice"])
	}
	// run-variance matches only the second plugin's broader glob.
	if byID["finance/close/run-variance"] != "second" {
		t.Errorf("run-variance assigned to %q, want second", byID["finance/close/run-variance"])
	}
}

// Spec: §7.8 — the PluginDescriptor wires each artifact's component files under
// <harness>/<plugin>/..., and the descriptor's name keys the per-plugin
// manifest. A skill in finance-pack on the codex harness lands under
// codex/finance-pack/skills/<name>/.
func TestRender_DescriptorWiring(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	if _, err := Render(context.Background(), renderOpts(t, reg, workdir, []string{"codex"})); err != nil {
		t.Fatalf("Render: %v", err)
	}
	tree := testharness.ReadTree(t, workdir)
	if _, ok := tree["codex/finance-pack/skills/pay-invoice/SKILL.md"]; !ok {
		t.Errorf("descriptor did not place pay-invoice under codex/finance-pack/skills/; got:\n%s", sortedTreeKeys(tree))
	}
	// The per-plugin manifest carries the plugin name from the descriptor.
	data, err := os.ReadFile(filepath.Join(workdir, "codex", "finance-pack", ".codex-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("read plugin.json: %v", err)
	}
	var pj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pj); err != nil {
		t.Fatalf("plugin.json invalid: %v", err)
	}
	if pj.Name != "finance-pack" {
		t.Errorf("plugin.json name = %q, want finance-pack", pj.Name)
	}
}

// Spec: §7.8 — re-rendering an output against an unchanged registry is
// idempotent: the second render produces the identical tree and reports
// Changed=false with an empty change set.
func TestRender_IdempotentReRender(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()
	opts := renderOpts(t, reg, workdir, []string{"claude-code", "codex"})

	first, err := Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	if !first.Changed {
		t.Errorf("first render must report Changed=true")
	}
	tree1 := materializedTreeNoLock(t, workdir)

	second, err := Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if second.Changed {
		t.Errorf("re-render of an unchanged registry must report Changed=false, got change set %v", second.ChangedArtifacts)
	}
	if len(second.ChangedArtifacts) != 0 {
		t.Errorf("re-render change set must be empty, got %v", second.ChangedArtifacts)
	}
	tree2 := materializedTreeNoLock(t, workdir)
	assertTreeEqual(t, tree1, tree2)
}

// Spec: §7.8 — when an artifact leaves the view, the next render removes its
// files (stale-file cleanup) and the change set reports the change.
func TestRender_StaleFileCleanup(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()
	opts := renderOpts(t, reg, workdir, []string{"claude-code"})

	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	staleFile := filepath.Join(workdir, "claude", "finance-pack", "skills", "run-variance", "SKILL.md")
	if _, err := os.Stat(staleFile); err != nil {
		t.Fatalf("run-variance skill missing after first render: %v", err)
	}

	// Drop run-variance from the registry by removing its directory.
	if err := os.RemoveAll(filepath.Join(reg, "team-finance", "finance", "close", "run-variance")); err != nil {
		t.Fatalf("remove run-variance: %v", err)
	}

	second, err := Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if !second.Changed {
		t.Errorf("dropping an artifact must report Changed=true")
	}
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Errorf("stale run-variance skill was not cleaned up: stat err=%v", err)
	}
	// pay-invoice survives.
	if _, err := os.Stat(filepath.Join(workdir, "claude", "finance-pack", "skills", "pay-invoice", "SKILL.md")); err != nil {
		t.Errorf("pay-invoice skill should survive the cleanup: %v", err)
	}
}

// Spec: §7.8 — the change set names the canonical artifact IDs whose output
// changed. Editing one artifact reports only that artifact (plus the shared
// manifest marker), not the unchanged ones.
func TestRender_ChangeSetNamesChangedArtifact(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()
	opts := renderOpts(t, reg, workdir, []string{"claude-code"})

	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}

	// Edit pay-invoice's skill body.
	edited := "---\nname: pay-invoice\ndescription: A pay-invoice skill.\n---\n\nEdited body.\n"
	if err := os.WriteFile(filepath.Join(reg, "team-finance", "finance", "ap", "pay-invoice", "SKILL.md"), []byte(edited), 0o644); err != nil {
		t.Fatalf("edit pay-invoice: %v", err)
	}

	second, err := Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if !second.Changed {
		t.Fatalf("editing an artifact must report Changed=true")
	}
	if !containsStr(second.ChangedArtifacts, "finance/ap/pay-invoice") {
		t.Errorf("change set %v must name finance/ap/pay-invoice", second.ChangedArtifacts)
	}
	if containsStr(second.ChangedArtifacts, "finance/close/run-variance") {
		t.Errorf("change set %v must not name the unchanged run-variance", second.ChangedArtifacts)
	}
}

// Spec: §7.8 — the change summary JSON carries the output ID, the changed flag,
// the count, and the artifact identifiers for the $PODIUM_CHANGE_SUMMARY file.
func TestRenderResult_ChangeSummaryJSON(t *testing.T) {
	t.Parallel()
	res := &RenderResult{OutputID: "acme-agents", Changed: true, ChangedArtifacts: []string{"finance/ap/pay-invoice"}}
	var got struct {
		Output    string   `json:"output"`
		Changed   bool     `json:"changed"`
		Count     int      `json:"count"`
		Artifacts []string `json:"artifacts"`
	}
	if err := json.Unmarshal(res.ChangeSummaryJSON(), &got); err != nil {
		t.Fatalf("change summary not valid JSON: %v", err)
	}
	if got.Output != "acme-agents" || !got.Changed || got.Count != 1 || len(got.Artifacts) != 1 {
		t.Errorf("change summary mismatch: %+v", got)
	}

	// An empty change set serializes artifacts as [] rather than null.
	empty := (&RenderResult{OutputID: "x"}).ChangeSummaryJSON()
	if !strings.Contains(string(empty), `"artifacts": []`) {
		t.Errorf("empty change summary must carry artifacts: []\n%s", empty)
	}
}

// Spec: §7.8 — Render wraps a registry-fetch failure with the output ID rather
// than panicking, so a misconfigured source surfaces a structured error.
func TestRender_FetchError(t *testing.T) {
	t.Parallel()
	// An empty registry source is rejected by the reused sync fetch guard.
	_, err := Render(context.Background(), RenderOptions{
		OutputID:  "acme-agents",
		Registry:  "",
		Workdir:   t.TempDir(),
		Harnesses: []string{"claude-code"},
		Plugins:   financePlugins(),
	})
	if err == nil {
		t.Fatalf("Render with no registry must error")
	}
	if !strings.Contains(err.Error(), "acme-agents") {
		t.Errorf("fetch error must name the output id: %v", err)
	}
}

// Spec: §7.8 — Render fails when the working directory cannot be written rather
// than reporting a successful render against an unwritable destination.
func TestRender_WriteError(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	// Point the workdir at a path whose parent is a regular file, so
	// materialize.Write cannot create the destination tree.
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	workdir := filepath.Join(blocker, "workdir")

	_, err := Render(context.Background(), renderOpts(t, reg, workdir, []string{"claude-code"}))
	if err == nil {
		t.Fatalf("Render into an unwritable workdir must error")
	}
}

// writeLock wraps a lock-write failure with a structured error. A regular file
// at <workdir>/.podium blocks the lock directory creation inside sync.WriteLock.
func TestWriteLock_Error(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, ".podium"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	err := writeLock(workdir, map[string]string{"a.md": "d"}, map[string]string{"a.md": ""})
	if err == nil {
		t.Fatalf("writeLock must error when the lock directory cannot be created")
	}
	if !strings.Contains(err.Error(), "write lock") {
		t.Errorf("error must name the lock-write failure: %v", err)
	}
}

// Spec: §7.8 — the three Claude surfaces share one marketplace, so a harness set
// naming more than one yields a single .claude-plugin/marketplace.json and a
// single claude/ subtree rather than a collision.
func TestRender_SharedClaudeMarketplace(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	if _, err := Render(context.Background(), renderOpts(t, reg, workdir, []string{"claude-code", "claude-desktop", "claude-cowork"})); err != nil {
		t.Fatalf("Render: %v", err)
	}
	tree := testharness.ReadTree(t, workdir)
	if _, ok := tree[".claude-plugin/marketplace.json"]; !ok {
		t.Errorf("shared Claude surfaces must produce one .claude-plugin/marketplace.json")
	}
	// Only the shared "claude/" subtree, no per-surface duplicates.
	for path := range tree {
		if strings.Contains(path, "claude-desktop/") || strings.Contains(path, "claude-cowork/") {
			t.Errorf("a per-surface subtree leaked at %q; the Claude surfaces share one subtree", path)
		}
	}
}

// Spec: §7.8 — a harness with no git-repo distribution (opencode, none) is not a
// publish target; Render rejects an output that names one.
func TestRender_RejectsNonPublishHarness(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	_, err := Render(context.Background(), renderOpts(t, reg, workdir, []string{"opencode"}))
	if err == nil {
		t.Fatalf("Render must reject a non-publish harness")
	}
	var noEmitter *ErrNoEmitter
	if !errors.As(err, &noEmitter) {
		t.Fatalf("error must be *ErrNoEmitter, got %T: %v", err, err)
	}
	if noEmitter.Harness != "opencode" {
		t.Errorf("ErrNoEmitter.Harness = %q, want opencode", noEmitter.Harness)
	}
	if !strings.Contains(noEmitter.Error(), "opencode") {
		t.Errorf("ErrNoEmitter.Error() = %q, want it to name opencode", noEmitter.Error())
	}
	if noEmitter.Unwrap() == nil {
		t.Errorf("ErrNoEmitter.Unwrap() must return the wrapped cause")
	}
}

// --- helpers -----------------------------------------------------------------

func materializedTreeNoLock(t *testing.T, dir string) map[string]string {
	t.Helper()
	full := testharness.ReadTree(t, dir)
	out := make(map[string]string, len(full))
	for p, c := range full {
		if strings.Contains(p, ".podium/sync.lock") {
			continue
		}
		out[p] = c
	}
	return out
}

func assertTreeEqual(t *testing.T, want, got map[string]string) {
	t.Helper()
	for p, w := range want {
		g, ok := got[p]
		if !ok {
			t.Errorf("re-render dropped %q", p)
			continue
		}
		if g != w {
			t.Errorf("re-render changed %q:\n first=%q\n second=%q", p, w, g)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("re-render added %q", p)
		}
	}
}

func sortedTreeKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		out += "  " + k + "\n"
	}
	return out
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
