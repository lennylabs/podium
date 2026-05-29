package e2e

// End-to-end tests for docs/authoring/your-first-agent.md (D-first-agent).
// The tutorial builds a commit-message-writer agent: a minimal agent that
// materializes to .claude/agents/<name>.md, then a runtime requirement, a
// bundled helper script under .claude/podium/<id>/, and a delegates_to
// edge surfaced through the dependency graph. Tests drive the podium CLI,
// the standalone registry server, and (for the runtime check) the
// materialize package directly.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/materialize"
)

const cmwID = "personal/dev-loop/commit-message-writer"

const cmwMinimalBody = "You write conventional-commit messages.\n\n" +
	"Read the staged diff using your shell tools. Identify the dominant change type.\n"

// staged-diff.sh from the doc's "Part 3: ship a helper script".
const cmwStagedDiff = "#!/usr/bin/env bash\n" +
	"# Print the staged diff, ignoring whitespace and lock-file noise.\n" +
	"git diff --cached --ignore-all-space -- ':!**/*.lock' ':!**/package-lock.json'\n"

// cmwAgent builds the commit-message-writer ARTIFACT.md with the given
// version, extra frontmatter lines, and prose body. The universal fields
// match the doc's Part 1 frontmatter.
func cmwAgent(version, extraFM, body string) string {
	return "---\n" +
		"type: agent\n" +
		"name: commit-message-writer\n" +
		"version: " + version + "\n" +
		"description: Draft a conventional-commit message from the currently staged diff.\n" +
		"when_to_use:\n" +
		"  - \"Right before committing, when the staged diff needs a tight message.\"\n" +
		"tags: [git, commit, dev-loop]\n" +
		"sensitivity: low\n" +
		extraFM +
		"---\n\n" + body
}

// cmwFullExtraFM is the Part 4 full-agent frontmatter addition: a system
// package requirement and a delegates_to edge.
const cmwFullExtraFM = "runtime_requirements:\n" +
	"  system_packages: [git]\n" +
	"delegates_to:\n" +
	"  - personal/dev-loop/conventional-commits@1.x\n"

// T-D-first-agent-1 — the minimal agent materializes to
// .claude/agents/<name>.md with the ARTIFACT.md bytes verbatim.
func TestFirstAgent_MinimalMaterializes(t *testing.T) {
	t.Parallel()
	art := cmwAgent("1.0.0", "", cmwMinimalBody)
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": art})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, ".claude/agents/commit-message-writer.md"))
	if got != art {
		t.Errorf("materialized agent != ARTIFACT.md bytes:\nwant %q\n got %q", art, got)
	}
	for _, bad := range []string{".claude/skills/commit-message-writer", ".claude/podium"} {
		if _, err := os.Stat(filepath.Join(tgt, bad)); err == nil {
			t.Errorf("unexpected path materialized: %s", bad)
		}
	}
	if !strings.Contains(res.Stdout, cmwID) {
		t.Errorf("stdout missing artifact id:\n%s", res.Stdout)
	}
}

// T-D-first-agent-2 — the minimal agent lints with no issues (no SKILL.md
// is required for type: agent).
func TestFirstAgent_MinimalLintsClean(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.0.0", "", cmwMinimalBody)})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// T-D-first-agent-3 — an agent missing version fails lint.
func TestFirstAgent_MissingVersionFails(t *testing.T) {
	t.Parallel()
	art := "---\ntype: agent\nname: commit-message-writer\ndescription: x\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": art})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, cmwID) {
		t.Errorf("missing required_field_missing diagnostic for %s:\n%s", cmwID, res.Stdout)
	}
}

// T-D-first-agent-4 — an agent with a non-semver version fails lint.
func TestFirstAgent_NonSemverFails(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("not-a-version", "", cmwMinimalBody)})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_version") {
		t.Errorf("missing lint.invalid_version:\n%s", res.Stdout)
	}
}

