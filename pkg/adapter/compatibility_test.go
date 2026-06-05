package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// spec: §4.3.4 — buildSkillCompatibility renders runtime_requirements and
// sandbox_profile into a human-readable string.
func TestBuildSkillCompatibility(t *testing.T) {
	t.Parallel()
	got := buildSkillCompatibility(&manifest.RuntimeRequirements{
		Python:         ">=3.10",
		Node:           ">=18",
		SystemPackages: []string{"ffmpeg", "imagemagick"},
	}, manifest.SandboxReadOnlyFS)
	for _, want := range []string{"Python >=3.10", "Node >=18", "ffmpeg", "imagemagick", "sandbox: read-only-fs"} {
		if !strings.Contains(got, want) {
			t.Errorf("compatibility %q missing %q", got, want)
		}
	}
}

// spec: §4.3.4 — with neither runtime_requirements nor sandbox_profile there
// is nothing to derive.
func TestBuildSkillCompatibility_Empty(t *testing.T) {
	t.Parallel()
	if got := buildSkillCompatibility(nil, ""); got != "" {
		t.Errorf("expected empty derivation, got %q", got)
	}
	if got := buildSkillCompatibility(&manifest.RuntimeRequirements{}, ""); got != "" {
		t.Errorf("empty requirements must derive nothing, got %q", got)
	}
}

// spec: §4.3.4 — the derived compatibility string is capped at 500 chars.
func TestBuildSkillCompatibility_Capped(t *testing.T) {
	t.Parallel()
	pkgs := make([]string, 200)
	for i := range pkgs {
		pkgs[i] = "package-with-a-long-name"
	}
	got := buildSkillCompatibility(&manifest.RuntimeRequirements{SystemPackages: pkgs}, "")
	if len(got) > skillCompatibilityMaxChars {
		t.Errorf("derived string length %d exceeds cap %d", len(got), skillCompatibilityMaxChars)
	}
}

const skillNoCompat = "---\nname: aggregate\ndescription: Aggregate data.\n---\n\nSkill body.\n"

// spec: §4.3.4 — when SKILL.md omits compatibility, the claude-code adapter
// (which consumes only the agentskills.io subset) derives it from ARTIFACT.md
// runtime_requirements and sandbox_profile and injects it into SKILL.md.
func TestClaudeCode_DerivesCompatibilityWhenOmitted(t *testing.T) {
	t.Parallel()
	out, err := ClaudeCode{}.Adapt(context.Background(), Source{
		ArtifactID: "team/aggregate",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n" +
			"runtime_requirements:\n  python: \">=3.10\"\nsandbox_profile: read-only-fs\n---\n"),
		SkillBytes: []byte(skillNoCompat),
	})
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	got := skillContent(t, out)
	if !strings.Contains(got, "compatibility:") {
		t.Fatalf("materialized SKILL.md missing derived compatibility:\n%s", got)
	}
	if !strings.Contains(got, "Python >=3.10") || !strings.Contains(got, "sandbox: read-only-fs") {
		t.Errorf("derived compatibility missing runtime/sandbox detail:\n%s", got)
	}
	// The original body and frontmatter survive the injection.
	if !strings.Contains(got, "Skill body.") || !strings.Contains(got, "name: aggregate") {
		t.Errorf("injection corrupted the SKILL.md:\n%s", got)
	}
	// The derived value parses back as a valid Skill with the field set.
	skill, perr := manifest.ParseSkill([]byte(got))
	if perr != nil {
		t.Fatalf("derived SKILL.md does not parse: %v", perr)
	}
	if skill.Compatibility == "" {
		t.Errorf("parsed compatibility is empty:\n%s", got)
	}
}

// spec: §4.3.4 — an author-supplied compatibility is preserved verbatim; the
// adapter does not overwrite it.
func TestClaudeCode_KeepsAuthoredCompatibility(t *testing.T) {
	t.Parallel()
	authored := "---\nname: aggregate\ndescription: Aggregate.\ncompatibility: Hand-written.\n---\n\nbody\n"
	out, err := ClaudeCode{}.Adapt(context.Background(), Source{
		ArtifactID:    "team/aggregate",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.10\"\n---\n"),
		SkillBytes:    []byte(authored),
	})
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	got := skillContent(t, out)
	if !strings.Contains(got, "Hand-written.") {
		t.Errorf("authored compatibility lost:\n%s", got)
	}
	if strings.Contains(got, "Python >=3.10") {
		t.Errorf("adapter overwrote the authored compatibility:\n%s", got)
	}
}

// spec: §4.3.4 — when ARTIFACT.md carries no runtime constraints there is
// nothing to derive and SKILL.md is materialized unchanged.
func TestClaudeCode_NoDerivationWithoutRuntimeInfo(t *testing.T) {
	t.Parallel()
	out, err := ClaudeCode{}.Adapt(context.Background(), Source{
		ArtifactID:    "team/aggregate",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"),
		SkillBytes:    []byte(skillNoCompat),
	})
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if got := skillContent(t, out); got != skillNoCompat {
		t.Errorf("SKILL.md changed with nothing to derive:\nin:  %q\nout: %q", skillNoCompat, got)
	}
}

// skillContent returns the SKILL.md file body from an adapter output set.
func skillContent(t *testing.T, out []File) string {
	t.Helper()
	for _, f := range out {
		if strings.HasSuffix(f.Path, "SKILL.md") {
			return string(f.Content)
		}
	}
	t.Fatalf("no SKILL.md in adapter output (%d files)", len(out))
	return ""
}
