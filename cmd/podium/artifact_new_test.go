package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLayersYAML drops a minimal layers.yaml under root/.podium/.
func writeLayersYAML(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	body := `layers:
  - name: policy
    description: Cross-cutting policies.
    review_required: true
  - name: common
    description: Generic skills any team can use.
  - name: alice-personal
    description: Personal playground.
    review_required: false
`
	if err := os.WriteFile(filepath.Join(dir, "layers.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write layers.yaml: %v", err)
	}
}

// runArtifactNew invokes the testable core with stdin/stdout/stderr
// buffers and returns the exit code plus captured output.
func runArtifactNew(args []string, stdin string) (exit int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	exit = artifactNewWithIO(args, strings.NewReader(stdin), &out, &errOut)
	return exit, out.String(), errOut.String()
}

func TestArtifactNew_NonInteractive_CreatesFiles(t *testing.T) {
	tmp := t.TempDir()
	writeLayersYAML(t, tmp)

	args := []string{
		"--root", tmp,
		"--layer", "common",
		"--name", "release-notes",
		"--description", "Draft release notes from a list of ticket keys.",
		"--tags", "release,workflow",
		"--template", "workflow",
		"--yes",
	}
	exit, stdout, stderr := runArtifactNew(args, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "Created common/release-notes/") {
		t.Errorf("stdout missing 'Created' line, got: %s", stdout)
	}

	dir := filepath.Join(tmp, "common", "release-notes")
	for _, f := range []string{"ARTIFACT.md", "SKILL.md"} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !bytes.Contains(data, []byte("Draft release notes")) {
			t.Errorf("%s missing description, got:\n%s", f, data)
		}
	}

	artifact, _ := os.ReadFile(filepath.Join(dir, "ARTIFACT.md"))
	if !bytes.Contains(artifact, []byte("tags: [release, workflow]")) {
		t.Errorf("ARTIFACT.md tags missing or malformed:\n%s", artifact)
	}

	skill, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !bytes.Contains(skill, []byte("## Workflow")) {
		t.Errorf("SKILL.md missing workflow-template section, got:\n%s", skill)
	}
}

func TestArtifactNew_RejectsBadName(t *testing.T) {
	tmp := t.TempDir()
	writeLayersYAML(t, tmp)
	args := []string{
		"--root", tmp,
		"--layer", "common",
		"--name", "Bad_Name", // underscores and uppercase not allowed
		"--description", "x",
		"--template", "skill",
		"--yes",
	}
	exit, _, stderr := runArtifactNew(args, "")
	if exit != 2 {
		t.Fatalf("expected exit=2, got %d (stderr=%s)", exit, stderr)
	}
	if !strings.Contains(stderr, "kebab-case") {
		t.Errorf("stderr missing kebab-case hint: %s", stderr)
	}
}

func TestArtifactNew_RejectsUnknownLayer(t *testing.T) {
	tmp := t.TempDir()
	writeLayersYAML(t, tmp)
	args := []string{
		"--root", tmp,
		"--layer", "engineering", // not declared
		"--name", "x",
		"--description", "x",
		"--template", "skill",
		"--yes",
	}
	exit, _, stderr := runArtifactNew(args, "")
	if exit != 2 {
		t.Fatalf("expected exit=2, got %d (stderr=%s)", exit, stderr)
	}
	if !strings.Contains(stderr, "available:") {
		t.Errorf("stderr missing layer catalog hint: %s", stderr)
	}
}

func TestArtifactNew_RejectsUnknownTemplate(t *testing.T) {
	tmp := t.TempDir()
	writeLayersYAML(t, tmp)
	args := []string{
		"--root", tmp,
		"--layer", "common",
		"--name", "x",
		"--description", "x",
		"--template", "made-up",
		"--yes",
	}
	exit, _, _ := runArtifactNew(args, "")
	if exit != 2 {
		t.Fatalf("expected exit=2, got %d", exit)
	}
}