// T-D-first-agent-5 — sync against a non-existent registry path exits non-zero.
func TestFirstAgent_SyncMissingRegistry(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope")
	res := runPodium(t, "", nil, "sync", "--registry", missing, "--target", t.TempDir(), "--harness", "claude-code")
	if res.Exit != 1 {
		t.Fatalf("exit=%d, want 1\nstderr=%s", res.Exit, res.Stderr)
	}
	if res.Stderr == "" {
		t.Errorf("expected an error message on stderr")
	}
}

// T-D-first-agent-6 — an agent's bundled resources land under
// .claude/podium/<artifact-id>/ for claude-code. spec: doc "Part 3".
func TestFirstAgent_BundledResourcePath(t *testing.T) {
	t.Parallel()
	body := cmwMinimalBody + "\nRead the staged diff by running [scripts/staged-diff.sh](scripts/staged-diff.sh).\n"
	reg := writeRegistry(t, map[string]string{
		cmwID + "/ARTIFACT.md":            cmwAgent("1.1.0", "", body),
		cmwID + "/scripts/staged-diff.sh": cmwStagedDiff,
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/agents/commit-message-writer.md"))
	scriptPath := filepath.Join(tgt, ".claude/podium", cmwID, "scripts/staged-diff.sh")
	if got := readFile(t, scriptPath); got != cmwStagedDiff {
		t.Errorf("bundled script not materialized verbatim at %s:\n%q", scriptPath, got)
	}
}

// T-D-first-agent-7 — a resolved markdown-link prose reference lints clean.
// spec: doc "Part 3" (broken paths fail lint).
func TestFirstAgent_ProseRefResolves(t *testing.T) {
	t.Parallel()
	body := cmwMinimalBody + "\nRead the staged diff by running [scripts/staged-diff.sh](scripts/staged-diff.sh).\n"
	reg := writeRegistry(t, map[string]string{
		cmwID + "/ARTIFACT.md":            cmwAgent("1.1.0", "", body),
		cmwID + "/scripts/staged-diff.sh": cmwStagedDiff,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-first-agent-8 — a markdown-link reference to a missing bundled file
// fails lint. spec: doc "Part 3".
func TestFirstAgent_ProseRefBrokenFails(t *testing.T) {
	t.Parallel()
	body := cmwMinimalBody + "\nRead the staged diff by running [staged-diff](scripts/staged-diff.sh).\n"
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.1.0", "", body)})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.prose_reference") || !strings.Contains(res.Stdout, "staged-diff.sh") {
		t.Errorf("missing prose_reference error for staged-diff.sh:\n%s", res.Stdout)
	}
}

// T-D-first-agent-9 — runtime_requirements (system_packages) round-trips
// through ARTIFACT.md after a none sync. spec: doc "Part 2".
func TestFirstAgent_RuntimeRequirementsRoundTrip(t *testing.T) {
	t.Parallel()
	extra := "runtime_requirements:\n  system_packages: [git]\n"
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.0.0", extra, cmwMinimalBody)})
	if res := runPodium(t, "", nil, "lint", "--registry", reg); res.Exit != 0 {
		t.Fatalf("lint exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(tgt, cmwID, "ARTIFACT.md"))
	if !strings.Contains(got, "system_packages:") || !strings.Contains(got, "git") {
		t.Errorf("materialized ARTIFACT.md missing system_packages/git:\n%s", got)
	}
}

// T-D-first-agent-10 — CheckRuntimeRequirements reports ErrRuntimeUnavailable
// when a required system package is absent. spec: doc "Part 2",
// materialize.CheckRuntimeRequirements.
func TestFirstAgent_RuntimeUnavailableWhenGitMissing(t *testing.T) {
	t.Parallel()
	err := materialize.CheckRuntimeRequirements(
		map[string]any{"system_packages": []string{"git"}},
		materialize.HostCapabilities{SystemPackages: []string{}},
	)
	if err == nil {
		t.Fatalf("expected an error when git is missing")
	}
	if !errors.Is(err, materialize.ErrRuntimeUnavailable) {
		t.Errorf("error %v is not ErrRuntimeUnavailable", err)
	}
	if !strings.Contains(err.Error(), "git") {
		t.Errorf("error %q does not name the missing package", err)
	}
}

// T-D-first-agent-11 — CheckRuntimeRequirements returns nil when the host
// provides the required package.
func TestFirstAgent_RuntimeAvailableWhenGitPresent(t *testing.T) {
	t.Parallel()
	err := materialize.CheckRuntimeRequirements(
		map[string]any{"system_packages": []string{"git"}},
		materialize.HostCapabilities{SystemPackages: []string{"git"}},
	)
	if err != nil {
		t.Errorf("expected nil when host has git, got %v", err)
	}
}

// T-D-first-agent-12 — scaffold --delegates-to writes a delegates_to block.
// spec: doc "Part 4".
func TestFirstAgent_ScaffoldDelegatesTo(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), cmwID)
	res := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "agent",
		"--description", "Draft a conventional-commit message.",
		"--delegates-to", "personal/dev-loop/conventional-commits@1.x",
		"--yes", out)
	if res.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "delegates_to:") || !strings.Contains(art, "personal/dev-loop/conventional-commits@1.x") {
		t.Errorf("ARTIFACT.md missing delegates_to block:\n%s", art)
	}
}

// T-D-first-agent-13 — the delegates_to edge is indexed and surfaced by
// /v1/dependents on the standalone server. spec: doc "Part 4".
func TestFirstAgent_DelegatesEdgeIndexed(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/dev-loop/conventional-commits/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"personal/dev-loop/conventional-commits/SKILL.md":    skillBody("conventional-commits"),
		cmwID + "/ARTIFACT.md":                               cmwAgent("1.1.0", cmwFullExtraFM, cmwMinimalBody),
	})
	srv := startServer(t, reg)
	var resp struct {
		Edges []struct {
			From, To, Kind string
		} `json:"edges"`
	}
	getJSON(t, srv.BaseURL+"/v1/dependents?id=personal/dev-loop/conventional-commits", &resp)
	found := false
	for _, e := range resp.Edges {
		if e.From == cmwID && e.To == "personal/dev-loop/conventional-commits" && e.Kind == "delegates_to" {
			found = true
		}
	}
	if !found {
		t.Errorf("delegates_to edge not found in dependents: %+v", resp.Edges)
	}
}

