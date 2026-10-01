package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §13.4 — a sign-stored-rows run given --plan-digest refuses when its own
// plan differs from the reviewed dry run's. An unsigned row stored after the
// dry run changes the plan under --include-unsigned: both binaries exit 3,
// print the run's plan as plan: lines, and sign nothing, so a verifying load of
// the reviewed row is still refused. A new dry run and a run with its digest
// sign every row.
// Matrix: §6.10 (materialize.signature_missing)
func TestSignStoredRows_RefusesAChangedPlan(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "registry-signing.key")
	env := rotationEnv(home, keyPath)
	unsignedEnv := append(append([]string{}, env...), "PODIUM_SIGN=none")
	verifyKey := "PODIUM_SIGNATURE_VERIFY_KEY=" + firstLine(generateKeyFile(t, keyPath).Stdout)
	reg := rehashRegistry(t)

	first := startServerArgs(t, unsignedEnv, "serve", "--standalone", "--layer-path", reg)
	stopProc(first.cmd)

	dry := signStoredRows(t, env, "--dry-run", "--include-unsigned")
	if dry.Exit != 0 {
		t.Fatalf("dry run exit=%d\nstdout:\n%s\nstderr:\n%s", dry.Exit, dry.Stdout, dry.Stderr)
	}
	for _, want := range []string{" stored=sha256:", "dry-run: plan mode=", "dry-run: plan digest sha256:"} {
		if !strings.Contains(dry.Stdout, want) {
			t.Errorf("dry run lacks %q:\n%s", want, dry.Stdout)
		}
	}
	reviewed := planDigestOf(t, dry.Stdout)

	// A second unsigned row arrives after the review.
	const secondID = "ops/acme/arrived-late"
	writeArtifact(t, reg, secondID, "---\ntype: skill\nversion: 1.0.0\nsensitivity: low\n---\n\n<!-- Skill body lives in SKILL.md. -->\n", skillBody("arrived-late"))
	second := startServerArgs(t, unsignedEnv, "serve", "--standalone", "--layer-path", reg)
	stopProc(second.cmd)

	refused := signStoredRows(t, env, "--include-unsigned", "--plan-digest="+reviewed)
	if refused.Exit != 3 || !strings.Contains(refused.Stderr, "plan changed since the reviewed dry run") {
		t.Fatalf("podium admin run exit=%d, want 3 naming the changed plan\nstdout:\n%s\nstderr:\n%s", refused.Exit, refused.Stdout, refused.Stderr)
	}
	if !hasLine(refused.Stdout, "plan: ", "/"+secondID+"@1.0.0 class=unmigrated") {
		t.Errorf("the refused run lists no plan: line for %s:\n%s", secondID, refused.Stdout)
	}
	if res := runServerBin(t, env, "sign-stored-rows", "--include-unsigned", "--plan-digest="+reviewed); res.Exit != 3 {
		t.Errorf("podium-server run exit=%d, want 3\nstderr:\n%s", res.Exit, res.Stderr)
	}

	signing := startServerArgs(t, env, "serve", "--standalone")
	if errStr, res := bridgeLoad(t, signing.BaseURL, rehashSkillID, verifyKey); !strings.HasPrefix(errStr, "materialize.signature_missing") {
		t.Fatalf("load after the refused runs = %q, want materialize.signature_missing\nstderr: %s", errStr, res.Stderr)
	}

	again := signStoredRows(t, env, "--dry-run", "--include-unsigned")
	if again.Exit != 0 {
		t.Fatalf("second dry run exit=%d\nstderr:\n%s", again.Exit, again.Stderr)
	}
	run := signStoredRows(t, env, "--include-unsigned", "--plan-digest="+planDigestOf(t, again.Stdout))
	if run.Exit != 0 || !strings.Contains(run.Stdout, "rehash: 0 unsigned left") {
		t.Fatalf("reviewed run exit=%d, want 0 and no unsigned row left\nstdout:\n%s\nstderr:\n%s", run.Exit, run.Stdout, run.Stderr)
	}
	if errStr, res := bridgeLoad(t, signing.BaseURL, rehashSkillID, verifyKey); errStr != "" {
		t.Fatalf("load after the reviewed run = %q, want success\nstderr: %s\nlog:\n%s", errStr, res.Stderr, signing.log())
	}
}

// writeArtifact adds a skill with its ARTIFACT.md and SKILL.md under the layer
// directory reg.
func writeArtifact(t *testing.T, reg, id, manifest, skill string) {
	t.Helper()
	dir := filepath.Join(reg, filepath.FromSlash(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"ARTIFACT.md": manifest, "SKILL.md": skill} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// hasLine reports whether out holds a line that starts with prefix and
// contains part.
func hasLine(out, prefix, part string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) && strings.Contains(line, part) {
			return true
		}
	}
	return false
}
