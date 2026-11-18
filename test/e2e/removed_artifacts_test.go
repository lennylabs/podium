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
