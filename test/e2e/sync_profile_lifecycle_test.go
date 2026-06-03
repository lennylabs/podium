package e2e

import (
	"path/filepath"
	"testing"
)

// spec: §7.5.3 — the lock's `profile:` field is "null when no profile was
// used". A sync with no active profile records the explicit `profile: null`
// (F-7.5.4).
func TestSync_NoProfileLockRendersNull(t *testing.T) {
	reg := cliReg(t)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	cliWantExit(t, res, 0, "sync with no profile")
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliContains(t, lock, "profile: null", "no-profile lock renders profile: null")
}

// spec: §7.5.5 — "running --add on something already materialized is a no-op
// with a warning." A redundant override --add warns on stderr and records no
// toggle (F-7.5.3).
func TestSyncOverride_RedundantAddWarns(t *testing.T) {
	reg := cliReg(t)
	tgt := t.TempDir()
	// Baseline materializes personal/greet and personal/note.
	cliWantExit(t, runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt,
		"--harness", "none", "--include", "personal/**"), 0, "baseline sync")
	// personal/greet is already materialized, so --add is a no-op with a warning.
	ovr := runPodium(t, "", nil, "sync", "override", "--add", "personal/greet",
		"--registry", reg, "--harness", "none", "--target", tgt)
	cliWantExit(t, ovr, 0, "redundant override --add")
	cliContains(t, ovr.Stderr, "already materialized", "redundant --add warns")
	// No toggle was recorded: the toggles section stays absent.
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliNotContains(t, lock, "toggles:", "redundant --add records no toggle")
}

// spec: §7.5.6 / §11 "Save-as test" — save-as on a fresh sync.yaml creates it
// with the new profile and an empty defaults: block (F-7.5.2), and the saved
// profile becomes the target's active profile (F-11.0.1).
func TestSyncSaveAs_FreshFileDefaultsAndActivation(t *testing.T) {
	reg := cliReg(t)
	tgt := t.TempDir()
	cliWantExit(t, runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt,
		"--harness", "none", "--include", "personal/**"), 0, "baseline sync")
	// The target has no sync.yaml yet, so save-as creates it fresh.
	sa := runPodium(t, "", nil, "sync", "save-as", "--profile", "team", "--target", tgt)
	cliWantExit(t, sa, 0, "save-as on fresh sync.yaml")
	cfg := readFile(t, filepath.Join(tgt, ".podium/sync.yaml"))
	cliContains(t, cfg, "defaults:", "fresh save-as emits a defaults: block")
	cliContains(t, cfg, "team:", "save-as writes the new profile")
	// The new profile is now the target's active profile.
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliContains(t, lock, "profile: team", "save-as activates the saved profile")
}
