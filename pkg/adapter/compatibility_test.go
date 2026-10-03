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

// artifactWithRuntime is a skill ARTIFACT.md that declares runtime_requirements
// and, when sandbox is non-empty, a sandbox_profile.
func artifactWithRuntime(sandbox string) []byte {
	s := "---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: \">=3.10\"\n"
	if sandbox != "" {
		s += "sandbox_profile: " + sandbox + "\n"
	}
	return []byte(s + "---\n")
}

// adaptSkill runs the named built-in adapter over one skill and returns the
// materialized SKILL.md content.
func adaptSkill(t *testing.T, harness string, artifact, skill []byte) string {
	t.Helper()
	a, err := DefaultRegistry().Get(harness)
	if err != nil {
		t.Fatalf("Get(%q): %v", harness, err)
	}
	out, err := a.Adapt(context.Background(), Source{
		ArtifactID:    "team/aggregate",
		ArtifactBytes: artifact,
		SkillBytes:    skill,
	})
	if err != nil {
		t.Fatalf("%s Adapt: %v", harness, err)
	}
	return skillContent(t, out)
}

// Spec: §4.3.4 — every harness adapter that writes a skill's SKILL.md derives
// compatibility from runtime_requirements and sandbox_profile when the author
// omitted it, preserves an authored value byte for byte, and leaves SKILL.md
// unchanged when there is nothing to derive or no leading frontmatter.
// Spec: §6.7 — the derivation applies to each project-scope adapter's output.
func TestAdapters_SkillCompatibilityDerivation(t *testing.T) {
	t.Parallel()
	// The sandbox_profile subcase covers only the harnesses whose §6.7.1
	// sandbox_profile cell is translatable; codex and pi reject the field at
	// the §6.9 guard, so a synced codex or pi SKILL.md never carries the clause.
	sandboxOK := map[string]bool{"claude-code": true, "cursor": true, "opencode": true, "gemini": true}
	authored := "---\nname: aggregate\ndescription: Aggregate.\ncompatibility: Hand-written.\n---\n\nbody\n"
	noFrontmatter := "name: aggregate\n\nSkill body.\n"
	for _, harness := range []string{"claude-code", "cursor", "codex", "opencode", "gemini", "pi"} {
		harness := harness
		t.Run(harness, func(t *testing.T) {
			t.Parallel()
			t.Run("derives_runtime_requirements", func(t *testing.T) {
				assertDerived(t, adaptSkill(t, harness, artifactWithRuntime(""), []byte(skillNoCompat)), "Requires Python >=3.10")
			})
			if sandboxOK[harness] {
				t.Run("derives_sandbox_profile", func(t *testing.T) {
					got := adaptSkill(t, harness, artifactWithRuntime("read-only-fs"), []byte(skillNoCompat))
					assertDerived(t, got, "Requires Python >=3.10; sandbox: read-only-fs")
				})
			}
			unchanged := []struct {
				name     string
				artifact []byte
				skill    string
			}{
				{"keeps_authored", artifactWithRuntime(""), authored},
				{"no_runtime_fields", []byte("---\ntype: skill\nversion: 1.0.0\n---\n"), skillNoCompat},
				{"no_leading_frontmatter", artifactWithRuntime(""), noFrontmatter},
			}
			for _, tc := range unchanged {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					if got := adaptSkill(t, harness, tc.artifact, []byte(tc.skill)); got != tc.skill {
						t.Errorf("SKILL.md changed:\nin:  %q\nout: %q", tc.skill, got)
					}
				})
			}
		})
	}
}

// Spec: §6.7 — the none adapter writes SKILL.md without translation, so it
// derives no compatibility even when ARTIFACT.md declares runtime fields.
func TestNone_DoesNotDeriveCompatibility(t *testing.T) {
	t.Parallel()
	if got := adaptSkill(t, "none", artifactWithRuntime("read-only-fs"), []byte(skillNoCompat)); got != skillNoCompat {
		t.Errorf("none adapter changed SKILL.md:\nin:  %q\nout: %q", skillNoCompat, got)
	}
}

// assertDerived checks that got parses as a SKILL.md whose compatibility is
// want and whose name and body survived the injection.
func assertDerived(t *testing.T, got, want string) {
	t.Helper()
	skill, err := manifest.ParseSkill([]byte(got))
	if err != nil {
		t.Fatalf("derived SKILL.md does not parse: %v\n%s", err, got)
	}
	if skill.Compatibility != want {
		t.Errorf("compatibility = %q, want %q", skill.Compatibility, want)
	}
	if skill.Name != "aggregate" || !strings.Contains(got, "Skill body.") {
		t.Errorf("injection corrupted the SKILL.md:\n%s", got)
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
