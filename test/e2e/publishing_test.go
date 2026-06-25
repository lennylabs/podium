package e2e

// End-to-end tests for docs/consuming/publishing.md (D-publishing).
//
// These tests drive the compiled podium binary through `podium publish` (§7.8)
// against a filesystem-source registry, rendering a multi-harness marketplace
// repository into a local git remote (a bare repo on disk) through a workflow
// whose prepare and publish commands run real git. They assert the rendered
// repository layout (the vendor manifests at their fixed root locations, the
// per-harness <subtree>/<plugin>/ content, and the Codex plugin subtree carrying
// .app.json and .mcp.json per the §6.7 distribution table), the once-per-plugin
// manifest entry for a multi-artifact plugin, idempotent re-render (a second run
// against the unchanged catalog yields no second commit), --dry-run printing the
// substituted commands while writing nothing to the remote, --check validating
// the config, and a harness set naming opencode or none exiting non-zero with
// config.invalid.
//
// The git interaction is configuration: the workflow's clone/add/commit/push are
// operator-authored argv commands, so the test asserts the observable result in
// the remote rather than any embedded git in Podium.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
)

// ---- fixtures ---------------------------------------------------------------

// pubSkillArtifact is a minimal type:skill ARTIFACT.md; the agentskills.io
// name/description live in the sibling SKILL.md.
const pubSkillArtifact = "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body in SKILL.md -->\n"

// pubSkillBody returns the SKILL.md for a skill whose leaf directory is name.
func pubSkillBody(name string) string {
	return "---\nname: " + name + "\ndescription: The " + name + " skill for publishing tests. Use when the user needs " + name + ".\n---\n\n" + name + " body.\n"
}

// pubAgentArtifact returns a type:agent ARTIFACT.md.
func pubAgentArtifact(desc string) string {
	return "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\n---\n\nAgent body.\n"
}

// pubMCPArtifact returns a type:mcp-server ARTIFACT.md.
func pubMCPArtifact(name string) string {
	return "---\ntype: mcp-server\nname: " + name + "\nversion: 1.0.0\ndescription: The " + name + " MCP server.\nserver_identifier: npx:@acme/" + name + "\n---\n\nbody\n"
}

// writePublishRegistry stages a multi-layer filesystem registry holding a
// finance domain (an agent, a skill, and an mcp-server, the three artifacts of
// finance-pack) plus a separate routing skill. The leading layer directory is
// stripped from the canonical ID, so team-finance/finance/ap/pay-invoice has the
// canonical ID finance/ap/pay-invoice.
func writePublishRegistry(t *testing.T) string {
	t.Helper()
	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		// finance-pack: three artifacts under finance/.
		testharness.WriteTreeOption{Path: "team-finance/finance/ap/pay-invoice/ARTIFACT.md", Content: pubAgentArtifact("Pay an approved invoice.")},
		testharness.WriteTreeOption{Path: "team-finance/finance/close/run-variance/ARTIFACT.md", Content: pubSkillArtifact},
		testharness.WriteTreeOption{Path: "team-finance/finance/close/run-variance/SKILL.md", Content: pubSkillBody("run-variance")},
		testharness.WriteTreeOption{Path: "team-finance/finance/data/warehouse/ARTIFACT.md", Content: pubMCPArtifact("warehouse")},
		// A second, single-artifact plugin.
		testharness.WriteTreeOption{Path: "team-shared/payment-helpers/routing-validator/ARTIFACT.md", Content: pubSkillArtifact},
		testharness.WriteTreeOption{Path: "team-shared/payment-helpers/routing-validator/SKILL.md", Content: pubSkillBody("routing-validator")},
	)
	return reg
}

// gitCmd runs a git command in dir with the pinned identity and config
// isolation, under a hard deadline. It is the test's own git runner so the
// commit author and the global/system config never depend on the host.
func gitCmd(t *testing.T, dir string, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitEnv()...)
	cmd.Stdin = bytes.NewReader(nil)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("git %s timed out\nstderr:\n%s", strings.Join(args, " "), se.String())
	}
	res := cliResult{Stdout: so.String(), Stderr: se.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.Exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("git %s: %v\nstderr:\n%s", strings.Join(args, " "), err, se.String())
	}
	return res
}

