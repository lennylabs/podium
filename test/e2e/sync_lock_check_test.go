package e2e

import (
	"encoding/json"
	"path/filepath"
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
			ID      string `json:"id"`
			Version string `json:"version"`
			Type    string `json:"type"`
			Layer   string `json:"layer"`
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

// spec: §7.5.5 / §7.5.7 — the no-flag interactive (TUI) forms of
// `podium sync override` and `podium profile edit` are not available; they
// print a clear message and exit 2 rather than silently doing nothing
// (F-7.5.12).
func TestSync_InteractiveTUIDeferral(t *testing.T) {
	ws := t.TempDir()
	reg := cliReg(t)
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\n")

	ovr := runPodium(t, ws, nil, "sync", "override", "--target", t.TempDir())
	cliWantExit(t, ovr, 2, "override no-flags")
	cliContains(t, ovr.Stderr, "interactive override (TUI) is not available", "override TUI message")

	// profile edit with no name errors asking for a name.
	noName := runPodium(t, ws, nil, "profile", "edit", "--target", t.TempDir())
	cliWantExit(t, noName, 2, "profile edit no-name")
	cliContains(t, noName.Stderr, "profile name required", "name-required message")

	// profile edit with a name but no batch flags defers to the TUI.
	noFlags := runPodium(t, ws, nil, "profile", "edit", "team", "--target", t.TempDir())
	cliWantExit(t, noFlags, 2, "profile edit no-flags")
	cliContains(t, noFlags.Stderr, "interactive profile editing (TUI) is not available", "profile-edit TUI message")
}
