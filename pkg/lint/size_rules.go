package lint

import (
	"fmt"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec §4.1 thresholds. Constants exposed so tests can reference
// the same numbers the rule emits.
const (
	// PerFileSoftCapBytes is the §4.1 per-file soft cap. Above
	// this, ingest emits a warning.
	PerFileSoftCapBytes = 1 * 1024 * 1024
	// PerPackageSoftCapBytes is the §4.1 per-package soft cap.
	// Above this, ingest emits an error.
	PerPackageSoftCapBytes = 10 * 1024 * 1024
	// SkillBodyWarnTokens is the §4.3.4 SKILL.md body warning
	// threshold (in tokens, 4-bytes-per-token approximation).
	SkillBodyWarnTokens = 5_000
	// SkillBodyWarnLines is the §4.3.4 SKILL.md body line warning
	// threshold.
	SkillBodyWarnLines = 500
	// ManifestErrTokens is the §4.1 manifest hard error threshold
	// (whole-manifest, including bundled SKILL.md when present).
	ManifestErrTokens = 20_000
)

// approxTokenCount estimates GPT-style token count from a byte
// stream by dividing by four — the de facto rule of thumb that
// matches OpenAI tokenizer averages on English prose. Lint
// thresholds in §4 are coarse, so an estimate suffices.
func approxTokenCount(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	return (len(b) + 3) / 4
}

// countLines returns the number of '\n' characters plus one for
// any trailing line that lacks a newline.
func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	lines := 0
	for _, c := range b {
		if c == '\n' {
			lines++
		}
	}
	if b[len(b)-1] != '\n' {
		lines++
	}
	return lines
}

// ruleBundledResourceSize implements §4.1's per-file and
// per-package soft caps for bundled resources.
type ruleBundledResourceSize struct{}

func (ruleBundledResourceSize) Code() string        { return "lint.bundled_resource_size" }

func (r ruleBundledResourceSize) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		total := int64(0)
		for path, body := range rec.Resources {
			size := int64(len(body))
			total += size
			if size > PerFileSoftCapBytes {
				out = append(out, Diagnostic{
					ArtifactID: rec.ID,
					Code:       r.Code(),
					Severity:   SeverityWarning,
					Message: fmt.Sprintf(
						"bundled resource %q is %d bytes, exceeding the per-file soft cap of %d bytes",
						path, size, PerFileSoftCapBytes),
				})
			}
		}
		if total > PerPackageSoftCapBytes {
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       r.Code(),
				Severity:   SeverityError,
				Message: fmt.Sprintf(
					"bundled resources total %d bytes, exceeding the per-package soft cap of %d bytes",
					total, PerPackageSoftCapBytes),
			})
		}
	}
	return out
}

// ruleManifestSize implements §4.1's manifest-token cap and
// §4.3.4's SKILL.md body 5K-token / 500-line warning + 20K-token
// error.
type ruleManifestSize struct{}

func (ruleManifestSize) Code() string        { return "lint.manifest_size" }

func (r ruleManifestSize) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		manifestTokens := approxTokenCount(rec.ArtifactBytes) + approxTokenCount(rec.SkillBytes)
		if manifestTokens > ManifestErrTokens {
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       r.Code(),
				Severity:   SeverityError,
				Message: fmt.Sprintf(
					"manifest is approximately %d tokens, exceeding the cap of %d tokens",
					manifestTokens, ManifestErrTokens),
			})
		}
		if rec.Artifact != nil && rec.Artifact.Type == manifest.TypeSkill && len(rec.SkillBytes) > 0 {
			bodyTokens := approxTokenCount(rec.SkillBytes)
			bodyLines := countLines(rec.SkillBytes)
			if bodyTokens > SkillBodyWarnTokens {
				out = append(out, Diagnostic{
					ArtifactID: rec.ID,
					Code:       r.Code(),
					Severity:   SeverityWarning,
					Message: fmt.Sprintf(
						"SKILL.md body is approximately %d tokens; the guidance recommends <= %d",
						bodyTokens, SkillBodyWarnTokens),
				})
			}
			if bodyLines > SkillBodyWarnLines {
				out = append(out, Diagnostic{
					ArtifactID: rec.ID,
					Code:       r.Code(),
					Severity:   SeverityWarning,
					Message: fmt.Sprintf(
						"SKILL.md body is %d lines; the guidance recommends <= %d",
						bodyLines, SkillBodyWarnLines),
				})
			}
		}
	}
	return out
}
