package lint

import (
	"context"
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
	// ManifestErrTokens is the §4.1 manifest hard error threshold.
	// For skills it applies to the SKILL.md body; for every other
	// type it applies to the ARTIFACT.md content.
	ManifestErrTokens = 20_000
	// DomainBodyWarnTokens is the §4.5.5 DOMAIN.md prose-body warning
	// threshold: "Body length is recommended <= 2000 tokens; lint warns
	// above."
	DomainBodyWarnTokens = 2_000
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
// per-package soft caps for bundled resources. The caps are
// configurable per §12 ("soft cap is configurable", F-12.0.2):
// perFileCap and perPackageCap override the package-default
// constants when non-zero, threaded from Linter.PerFileSoftCapBytes /
// Linter.PerPackageSoftCapBytes.
type ruleBundledResourceSize struct {
	perFileCap    int64
	perPackageCap int64
}

func (ruleBundledResourceSize) Code() string { return "lint.bundled_resource_size" }

// perFile returns the effective per-file soft cap: the configured
// override when set, else the §4.1 default.
func (r ruleBundledResourceSize) perFile() int64 {
	if r.perFileCap > 0 {
		return r.perFileCap
	}
	return PerFileSoftCapBytes
}

// perPackage returns the effective per-package soft cap: the
// configured override when set, else the §4.1 default.
func (r ruleBundledResourceSize) perPackage() int64 {
	if r.perPackageCap > 0 {
		return r.perPackageCap
	}
	return PerPackageSoftCapBytes
}

func (r ruleBundledResourceSize) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	perFile := r.perFile()
	perPackage := r.perPackage()
	var out []Diagnostic
	for _, rec := range records {
		total := int64(0)
		for path, body := range rec.Resources {
			size := int64(len(body))
			total += size
			if size > perFile {
				out = append(out, Diagnostic{
					ArtifactID: rec.ID,
					Code:       r.Code(),
					Severity:   SeverityWarning,
					Message: fmt.Sprintf(
						"bundled resource %q is %d bytes, exceeding the per-file soft cap of %d bytes",
						path, size, perFile),
				})
			}
		}
		if total > perPackage {
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       r.Code(),
				Severity:   SeverityError,
				Message: fmt.Sprintf(
					"bundled resources total %d bytes, exceeding the per-package soft cap of %d bytes",
					total, perPackage),
			})
		}
	}
	return out
}

// ruleDomainBodySize implements §4.5.5: the DOMAIN.md prose body is
// "recommended <= 2000 tokens; lint warns above". It walks every
// DOMAIN.md in the registry and warns when the parsed body exceeds
// DomainBodyWarnTokens, using the same 4-bytes-per-token approximation
// as the manifest size rules. The warning is advisory; ingest still
// succeeds. Author-facing (reg-driven), like the other DOMAIN.md rules.
type ruleDomainBodySize struct{}

func (ruleDomainBodySize) Code() string { return "lint.domain_body_size" }

func (r ruleDomainBodySize) Check(ctx context.Context, reg *filesystem.Registry, _ []filesystem.ArtifactRecord) []Diagnostic {
	if reg == nil {
		return nil
	}
	var out []Diagnostic
	// spec: §9.3 — bound the walk with the request context so a large
	// registry cannot pin the linter.
	for _, layer := range reg.Layers {
		if ctx.Err() != nil {
			break
		}
		for path, dom := range walkDomainsInLayer(layer) {
			if dom == nil {
				continue
			}
			if tokens := approxTokenCount([]byte(dom.Body)); tokens > DomainBodyWarnTokens {
				out = append(out, Diagnostic{
					ArtifactID: path,
					Code:       r.Code(),
					Severity:   SeverityWarning,
					Message: fmt.Sprintf(
						"DOMAIN.md body is approximately %d tokens; §4.5.5 recommends <= %d",
						tokens, DomainBodyWarnTokens),
				})
			}
		}
	}
	return out
}

// ruleManifestSize implements §4.1's manifest-token cap and
// §4.3.4's SKILL.md body 5K-token / 500-line warning + 20K-token
// error.
//
// Per §4.1 the manifest-size cap applies to the SKILL.md body for
// skills (the parsed prose body, excluding YAML frontmatter), and to
// the ARTIFACT.md content for every other type.
type ruleManifestSize struct{}

func (ruleManifestSize) Code() string { return "lint.manifest_size" }

func (r ruleManifestSize) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		isSkill := rec.Artifact != nil && rec.Artifact.Type == manifest.TypeSkill

		// The body the §4.1 cap measures: SKILL.md body for skills,
		// the ARTIFACT.md content otherwise. A skill missing its
		// SKILL.md (rec.Skill == nil) is reported by
		// ruleSkillCompliance; here it measures an empty body.
		var capBody []byte
		if isSkill {
			if rec.Skill != nil {
				capBody = []byte(rec.Skill.Body)
			}
		} else {
			capBody = rec.ArtifactBytes
		}

		if tokens := approxTokenCount(capBody); tokens > ManifestErrTokens {
			subject := "manifest"
			if isSkill {
				subject = "SKILL.md body"
			}
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       r.Code(),
				Severity:   SeverityError,
				Message: fmt.Sprintf(
					"%s is approximately %d tokens, exceeding the cap of %d tokens",
					subject, tokens, ManifestErrTokens),
			})
		}

		// §4.3.4 SKILL.md body soft caps, measured on the parsed body.
		if isSkill && rec.Skill != nil {
			body := []byte(rec.Skill.Body)
			if bodyTokens := approxTokenCount(body); bodyTokens > SkillBodyWarnTokens {
				out = append(out, Diagnostic{
					ArtifactID: rec.ID,
					Code:       r.Code(),
					Severity:   SeverityWarning,
					Message: fmt.Sprintf(
						"SKILL.md body is approximately %d tokens; the guidance recommends <= %d",
						bodyTokens, SkillBodyWarnTokens),
				})
			}
			if bodyLines := countLines(body); bodyLines > SkillBodyWarnLines {
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