// T-D-first-agent-14 — podium impact lists the agent as a dependent of the
// delegated artifact. spec: doc "Part 4".
func TestFirstAgent_ImpactListsDependent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/dev-loop/conventional-commits/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- body -->\n",
		"personal/dev-loop/conventional-commits/SKILL.md":    skillBody("conventional-commits"),
		cmwID + "/ARTIFACT.md":                               cmwAgent("1.1.0", cmwFullExtraFM, cmwMinimalBody),
	})
	srv := startServer(t, reg)
	res := runPodium(t, "", nil, "impact", "--registry", srv.BaseURL, "personal/dev-loop/conventional-commits")
	if res.Exit != 0 {
		t.Fatalf("impact exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, cmwID) || !strings.Contains(res.Stdout, "delegates_to") {
		t.Errorf("impact stdout missing the dependent edge:\n%s", res.Stdout)
	}
}

// T-D-first-agent-15 — the full agent (script + delegates_to) lints clean.
// spec: doc "The full agent".
func TestFirstAgent_FullLintsClean(t *testing.T) {
	t.Parallel()
	body := cmwMinimalBody + "\nRead the staged diff by running [scripts/staged-diff.sh](scripts/staged-diff.sh).\n"
	reg := writeRegistry(t, map[string]string{
		cmwID + "/ARTIFACT.md":            cmwAgent("1.1.0", cmwFullExtraFM, body),
		cmwID + "/scripts/staged-diff.sh": cmwStagedDiff,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Fatalf("lint exit=%d stdout=%q stderr=%q", res.Exit, res.Stdout, res.Stderr)
	}
}

// T-D-first-agent-16 — the full agent materializes both the manifest under
// .claude/agents/ and the script under .claude/podium/<id>/. spec: doc
// "The full agent".
func TestFirstAgent_FullMaterializes(t *testing.T) {
	t.Parallel()
	body := cmwMinimalBody + "\nRead the staged diff by running [scripts/staged-diff.sh](scripts/staged-diff.sh).\n"
	reg := writeRegistry(t, map[string]string{
		cmwID + "/ARTIFACT.md":            cmwAgent("1.1.0", cmwFullExtraFM, body),
		cmwID + "/scripts/staged-diff.sh": cmwStagedDiff,
	})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/agents/commit-message-writer.md"))
	mustExist(t, filepath.Join(tgt, ".claude/podium", cmwID, "scripts/staged-diff.sh"))
	for _, bad := range []string{".claude/skills", ".claude/rules"} {
		if _, err := os.Stat(filepath.Join(tgt, bad)); err == nil {
			t.Errorf("unexpected %s for an agent", bad)
		}
	}
}

// T-D-first-agent-17 — sync --dry-run reports the agent but writes nothing.
func TestFirstAgent_DryRun(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.0.0", "", cmwMinimalBody)})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code", "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, cmwID) {
		t.Fatalf("dry-run exit=%d stdout=%q", res.Exit, res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(tgt, ".claude/agents/commit-message-writer.md")); err == nil {
		t.Errorf("dry-run wrote the agent file")
	}
}

// T-D-first-agent-18 — sync --json emits an envelope naming the adapter and
// the agent.
func TestFirstAgent_SyncJSON(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.0.0", "", cmwMinimalBody)})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code", "--json")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	var env struct {
		Adapter   string `json:"adapter"`
		Artifacts []struct {
			ID string `json:"id"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, res.Stdout)
	}
	if env.Adapter != "claude-code" {
		t.Errorf("adapter=%q, want claude-code", env.Adapter)
	}
	found := false
	for _, a := range env.Artifacts {
		if a.ID == cmwID {
			found = true
		}
	}
	if !found {
		t.Errorf("artifacts missing %s: %+v", cmwID, env.Artifacts)
	}
}

// T-D-first-agent-19 — an agent missing the type field fails lint.
func TestFirstAgent_MissingType(t *testing.T) {
	t.Parallel()
	art := "---\nname: commit-message-writer\nversion: 1.0.0\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": art})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.required_field_missing") || !strings.Contains(res.Stdout, "type is required") {
		t.Errorf("missing 'type is required' diagnostic:\n%s", res.Stdout)
	}
}

// T-D-first-agent-20 — an agent name containing an underscore fails lint
// with lint.invalid_name. spec: ruleNameSyntax.
func TestFirstAgent_InvalidNameUnderscore(t *testing.T) {
	t.Parallel()
	art := "---\ntype: agent\nname: commit_message_writer\nversion: 1.0.0\ndescription: x\n---\n\nbody\n"
	reg := writeRegistry(t, map[string]string{"personal/dev-loop/commit_message_writer/ARTIFACT.md": art})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 1 {
		t.Fatalf("lint exit=%d, want 1\nstdout=%s", res.Exit, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint.invalid_name") {
		t.Errorf("missing lint.invalid_name:\n%s", res.Stdout)
	}
}

// T-D-first-agent-21 — scaffold produces a lint-clean agent ARTIFACT.md.
func TestFirstAgent_ScaffoldLintsClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	out := filepath.Join(root, cmwID)
	sc := runPodium(t, "", nil, "artifact", "scaffold",
		"--type", "agent",
		"--description", "Draft a conventional-commit message from the currently staged diff.",
		"--tags", "git,commit,dev-loop",
		"--sensitivity", "low",
		"--when-to-use", "Right before committing",
		"--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	if art := readFile(t, filepath.Join(out, "ARTIFACT.md")); !strings.Contains(art, "type: agent") {
		t.Errorf("scaffolded ARTIFACT.md missing type: agent:\n%s", art)
	}
	lint := runPodium(t, "", nil, "lint", "--registry", root)
	if lint.Exit != 0 || !strings.Contains(lint.Stdout, "lint: no issues.") {
		t.Errorf("scaffolded agent did not lint clean: exit=%d stdout=%q", lint.Exit, lint.Stdout)
	}
}

// T-D-first-agent-22 — sync with an unknown harness exits non-zero.
func TestFirstAgent_UnknownHarness(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.0.0", "", cmwMinimalBody)})
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", t.TempDir(), "--harness", "unknown-harness-xyz")
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit\nstdout=%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "config.unknown_harness") && !strings.Contains(res.Stderr, "unknown-harness-xyz") {
		t.Errorf("stderr missing unknown-harness signal: %q", res.Stderr)
	}
}

// T-D-first-agent-23 — a backtick (non-link) prose reference is NOT scanned
// by lint.prose_reference, so a missing target passes. Documents the gap
// between the doc's backtick prose and the markdown-link lint rule.
func TestFirstAgent_BacktickRefNotScanned(t *testing.T) {
	t.Parallel()
	body := cmwMinimalBody + "\nRead the staged diff by running `scripts/staged-diff.sh`.\n"
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.1.0", "", body)})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0 (backtick refs are not scanned)\nstdout=%s", res.Exit, res.Stdout)
	}
	if strings.Contains(res.Stdout, "lint.prose_reference") {
		t.Errorf("backtick reference unexpectedly triggered prose_reference:\n%s", res.Stdout)
	}
}

// T-D-first-agent-24 — sync removes a stale .claude/agents/<name>.md when
// the artifact is deleted from the registry. spec: §7.5 stale cleanup.
func TestFirstAgent_StaleCleanup(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.0.0", "", cmwMinimalBody)})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("first sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	agentFile := filepath.Join(tgt, ".claude/agents/commit-message-writer.md")
	mustExist(t, agentFile)
	if err := os.RemoveAll(filepath.Join(reg, cmwID)); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code"); res.Exit != 0 {
		t.Fatalf("second sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if _, err := os.Stat(agentFile); err == nil {
		t.Errorf("stale agent file survived after the artifact was removed")
	}
}

// T-D-first-agent-25 — an unresolvable delegates_to target does not block a
// filesystem sync; no filesystem lint rule validates the edge. Documents
// the doc-accuracy gap (the existence/type/version checks the doc
// describes apply on server ingest, not filesystem lint/sync).
func TestFirstAgent_UnresolvedDelegateDoesNotBlockSync(t *testing.T) {
	t.Parallel()
	extra := "delegates_to:\n  - personal/dev-loop/conventional-commits@1.x\n"
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.0.0", extra, cmwMinimalBody)})
	lint := runPodium(t, "", nil, "lint", "--registry", reg)
	if lint.Exit != 0 {
		t.Errorf("lint exit=%d, want 0 (no filesystem rule validates delegates_to)\nstdout=%s", lint.Exit, lint.Stdout)
	}
	tgt := t.TempDir()
	sync := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	if sync.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", sync.Exit, sync.Stderr)
	}
	mustExist(t, filepath.Join(tgt, ".claude/agents/commit-message-writer.md"))
}

// T-D-first-agent-26 — the none harness writes the canonical agent layout.
func TestFirstAgent_NoneCanonical(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{cmwID + "/ARTIFACT.md": cmwAgent("1.0.0", "", cmwMinimalBody)})
	tgt := t.TempDir()
	if res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none"); res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, cmwID, "ARTIFACT.md"))
	if _, err := os.Stat(filepath.Join(tgt, ".claude/agents")); err == nil {
		t.Errorf(".claude/agents created under --harness none")
	}
}
