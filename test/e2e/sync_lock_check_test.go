package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §7.5 — the --dry-run --json envelope is
// {profile, target, harness, scope, artifacts: [{id, version, type, layer}]}
// (F-7.5.9). A jq consumer reads .harness, .scope.include, and
// .artifacts[].version directly.
func TestSync_DryRunJSONEnvelope(t *testing.T) {
	reg := cliReg(t)
	ws := t.TempDir()
	tgt := t.TempDir()
	writeWorkspaceConfig(t, ws,
		"defaults:\n  registry: "+reg+"\n  harness: none\n"+
			"profiles:\n  finance:\n    include: [\"finance/**\"]\n")

	res := runPodium(t, ws, nil, "sync", "--profile", "finance", "--target", tgt, "--dry-run", "--json")
	cliWantExit(t, res, 0, "sync --dry-run --json")

	var env struct {
		Profile string `json:"profile"`
		Target  string `json:"target"`
		Harness string `json:"harness"`
		Scope   struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
			Type    []string `json:"type"`
		} `json:"scope"`
		Artifacts []struct {
			ID          string `json:"id"`
			Version     string `json:"version"`
			ContentHash string `json:"content_hash"`
			Type        string `json:"type"`
			Layer       string `json:"layer"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("envelope not valid JSON: %v\n%s", err, res.Stdout)
	}
	if env.Profile != "finance" || env.Harness != "none" {
		t.Errorf("envelope profile/harness = %q/%q, want finance/none", env.Profile, env.Harness)
	}
	if len(env.Scope.Include) != 1 || env.Scope.Include[0] != "finance/**" {
		t.Errorf("scope.include = %v", env.Scope.Include)
	}
	if len(env.Artifacts) == 0 {
		t.Fatalf("no artifacts in envelope:\n%s", res.Stdout)
	}
	a := env.Artifacts[0]
	if a.ID != "finance/invoice" || a.Version != "1.0.0" || a.Type != "context" {
		t.Errorf("artifact = %+v, want finance/invoice 1.0.0 context", a)
	}
	// spec: §7.5 / §14.11 (F-14.11.3) — each artifact carries content_hash so a
	// pre-flight check can verify the full (id, version, content_hash) triple.
	if !strings.HasPrefix(a.ContentHash, "sha256:") {
		t.Errorf("artifact content_hash = %q, want sha256: prefix", a.ContentHash)
	}
}

// spec: §7.5.3 — last_synced_by is "full" for a manual one-shot sync and
// "override" after `podium sync override` re-materializes (F-7.5.7).
func TestSync_LastSyncedByProvenance(t *testing.T) {
	reg := cliReg(t)
	ws := t.TempDir()
	tgt := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\n")

	res := runPodium(t, ws, nil, "sync", "--target", tgt, "--harness", "none")
	cliWantExit(t, res, 0, "manual sync")
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliContains(t, lock, "last_synced_by: full", "manual sync stamps full")

	// An override that re-materializes stamps "override".
	ovr := runPodium(t, ws, nil, "sync", "override", "--target", tgt,
		"--add", "personal/note", "--registry", reg, "--harness", "none")
	cliWantExit(t, ovr, 0, "sync override --add")
	lock2 := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliContains(t, lock2, "last_synced_by: override", "override stamps override")
}

// spec: §7.5.2 — `podium sync --check` validates the merged config and reports
// warnings (unresolved profiles, malformed globs, collisions) without erroring
// (F-7.5.10).
func TestSync_CheckReportsWarnings(t *testing.T) {
	ws := t.TempDir()
	// defaults.profile references a missing profile, and a profile has a
	// malformed glob.
	writeWorkspaceConfig(t, ws,
		"defaults:\n  registry: /reg\n  profile: ghost\n"+
			"profiles:\n  finance:\n    include: [\"finance/[\"]\n")

	res := runPodium(t, ws, nil, "sync", "--check")
	cliWantExit(t, res, 0, "sync --check (warnings are not errors)")
	cliContains(t, res.Stderr, "undefined profile \"ghost\"", "unresolved profile warning")
	cliContains(t, res.Stderr, "malformed glob", "malformed glob warning")
}

// spec: §7.5.5 / §7.5.7 (F-7.5.1) — the no-flag interactive (TUI) forms of
// `podium sync override` and `podium profile edit` launch a checklist / editor
// driven from stdin. Scripted commands write the same result the batch flags
// produce. `podium profile edit` with no name still errors asking for a name.
func TestSync_InteractiveTUIApplies(t *testing.T) {
	reg := cliReg(t)

	// override no-flags: leaf 1 is finance/invoice (IDs sort
	// finance/invoice, personal/greet, personal/note). Checking it and saving
	// materializes the artifact and records it in toggles.add, like `--add`.
	tgt := t.TempDir()
	ovr := runPodiumStdin(t, "", nil, "1\nsave\n",
		"sync", "override", "--target", tgt, "--registry", reg, "--harness", "none")
	cliWantExit(t, ovr, 0, "override TUI add")
	if _, err := os.Stat(filepath.Join(tgt, "finance/invoice/ARTIFACT.md")); err != nil {
		t.Errorf("checked artifact not materialized: %v", err)
	}
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliContains(t, lock, "finance/invoice", "override TUI records the toggle in the lock")

	// profile edit with no name still errors asking for a name.
	noName := runPodium(t, "", nil, "profile", "edit", "--target", t.TempDir())
	cliWantExit(t, noName, 2, "profile edit no-name")
	cliContains(t, noName.Stderr, "profile name required", "name-required message")

	// profile edit <name> no-flags: scripted add-include writes the pattern.
	pdir := t.TempDir()
	pe := runPodiumStdin(t, "", nil, "add-include finance/**\nsave\n",
		"profile", "edit", "team", "--target", pdir)
	cliWantExit(t, pe, 0, "profile edit TUI add-include")
	cfg := readFile(t, filepath.Join(pdir, ".podium/sync.yaml"))
	cliContains(t, cfg, "finance/**", "profile edit TUI writes the include pattern")
}
