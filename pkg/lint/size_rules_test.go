package lint_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec: §4.1 — bundled resources above the per-file soft cap (1
// MB) yield a lint warning.
func TestRuleBundledResourceSize_PerFileWarn(t *testing.T) {
	t.Parallel()
	big := make([]byte, lint.PerFileSoftCapBytes+1)
	rec := filesystem.ArtifactRecord{
		ID:        "team/big-context",
		Artifact:  &manifest.Artifact{Type: manifest.TypeContext},
		Resources: map[string][]byte{"data.bin": big},
	}
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
	gotWarn := false
	for _, d := range diags {
		if d.Code == "lint.bundled_resource_size" && d.Severity == lint.SeverityWarning {
			gotWarn = true
			if !strings.Contains(d.Message, "data.bin") {
				t.Errorf("Message missing path: %s", d.Message)
			}
		}
	}
	if !gotWarn {
		t.Errorf("missing per-file warning for big resource; got %+v", diags)
	}
}

// Spec: §4.1 — bundled resources whose total exceeds the
// per-package soft cap (10 MB) yield a lint error.
func TestRuleBundledResourceSize_PerPackageError(t *testing.T) {
	t.Parallel()
	chunk := make([]byte, 600_000)
	resources := map[string][]byte{}
	// 20 chunks × 600_000 = 12 MB > 10 MB cap.
	for i := 0; i < 20; i++ {
		resources[string(rune('a'+i%26))+".bin"] = chunk
	}
	rec := filesystem.ArtifactRecord{
		ID:        "team/big-package",
		Artifact:  &manifest.Artifact{Type: manifest.TypeContext},
		Resources: resources,
	}
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
	gotErr := false
	for _, d := range diags {
		if d.Code == "lint.bundled_resource_size" && d.Severity == lint.SeverityError {
			gotErr = true
		}
	}
	if !gotErr {
		t.Errorf("missing per-package error for oversized package; got %+v", diags)
	}
}

// Spec: §4.1 / §4.3.4 — SKILL.md body above 5K tokens yields a
// warning.
func TestRuleManifestSize_SkillBodyWarnsAtTokenBudget(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", lint.SkillBodyWarnTokens*4+1) // ~5K+1 tokens
	rec := filesystem.ArtifactRecord{
		ID:         "skills/big",
		Artifact:   &manifest.Artifact{Type: manifest.TypeSkill},
		SkillBytes: []byte(body),
	}
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
	gotWarn := false
	for _, d := range diags {
		if d.Code == "lint.manifest_size" && d.Severity == lint.SeverityWarning &&
			strings.Contains(d.Message, "tokens") {
			gotWarn = true
		}
	}
	if !gotWarn {
		t.Errorf("missing token warning for big SKILL.md body; got %+v", diags)
	}
}

// Spec: §4.3.4 — SKILL.md body above 500 lines yields a warning.
func TestRuleManifestSize_SkillBodyWarnsAtLineBudget(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x\n", lint.SkillBodyWarnLines+1)
	rec := filesystem.ArtifactRecord{
		ID:         "skills/many-lines",
		Artifact:   &manifest.Artifact{Type: manifest.TypeSkill},
		SkillBytes: []byte(body),
	}
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
	gotWarn := false
	for _, d := range diags {
		if d.Code == "lint.manifest_size" && strings.Contains(d.Message, "lines") {
			gotWarn = true
		}
	}
	if !gotWarn {
		t.Errorf("missing line warning for big SKILL.md body; got %+v", diags)
	}
}

// Spec: §4.1 — manifest above the 20K-token cap yields an error.
func TestRuleManifestSize_ManifestErrorsAtTokenCap(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", lint.ManifestErrTokens*4+8)
	rec := filesystem.ArtifactRecord{
		ID:            "skills/huge",
		Artifact:      &manifest.Artifact{Type: manifest.TypeSkill},
		ArtifactBytes: []byte(body),
	}
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
	gotErr := false
	for _, d := range diags {
		if d.Code == "lint.manifest_size" && d.Severity == lint.SeverityError {
			gotErr = true
		}
	}
	if !gotErr {
		t.Errorf("missing manifest size error; got %+v", diags)
	}
}

// Spec: §4.1 / §4.3.4 — small manifests pass cleanly with no
// size diagnostics so existing happy-path tests keep working.
func TestRuleManifestSize_SmallManifestPasses(t *testing.T) {
	t.Parallel()
	rec := filesystem.ArtifactRecord{
		ID:            "skills/tiny",
		Artifact:      &manifest.Artifact{Type: manifest.TypeSkill, Name: "tiny", Version: "1.0.0"},
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nname: tiny\n---\n"),
		SkillBytes:    []byte("---\nname: tiny\ndescription: x\n---\nbody"),
	}
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
	for _, d := range diags {
		if d.Code == "lint.manifest_size" || d.Code == "lint.bundled_resource_size" {
			t.Errorf("unexpected size diagnostic on small manifest: %s", d.Message)
		}
	}
}
