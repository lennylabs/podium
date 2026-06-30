package e2e

// End-to-end tests for docs/consuming/publishing.md (D-publishing).
//
// These tests drive the compiled podium binary through `podium sync --config`
// (§7.5.2, §7.8) against a sync.yaml carrying a `kind: marketplace` target. A
// marketplace target renders the multi-harness marketplace repository into the
// target working directory and runs the target's prepare/publish workflow into a
// local git remote (a bare repo on disk) through real git argv commands. They
// assert the rendered repository layout (the vendor manifests at their fixed root
// locations, the per-harness <subtree>/<plugin>/ content, and the Codex plugin
// subtree carrying the .codex-plugin/plugin.json and .mcp.json per the §7.8
// emitter prose), the once-per-plugin manifest entry for a multi-artifact plugin,
// idempotent re-render (a second run against the unchanged catalog yields no
// second commit), --dry-run printing the substituted commands while writing
// nothing to the remote, --check --config validating a mixed config without
// writing any target tree or lock, a `kind: workspace` target with a workflow
// running its prepare/publish commands around the materialization, a marketplace
// target against a server-source registry passing the resolved credential so the
// restricted effective view is rendered, and a harness set naming opencode or
// none exiting non-zero with config.invalid.
//
// The git interaction is configuration: the workflow's clone/add/commit/push are
// operator-authored argv commands, so the test asserts the observable result in
// the remote rather than any embedded git in Podium.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
// workflow inherits these from the podium process environment.
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

