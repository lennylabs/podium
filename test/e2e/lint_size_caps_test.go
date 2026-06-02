package e2e

// End-to-end coverage for the configurable bundled-resource size caps
// (spec §12, F-12.0.2). The standalone server builds its ingest linter via
// NewIngestLinter, which reads the per-file and per-package soft caps from
// PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES / PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES.
// A lowered per-package cap turns a package that is well under the 10 MB
// default into an ingest-time lint error, so the artifact is rejected and is
// not loadable; with the default cap the same artifact loads.

import (
	"strings"
	"testing"
)

// spec: §12 (F-12.0.2) — a per-package soft cap lowered via the environment
// rejects a bundled package over that cap at standalone-boot ingest, while a
// clean sibling still loads. The default cap accepts the same package.
func TestLintSizeCaps_ConfigurablePerPackageCapRejects(t *testing.T) {
	// 8 KB bundled resource: under the 10 MB default, over a 4 KB cap.
	bigResource := strings.Repeat("x", 8*1024)
	files := map[string]string{
		"finance/big/ARTIFACT.md":   "---\ntype: context\nversion: 1.0.0\ndescription: Oversized bundled package reference content here.\nsensitivity: low\n---\n\nbody\n",
		"finance/big/data.bin":      bigResource,
		"finance/small/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: A small clean reference artifact for coverage here.\nsensitivity: low\n---\n\nbody\n",
	}

	// Lowered per-package cap: the big package is rejected, the small one loads.
	regLowered := writeRegistry(t, files)
	srv := startServerArgs(t,
		[]string{"HOME=" + t.TempDir(), "PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES=4096"},
		"serve", "--standalone", "--layer-path", regLowered)
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/small"); st != 200 {
		t.Errorf("clean artifact load = %d, want 200", st)
	}
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/big"); st != 404 {
		t.Errorf("oversized package load = %d, want 404 (rejected by lowered per-package cap)", st)
	}

	// Default cap: the same big package ingests and loads.
	regDefault := writeRegistry(t, files)
	srvDefault := startServerArgs(t,
		[]string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--layer-path", regDefault)
	if st := getStatus(t, srvDefault.BaseURL+"/v1/load_artifact?id=finance/big"); st != 200 {
		t.Errorf("with default cap, big package load = %d, want 200", st)
	}
}
