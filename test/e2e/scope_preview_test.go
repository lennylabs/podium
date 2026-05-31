package e2e

import (
	"strings"
	"testing"
)

// scopePreviewRegistry seeds two artifacts of different types so the §3.5
// aggregate counts have something non-trivial to report.
func scopePreviewRegistry(t *testing.T) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		"finance/glossary/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: finance glossary\nsensitivity: low\n---\n\nGlossary body.\n",
		"eng/runbook/ARTIFACT.md":      "---\ntype: rule\nversion: 1.0.0\ndescription: deploy runbook\nsensitivity: medium\n---\n\nRunbook body.\n",
	})
}

// Spec: §3.5 (F-3.5.2) — `podium status` surfaces the scope-preview aggregate
// counts for human inspection. End to end against a running standalone server.
func TestScopePreview_StatusSurfaces(t *testing.T) {
	t.Parallel()
	reg := scopePreviewRegistry(t)
	srv := startServer(t, reg)

	res := runPodium(t, "", []string{"HOME=" + t.TempDir()},
		"status", "--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Fatalf("status exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "scope preview:") {
		t.Fatalf("status missing scope preview section:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "artifacts:        2") {
		t.Errorf("status scope preview count wrong (want 2 distinct artifacts):\n%s", res.Stdout)
	}
	// The per-type and per-sensitivity breakdowns are present.
	if !strings.Contains(res.Stdout, "by type:") || !strings.Contains(res.Stdout, "by sensitivity:") {
		t.Errorf("status scope preview missing breakdowns:\n%s", res.Stdout)
	}
}

// Spec: §3.5 (F-3.5.4) — `podium sync --preview` prints the aggregate counts
// and writes nothing. End to end against a running standalone server.
func TestScopePreview_SyncPreview(t *testing.T) {
	t.Parallel()
	reg := scopePreviewRegistry(t)
	srv := startServer(t, reg)

	res := runPodium(t, "", []string{"HOME=" + t.TempDir()},
		"sync", "--preview", "--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Fatalf("sync --preview exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "scope preview:") {
		t.Fatalf("sync --preview missing scope preview header:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "artifacts:        2") {
		t.Errorf("sync --preview count wrong (want 2 distinct artifacts):\n%s", res.Stdout)
	}
}

// Spec: §3.5 (F-3.5.4) — the preview is served by GET /v1/scope/preview, so
// `podium sync --preview` against a filesystem-source registry is rejected.
func TestScopePreview_SyncPreviewFilesystemRejected(t *testing.T) {
	t.Parallel()
	reg := scopePreviewRegistry(t)

	res := runPodium(t, "", []string{"HOME=" + t.TempDir()},
		"sync", "--preview", "--registry", reg)
	if res.Exit != 2 {
		t.Errorf("sync --preview filesystem exit=%d, want 2\nstderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--preview requires a server registry") {
		t.Errorf("stderr missing filesystem-rejection message:\n%s", res.Stderr)
	}
}
