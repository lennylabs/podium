package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §7.8 — a kind: marketplace target renders the publishing identity's
// effective view (read over the same HTTP API podium sync uses) into a
// multi-harness marketplace repository. This integration test runs a real
// registry in-process (the shared bootstrap the standalone server uses), points
// the §7.8 render at it as a server source, and asserts the multi-harness
// layout, the once-per-plugin manifest entry for a multi-artifact plugin,
// idempotent re-render, and the change set on a dropped artifact.
//
// The render reaches the registry over HTTP through sync.FetchRecords, so the
// test exercises the server-source record-fetch path the marketplace render
// reuses, not the filesystem shortcut the pkg/sync unit tests use.
func TestPublishRender_ServerSourceMultiHarness(t *testing.T) {
	t.Parallel()
	dir := referenceRegistryPath(t)
	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	workdir := t.TempDir()
	opts := sync.RenderOptions{
		OutputID:  "acme-agents",
		Registry:  ts.URL,
		Workdir:   workdir,
		Harnesses: []string{"claude-code", "codex", "cursor"},
		Plugins: []sync.PluginFilter{
			// finance-pack bundles two artifacts: finance/ap/pay-invoice (agent)
			// and finance/close/run-variance (skill).
			{Name: "finance-pack", Include: []string{"finance/**"}},
			// helpers holds the shared skill.
			{Name: "helpers", Include: []string{"payment-helpers/**"}},
		},
	}

	res, err := sync.Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !res.Changed {
		t.Errorf("first render must report Changed=true")
	}

	tree := readTreeNoLock(t, workdir)

	// Multi-harness layout: each format's manifest at its fixed root location
	// and per-harness, per-plugin content under <harness>/<plugin>/....
	for _, want := range []string{
		".claude-plugin/marketplace.json",
		".agents/plugins/marketplace.json",
		".cursor-plugin/marketplace.json",
		// finance-pack content per harness.
		"claude/finance-pack/.claude-plugin/plugin.json",
		"claude/finance-pack/agents/pay-invoice.md",        // the agent
		"claude/finance-pack/skills/run-variance/SKILL.md", // the skill
		"codex/finance-pack/.codex-plugin/plugin.json",
		"codex/finance-pack/skills/run-variance/SKILL.md",
		"cursor/finance-pack/.cursor-plugin/plugin.json",
		// helpers content.
		"claude/helpers/skills/routing-validator/SKILL.md",
	} {
		if _, ok := tree[want]; !ok {
			t.Errorf("missing %q in render tree; got:\n%s", want, treeKeys(tree))
		}
	}

	// Once-per-plugin manifest entry: finance-pack carries two artifacts but is
	// listed once in the Claude marketplace manifest.
	assertPluginListedOnce(t, filepath.Join(workdir, ".claude-plugin", "marketplace.json"), "acme-agents", "finance-pack")

	// Idempotent re-render: a second render against the unchanged registry
	// produces the identical tree and reports no change.
	second, err := sync.Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if second.Changed {
		t.Errorf("re-render of an unchanged registry must report Changed=false, got %v", second.ChangedArtifacts)
	}
	tree2 := readTreeNoLock(t, workdir)
	assertTreesEqual(t, tree, tree2)
}