// writeSyncConfigMarketplace writes a sync.yaml under <workspace>/.podium/ that
// declares one `kind: marketplace` target. registry is a filesystem path or a
// server URL, and remote is the bare repo the workflow clones, commits to, and
// pushes. The render and the workflow operate in the configured target:
// directory (Decision 4 / §7.5.2), which persists across runs, so the prepare
// phase clones it on a first run and refreshes it on a re-run: it clones into
// $PODIUM_WORKDIR when the directory holds no git checkout, otherwise it fetches
// and hard-resets to the remote branch. The publish phase adds, commits (skipped
// when the render produced no diff), and pushes. The function returns the config
// path.
func writeSyncConfigMarketplace(t *testing.T, workspace, registry, remote, harnesses string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	path := filepath.Join(dir, "sync.yaml")
	workdir := filepath.Join(workspace, "build", "acme-agents")
	// An idempotent prepare for a persistent target: clone on a first run, fetch
	// and reset on a re-run. Written as one sh: command so the conditional stays
	// in the operator's shell rather than in Podium.
	prepare := `if [ -d "$PODIUM_WORKDIR/.git" ]; then ` +
		`git -C "$PODIUM_WORKDIR" fetch origin "$PODIUM_GIT_BRANCH" && ` +
		`git -C "$PODIUM_WORKDIR" reset --hard "origin/$PODIUM_GIT_BRANCH"; ` +
		`else ` +
		`git clone --branch "$PODIUM_GIT_BRANCH" "$PODIUM_GIT_REMOTE" "$PODIUM_WORKDIR"; ` +
		`fi`
	body := "" +
		"defaults:\n" +
		"  registry: " + registry + "\n" +
		"  identity: publisher@acme.com\n" +
		"targets:\n" +
		"  - id: acme-agents\n" +
		"    kind: marketplace\n" +
		"    target: " + workdir + "\n" +
		"    git:\n" +
		"      remote: " + remote + "\n" +
		"      branch: main\n" +
		"    harnesses: [" + harnesses + "]\n" +
		"    commit_message: \"Sync Podium catalog ({{.ChangedCount}} changes)\"\n" +
		"    plugins:\n" +
		"      - name: finance-pack\n" +
		"        include: [\"finance/**\"]\n" +
		"      - name: helpers\n" +
		"        include: [\"payment-helpers/**\"]\n" +
		"    workflow:\n" +
		"      prepare:\n" +
		"        - sh: " + strconv.Quote(prepare) + "\n" +
		"      publish:\n" +
		"        - run: [\"git\", \"-C\", \"$PODIUM_WORKDIR\", \"add\", \"-A\"]\n" +
		"        - run: [\"git\", \"-C\", \"$PODIUM_WORKDIR\", \"commit\", \"-m\", \"$PODIUM_COMMIT_MESSAGE\"]\n" +
		"          skip_if_no_changes: true\n" +
		"        - run: [\"git\", \"-C\", \"$PODIUM_WORKDIR\", \"push\", \"origin\", \"$PODIUM_GIT_BRANCH\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
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
// per-target sync lock and change summary under .podium/; the filter drops them
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

// A full `podium sync --config` run against a kind: marketplace target renders
// the three-harness marketplace into a local bare remote: prepare clones it,
// Podium renders the tree into the target working directory, and publish adds,
// commits, and pushes. The pushed repository carries each format's manifest at
// its fixed root location and the per-harness, per-plugin content, including the
// Codex plugin subtree's .codex-plugin/plugin.json and .mcp.json (§7.8 emitter
// prose).
func TestPublishing_MarketplaceTargetRendersAndPushes(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	base := remoteCommitCount(t, remote)
	ws := t.TempDir()
	cfg := writeSyncConfigMarketplace(t, ws, reg, remote, "claude-code, codex, cursor")

	res := runPodium(t, "", gitEnv(), "sync", "--config", cfg)
	if res.Exit != 0 {
		t.Fatalf("sync --config exit=%d\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}

	if n := remoteCommitCount(t, remote); n != base+1 {
		t.Fatalf("remote commit count = %d, want %d (one publish commit) after first sync", n, base+1)
	}

	// Decision 4 / §7.5.2: the render and the workflow checkout land in the
	// configured target: directory (<ws>/build/acme-agents) rather than an
	// allocated temp directory. The directory survives the run (the operator owns
	// it), and it carries the rendered marketplace manifest the workflow committed.
	targetDir := filepath.Join(ws, "build", "acme-agents")
	if _, err := os.Stat(filepath.Join(targetDir, ".claude-plugin", "marketplace.json")); err != nil {
		t.Errorf("rendered tree did not land in the configured target: dir %s: %v", targetDir, err)
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
		"cursor/finance-pack/.cursor-plugin/plugin.json",
		"cursor/finance-pack/skills/run-variance/SKILL.md",
		// helpers (single-artifact plugin) content.
		"claude/helpers/skills/routing-validator/SKILL.md",
	} {
		if _, ok := tree[want]; !ok {
			t.Errorf("missing %q in pushed tree; got:\n%s", want, pubTreeKeys(tree))
		}
	}

	// The Codex per-plugin manifest is valid JSON carrying the plugin name, and
	// the §7.8 emitter component set omits .app.json, so the subtree must not
	// carry one.
	assertJSONField(t, tree["codex/finance-pack/.codex-plugin/plugin.json"], "name", "finance-pack")
	for p := range tree {
		if strings.HasSuffix(p, ".app.json") {
			t.Errorf("pushed tree must not carry a Codex .app.json (omitted from the §7.8 component set), got %q", p)
		}
	}
}

// A multi-artifact plugin (finance-pack carries an agent, a skill, and an
// mcp-server) is listed exactly once in each vendor marketplace manifest, keyed
// by the plugin name (§7.8: one manifest entry per plugin, not per artifact).
func TestPublishing_MultiArtifactPluginListedOnce(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	ws := t.TempDir()
	cfg := writeSyncConfigMarketplace(t, ws, reg, remote, "claude-code, codex, cursor")

	res := runPodium(t, "", gitEnv(), "sync", "--config", cfg)
	if res.Exit != 0 {
		t.Fatalf("sync --config exit=%d stderr=%s", res.Exit, res.Stderr)
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

// A second `podium sync --config` against the unchanged catalog produces no diff
// against the checkout, so skip_if_no_changes suppresses the commit and the
// remote keeps exactly one commit (§7.8 reconciliation / idempotent re-render).
func TestPublishing_IdempotentReRenderSkipsCommit(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	base := remoteCommitCount(t, remote)
	ws := t.TempDir()
	cfg := writeSyncConfigMarketplace(t, ws, reg, remote, "claude-code, codex")

	if r := runPodium(t, "", gitEnv(), "sync", "--config", cfg); r.Exit != 0 {
		t.Fatalf("first sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	afterFirst := remoteCommitCount(t, remote)
	if afterFirst != base+1 {
		t.Fatalf("after first sync commit count = %d, want %d (one publish commit)", afterFirst, base+1)
	}

	r2 := runPodium(t, "", gitEnv(), "sync", "--config", cfg)
	if r2.Exit != 0 {
		t.Fatalf("second sync exit=%d stderr=%s", r2.Exit, r2.Stderr)
	}
	if n := remoteCommitCount(t, remote); n != afterFirst {
		t.Errorf("after re-render of an unchanged catalog commit count = %d, want %d (skip_if_no_changes suppressed the commit)", n, afterFirst)
	}
	// The skip is reported on stderr.
	if !strings.Contains(r2.Stderr, "skipped (no changes)") {
		t.Errorf("second sync stderr missing 'skipped (no changes)':\n%s", r2.Stderr)
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
	ws := t.TempDir()
	cfg := writeSyncConfigMarketplace(t, ws, reg, remote, "claude-code")

	res := runPodium(t, "", gitEnv(), "sync", "--config", cfg, "--dry-run")
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
	// A --dry-run renders into a temp directory and never touches the configured
	// target: directory, so the target directory is not created.
	targetDir := filepath.Join(ws, "build", "acme-agents")
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote the configured target: dir %s (stat err=%v); a dry run renders into a temp dir", targetDir, err)
	}

	// --dry-run --json emits the structured envelope on stdout (the command
	// preview goes to stderr): one entry per target with the changed flag, the
	// changed artifacts, and published=false.
	jres := runPodium(t, "", gitEnv(), "sync", "--config", cfg, "--dry-run", "--json")
	if jres.Exit != 0 {
		t.Fatalf("dry-run --json exit=%d stderr=%s", jres.Exit, jres.Stderr)
	}
	envBody := jres.Stdout[strings.IndexByte(jres.Stdout, '{'):]
	var env struct {
		Target           string   `json:"target"`
		Changed          bool     `json:"changed"`
		ChangedArtifacts []string `json:"changed_artifacts"`
		Published        bool     `json:"published"`
	}
	if err := json.Unmarshal([]byte(envBody), &env); err != nil {
		t.Fatalf("--dry-run --json output not valid JSON: %v\n%s", err, jres.Stdout)
	}
	if env.Target != "acme-agents" || !env.Changed || env.Published {
		t.Errorf("marketplace JSON envelope = %+v, want target=acme-agents changed=true published=false", env)
	}
	if len(env.ChangedArtifacts) == 0 {
		t.Errorf("marketplace JSON envelope changed_artifacts empty: %+v", env)
	}
}

// --check --config validates a mixed config (a kind: workspace target and a
// kind: marketplace target) and materializes neither: it exits 0, never touches
// the remote, and writes neither the marketplace target tree nor its
// <target>/.podium/sync.lock.
func TestPublishing_CheckConfigValidatesWritesNothing(t *testing.T) {
	t.Parallel()
	requireGit(t)
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	base := remoteCommitCount(t, remote)
	ws := t.TempDir()
	// A mixed config: the marketplace target plus a kind: workspace target.
	cfg := writeSyncConfigMixed(t, ws, reg, remote)

	res := runPodium(t, "", gitEnv(), "sync", "--config", cfg, "--check")
	if res.Exit != 0 {
		t.Fatalf("check exit=%d\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "ok") {
		t.Errorf("check stdout missing 'ok':\n%s", res.Stdout)
	}
	if n := remoteCommitCount(t, remote); n != base {
		t.Errorf("--check pushed to the remote: commit count = %d, want %d (unchanged)", n, base)
	}
	// The marketplace target's working directory carries no rendered tree and no
	// sync lock.
	mktWorkdir := filepath.Join(ws, "build", "acme-agents")
	if _, err := os.Stat(filepath.Join(mktWorkdir, ".podium", "sync.lock")); !os.IsNotExist(err) {
		t.Errorf("--check wrote a marketplace sync.lock at %s (stat err=%v)", mktWorkdir, err)
	}
	// The workspace target's directory carries no sync lock.
	wsTarget := filepath.Join(ws, "out", "claude")
	if _, err := os.Stat(filepath.Join(wsTarget, ".podium", "sync.lock")); !os.IsNotExist(err) {
		t.Errorf("--check wrote a workspace sync.lock at %s (stat err=%v)", wsTarget, err)
	}
}

// A kind: workspace target carrying a workflow materializes the project-files
// layout and runs the target's prepare and publish commands around the
// materialization (Decision 3). The test asserts the observable side effect: the
// prepare command writes a marker file before materialization, and the publish
// command writes a second marker after, both with PODIUM_WORKDIR pointing at the
// target directory.
func TestPublishing_WorkspaceTargetRunsWorkflow(t *testing.T) {
	t.Parallel()
	reg := writePublishRegistry(t)
	ws := t.TempDir()
	target := filepath.Join(ws, "out", "claude")
	cfg := writeSyncConfigWorkspaceWorkflow(t, ws, reg, target)

	res := runPodium(t, "", nil, "sync", "--config", cfg, "--json")
	if res.Exit != 0 {
		t.Fatalf("sync --config (workspace workflow) exit=%d\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	// --json emits the workspace sync envelope on stdout, keyed by harness.
	if !strings.Contains(res.Stdout, "\"harness\"") {
		t.Errorf("workspace --json output missing the sync envelope:\n%s", res.Stdout)
	}
	// The materialization wrote the harness-native layout into the target.
	if _, err := os.Stat(filepath.Join(target, ".podium", "sync.lock")); err != nil {
		t.Errorf("workspace target did not materialize (no sync.lock): %v", err)
	}
	// The prepare command ran before materialization and wrote its marker.
	prepMarker := filepath.Join(ws, "prepare-ran")
	if b, err := os.ReadFile(prepMarker); err != nil {
		t.Errorf("prepare workflow command did not run (no marker): %v", err)
	} else if strings.TrimSpace(string(b)) != target {
		t.Errorf("prepare marker = %q, want PODIUM_WORKDIR=%q", strings.TrimSpace(string(b)), target)
	}
	// The publish command ran after materialization and wrote its marker.
	pubMarker := filepath.Join(ws, "publish-ran")
	if _, err := os.Stat(pubMarker); err != nil {
		t.Errorf("publish workflow command did not run (no marker): %v", err)
	}
}

// A kind: workspace target whose publish command declares skip_if_no_changes
// skips the command on a re-sync that wrote no delta (Decision 3, §7.5.2
// $PODIUM_CHANGED). The first sync into an empty target changes the tree, so the
// publish command runs; the second sync against the unchanged catalog writes no
// file, so $PODIUM_CHANGED is false and the command is skipped. The publish
// command appends a line to a counter file, so the file carries one line after
// two syncs and the second run reports "skipped (no changes)".
func TestPublishing_WorkspaceTargetSkipIfNoChanges(t *testing.T) {
	t.Parallel()
	reg := writePublishRegistry(t)
	ws := t.TempDir()
	target := filepath.Join(ws, "out", "claude")
	counter := filepath.Join(ws, "publish-count")
	cfg := writeSyncConfigWorkspaceSkipWorkflow(t, ws, reg, target, counter)

	if r := runPodium(t, "", nil, "sync", "--config", cfg); r.Exit != 0 {
		t.Fatalf("first sync exit=%d\nstdout=%s\nstderr=%s", r.Exit, r.Stdout, r.Stderr)
	}
	if n := countLines(t, counter); n != 1 {
		t.Fatalf("after first sync publish-count = %d, want 1 (the publish command ran)", n)
	}

	r2 := runPodium(t, "", nil, "sync", "--config", cfg)
	if r2.Exit != 0 {
		t.Fatalf("second sync exit=%d\nstdout=%s\nstderr=%s", r2.Exit, r2.Stdout, r2.Stderr)
	}
	if n := countLines(t, counter); n != 1 {
		t.Errorf("after re-sync of an unchanged catalog publish-count = %d, want 1 (skip_if_no_changes suppressed the publish command)", n)
	}
	if !strings.Contains(r2.Stderr, "skipped (no changes)") {
		t.Errorf("second sync stderr missing 'skipped (no changes)':\n%s", r2.Stderr)
	}
}

// A marketplace target against a server-source registry passes the resolved
// registry credential to the render's effective-view fetch (§4.6): a run that
// resolves PODIUM_TOKEN renders the restricted view that credential authorizes,
// and a run with no credential renders the anonymous public view. The stub
// registry returns a finance artifact only when the request carries the
// restricted bearer token, so the rendered marketplace tree differs by
// credential.
func TestPublishing_ServerSourceCredentialSelectsEffectiveView(t *testing.T) {
	t.Parallel()
	requireGit(t)

	const restrictedToken = "restricted-credential"
	srv := newEffectiveViewStub(t, restrictedToken)
	defer srv.Close()

	// Restricted view: PODIUM_TOKEN resolves the credential, so the finance
	// artifact reaches the render and the finance-pack plugin appears.
	t.Run("resolved credential renders restricted view", func(t *testing.T) {
		remote := bareRemote(t)
		ws := t.TempDir()
		cfg := writeSyncConfigMarketplace(t, ws, srv.URL, remote, "claude-code")
		res := runPodium(t, "", append(gitEnv(), "PODIUM_TOKEN="+restrictedToken), "sync", "--config", cfg)
		if res.Exit != 0 {
			t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
		}
		tree := remoteTree(t, remote)
		if _, ok := tree["claude/finance-pack/skills/run-variance/SKILL.md"]; !ok {
			t.Errorf("restricted view missing the credential-gated finance artifact; tree:\n%s", pubTreeKeys(tree))
		}
	})

	// Anonymous view: no credential resolves, so the finance artifact is
	// withheld and the finance-pack plugin renders nothing.
	t.Run("unresolved credential renders anonymous view", func(t *testing.T) {
		remote := bareRemote(t)
		ws := t.TempDir()
		cfg := writeSyncConfigMarketplace(t, ws, srv.URL, remote, "claude-code")
		// Scrub every credential source so the dispatch resolves no token.
		env := append(gitEnv(),
			"PODIUM_TOKEN=",
			"PODIUM_SESSION_TOKEN=",
			"PODIUM_SESSION_TOKEN_FILE=",
			"PODIUM_TOKEN_KEYCHAIN_NAME=podium-nonexistent-test-service",
		)
		res := runPodium(t, "", env, "sync", "--config", cfg)
		if res.Exit != 0 {
			t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
		}
		tree := remoteTree(t, remote)
		if _, ok := tree["claude/finance-pack/skills/run-variance/SKILL.md"]; ok {
			t.Errorf("anonymous view rendered the credential-gated finance artifact; tree:\n%s", pubTreeKeys(tree))
		}
	})
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
			ws := t.TempDir()
			cfg := writeSyncConfigMarketplace(t, ws, reg, remote, harness)
			res := runPodium(t, "", gitEnv(), "sync", "--config", cfg, "--check")
			if res.Exit != 2 {
				t.Fatalf("sync --check with harness %q exit=%d, want 2\nstdout=%s\nstderr=%s",
					harness, res.Exit, res.Stdout, res.Stderr)
			}
			if !strings.Contains(res.Stderr, "config.invalid") {
				t.Errorf("stderr missing 'config.invalid' for harness %q:\n%s", harness, res.Stderr)
			}
		})
	}
}

// A workflow command that exits non-zero is a runtime failure: the pipeline
// fails fast and the multi-target sync exits 1 (not 2, which is reserved for
// config errors). This drives a live marketplace render against the filesystem
// registry, then a publish command that exits non-zero.
func TestPublishing_WorkflowCommandFailureExits1(t *testing.T) {
	t.Parallel()
	reg := writePublishRegistry(t)
	ws := t.TempDir()
	cfg := writeSyncConfigFailingMarketplace(t, ws, reg)

	res := runPodium(t, "", gitEnv(), "sync", "--config", cfg)
	if res.Exit != 1 {
		t.Fatalf("sync --config (failing workflow command) exit=%d, want 1\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
}

// A kind: workspace target whose prepare workflow command exits non-zero fails
// before materialization and exits 1, surfacing the workspace workflow failure
// path (Decision 3).
func TestPublishing_WorkspacePrepareFailureExits1(t *testing.T) {
	t.Parallel()
	reg := writePublishRegistry(t)
	ws := t.TempDir()
	target := filepath.Join(ws, "out", "claude")
	cfg := writeSyncConfigWorkspaceFailingPrepare(t, ws, reg, target)

	res := runPodium(t, "", nil, "sync", "--config", cfg)
	if res.Exit != 1 {
		t.Fatalf("sync --config (failing workspace prepare) exit=%d, want 1\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	// The prepare failure ran before materialization, so the target carries no
	// sync.lock.
	if _, err := os.Stat(filepath.Join(target, ".podium", "sync.lock")); !os.IsNotExist(err) {
		t.Errorf("a prepare failure must abort before materialization (stat err=%v)", err)
	}
}

// A kind: workspace target whose publish workflow command exits non-zero fails
// after materialization and exits 1: the materialization wrote the target tree,
// and the post-sync publish phase failure surfaces as a runtime failure.
func TestPublishing_WorkspacePublishFailureExits1(t *testing.T) {
	t.Parallel()
	reg := writePublishRegistry(t)
	ws := t.TempDir()
	target := filepath.Join(ws, "out", "claude")
	cfg := writeSyncConfigWorkspaceFailingPublish(t, ws, reg, target)

	res := runPodium(t, "", nil, "sync", "--config", cfg)
	if res.Exit != 1 {
		t.Fatalf("sync --config (failing workspace publish) exit=%d, want 1\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	// The publish failure ran after materialization, so the target carries the
	// sync.lock.
	if _, err := os.Stat(filepath.Join(target, ".podium", "sync.lock")); err != nil {
		t.Errorf("the materialization must complete before the publish phase: %v", err)
	}
}

// An explicit --config path that does not exist is a config error and exits 2.
func TestPublishing_MissingConfigExits2(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), ".podium", "absent.yaml")
	res := runPodium(t, "", nil, "sync", "--config", missing)
	if res.Exit != 2 {
		t.Fatalf("sync --config (missing) exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
}

// A malformed sync.yaml is a config-load failure and exits 1, matching the
// sync.ReadConfigFile parse-error path.
func TestPublishing_MalformedConfigExits1(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "sync.yaml")
	if err := os.WriteFile(path, []byte("defaults:\n  registry: [not, a, string]\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res := runPodium(t, "", nil, "sync", "--config", path)
	if res.Exit != 1 {
		t.Fatalf("sync --config (malformed) exit=%d, want 1\nstderr=%s", res.Exit, res.Stderr)
	}
}

// `podium publish` is removed: marketplace publishing is a `kind: marketplace`
// target rendered by `podium sync --config` (§7.5.2, §7.8). The binary no longer
// registers `publish`, so the top-level dispatch reports it as an unknown command
// and exits 2.
func TestPublishing_PublishCommandRemoved(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "publish")
	if res.Exit != 2 {
		t.Fatalf("publish exit=%d, want 2\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown command: publish") {
		t.Fatalf("publish stderr=%q, want \"unknown command: publish\"", res.Stderr)
	}
}

// ---- config + stub helpers --------------------------------------------------

// writeSyncConfigFailingMarketplace writes a sync.yaml whose marketplace target
// declares a publish workflow whose only command exits non-zero, with no git
// remote so the render runs against the filesystem registry and the failing
// command surfaces a runtime failure.
func writeSyncConfigFailingMarketplace(t *testing.T, workspace, registry string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	path := filepath.Join(dir, "sync.yaml")
	mktWorkdir := filepath.Join(workspace, "build", "acme-agents")
	body := "" +
		"defaults:\n" +
		"  registry: " + registry + "\n" +
		"  identity: publisher@acme.com\n" +
		"targets:\n" +
		"  - id: acme-agents\n" +
		"    kind: marketplace\n" +
		"    target: " + mktWorkdir + "\n" +
		"    harnesses: [claude-code]\n" +
		"    plugins:\n" +
		"      - name: finance-pack\n" +
		"        include: [\"finance/**\"]\n" +
		"    workflow:\n" +
		"      publish:\n" +
		"        - run: [\"false\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
	return path
}

// writeSyncConfigWorkspaceFailingPrepare writes a sync.yaml with one kind:
// workspace target whose prepare workflow command exits non-zero, so the run
// fails before materialization.
func writeSyncConfigWorkspaceFailingPrepare(t *testing.T, workspace, registry, target string) string {
	t.Helper()
	return writeSyncConfigWorkspacePhaseFailure(t, workspace, registry, target, "prepare")
}

// writeSyncConfigWorkspaceFailingPublish writes a sync.yaml with one kind:
// workspace target whose publish workflow command exits non-zero, so the run
// fails after materialization.
func writeSyncConfigWorkspaceFailingPublish(t *testing.T, workspace, registry, target string) string {
	t.Helper()
	return writeSyncConfigWorkspacePhaseFailure(t, workspace, registry, target, "publish")
}

// writeSyncConfigWorkspacePhaseFailure writes a sync.yaml with one kind:
// workspace target whose named workflow phase declares a single command that
// exits non-zero.
func writeSyncConfigWorkspacePhaseFailure(t *testing.T, workspace, registry, target, phase string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	path := filepath.Join(dir, "sync.yaml")
	body := "" +
		"defaults:\n" +
		"  registry: " + registry + "\n" +
		"targets:\n" +
		"  - id: claude-workspace\n" +
		"    kind: workspace\n" +
		"    harness: claude-code\n" +
		"    target: " + target + "\n" +
		"    workflow:\n" +
		"      " + phase + ":\n" +
		"        - run: [\"false\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
	return path
}

// writeSyncConfigMixed writes a sync.yaml carrying a kind: marketplace target and
// a kind: workspace target, for the --check --config mixed-config test. Both
// targets share the filesystem registry.
func writeSyncConfigMixed(t *testing.T, workspace, registry, remote string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	path := filepath.Join(dir, "sync.yaml")
	mktWorkdir := filepath.Join(workspace, "build", "acme-agents")
	wsTarget := filepath.Join(workspace, "out", "claude")
	body := "" +
		"defaults:\n" +
		"  registry: " + registry + "\n" +
		"  identity: publisher@acme.com\n" +
		"targets:\n" +
		"  - id: claude-workspace\n" +
		"    kind: workspace\n" +
		"    harness: claude-code\n" +
		"    target: " + wsTarget + "\n" +
		"  - id: acme-agents\n" +
		"    kind: marketplace\n" +
		"    target: " + mktWorkdir + "\n" +
		"    git:\n" +
		"      remote: " + remote + "\n" +
		"      branch: main\n" +
		"    harnesses: [claude-code]\n" +
		"    plugins:\n" +
		"      - name: finance-pack\n" +
		"        include: [\"finance/**\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
	return path
}

// writeSyncConfigWorkspaceWorkflow writes a sync.yaml with one kind: workspace
// target carrying a workflow whose prepare command writes a "prepare-ran" marker
// holding $PODIUM_WORKDIR and whose publish command writes a "publish-ran"
// marker. The markers land in the workspace root so the test reads them after
// the run.
func writeSyncConfigWorkspaceWorkflow(t *testing.T, workspace, registry, target string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	path := filepath.Join(dir, "sync.yaml")
	prepMarker := filepath.Join(workspace, "prepare-ran")
	pubMarker := filepath.Join(workspace, "publish-ran")
	body := "" +
		"defaults:\n" +
		"  registry: " + registry + "\n" +
		"targets:\n" +
		"  - id: claude-workspace\n" +
		"    kind: workspace\n" +
		"    harness: claude-code\n" +
		"    target: " + target + "\n" +
		"    workflow:\n" +
		"      prepare:\n" +
		"        - sh: \"printf %s \\\"$PODIUM_WORKDIR\\\" > " + prepMarker + "\"\n" +
		"      publish:\n" +
		"        - sh: \"echo done > " + pubMarker + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
	return path
}

// writeSyncConfigWorkspaceSkipWorkflow writes a sync.yaml whose kind: workspace
// target carries a publish command that appends a line to counter and declares
// skip_if_no_changes. The command therefore runs only when the sync altered the
// target tree, so the counter records one append per changed sync.
func writeSyncConfigWorkspaceSkipWorkflow(t *testing.T, workspace, registry, target, counter string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	path := filepath.Join(dir, "sync.yaml")
	body := "" +
		"defaults:\n" +
		"  registry: " + registry + "\n" +
		"targets:\n" +
		"  - id: claude-workspace\n" +
		"    kind: workspace\n" +
		"    harness: claude-code\n" +
		"    target: " + target + "\n" +
		"    workflow:\n" +
		"      publish:\n" +
		"        - sh: \"echo changed >> " + counter + "\"\n" +
		"          skip_if_no_changes: true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
	return path
}

// countLines returns the number of newline-terminated lines in the file at path,
// or 0 when the file does not exist.
func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Count(string(b), "\n")
}

// newEffectiveViewStub returns an httptest server that serves the §7.5 sync HTTP
// API (GET /v1/sync/manifest and GET /v1/load_artifact). It gates one finance
// artifact behind the restricted bearer token: a request carrying
// "Authorization: Bearer <restrictedToken>" sees the finance artifact in the
// manifest, and an anonymous request does not. The render's effective-view fetch
// therefore differs by the credential the dispatch resolves (§4.6).
func newEffectiveViewStub(t *testing.T, restrictedToken string) *httptest.Server {
	t.Helper()
	// The artifacts the restricted principal sees. The anonymous principal sees
	// none, so the finance-pack plugin renders nothing.
	type art struct{ id, frontmatter, skill string }
	restricted := []art{
		{
			id:          "finance/close/run-variance",
			frontmatter: pubSkillArtifact,
			skill:       pubSkillBody("run-variance"),
		},
	}
	authorized := func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer "+restrictedToken
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/sync/manifest"):
			resp := map[string]any{"artifacts": []map[string]any{}}
			if authorized(r) {
				list := make([]map[string]any, 0, len(restricted))
				for _, a := range restricted {
					list = append(list, map[string]any{"id": a.id, "type": "skill", "version": "1.0.0", "layer": "team-finance"})
				}
				resp["artifacts"] = list
			}
			_ = json.NewEncoder(w).Encode(resp)
		case strings.HasPrefix(r.URL.Path, "/v1/load_artifact"):
			id := r.URL.Query().Get("id")
			for _, a := range restricted {
				if a.id == id && authorized(r) {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id":           a.id,
						"type":         "skill",
						"content_hash": "sha256:deadbeef",
						"layer":        "team-finance",
						"frontmatter":  a.frontmatter,
						"skill_raw":    a.skill,
					})
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "artifact.not_found", "message": "no such artifact"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv
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
