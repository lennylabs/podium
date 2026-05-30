package lint_test

import (
	"context"
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
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
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
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
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
// warning. The cap measures the parsed body (rec.Skill.Body), per §4.1.
func TestRuleManifestSize_SkillBodyWarnsAtTokenBudget(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", lint.SkillBodyWarnTokens*4+1) // ~5K+1 tokens
	rec := filesystem.ArtifactRecord{
		ID:       "skills/big",
		Artifact: &manifest.Artifact{Type: manifest.TypeSkill},
		Skill:    &manifest.Skill{Body: body},
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
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

// Spec: §4.3.4 — SKILL.md body above 500 lines yields a warning,
// measured on the parsed body.
func TestRuleManifestSize_SkillBodyWarnsAtLineBudget(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x\n", lint.SkillBodyWarnLines+1)
	rec := filesystem.ArtifactRecord{
		ID:       "skills/many-lines",
		Artifact: &manifest.Artifact{Type: manifest.TypeSkill},
		Skill:    &manifest.Skill{Body: body},
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
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

// Spec: §4.1 — a non-skill manifest whose ARTIFACT.md content exceeds the
// 20K-token cap yields an error.
func TestRuleManifestSize_NonSkillManifestErrorsAtTokenCap(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", lint.ManifestErrTokens*4+8)
	rec := filesystem.ArtifactRecord{
		ID:            "context/huge",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte(body),
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
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

// Spec: §4.1 — a skill whose SKILL.md body exceeds the 20K-token cap
// yields an error even when the body is the only large content.
func TestRuleManifestSize_SkillBodyErrorsAtHardCap(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", lint.ManifestErrTokens*4+8)
	rec := filesystem.ArtifactRecord{
		ID:       "skills/huge-body",
		Artifact: &manifest.Artifact{Type: manifest.TypeSkill},
		Skill:    &manifest.Skill{Body: body},
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	gotErr := false
	for _, d := range diags {
		if d.Code == "lint.manifest_size" && d.Severity == lint.SeverityError {
			gotErr = true
		}
	}
	if !gotErr {
		t.Errorf("missing manifest size error for oversized SKILL.md body; got %+v", diags)
	}
}

// Spec: §4.1 (F-4.1.2) — for skills the 20K-token cap applies to the
// SKILL.md body, not the ARTIFACT.md file or the SKILL.md frontmatter. A
// skill whose whole files are large but whose parsed body is small
// produces no size error. This fails under the pre-fix rule that summed
// ArtifactBytes and SkillBytes.
func TestRuleManifestSize_SkillCapMeasuresBodyNotFiles(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("a", lint.ManifestErrTokens*4+8) // > 20K tokens
	rec := filesystem.ArtifactRecord{
		ID:            "skills/frontmatter-heavy",
		Artifact:      &manifest.Artifact{Type: manifest.TypeSkill},
		ArtifactBytes: []byte(huge),
		SkillBytes:    []byte("---\nname: x\nmetadata: " + huge + "\n---\nshort body"),
		Skill:         &manifest.Skill{Body: "short body"},
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	for _, d := range diags {
		if d.Code == "lint.manifest_size" {
			t.Errorf("unexpected manifest_size diagnostic; the skill cap must measure the SKILL.md body only: %s", d.Message)
		}
	}
}

// Spec: §4.3.4 (F-4.1.2) — the SKILL.md body soft caps measure the parsed
// body, not the whole SKILL.md file. Large frontmatter that pushes the
// file over 5K tokens does not warn when the body is small. This fails
// under the pre-fix rule that measured SkillBytes.
func TestRuleManifestSize_SkillSoftCapMeasuresBodyNotFrontmatter(t *testing.T) {
	t.Parallel()
	bigFM := strings.Repeat("a", lint.SkillBodyWarnTokens*4+1)
	rec := filesystem.ArtifactRecord{
		ID:         "skills/fm-heavy",
		Artifact:   &manifest.Artifact{Type: manifest.TypeSkill},
		SkillBytes: []byte("---\nname: x\nmetadata: " + bigFM + "\n---\nshort"),
		Skill:      &manifest.Skill{Body: "short"},
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	for _, d := range diags {
		if d.Code == "lint.manifest_size" {
			t.Errorf("soft cap must measure the SKILL.md body, not frontmatter: %s", d.Message)
		}
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
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	for _, d := range diags {
		if d.Code == "lint.manifest_size" || d.Code == "lint.bundled_resource_size" {
			t.Errorf("unexpected size diagnostic on small manifest: %s", d.Message)
		}
	}
}