// bareRemote initializes a bare git repository on disk seeded with one commit on
// a main branch and returns its path. A bare repo is the local stand-in for the
// git remote the workflow clones and pushes. The seed commit is required because
// the prepare workflow clones `--branch main`, which fails on an unborn branch:
// a real marketplace repository already exists with at least a README before the
// first publish.
func bareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if r := gitCmd(t, dir, "init", "--bare", "--initial-branch=main", dir); r.Exit != 0 {
		t.Fatalf("git init --bare exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	// Seed the remote with an initial commit on main so the prepare clone of
	// `--branch main` resolves.
	seed := t.TempDir()
	if r := gitCmd(t, seed, "clone", dir, seed); r.Exit != 0 {
		t.Fatalf("clone for seed: exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# marketplace\n"), 0o644); err != nil {
		t.Fatalf("write seed README: %v", err)
	}
	if r := gitCmd(t, seed, "checkout", "-B", "main"); r.Exit != 0 {
		t.Fatalf("seed checkout main: exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := gitCmd(t, seed, "add", "-A"); r.Exit != 0 {
		t.Fatalf("seed add: exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := gitCmd(t, seed, "commit", "-m", "seed"); r.Exit != 0 {
		t.Fatalf("seed commit: exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := gitCmd(t, seed, "push", "origin", "main"); r.Exit != 0 {
		t.Fatalf("seed push: exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	return dir
}

// gitEnv returns the env entries that pin a git identity and disable any system
// or global git config so a CI runner with no ~/.gitconfig can still commit. The
// publish workflow inherits these from the podium process environment.
func gitEnv() []string {
	return []string{
		"GIT_AUTHOR_NAME=podium-bot",
		"GIT_AUTHOR_EMAIL=podium-bot@acme.com",
		"GIT_COMMITTER_NAME=podium-bot",
		"GIT_COMMITTER_EMAIL=podium-bot@acme.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	}
}

// writePublishYAML writes a publish.yaml at an explicit path. registry is a
// filesystem path so the render needs no live server, and remote is the bare
// repo the workflow clones, commits to, and pushes. The workflow uses real git
// argv commands: prepare clones the branch, publish adds, commits (skipped when
// the render produced no diff), and pushes.
func writePublishYAML(t *testing.T, registry, remote, harnesses string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "publish.yaml")
	body := "" +
		"defaults:\n" +
		"  registry: " + registry + "\n" +
		"  identity: publisher@acme.com\n" +
		"  workflow:\n" +
		"    prepare:\n" +
		"      - run: [\"git\", \"clone\", \"--branch\", \"$PODIUM_GIT_BRANCH\", \"$PODIUM_GIT_REMOTE\", \"$PODIUM_WORKDIR\"]\n" +
		"    publish:\n" +
		"      - run: [\"git\", \"-C\", \"$PODIUM_WORKDIR\", \"add\", \"-A\"]\n" +
		"      - run: [\"git\", \"-C\", \"$PODIUM_WORKDIR\", \"commit\", \"-m\", \"$PODIUM_COMMIT_MESSAGE\"]\n" +
		"        skip_if_no_changes: true\n" +
		"      - run: [\"git\", \"-C\", \"$PODIUM_WORKDIR\", \"push\", \"origin\", \"$PODIUM_GIT_BRANCH\"]\n" +
		"marketplaces:\n" +
		"  - id: acme-agents\n" +
		"    git:\n" +
		"      remote: " + remote + "\n" +
		"      branch: main\n" +
		"    harnesses: [" + harnesses + "]\n" +
		"    commit_message: \"Sync Podium catalog ({{.ChangedCount}} changes)\"\n" +
		"    plugins:\n" +
		"      - name: finance-pack\n" +
		"        include: [\"finance/**\"]\n" +
		"      - name: helpers\n" +
		"        include: [\"payment-helpers/**\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write publish.yaml: %v", err)
	}
	return path
}

// requireGit skips the test when git is not on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// remoteCommitCount returns the number of commits on the remote's main branch by
// cloning it and counting. A bare repo cannot run `git log` against a worktree,
// so the helper clones into a temp dir first.
func remoteCommitCount(t *testing.T, remote string) int {
	t.Helper()
	clone := t.TempDir()
	if r := gitCmd(t, clone, "clone", remote, clone); r.Exit != 0 {
		// An empty bare repo has no commits; clone of an unborn branch still
		// succeeds, so a non-zero here is a real failure.
		t.Fatalf("clone remote for count: exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	r := gitCmd(t, clone, "rev-list", "--count", "HEAD")
	if r.Exit != 0 {
		// No commits yet: rev-list fails on an unborn HEAD.
		return 0
	}
	return atoiTrim(t, r.Stdout)
}

// remoteTree clones the remote's main branch and returns every committed file
// keyed by its slash-separated path relative to the worktree, excluding the .git
// directory and the .podium/ sync state. The workflow's `git add -A` commits the
// per-output sync lock and change summary under .podium/; the filter drops them
// so the layout assertions compare only the rendered marketplace tree.
func remoteTree(t *testing.T, remote string) map[string]string {
	t.Helper()
	clone := t.TempDir()
	if r := gitCmd(t, clone, "clone", remote, clone); r.Exit != 0 {
		t.Fatalf("clone remote: exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	out := map[string]string{}
	err := filepath.WalkDir(clone, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(clone, p)
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
		t.Fatalf("walk clone: %v", err)
	}
	return out
}

func atoiTrim(t *testing.T, s string) int {
	t.Helper()
	s = strings.TrimSpace(s)
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("non-numeric rev-list count %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ---- tests ------------------------------------------------------------------

// A full `podium publish` run renders the three-harness marketplace into a local
// bare remote: prepare clones it, Podium renders the tree, publish adds, commits,
// and pushes. The pushed repository carries each format's manifest at its fixed
// root location and the per-harness, per-plugin content, including the Codex
// plugin subtree's .app.json and .mcp.json (§6.7 distribution table).
func TestPublishing_FullRunRendersAndPushes(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	base := remoteCommitCount(t, remote)
	cfg := writePublishYAML(t, reg, remote, "claude-code, codex, cursor")

	res := runPodium(t, "", gitEnv(), "publish", "--config", cfg, "--output", "acme-agents")
	if res.Exit != 0 {
		t.Fatalf("publish exit=%d\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}

	if n := remoteCommitCount(t, remote); n != base+1 {
		t.Fatalf("remote commit count = %d, want %d (one publish commit) after first publish", n, base+1)
	}

	tree := remoteTree(t, remote)
	for _, want := range []string{
		// Vendor manifests at fixed root locations.
		".claude-plugin/marketplace.json",
		".agents/plugins/marketplace.json",
		".cursor-plugin/marketplace.json",
		// finance-pack per-harness, per-plugin content.
		"claude/finance-pack/.claude-plugin/plugin.json",
		"claude/finance-pack/agents/pay-invoice.md",
		"claude/finance-pack/skills/run-variance/SKILL.md",
		"claude/finance-pack/.mcp.json",
		"codex/finance-pack/.codex-plugin/plugin.json",
		"codex/finance-pack/skills/run-variance/SKILL.md",
		"codex/finance-pack/.mcp.json",
		// The Codex plugin subtree carries .app.json per the §6.7 table.
		"codex/finance-pack/.app.json",
		"cursor/finance-pack/.cursor-plugin/plugin.json",
		"cursor/finance-pack/skills/run-variance/SKILL.md",
		// helpers (single-artifact plugin) content.
		"claude/helpers/skills/routing-validator/SKILL.md",
	} {
		if _, ok := tree[want]; !ok {
			t.Errorf("missing %q in pushed tree; got:\n%s", want, pubTreeKeys(tree))
		}
	}

	// The Codex .app.json is valid JSON carrying the plugin name.
	assertJSONField(t, tree["codex/finance-pack/.app.json"], "name", "finance-pack")
}

// A multi-artifact plugin (finance-pack carries an agent, a skill, and an
// mcp-server) is listed exactly once in each vendor marketplace manifest, keyed
// by the plugin name (§7.8: one manifest entry per plugin, not per artifact).
func TestPublishing_MultiArtifactPluginListedOnce(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	cfg := writePublishYAML(t, reg, remote, "claude-code, codex, cursor")

	res := runPodium(t, "", gitEnv(), "publish", "--config", cfg, "--output", "acme-agents")
	if res.Exit != 0 {
		t.Fatalf("publish exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	tree := remoteTree(t, remote)
	for _, manifest := range []string{
		".claude-plugin/marketplace.json",
		".agents/plugins/marketplace.json",
		".cursor-plugin/marketplace.json",
	} {
		assertPluginCount(t, manifest, tree[manifest], "acme-agents", "finance-pack", 1)
	}
}

// A second `podium publish` against the unchanged catalog produces no diff
// against the checkout, so skip_if_no_changes suppresses the commit and the
// remote keeps exactly one commit (§7.8 reconciliation / idempotent re-render).
func TestPublishing_IdempotentReRenderSkipsCommit(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	base := remoteCommitCount(t, remote)
	cfg := writePublishYAML(t, reg, remote, "claude-code, codex")

	if r := runPodium(t, "", gitEnv(), "publish", "--config", cfg, "--output", "acme-agents"); r.Exit != 0 {
		t.Fatalf("first publish exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	afterFirst := remoteCommitCount(t, remote)
	if afterFirst != base+1 {
		t.Fatalf("after first publish commit count = %d, want %d (one publish commit)", afterFirst, base+1)
	}

	r2 := runPodium(t, "", gitEnv(), "publish", "--config", cfg, "--output", "acme-agents")
	if r2.Exit != 0 {
		t.Fatalf("second publish exit=%d stderr=%s", r2.Exit, r2.Stderr)
	}
	if n := remoteCommitCount(t, remote); n != afterFirst {
		t.Errorf("after re-render of an unchanged catalog commit count = %d, want %d (skip_if_no_changes suppressed the commit)", n, afterFirst)
	}
	// The skip is reported on stderr.
	if !strings.Contains(r2.Stderr, "skipped (no changes)") {
		t.Errorf("second publish stderr missing 'skipped (no changes)':\n%s", r2.Stderr)
	}
}

// --dry-run prints each prepare and publish command with its PODIUM_* variables
// substituted and writes nothing to the remote: no commit is created (§7.8
// "renders into a temporary directory and prints each command").
func TestPublishing_DryRunPrintsCommandsWritesNothing(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	base := remoteCommitCount(t, remote)
	cfg := writePublishYAML(t, reg, remote, "claude-code")

	res := runPodium(t, "", gitEnv(), "publish", "--config", cfg, "--output", "acme-agents", "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	out := res.Stdout + res.Stderr
	// The substituted commit/push commands appear with the remote substituted in.
	for _, want := range []string{"git", "commit", "push", remote} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}
	// Nothing was pushed: the remote keeps only the seed commit.
	if n := remoteCommitCount(t, remote); n != base {
		t.Errorf("dry-run pushed to the remote: commit count = %d, want %d (unchanged)", n, base)
	}
}

// --check validates the config and renders nothing: it exits 0 for a valid
// config and never touches the remote.
func TestPublishing_CheckValidatesConfig(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	base := remoteCommitCount(t, remote)
	cfg := writePublishYAML(t, reg, remote, "claude-code, codex, cursor")

	res := runPodium(t, "", gitEnv(), "publish", "--config", cfg, "--check")
	if res.Exit != 0 {
		t.Fatalf("check exit=%d\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "ok") {
		t.Errorf("check stdout missing 'ok':\n%s", res.Stdout)
	}
	if n := remoteCommitCount(t, remote); n != base {
		t.Errorf("--check pushed to the remote: commit count = %d, want %d (unchanged)", n, base)
	}
}

// A harness set naming a non-publish-target harness (opencode or none) is
// rejected at config validation with config.invalid and exit 2, the same as
// syncCmd's config-error code (§7.8 decision 1).
func TestPublishing_NonPublishHarnessRejected(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)

	for _, harness := range []string{"opencode", "none"} {
		t.Run(harness, func(t *testing.T) {
			cfg := writePublishYAML(t, reg, remote, harness)
			res := runPodium(t, "", gitEnv(), "publish", "--config", cfg, "--check")
			if res.Exit != 2 {
				t.Fatalf("publish --check with harness %q exit=%d, want 2\nstdout=%s\nstderr=%s",
					harness, res.Exit, res.Stdout, res.Stderr)
			}
			if !strings.Contains(res.Stderr, "config.invalid") {
				t.Errorf("stderr missing 'config.invalid' for harness %q:\n%s", harness, res.Stderr)
			}
		})
	}
}

// ---- assertion helpers ------------------------------------------------------

func pubTreeKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// stable order for the failure message.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("  " + k + "\n")
	}
	return b.String()
}

// assertJSONField decodes data and asserts the top-level string field equals
// want.
func assertJSONField(t *testing.T, data, field, want string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, data)
	}
	got, _ := m[field].(string)
	if got != want {
		t.Errorf("JSON field %q = %q, want %q\n%s", field, got, want, data)
	}
}

// assertPluginCount decodes a vendor marketplace manifest and asserts the
// marketplace name and that the named plugin appears exactly want times in the
// plugins array.
func assertPluginCount(t *testing.T, label, data, wantMarket, wantPlugin string, want int) {
	t.Helper()
	var m struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name string `json:"name"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("%s not valid JSON: %v\n%s", label, err, data)
	}
	if m.Name != wantMarket {
		t.Errorf("%s marketplace name = %q, want %q", label, m.Name, wantMarket)
	}
	count := 0
	for _, p := range m.Plugins {
		if p.Name == wantPlugin {
			count++
		}
	}
	if count != want {
		t.Errorf("%s lists plugin %q %d times, want %d:\n%s", label, wantPlugin, count, want, data)
	}
}