func TestArtifactNew_RefusesExistingDir(t *testing.T) {
	tmp := t.TempDir()
	writeLayersYAML(t, tmp)
	dir := filepath.Join(tmp, "common", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	args := []string{
		"--root", tmp,
		"--layer", "common",
		"--name", "x",
		"--description", "x",
		"--template", "skill",
		"--yes",
	}
	exit, _, stderr := runArtifactNew(args, "")
	if exit != 1 {
		t.Fatalf("expected exit=1, got %d", exit)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("stderr missing 'already exists': %s", stderr)
	}
}

func TestArtifactNew_ForceOverwrites(t *testing.T) {
	tmp := t.TempDir()
	writeLayersYAML(t, tmp)
	dir := filepath.Join(tmp, "common", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale"), []byte("old"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	args := []string{
		"--root", tmp,
		"--layer", "common",
		"--name", "x",
		"--description", "fresh",
		"--template", "skill",
		"--force",
		"--yes",
	}
	exit, _, stderr := runArtifactNew(args, "")
	if exit != 0 {
		t.Fatalf("expected exit=0, got %d (stderr=%s)", exit, stderr)
	}
}

func TestArtifactNew_ReviewRequiredNotice(t *testing.T) {
	tmp := t.TempDir()
	writeLayersYAML(t, tmp)
	args := []string{
		"--root", tmp,
		"--layer", "policy", // review_required: true
		"--name", "x",
		"--description", "x",
		"--template", "policy",
		"--yes",
	}
	exit, stdout, stderr := runArtifactNew(args, "")
	if exit != 0 {
		t.Fatalf("exit=%d, stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "review_required") {
		t.Errorf("stdout missing review_required notice: %s", stdout)
	}
}

func TestArtifactNew_FallsBackToDirectoryListing(t *testing.T) {
	tmp := t.TempDir()
	// No .podium/layers.yaml; create a bare layer directory.
	if err := os.MkdirAll(filepath.Join(tmp, "common"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	args := []string{
		"--root", tmp,
		"--layer", "common",
		"--name", "x",
		"--description", "x",
		"--template", "skill",
		"--yes",
	}
	exit, _, stderr := runArtifactNew(args, "")
	if exit != 0 {
		t.Fatalf("expected exit=0 with directory-listing fallback, got %d (stderr=%s)", exit, stderr)
	}
}

func TestArtifactNew_NonInteractive_RequiresFlags(t *testing.T) {
	tmp := t.TempDir()
	writeLayersYAML(t, tmp)
	cases := []struct {
		name string
		args []string
	}{
		{"no name", []string{"--root", tmp, "--layer", "common", "--description", "x", "--template", "skill", "--yes"}},
		{"no description", []string{"--root", tmp, "--layer", "common", "--name", "x", "--template", "skill", "--yes"}},
		{"no layer", []string{"--root", tmp, "--name", "x", "--description", "x", "--template", "skill", "--yes"}},
		{"no template", []string{"--root", tmp, "--layer", "common", "--name", "x", "--description", "x", "--yes"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exit, _, _ := runArtifactNew(c.args, "")
			if exit != 2 {
				t.Fatalf("expected exit=2 (missing flag), got %d", exit)
			}
		})
	}
}

func TestRenderArtifactMD_FormatsFrontmatter(t *testing.T) {
	got := renderArtifactMD("Look up tickets.", []string{"jira", "lookup"})
	want := []string{
		"type: skill",
		"version: 0.1.0",
		"description: Look up tickets.",
		"tags: [jira, lookup]",
		"sensitivity: low",
		"license: MIT",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("rendered ARTIFACT.md missing %q\nGot:\n%s", w, got)
		}
	}
}

func TestRenderSkillBody_AllTemplates(t *testing.T) {
	for _, tmpl := range validTemplates {
		t.Run(tmpl, func(t *testing.T) {
			body := renderSkillBody(tmpl, "x", "desc")
			if !strings.Contains(body, "name: x") {
				t.Errorf("template %s missing name header", tmpl)
			}
			if !strings.Contains(body, "description: desc") {
				t.Errorf("template %s missing description header", tmpl)
			}
		})
	}
}
