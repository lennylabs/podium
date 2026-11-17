package lint_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// bundledSizeDiags lints rec under l and returns its
// lint.bundled_resource_size diagnostics of severity sev.
func bundledSizeDiags(l *lint.Linter, rec filesystem.ArtifactRecord, sev lint.Severity) []lint.Diagnostic {
	var out []lint.Diagnostic
	for _, d := range l.Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec}) {
		if d.Code == "lint.bundled_resource_size" && d.Severity == sev {
			out = append(out, d)
		}
	}
	return out
}

// Spec: §12, §13.12 — "Per-package and per-file size lints at ingest
// time; soft cap is configurable." A Linter with a lowered per-file cap
// warns on a resource that is below the default 1 MB cap but above the
// configured value.
func TestBundledResourceSize_PerFileCapConfigurable(t *testing.T) {
	t.Parallel()
	// 2 KB resource: well under the 1 MB default, over a 1 KB configured cap.
	rec := filesystem.ArtifactRecord{
		ID:        "team/small-context",
		Artifact:  &manifest.Artifact{Type: manifest.TypeContext},
		Resources: map[string][]byte{"data.bin": make([]byte, 2*1024)},
	}
	if got := bundledSizeDiags(&lint.Linter{}, rec, lint.SeverityWarning); len(got) != 0 {
		t.Fatalf("default cap should not warn on a 2 KB resource: %+v", got)
	}
	lowered := &lint.Linter{Thresholds: lint.Thresholds{PerFileSoftCapBytes: 1024}}
	if got := bundledSizeDiags(lowered, rec, lint.SeverityWarning); len(got) != 1 {
		t.Errorf("lowered per-file cap should warn once on a 2 KB resource; got %+v", got)
	}
}

// Spec: §12, §13.12 — the per-package soft cap is likewise configurable;
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
	l := &lint.Linter{Thresholds: lint.Thresholds{PerPackageSoftCapBytes: 3 * 1024}}
	if got := bundledSizeDiags(l, rec, lint.SeverityError); len(got) != 1 {
		t.Errorf("lowered per-package cap should error on a 4 KB package; got %+v", got)
	}
}

// Spec: §13.12 — NewIngestLinter reads the soft caps from
// PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES and
// PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES, so an operator can tune them
// per deployment. t.Setenv precludes t.Parallel.
func TestNewIngestLinter_ReadsCapEnv(t *testing.T) {
	t.Setenv("PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES", "1024")
	t.Setenv("PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES", "4096")
	l := lint.NewIngestLinter(true)
	if l.Thresholds.PerFileSoftCapBytes != 1024 {
		t.Errorf("PerFileSoftCapBytes = %d, want 1024", l.Thresholds.PerFileSoftCapBytes)
	}
	if l.Thresholds.PerPackageSoftCapBytes != 4096 {
		t.Errorf("PerPackageSoftCapBytes = %d, want 4096", l.Thresholds.PerPackageSoftCapBytes)
	}
	if l.HTTPClient != nil {
		t.Errorf("offline linter carries an HTTP client")
	}

	rec := filesystem.ArtifactRecord{
		ID:        "team/ctx",
		Artifact:  &manifest.Artifact{Type: manifest.TypeContext},
		Resources: map[string][]byte{"data.bin": make([]byte, 2*1024)},
	}
	if got := bundledSizeDiags(l, rec, lint.SeverityWarning); len(got) != 1 {
		t.Errorf("env-configured per-file cap should warn on a 2 KB resource; got %+v", got)
	}
}

// Spec: §13.12 — an invalid value keeps the default in force.
func TestNewIngestLinter_IgnoresInvalidCapEnv(t *testing.T) {
	t.Setenv("PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES", "not-a-number")
	t.Setenv("PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES", "-5")
	l := lint.NewIngestLinter(false)
	if l.Thresholds.PerFileSoftCapBytes != lint.PerFileSoftCapBytes {
		t.Errorf("PerFileSoftCapBytes = %d, want the default %d", l.Thresholds.PerFileSoftCapBytes, lint.PerFileSoftCapBytes)
	}
	if l.Thresholds.PerPackageSoftCapBytes != lint.PerPackageSoftCapBytes {
		t.Errorf("PerPackageSoftCapBytes = %d, want the default %d", l.Thresholds.PerPackageSoftCapBytes, lint.PerPackageSoftCapBytes)
	}
	if l.HTTPClient == nil {
		t.Errorf("online linter carries no HTTP client")
	}
}
