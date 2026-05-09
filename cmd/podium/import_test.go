package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §3 — `podium import --source <skills-dir>` rewrites each
// subdirectory's SKILL.md as a Podium artifact (ARTIFACT.md +
// SKILL.md). Bundled non-skill files stay in the artifact dir as
// resources.
func TestImport_ConvertsSkillTree(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	skill := filepath.Join(source, "hello-world")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: hello\n---\nBody."), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skill, "data.json"), []byte(`{"k":"v"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rc := importCmd([]string{
		"--source", source,
		"--target", target,
		"--type", "skill",
		"--version", "1.0.0",
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	artifact := filepath.Join(target, "hello-world", "ARTIFACT.md")
	got, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("ARTIFACT.md missing: %v", err)
	}
	if !strings.Contains(string(got), "type: skill") {
		t.Errorf("ARTIFACT.md missing type: %s", got)
	}
	skillFile := filepath.Join(target, "hello-world", "SKILL.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Errorf("SKILL.md missing: %v", err)
	}
	resource := filepath.Join(target, "hello-world", "data.json")
	if _, err := os.Stat(resource); err != nil {
		t.Errorf("bundled resource missing: %v", err)
	}
}

// Spec: §3 — empty source returns an error so the operator notices.
func TestImport_EmptySourceFails(t *testing.T) {
	rc := importCmd([]string{
		"--source", t.TempDir(),
		"--target", t.TempDir(),
	})
	if rc == 0 {
		t.Errorf("rc = 0, want non-zero")
	}
}

// Spec: §3 — --dry-run prints the plan without touching the target.
func TestImport_DryRun(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	skill := filepath.Join(source, "x")
	_ = os.MkdirAll(skill, 0o755)
	_ = os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644)
	rc := importCmd([]string{"--source", source, "--target", target, "--dry-run"})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if _, err := os.Stat(filepath.Join(target, "x")); err == nil {
		t.Errorf("dry-run created target")
	}
}
