package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §7.3.1 (Removed artifacts) — through the binary: deleting an
// artifact's folder from a local layer and reingesting prints
// `removed: <id>` on standard output, exits 0, and stops the registry
// serving the artifact; restoring the folder and reingesting serves it again.
func TestRemovedArtifact_SoftDeletedAndRestoredThroughTheCLI(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	l := newRepublishLayer(t, srv, "ops")
	l.writeArtifact(t, versionSpec{ID: "ops/kept", Version: "1.0.0", Description: "Checklist for the weekly on-call handover"})
	l.publishVersion(t, versionSpec{ID: "ops/gone", Version: "1.0.0", Description: "Runbook for restarting the ingest worker"})

	saved, err := os.ReadFile(filepath.Join(l.dir, "ops", "gone", "ARTIFACT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(l.dir, "ops", "gone")); err != nil {
		t.Fatal(err)
	}
	ri := runPodium(t, "", srv.cliEnv(), "layer", "reingest", "--registry", srv.BaseURL, "ops")
	if ri.Exit != 0 || !strings.Contains(ri.Stdout, "removed: ops/gone\n") || strings.Contains(ri.Stdout, "removed: ops/kept") {
		t.Fatalf("reingest after deleting ops/gone: exit=%d stdout=%q stderr=%q, want exit 0 and removed: ops/gone alone", ri.Exit, ri.Stdout, ri.Stderr)
	}
	if show := runPodium(t, "", srv.cliEnv(), "artifact", "show", "ops/gone", "--registry", srv.BaseURL); show.Exit == 0 || !strings.Contains(show.Stderr, "registry.not_found") {
		t.Errorf("artifact show of a removed artifact: exit=%d stderr=%q, want registry.not_found", show.Exit, show.Stderr)
	}
	if show := runPodium(t, "", srv.cliEnv(), "artifact", "show", "ops/kept", "--registry", srv.BaseURL); show.Exit != 0 {
		t.Errorf("artifact show of a kept artifact: exit=%d stderr=%q", show.Exit, show.Stderr)
	}

	if err := os.MkdirAll(filepath.Join(l.dir, "ops", "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.dir, "ops", "gone", "ARTIFACT.md"), saved, 0o644); err != nil {
		t.Fatal(err)
	}
	ri = runPodium(t, "", srv.cliEnv(), "layer", "reingest", "--registry", srv.BaseURL, "ops")
	if ri.Exit != 0 || strings.Contains(ri.Stdout, "removed:") {
		t.Fatalf("reingest after restoring ops/gone: exit=%d stdout=%q stderr=%q", ri.Exit, ri.Stdout, ri.Stderr)
	}
	if show := runPodium(t, "", srv.cliEnv(), "artifact", "show", "ops/gone", "--json", "--registry", srv.BaseURL); show.Exit != 0 || !strings.Contains(show.Stdout, `"version": "1.0.0"`) {
		t.Errorf("artifact show after restoring: exit=%d stdout=%q stderr=%q, want version 1.0.0", show.Exit, show.Stdout, show.Stderr)
	}
}

// Spec: §7.3.1 (Removed artifacts), §4.7.6 — through the binary: a skill with
// no declared version whose folder returns unchanged after its removal is
// restored at its assigned version, and the restoring reingest exits 0.
func TestRemovedArtifact_AutoVersionedSkillRestoresThroughTheCLI(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	l := newRepublishLayer(t, srv, "ops")
	l.writeArtifact(t, versionSpec{ID: "ops/kept", Version: "1.0.0", Description: "Checklist for the weekly on-call handover"})
	skillDir := filepath.Join(l.dir, "ops", "greet")
	skill := []byte("---\nname: greet\ndescription: Greet the user at the start of every session.\n---\n\nGreet the user.\n")
	writeSkill := func() {
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), skill, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reingest := func() cliResult {
		return runPodium(t, "", srv.cliEnv(), "layer", "reingest", "--registry", srv.BaseURL, "ops")
	}
	writeSkill()
	if ri := reingest(); ri.Exit != 0 {
		t.Fatalf("first reingest: exit=%d stderr=%q", ri.Exit, ri.Stderr)
	}
	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatal(err)
	}
	if ri := reingest(); ri.Exit != 0 || !strings.Contains(ri.Stdout, "removed: ops/greet\n") {
		t.Fatalf("reingest after deleting ops/greet: exit=%d stdout=%q stderr=%q", ri.Exit, ri.Stdout, ri.Stderr)
	}
	writeSkill()
	if ri := reingest(); ri.Exit != 0 || strings.Contains(ri.Stderr, "auto_version_duplicate_content") {
		t.Fatalf("reingest after restoring ops/greet: exit=%d stdout=%q stderr=%q, want exit 0 and no rejection", ri.Exit, ri.Stdout, ri.Stderr)
	}
	if show := runPodium(t, "", srv.cliEnv(), "artifact", "show", "ops/greet", "--json", "--registry", srv.BaseURL); show.Exit != 0 || !strings.Contains(show.Stdout, `"version": "0.0.0-auto.0"`) {
		t.Errorf("artifact show after restoring: exit=%d stdout=%q stderr=%q, want version 0.0.0-auto.0", show.Exit, show.Stdout, show.Stderr)
	}
}

// Spec: §7.3.1 (Removed artifacts) — through the binary: moving a removed
// artifact's folder into another layer is refused as an immutability
// violation until the purge, and the artifact is not served.
func TestRemovedArtifact_MoveToAnotherLayerIsRefusedThroughTheCLI(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	from := newRepublishLayer(t, srv, "from")
	to := newRepublishLayer(t, srv, "to")
	from.publishVersion(t, versionSpec{ID: "ops/move", Version: "1.0.0", Description: "Runbook for restarting the ingest worker"})
	src := filepath.Join(from.dir, "ops", "move")
	dst := filepath.Join(to.dir, "ops", "move")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	if ri := runPodium(t, "", srv.cliEnv(), "layer", "reingest", "--registry", srv.BaseURL, "from"); ri.Exit != 0 || !strings.Contains(ri.Stdout, "removed: ops/move\n") {
		t.Fatalf("reingest of the source layer: exit=%d stdout=%q stderr=%q", ri.Exit, ri.Stdout, ri.Stderr)
	}
	ri := runPodium(t, "", srv.cliEnv(), "layer", "reingest", "--registry", srv.BaseURL, "to")
	if ri.Exit == 0 || !strings.Contains(ri.Stderr, "ingest.immutable_violation") {
		t.Fatalf("reingest of the destination layer: exit=%d stdout=%q stderr=%q, want a non-zero exit naming ingest.immutable_violation", ri.Exit, ri.Stdout, ri.Stderr)
	}
	if show := runPodium(t, "", srv.cliEnv(), "artifact", "show", "ops/move", "--registry", srv.BaseURL); show.Exit == 0 || !strings.Contains(show.Stderr, "registry.not_found") {
		t.Errorf("artifact show of the moved artifact: exit=%d stderr=%q, want registry.not_found", show.Exit, show.Stderr)
	}
}

// Spec: §7.3.1 (Removed artifacts) — through the binary: a reingest refused
// with ingest.lint_failed after the cycle read the whole snapshot still prints
// removed: <id> for the artifact the cycle removed.
func TestRemovedArtifact_RefusedCycleReportsRemovalsThroughTheCLI(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	l := newRepublishLayer(t, srv, "ops")
	l.writeArtifact(t, versionSpec{ID: "ops/kept", Version: "1.0.0", Description: "Checklist for the weekly on-call handover"})
	l.publishVersion(t, versionSpec{ID: "ops/gone", Version: "1.0.0", Description: "Runbook for restarting the ingest worker"})
	if err := os.RemoveAll(filepath.Join(l.dir, "ops", "gone")); err != nil {
		t.Fatal(err)
	}
	// ops/kept loses its declared version, which lint refuses, so the cycle
	// stores nothing and is refused as a whole.
	if err := os.WriteFile(filepath.Join(l.dir, "ops", "kept", "ARTIFACT.md"),
		[]byte("---\ntype: agent\ndescription: Checklist for the weekly on-call handover\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ri := runPodium(t, "", srv.cliEnv(), "layer", "reingest", "--registry", srv.BaseURL, "ops")
	if ri.Exit == 0 || !strings.Contains(ri.Stderr, "ingest.lint_failed") || !strings.Contains(ri.Stdout, "removed: ops/gone\n") {
		t.Fatalf("refused reingest: exit=%d stdout=%q stderr=%q, want a non-zero exit, ingest.lint_failed, and removed: ops/gone", ri.Exit, ri.Stdout, ri.Stderr)
	}
}
