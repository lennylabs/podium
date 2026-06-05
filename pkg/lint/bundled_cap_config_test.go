package lint_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec: §12 — "Per-package and per-file size lints at ingest
// time; soft cap is configurable." A Linter with a lowered per-file cap
// warns on a resource that is below the default 1 MB cap but above the
// configured value.
func TestBundledResourceSize_PerFileCapConfigurable(t *testing.T) {
	t.Parallel()
	// 2 KB resource: well under the 1 MB default, over a 1 KB configured cap.
	res := make([]byte, 2*1024)
	rec := filesystem.ArtifactRecord{
		ID:        "team/small-context",
		Artifact:  &manifest.Artifact{Type: manifest.TypeContext},
		Resources: map[string][]byte{"data.bin": res},
	}

	// Default linter: no warning (under the 1 MB default).
	def := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	for _, d := range def {
		if d.Code == "lint.bundled_resource_size" {
			t.Fatalf("default cap should not warn on a 2 KB resource: %s", d.Message)
		}
	}

	// Lowered per-file cap: warns.
	lowered := (&lint.Linter{PerFileSoftCapBytes: 1024}).
		Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	gotWarn := false
	for _, d := range lowered {
		if d.Code == "lint.bundled_resource_size" && d.Severity == lint.SeverityWarning {
			gotWarn = true
		}
	}
	if !gotWarn {
		t.Errorf("lowered per-file cap should warn on a 2 KB resource; got %+v", lowered)
	}
}

// Spec: §12 — the per-package soft cap is likewise configurable;
// a lowered cap turns a package below the 10 MB default into an error.
func TestBundledResourceSize_PerPackageCapConfigurable(t *testing.T) {
	t.Parallel()
	rec := filesystem.ArtifactRecord{
		ID:       "team/small-package",
		Artifact: &manifest.Artifact{Type: manifest.TypeContext},
		Resources: map[string][]byte{
			"a.bin": make([]byte, 2*1024),
			"b.bin": make([]byte, 2*1024),
		},
	}
	// 4 KB total, under the 10 MB default but over a 3 KB configured cap.
	diags := (&lint.Linter{PerPackageSoftCapBytes: 3 * 1024}).
		Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	gotErr := false
	for _, d := range diags {
		if d.Code == "lint.bundled_resource_size" && d.Severity == lint.SeverityError {
			gotErr = true
		}
	}
	if !gotErr {
		t.Errorf("lowered per-package cap should error on a 4 KB package; got %+v", diags)
	}
}

// Spec: §12 — NewIngestLinter reads the soft caps from
// PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES / PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES
// so an operator can tune them per deployment. t.Setenv precludes t.Parallel.
func TestNewIngestLinter_ReadsCapEnv(t *testing.T) {
	t.Setenv("PODIUM_INGEST_OFFLINE", "true")
	t.Setenv("PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES", "1024")
	t.Setenv("PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES", "4096")
	l := lint.NewIngestLinter(true)
	if l.PerFileSoftCapBytes != 1024 {
		t.Errorf("PerFileSoftCapBytes = %d, want 1024", l.PerFileSoftCapBytes)
	}
	if l.PerPackageSoftCapBytes != 4096 {
		t.Errorf("PerPackageSoftCapBytes = %d, want 4096", l.PerPackageSoftCapBytes)
	}

	rec := filesystem.ArtifactRecord{
		ID:        "team/ctx",
		Artifact:  &manifest.Artifact{Type: manifest.TypeContext},
		Resources: map[string][]byte{"data.bin": make([]byte, 2*1024)},
	}
	diags := l.Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	gotWarn := false
	for _, d := range diags {
		if d.Code == "lint.bundled_resource_size" && d.Severity == lint.SeverityWarning {
			gotWarn = true
		}
	}
	if !gotWarn {
		t.Errorf("env-configured per-file cap should warn on a 2 KB resource; got %+v", diags)
	}
}

// An unset or invalid env value leaves the default constant in force.
func TestNewIngestLinter_IgnoresInvalidCapEnv(t *testing.T) {
	t.Setenv("PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES", "not-a-number")
	t.Setenv("PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES", "-5")
	l := lint.NewIngestLinter(true)
	if l.PerFileSoftCapBytes != 0 {
		t.Errorf("PerFileSoftCapBytes = %d, want 0 (default in force)", l.PerFileSoftCapBytes)
	}
	if l.PerPackageSoftCapBytes != 0 {
		t.Errorf("PerPackageSoftCapBytes = %d, want 0 (default in force)", l.PerPackageSoftCapBytes)
	}
}