// Spec: §7.8 — $PODIUM_CHANGED is "whether the render produced a diff against
// the checkout" (line 190). The Pattern A CI flow points --workdir at a fresh
// actions/checkout, which carries the committed marketplace content but no
// sync-local .podium/sync.lock (the marketplace repository does not commit the
// lock). Re-rendering the unchanged effective view into that checkout must report
// Changed=false, so skip_if_no_changes suppresses an empty commit. This drives
// the case through the real HTTP record-fetch path.
func TestPublishRender_ChangedFalseOnFreshCheckout(t *testing.T) {
	t.Parallel()
	dir := referenceRegistryPath(t)
	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	plugins := []sync.PluginFilter{{Name: "finance-pack", Include: []string{"finance/**"}}}
	source := t.TempDir()
	opts := sync.RenderOptions{
		OutputID:  "acme-agents",
		Registry:  ts.URL,
		Workdir:   source,
		Harnesses: []string{"claude-code", "codex"},
		Plugins:   plugins,
	}
	if _, err := sync.Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}

	// Simulate the fresh checkout: copy the committed content, drop .podium/.
	checkout := t.TempDir()
	for p, c := range readTreeNoLock(t, source) {
		dst := filepath.Join(checkout, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", p, err)
		}
		if err := os.WriteFile(dst, []byte(c), 0o644); err != nil {
			t.Fatalf("write %q: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(checkout, ".podium", "sync.lock")); !os.IsNotExist(err) {
		t.Fatalf("fresh checkout must carry no sync.lock: stat err=%v", err)
	}

	res, err := sync.Render(context.Background(), sync.RenderOptions{
		OutputID:  opts.OutputID,
		Registry:  ts.URL,
		Workdir:   checkout,
		Harnesses: opts.Harnesses,
		Plugins:   opts.Plugins,
	})
	if err != nil {
		t.Fatalf("re-render into fresh checkout: %v", err)
	}
	if res.Changed {
		t.Errorf("re-render of an identical fresh checkout must report Changed=false, got %v", res.ChangedArtifacts)
	}
}

// Spec: §7.8 — when an artifact leaves the effective view, the next render
// removes its files and the change set reports the change. This drives the
// removal through the registry by serving a narrower fixture on the second
// render via a fresh filesystem copy with one artifact dropped.
func TestPublishRender_StaleCleanupAndChangeSet(t *testing.T) {
	t.Parallel()

	// Copy the reference fixture so the test can mutate it.
	src := referenceRegistryPath(t)
	reg := t.TempDir()
	copyTree(t, src, reg)

	workdir := t.TempDir()
	plugins := []sync.PluginFilter{{Name: "finance-pack", Include: []string{"finance/**"}}}

	render := func() *sync.RenderResult {
		srv, err := server.NewFromFilesystem(reg)
		if err != nil {
			t.Fatalf("NewFromFilesystem: %v", err)
		}
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()
		res, err := sync.Render(context.Background(), sync.RenderOptions{
			OutputID:  "acme-agents",
			Registry:  ts.URL,
			Workdir:   workdir,
			Harnesses: []string{"claude-code"},
			Plugins:   plugins,
		})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		return res
	}

	render()
	stale := filepath.Join(workdir, "claude", "finance-pack", "skills", "run-variance", "SKILL.md")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("run-variance skill missing after first render: %v", err)
	}

	// Drop finance/close/run-variance from the served registry.
	if err := os.RemoveAll(filepath.Join(reg, "team-finance", "finance", "close", "run-variance")); err != nil {
		t.Fatalf("remove run-variance: %v", err)
	}

	second := render()
	if !second.Changed {
		t.Errorf("dropping an artifact must report Changed=true")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale run-variance skill was not cleaned up: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "claude", "finance-pack", "agents", "pay-invoice.md")); err != nil {
		t.Errorf("pay-invoice agent should survive: %v", err)
	}
}

// Spec: §7.8 — sync.RunMarketplace drives the prepare->render->publish pipeline
// for a kind: marketplace target against a server-source registry. This
// integration test runs the runner against the in-process registry with operator
// commands that touch marker files, asserting the render populated the supplied
// working directory, the operator publish phase ran, and the change-driven
// $PODIUM_CHANGED variable reached the workflow.
func TestPublishRunMarketplace_ServerSourceWorkflow(t *testing.T) {
	t.Parallel()
	dir := referenceRegistryPath(t)
	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	workdir := t.TempDir()
	out := sync.ResolvedOutput{
		ID:        "acme-agents",
		Registry:  ts.URL,
		Identity:  "publisher@acme.com",
		Git:       sync.GitRemote{Remote: "git@example.com:acme/agents.git", Branch: "main"},
		Harnesses: []string{"claude-code"},
		Plugins:   []sync.PluginFilter{{Name: "finance-pack", Include: []string{"finance/**"}}},
		Workflow: sync.Workflow{
			Publish: []sync.Command{
				{Sh: `printf 'changed=%s\n' "$PODIUM_CHANGED" >> "$PODIUM_WORKDIR/published"`},
			},
		},
	}

	res, err := sync.RunMarketplace(context.Background(), sync.RunOptions{
		Output:  out,
		Workdir: workdir,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatalf("RunMarketplace: %v", err)
	}
	if !res.Published {
		t.Errorf("a live run must report Published=true")
	}
	if res.Render == nil || !res.Render.Changed {
		t.Errorf("the first render against a server source must report Changed=true: %+v", res.Render)
	}
	// The render populated the supplied working directory.
	if _, err := os.Stat(filepath.Join(workdir, ".claude-plugin", "marketplace.json")); err != nil {
		t.Errorf("the render must populate the supplied working directory: %v", err)
	}
	// The operator publish command ran and saw the change-driven variable.
	body, err := os.ReadFile(filepath.Join(workdir, "published"))
	if err != nil {
		t.Fatalf("the operator publish command must run: %v", err)
	}
	if !strings.Contains(string(body), "changed=true") {
		t.Errorf("the publish command must see $PODIUM_CHANGED=true, got %q", body)
	}
}

// --- helpers -----------------------------------------------------------------

func readTreeNoLock(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".podium/") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

func treeKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("  " + k + "\n")
	}
	return b.String()
}

func assertPluginListedOnce(t *testing.T, manifestPath, wantMarket, wantPlugin string) {
	t.Helper()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest %s: %v", manifestPath, err)
	}
	var m struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name string `json:"name"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest not valid JSON: %v\n%s", err, data)
	}
	if m.Name != wantMarket {
		t.Errorf("marketplace name = %q, want %q", m.Name, wantMarket)
	}
	count := 0
	for _, p := range m.Plugins {
		if p.Name == wantPlugin {
			count++
		}
	}
	if count != 1 {
		t.Errorf("plugin %q listed %d times, want exactly 1:\n%s", wantPlugin, count, data)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
