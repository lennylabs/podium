package e2e

// End-to-end tests for docs/deployment/solo-filesystem.md (D-solo-fs).
// The page documents the filesystem-only Podium setup: podium sync/lint/init
// against a directory tree, multi-layer .registry-config, watch mode, overlay,
// override/save-as, and migration to a standalone server.
//
// Known gaps and skips:
//   - T-D-solo-fs-6, -7, -42: layer-order assertions via filesystem.Open are
//     covered by e2e collision/precedence variants instead; the pure Go API
//     import is also feasible and used for -6 and -7.
//   - T-D-solo-fs-53: server-source materialization is wired (F-2.2.2), so
//     the bit-identical filesystem-vs-server sync comparison runs.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// ---- fixtures ---------------------------------------------------------------

// solofsSkillArtifact is a minimal type:skill ARTIFACT.md.
const solofsSkillArtifact = "---\ntype: skill\nversion: 1.0.0\ntags: [demo]\nsensitivity: low\n---\n\nSkill body lives in SKILL.md.\n"

// solofsRuleArtifact returns a minimal type:rule ARTIFACT.md.
func solofsRuleArtifact(mode string) string {
	return "---\ntype: rule\nversion: 1.0.0\nrule_mode: " + mode + "\n---\n\nRule body.\n"
}

// solofsMultiLayerConfig is the minimal .registry-config for multi-layer mode.
const solofsMultiLayerConfig = "multi_layer: true\n"

// solofsRegistryConfig returns a .registry-config with an explicit layer_order.
func solofsRegistryConfig(order ...string) string {
	s := "multi_layer: true\nlayer_order:\n"
	for _, id := range order {
		s += "  - " + id + "\n"
	}
	return s
}

// solofsWriteFile writes content to path, creating parent directories.
func solofsWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// solofsWriteSyncYAML writes <ws>/.podium/sync.yaml.
func solofsWriteSyncYAML(t *testing.T, ws, body string) {
	t.Helper()
	dir := filepath.Join(ws, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sync.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
}

// ---- tests ------------------------------------------------------------------

// T-D-solo-fs-1
func TestSoloFS_1_InitWritesSyncYAML(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	reg := t.TempDir()
	res := runPodium(t, ws, nil, "init", "--registry", reg, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))
	if !strings.Contains(yaml, "registry: "+reg) {
		t.Errorf("sync.yaml missing registry %q:\n%s", reg, yaml)
	}
	if !strings.Contains(yaml, "harness: claude-code") {
		t.Errorf("sync.yaml missing harness: claude-code:\n%s", yaml)
	}
	if !strings.Contains(res.Stdout, "Wrote") {
		t.Errorf("stdout missing 'Wrote': %q", res.Stdout)
	}
}

// T-D-solo-fs-2
func TestSoloFS_2_InitWritesGitignore(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	res := runPodium(t, ws, nil, "init", "--registry", "/some/path")
	if res.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	gi := readFile(t, filepath.Join(ws, ".gitignore"))
	for _, want := range []string{".podium/sync.local.yaml", ".podium/overlay/"} {
		if !strings.Contains(gi, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, gi)
		}
	}
}

// T-D-solo-fs-3
func TestSoloFS_3_InitGlobalWritesHomeConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := runPodium(t, "", []string{"HOME=" + home}, "init", "--global", "--registry", "/path/to/registry")
	if res.Exit != 0 {
		t.Fatalf("init --global exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	yaml := readFile(t, filepath.Join(home, ".podium", "sync.yaml"))
	if !strings.Contains(yaml, "registry: /path/to/registry") {
		t.Errorf("~/.podium/sync.yaml missing registry:\n%s", yaml)
	}
}

// T-D-solo-fs-4
func TestSoloFS_4_InitRefusesOverwriteWithoutForce(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if r := runPodium(t, ws, nil, "init", "--registry", "/first-path"); r.Exit != 0 {
		t.Fatalf("first init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	first := readFile(t, filepath.Join(ws, ".podium", "sync.yaml"))

	r2 := runPodium(t, ws, nil, "init", "--registry", "/second-path")
	if r2.Exit == 0 {
		t.Errorf("second init expected non-zero exit, got 0")
	}
	if !strings.Contains(r2.Stderr, "already exists") {
		t.Errorf("stderr missing 'already exists': %q", r2.Stderr)
	}
	if readFile(t, filepath.Join(ws, ".podium", "sync.yaml")) != first {
		t.Errorf("sync.yaml changed despite refused overwrite")
	}

	if r3 := runPodium(t, ws, nil, "init", "--registry", "/third-path", "--force"); r3.Exit != 0 {
		t.Fatalf("forced init exit=%d stderr=%s", r3.Exit, r3.Stderr)
	}
	if yaml := readFile(t, filepath.Join(ws, ".podium", "sync.yaml")); !strings.Contains(yaml, "/third-path") {
		t.Errorf("sync.yaml not overwritten with --force:\n%s", yaml)
	}
}

// T-D-solo-fs-5
func TestSoloFS_5_MultiLayerEachSubdirIsLayer(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": solofsMultiLayerConfig,
		"team-shared/finance/close-reporting/run-variance-analysis/ARTIFACT.md": solofsSkillArtifact,
		"team-shared/finance/close-reporting/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
		"personal/notes/ARTIFACT.md":                                            contextArtifact("notes"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "finance", "close-reporting", "run-variance-analysis", "ARTIFACT.md"))
	mustExist(t, filepath.Join(target, "finance", "close-reporting", "run-variance-analysis", "SKILL.md"))
	mustExist(t, filepath.Join(target, "notes", "ARTIFACT.md"))
}

// T-D-solo-fs-6
// Layer order defaults to alphabetical when layer_order is absent.
// Verified via filesystem.Open directly, asserting Layers slice order.
func TestSoloFS_6_LayerOrderDefaultsAlphabetical(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":             solofsMultiLayerConfig,
		"zebra-layer/note/ARTIFACT.md": contextArtifact("zebra"),
		"alpha-layer/note/ARTIFACT.md": contextArtifact("alpha"),
	})
	r, err := filesystem.Open(reg)
	if err != nil {
		t.Fatalf("filesystem.Open: %v", err)
	}
	if len(r.Layers) < 2 {
		t.Fatalf("expected at least 2 layers, got %d", len(r.Layers))
	}
	if r.Layers[0].ID != "alpha-layer" {
		t.Errorf("Layers[0].ID = %q, want alpha-layer (alphabetical first)", r.Layers[0].ID)
	}
	if r.Layers[1].ID != "zebra-layer" {
		t.Errorf("Layers[1].ID = %q, want zebra-layer (alphabetical second)", r.Layers[1].ID)
	}
}

// T-D-solo-fs-7
// layer_order in .registry-config overrides alphabetical ordering.
// Verified via filesystem.Open directly.
func TestSoloFS_7_LayerOrderExplicitOverridesAlphabetical(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":             solofsRegistryConfig("team-shared", "personal"),
		"personal/note/ARTIFACT.md":    contextArtifact("personal"),
		"team-shared/note/ARTIFACT.md": contextArtifact("team-shared"),
	})
	r, err := filesystem.Open(reg)
	if err != nil {
		t.Fatalf("filesystem.Open: %v", err)
	}
	if len(r.Layers) < 2 {
		t.Fatalf("expected at least 2 layers, got %d", len(r.Layers))
	}
	if r.Layers[0].ID != "team-shared" {
		t.Errorf("Layers[0].ID = %q, want team-shared", r.Layers[0].ID)
	}
	if r.Layers[1].ID != "personal" {
		t.Errorf("Layers[1].ID = %q, want personal", r.Layers[1].ID)
	}
}

// T-D-solo-fs-8
func TestSoloFS_8_AbsentRegistryConfigSingleLayer(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/report/ARTIFACT.md": contextArtifact("report"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "finance", "report", "ARTIFACT.md"))
}

// T-D-solo-fs-9
func TestSoloFS_9_MultiLayerFalseSingleLayer(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":          "multi_layer: false\n",
		"notes/my-note/ARTIFACT.md": contextArtifact("my-note"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "notes", "my-note", "ARTIFACT.md"))
}

// T-D-solo-fs-10
func TestSoloFS_10_TopLevelArtifactAmbiguous(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": solofsMultiLayerConfig,
		"ARTIFACT.md":      contextArtifact("top-level"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit for ambiguous top-level ARTIFACT.md, got 0")
	}
	if !strings.Contains(res.Stderr, "ambiguous") {
		t.Errorf("stderr missing 'ambiguous': %q", res.Stderr)
	}
}

// T-D-solo-fs-11
func TestSoloFS_11_DotDirectoriesSkippedAsLayers(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":            solofsMultiLayerConfig,
		".git/HEAD":                   "ref: refs/heads/main\n",
		"real-layer/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	mustExist(t, filepath.Join(target, "note", "ARTIFACT.md"))
}

// T-D-solo-fs-12
func TestSoloFS_12_InvalidRegistryConfigNonZero(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": "multi_layer: [not a bool",
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit for invalid .registry-config, got 0")
	}
	// The error wraps ErrConfigInvalid; some text about config or invalid appears.
	combined := res.Stderr + res.Stdout
	if !strings.Contains(combined, "invalid") && !strings.Contains(combined, "config") {
		t.Errorf("output missing 'invalid'/'config': stderr=%q stdout=%q", res.Stderr, res.Stdout)
	}
}

// T-D-solo-fs-13
func TestSoloFS_13_NonExistentRegistryNonZero(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", "/tmp/definitely-does-not-exist-podium-test-solo", "--target", target)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit for missing registry, got 0")
	}
	combined := res.Stderr + res.Stdout
	if !strings.Contains(combined, "not") && !strings.Contains(combined, "exist") && !strings.Contains(combined, "found") {
		t.Errorf("output missing path-not-found message: stderr=%q stdout=%q", res.Stderr, res.Stdout)
	}
}

// T-D-solo-fs-14
func TestSoloFS_14_SyncSkillViaClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": solofsMultiLayerConfig,
		"team-shared/finance/close-reporting/run-variance-analysis/ARTIFACT.md": solofsSkillArtifact,
		"team-shared/finance/close-reporting/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, ".claude", "skills", "run-variance-analysis", "SKILL.md"))
}

// T-D-solo-fs-15
func TestSoloFS_15_SyncRuleViaClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/ts-style/ARTIFACT.md": solofsRuleArtifact("always"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, ".claude", "rules", "ts-style.md"))
}

// T-D-solo-fs-16
func TestSoloFS_16_SyncAgentViaClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/pay-invoice/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: Pay invoice agent.\n---\n\nAgent body.\n",
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, ".claude", "agents", "pay-invoice.md"))
}

// T-D-solo-fs-17
func TestSoloFS_17_SyncContextViaClaudeCode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/company-glossary/ARTIFACT.md": contextArtifact("company glossary"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "claude-code")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	// context lands in the harness-neutral .podium/context/<artifact-id>/ bucket
	mustExist(t, filepath.Join(target, ".podium", "context", "shared", "company-glossary", "ARTIFACT.md"))
}

// T-D-solo-fs-18
func TestSoloFS_18_SyncProducesLockFile(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	lockPath := filepath.Join(target, ".podium", "sync.lock")
	mustExist(t, lockPath)
	lockContent := readFile(t, lockPath)
	// Must be valid YAML (non-empty, contains harness field).
	if !strings.Contains(lockContent, "harness") {
		t.Errorf("lock file missing 'harness' field:\n%s", lockContent)
	}
	if !strings.Contains(lockContent, "none") {
		t.Errorf("lock file harness not 'none':\n%s", lockContent)
	}
	if !strings.Contains(lockContent, "artifacts") {
		t.Errorf("lock file missing 'artifacts' list:\n%s", lockContent)
	}
}

// T-D-solo-fs-19
func TestSoloFS_19_SyncIdempotent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/glossary/ARTIFACT.md": contextArtifact("glossary"),
	})
	target := t.TempDir()
	res1 := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res1.Exit != 0 {
		t.Fatalf("first sync exit=%d stderr=%s", res1.Exit, res1.Stderr)
	}
	first := readTreeFiltered(t, target)

	res2 := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res2.Exit != 0 {
		t.Fatalf("second sync exit=%d stderr=%s", res2.Exit, res2.Stderr)
	}
	second := readTreeFiltered(t, target)

	if len(first) != len(second) {
		t.Fatalf("file count changed: %d -> %d", len(first), len(second))
	}
	for path, content := range first {
		if second[path] != content {
			t.Errorf("content for %s changed between runs", path)
		}
	}
}

// T-D-solo-fs-20
func TestSoloFS_20_StaleFilesRemovedOnDelete(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/artifact-a/ARTIFACT.md": contextArtifact("artifact-a"),
		"shared/artifact-b/ARTIFACT.md": contextArtifact("artifact-b"),
	})
	target := t.TempDir()
	res1 := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res1.Exit != 0 {
		t.Fatalf("first sync exit=%d stderr=%s", res1.Exit, res1.Stderr)
	}
	mustExist(t, filepath.Join(target, "shared", "artifact-a", "ARTIFACT.md"))
	mustExist(t, filepath.Join(target, "shared", "artifact-b", "ARTIFACT.md"))

	// Delete artifact-b from the registry.
	if err := os.RemoveAll(filepath.Join(reg, "shared", "artifact-b")); err != nil {
		t.Fatalf("remove artifact-b: %v", err)
	}

	res2 := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res2.Exit != 0 {
		t.Fatalf("second sync exit=%d stderr=%s", res2.Exit, res2.Stderr)
	}
	mustExist(t, filepath.Join(target, "shared", "artifact-a", "ARTIFACT.md"))
	if _, err := os.Stat(filepath.Join(target, "shared", "artifact-b", "ARTIFACT.md")); err == nil {
		t.Errorf("stale artifact-b/ARTIFACT.md still present after second sync")
	}
}

// T-D-solo-fs-21
func TestSoloFS_21_SyncDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "dry-run") {
		t.Errorf("stdout missing 'dry-run' marker: %q", res.Stdout)
	}
	// Target must remain empty (no files written, no lock file).
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("readdir target: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("dry-run wrote files to target: %v", entries)
	}
}

// T-D-solo-fs-22
func TestSoloFS_22_SyncReadsRegistryFromSyncYAML(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	ws := t.TempDir()
	solofsWriteSyncYAML(t, ws, "defaults:\n  registry: "+reg+"\n")
	target := t.TempDir()
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY="}, "sync", "--target", target)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "shared", "note", "ARTIFACT.md"))
}

// T-D-solo-fs-23
func TestSoloFS_23_SyncNoRegistryNoConfigFails(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	target := t.TempDir()
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY="}, "sync", "--target", target)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit with no registry, got 0")
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required': %q", res.Stderr)
	}
}

// T-D-solo-fs-24
func TestSoloFS_24_SyncUnknownHarnessFails(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "not-an-adapter")
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit for unknown harness, got 0")
	}
	if !strings.Contains(res.Stderr, "config.unknown_harness") {
		t.Errorf("stderr missing 'config.unknown_harness': %q", res.Stderr)
	}
}

// T-D-solo-fs-25
func TestSoloFS_25_LintCleanRegistryExits0(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"personal/hello/greet/ARTIFACT.md": greetSkillArtifact,
		"personal/hello/greet/SKILL.md":    greetSkillBody,
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d stdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "no issues") {
		t.Errorf("stdout missing 'no issues': %q", res.Stdout)
	}
}

// T-D-solo-fs-26
func TestSoloFS_26_LintMissingRegistryExits1(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint", "--registry", "/tmp/no-such-podium-registry-solo")
	if res.Exit != 1 {
		t.Errorf("exit=%d, want 1 (stderr=%s)", res.Exit, res.Stderr)
	}
}

// T-D-solo-fs-27
func TestSoloFS_27_LintNoRegistryFlagExits2(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "lint")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--registry is required") {
		t.Errorf("stderr missing '--registry is required': %q", res.Stderr)
	}
}

// T-D-solo-fs-28
func TestSoloFS_28_WatchRematerializesOnChange(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	w := startWatch(t, reg, target, "none")
	if !pollFile(filepath.Join(target, "shared", "note", "ARTIFACT.md"), 10*time.Second) {
		t.Fatalf("initial sync did not materialize\nlog:\n%s", w.log())
	}
	// Add a second artifact to the registry.
	solofsWriteFile(t, filepath.Join(reg, "shared", "note2", "ARTIFACT.md"), contextArtifact("note2"))
	if !pollFile(filepath.Join(target, "shared", "note2", "ARTIFACT.md"), 10*time.Second) {
		t.Errorf("watcher did not materialize new artifact\nlog:\n%s", w.log())
	}
	w.stop(t)
}

// T-D-solo-fs-29
func TestSoloFS_29_WatchExits0OnSIGINT(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	w := startWatch(t, reg, target, "none")
	if !pollFile(filepath.Join(target, "shared", "note", "ARTIFACT.md"), 10*time.Second) {
		t.Fatalf("initial sync did not materialize\nlog:\n%s", w.log())
	}
	if code := w.stop(t); code != 0 {
		t.Errorf("watch exit=%d on SIGINT, want 0\nlog:\n%s", code, w.log())
	}
}

// T-D-solo-fs-30
func TestSoloFS_30_OverlayOverridesRegistry(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/glossary/ARTIFACT.md": contextArtifact("from-registry"),
	})
	overlay := writeRegistry(t, map[string]string{
		"shared/glossary/ARTIFACT.md": "---\ntype: context\nversion: 1.1.0\ndescription: from-overlay\n---\n\nfrom-overlay body.\n",
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--overlay", overlay, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(target, "shared", "glossary", "ARTIFACT.md"))
	if !strings.Contains(got, "from-overlay") {
		t.Errorf("overlay did not take precedence; got:\n%s", got)
	}
}

// T-D-solo-fs-31
func TestSoloFS_31_OverrideAddRecordsInLock(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/base/ARTIFACT.md":  contextArtifact("base"),
		"shared/extra/ARTIFACT.md": contextArtifact("extra"),
	})
	target := t.TempDir()
	res1 := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res1.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res1.Exit, res1.Stderr)
	}
	res2 := runPodium(t, "", nil, "sync", "override", "--target", target, "--add", "shared/extra")
	if res2.Exit != 0 {
		t.Fatalf("override exit=%d stderr=%s", res2.Exit, res2.Stderr)
	}
	if !strings.Contains(res2.Stdout, "toggles.add") {
		t.Errorf("stdout missing 'toggles.add': %q", res2.Stdout)
	}
	lock := readFile(t, filepath.Join(target, ".podium", "sync.lock"))
	if !strings.Contains(lock, "shared/extra") {
		t.Errorf("lock file missing 'shared/extra': %s", lock)
	}
}

// T-D-solo-fs-32
func TestSoloFS_32_OverrideRemoveRecordsInLock(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/artifact-a/ARTIFACT.md": contextArtifact("artifact-a"),
	})
	target := t.TempDir()
	res1 := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res1.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res1.Exit, res1.Stderr)
	}
	res2 := runPodium(t, "", nil, "sync", "override", "--target", target, "--remove", "shared/artifact-a")
	if res2.Exit != 0 {
		t.Fatalf("override exit=%d stderr=%s", res2.Exit, res2.Stderr)
	}
	if !strings.Contains(res2.Stdout, "toggles.remove") {
		t.Errorf("stdout missing 'toggles.remove': %q", res2.Stdout)
	}
	lock := readFile(t, filepath.Join(target, ".podium", "sync.lock"))
	if !strings.Contains(lock, "shared/artifact-a") {
		t.Errorf("lock file missing 'shared/artifact-a': %s", lock)
	}
}

// T-D-solo-fs-33
func TestSoloFS_33_OverrideResetClearsToggles(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/artifact-a/ARTIFACT.md": contextArtifact("artifact-a"),
	})
	target := t.TempDir()
	if r := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none"); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, "", nil, "sync", "override", "--target", target, "--add", "shared/artifact-a"); r.Exit != 0 {
		t.Fatalf("override --add exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, "", nil, "sync", "override", "--target", target, "--reset"); r.Exit != 0 {
		t.Fatalf("override --reset exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	lock := readFile(t, filepath.Join(target, ".podium", "sync.lock"))
	// After reset the toggles section should be absent or its lists empty.
	// We check that no non-empty add/remove entries appear.
	if strings.Contains(lock, "- shared/artifact-a") {
		t.Errorf("lock file still has toggle entries after --reset:\n%s", lock)
	}
}

// T-D-solo-fs-34
func TestSoloFS_34_SaveAsCreatesProfile(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	if r := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none"); r.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	res := runPodium(t, "", nil, "sync", "save-as", "--target", target, "--profile", "my-profile")
	if res.Exit != 0 {
		t.Fatalf("save-as exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "my-profile") {
		t.Errorf("stdout missing 'my-profile': %q", res.Stdout)
	}
}

// T-D-solo-fs-35
func TestSoloFS_35_SaveAsNoProfileExits2(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "save-as", "--target", target)
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--profile is required") {
		t.Errorf("stderr missing '--profile is required': %q", res.Stderr)
	}
}

// T-D-solo-fs-36
func TestSoloFS_36_MultipleWorkspacesIndependentLocks(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	targetA := t.TempDir()
	targetB := t.TempDir()

	if r := runPodium(t, "", nil, "sync", "--registry", reg, "--target", targetA, "--harness", "none"); r.Exit != 0 {
		t.Fatalf("sync A exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	if r := runPodium(t, "", nil, "sync", "--registry", reg, "--target", targetB, "--harness", "none"); r.Exit != 0 {
		t.Fatalf("sync B exit=%d stderr=%s", r.Exit, r.Stderr)
	}

	lockA := readFile(t, filepath.Join(targetA, ".podium", "sync.lock"))
	lockB := readFile(t, filepath.Join(targetB, ".podium", "sync.lock"))

	if !strings.Contains(lockA, targetA) {
		t.Errorf("lock A does not reference targetA path:\n%s", lockA)
	}
	if !strings.Contains(lockB, targetB) {
		t.Errorf("lock B does not reference targetB path:\n%s", lockB)
	}
	if strings.Contains(lockA, targetB) {
		t.Errorf("lock A unexpectedly references targetB path")
	}
	if strings.Contains(lockB, targetA) {
		t.Errorf("lock B unexpectedly references targetA path")
	}
}

// T-D-solo-fs-37
// podium serve --standalone --layer-path accepts the same filesystem registry.
func TestSoloFS_37_ServeStandaloneLayerPath(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                         solofsMultiLayerConfig,
		"alice-personal/notes/welcome/ARTIFACT.md": contextArtifact("welcome"),
	})
	srv := startServer(t, reg)

	var layers map[string]any
	getJSON(t, srv.BaseURL+"/v1/layers", &layers)
	arr, _ := layers["layers"].([]any)
	found := false
	for _, l := range arr {
		lm, _ := l.(map[string]any)
		// store.LayerConfig serializes with capitalized Go field names.
		if lm["ID"] == "alice-personal" && lm["SourceType"] == "local" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("/v1/layers missing alice-personal local layer: %v", layers)
	}
}

// T-D-solo-fs-38
// --layer-path flag maps to PODIUM_LAYER_PATH; confirmed via e2e: start server with
// --layer-path and verify /v1/layers reflects it (same as -37 effectively).
func TestSoloFS_38_LayerPathFlagEnvMapping(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":         solofsMultiLayerConfig,
		"mylayer/note/ARTIFACT.md": contextArtifact("note"),
	})
	srv := startServerArgs(t,
		[]string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--layer-path", reg,
	)
	var layers map[string]any
	getJSON(t, srv.BaseURL+"/v1/layers", &layers)
	arr, _ := layers["layers"].([]any)
	found := false
	for _, l := range arr {
		lm, _ := l.(map[string]any)
		if lm["ID"] == "mylayer" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("/v1/layers missing 'mylayer' layer; --layer-path not mapped correctly: %v", layers)
	}
}

// T-D-solo-fs-39
func TestSoloFS_39_SearchFailsAgainstFilesystemRegistry(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	res := runPodium(t, "", []string{"PODIUM_REGISTRY=" + reg}, "search", "some query")
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit for search against filesystem registry, got 0")
	}
}

// T-D-solo-fs-40
func TestSoloFS_40_LoginRequiresIssuer(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "login", "--registry", "/tmp/some/filesystem/path")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--issuer or PODIUM_OAUTH_AUTHORIZATION_ENDPOINT is required") {
		t.Errorf("stderr missing issuer message: %q", res.Stderr)
	}
}

// T-D-solo-fs-41
func TestSoloFS_41_LayerOrderGhostLayerIgnored(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":            solofsRegistryConfig("ghost-layer", "real-layer"),
		"real-layer/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "note", "ARTIFACT.md"))
}

// T-D-solo-fs-42
// layer_order extra layers not in explicit list are appended alphabetically.
// Verified via filesystem.Open directly.
func TestSoloFS_42_LayerOrderExtraLayersAppendedAlphabetically(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":             solofsRegistryConfig("beta-layer"),
		"alpha-layer/note/ARTIFACT.md": contextArtifact("alpha"),
		"beta-layer/note/ARTIFACT.md":  contextArtifact("beta"),
		"gamma-layer/note/ARTIFACT.md": contextArtifact("gamma"),
	})
	r, err := filesystem.Open(reg)
	if err != nil {
		t.Fatalf("filesystem.Open: %v", err)
	}
	if len(r.Layers) < 3 {
		t.Fatalf("expected at least 3 layers, got %d", len(r.Layers))
	}
	if r.Layers[0].ID != "beta-layer" {
		t.Errorf("Layers[0].ID = %q, want beta-layer (explicit first)", r.Layers[0].ID)
	}
	if r.Layers[1].ID != "alpha-layer" {
		t.Errorf("Layers[1].ID = %q, want alpha-layer (unlisted, alphabetical)", r.Layers[1].ID)
	}
	if r.Layers[2].ID != "gamma-layer" {
		t.Errorf("Layers[2].ID = %q, want gamma-layer (unlisted, alphabetical)", r.Layers[2].ID)
	}
}

// T-D-solo-fs-43
// Same artifact ID in two layers raises ingest.collision via lint
// (lint uses CollisionPolicyDefault; sync uses CollisionPolicyHighestWins).
func TestSoloFS_43_CollisionRaisedByLint(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                solofsMultiLayerConfig,
		"layer-a/shared/note/ARTIFACT.md": contextArtifact("from-a"),
		"layer-b/shared/note/ARTIFACT.md": contextArtifact("from-b"),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit for collision, got 0 (stdout=%s)", res.Stdout)
	}
	combined := res.Stdout + res.Stderr
	if !strings.Contains(combined, "collision") && !strings.Contains(combined, "ingest.collision") {
		t.Errorf("output missing collision diagnostic: stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
}

// T-D-solo-fs-44
// sync with CollisionPolicyHighestWins: later layer in layer_order wins.
func TestSoloFS_44_CollisionHighestWinsLaterLayerWins(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                       solofsRegistryConfig("base-layer", "override-layer"),
		"base-layer/shared/note/ARTIFACT.md":     "---\ntype: context\nversion: 1.0.0\ndescription: from-base\n---\n\nfrom-base body.\n",
		"override-layer/shared/note/ARTIFACT.md": "---\ntype: context\nversion: 1.1.0\ndescription: from-override\n---\n\nfrom-override body.\n",
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	got := readFile(t, filepath.Join(target, "shared", "note", "ARTIFACT.md"))
	if !strings.Contains(got, "from-override") {
		t.Errorf("later layer should win; got:\n%s", got)
	}
}

// T-D-solo-fs-45
// DOMAIN.md files at layer root are not treated as artifacts.
func TestSoloFS_45_DomainMDInLayerNotTreatedAsArtifact(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":              solofsMultiLayerConfig,
		"team-shared/DOMAIN.md":         "---\ntitle: Finance\n---\n\nFinance domain.\n",
		"team-shared/notes/ARTIFACT.md": contextArtifact("notes"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "notes", "ARTIFACT.md"))
	// DOMAIN.md should not appear as an artifact output at the target root.
	if _, err := os.Stat(filepath.Join(target, "DOMAIN.md")); err == nil {
		t.Errorf("DOMAIN.md appeared directly in target as an artifact")
	}
}

// T-D-solo-fs-46
// DOMAIN.md at multi_layer root top level triggers ErrLayerPathAmbiguous.
func TestSoloFS_46_TopLevelDomainMDAmbiguous(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": solofsMultiLayerConfig,
		"DOMAIN.md":        "---\ntitle: Root\n---\n\nRoot domain.\n",
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit for DOMAIN.md at multi_layer root, got 0")
	}
	if !strings.Contains(res.Stderr, "ambiguous") {
		t.Errorf("stderr missing 'ambiguous': %q", res.Stderr)
	}
}

// T-D-solo-fs-47
func TestSoloFS_47_SyncDryRunJSONStructured(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--dry-run", "--json")
	if res.Exit != 0 {
		t.Fatalf("dry-run --json exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, res.Stdout)
	}
	if _, ok := env["artifacts"]; !ok {
		t.Errorf("JSON envelope missing 'artifacts': %v", env)
	}
}

// T-D-solo-fs-48
// Visibility declarations are recorded but not enforced in filesystem mode.
func TestSoloFS_48_VisibilityNotEnforced(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/private-note/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: private note\nsensitivity: high\n---\n\nPrivate body.\n",
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "shared", "private-note", "ARTIFACT.md"))
}

// T-D-solo-fs-49
// sync.yaml relative registry path resolves against the workspace directory.
func TestSoloFS_49_RelativeRegistryPathResolved(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	// Create the registry inside the workspace directory.
	regDir := filepath.Join(ws, "my-registry")
	if err := os.MkdirAll(filepath.Join(regDir, "shared", "note"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(regDir, "shared", "note", "ARTIFACT.md"), []byte(contextArtifact("note")), 0o644); err != nil {
		t.Fatalf("write ARTIFACT.md: %v", err)
	}
	// Write sync.yaml with a relative path.
	solofsWriteSyncYAML(t, ws, "defaults:\n  registry: ./my-registry\n")
	target := t.TempDir()
	res := runPodium(t, ws, []string{"PODIUM_REGISTRY="}, "sync", "--target", target)
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "shared", "note", "ARTIFACT.md"))
}

// T-D-solo-fs-50
// Git history serves as the audit trail; no Podium-side audit stream in filesystem mode.
func TestSoloFS_50_NoAuditLogInFilesystemMode(t *testing.T) {
	t.Parallel()
	if _, ok := runExternal(t, t.TempDir(), 10*time.Second, "git", "version"); !ok {
		t.Skip("git not installed")
	}
	gitReg := t.TempDir()
	if r, _ := runExternal(t, gitReg, 10*time.Second, "git", "init"); r.Exit != 0 {
		t.Fatalf("git init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	solofsWriteFile(t, filepath.Join(gitReg, ".registry-config"), solofsMultiLayerConfig)
	solofsWriteFile(t, filepath.Join(gitReg, "shared", "note", "ARTIFACT.md"), contextArtifact("note"))
	runExternal(t, gitReg, 10*time.Second, "git", "-c", "user.email=alice@acme.com", "-c", "user.name=alice", "add", ".")
	runExternal(t, gitReg, 10*time.Second, "git", "-c", "user.email=alice@acme.com", "-c", "user.name=alice", "commit", "-m", "initial")

	// Modify and commit again.
	solofsWriteFile(t, filepath.Join(gitReg, "shared", "note", "ARTIFACT.md"), contextArtifact("note-v2"))
	runExternal(t, gitReg, 10*time.Second, "git", "-c", "user.email=alice@acme.com", "-c", "user.name=alice", "add", ".")
	runExternal(t, gitReg, 10*time.Second, "git", "-c", "user.email=alice@acme.com", "-c", "user.name=alice", "commit", "-m", "update note")

	// Git log should show two commits.
	logRes, _ := runExternal(t, gitReg, 10*time.Second, "git", "log", "--oneline")
	lines := strings.Split(strings.TrimSpace(logRes.Stdout), "\n")
	if len(lines) < 2 {
		t.Errorf("git log shows fewer than 2 commits: %q", logRes.Stdout)
	}

	// podium sync must not produce any audit log.
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", gitReg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	// No audit log under target or gitReg.
	for _, root := range []string{target, gitReg} {
		_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if strings.Contains(fi.Name(), "audit") && !strings.HasPrefix(fi.Name(), ".") {
				t.Errorf("unexpected audit file found: %s", p)
			}
			return nil
		})
	}
}

// T-D-solo-fs-51
// Freeze windows and signing enforcement are unavailable in filesystem mode:
// podium sync succeeds without requiring a signature or checking a freeze window.
func TestSoloFS_51_NoFreezeOrSigningEnforcement(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync blocked by signing/freeze enforcement (exit=%d stderr=%s)", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(target, "shared", "note", "ARTIFACT.md"))

	// podium verify --content-hash with --provider noop operates locally;
	// it does not query a server or check a freeze window.
	verRes := runPodium(t, "", nil, "verify",
		"--content-hash", "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--signature", "noop:sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"--provider", "noop",
	)
	// Either succeeds or fails locally; what matters is that it does not error
	// with a server-required message.
	if strings.Contains(verRes.Stderr, "registry is required") || strings.Contains(verRes.Stderr, "freeze") {
		t.Errorf("verify unexpectedly required a server or freeze check: %q", verRes.Stderr)
	}
}

// T-D-solo-fs-52
// podium-mcp cannot be used against a filesystem registry path.
func TestSoloFS_52_MCPFailsAgainstFilesystemRegistry(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"shared/note/ARTIFACT.md": contextArtifact("note"),
	})
	mcpEnv := []string{
		"PODIUM_REGISTRY=" + reg,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	}
	res := mcpExec(t, mcpEnv,
		rpcReq{ID: 1, Method: "initialize", Params: initParams},
		toolCall(2, "load_domain", map[string]any{"id": "shared"}),
	)
	// The load_domain call should surface an error because there is no HTTP
	// server backing the filesystem path.
	env2 := rpcEnvelope(t, res.Stdout, 2)
	hasErr := false
	if e, ok := env2["error"]; ok && e != nil {
		hasErr = true
	}
	if res2, ok := env2["result"].(map[string]any); ok {
		if e, _ := res2["error"].(string); e != "" {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("expected load_domain to return an error for filesystem registry, but got: %v", env2)
	}
}

// T-D-solo-fs-53 (F-2.2.2)
// podium sync output is bit-identical between a filesystem source and a
// standalone server pointed at the same registry. The server-source path
// reads the effective view over HTTP and runs the same harness adapter and
// materialization writer, so the on-disk result matches the filesystem source
// byte for byte.
//
// spec: §2.2, §7.5
func TestSoloFS_53_BitIdenticalFilesystemVsServer(t *testing.T) {
	t.Parallel()
	registry := writeRegistry(t, map[string]string{
		".registry-config":          "multi_layer: true\n",
		"team/glossary/ARTIFACT.md": contextArtifact("glossary"),
		"team/policy/ARTIFACT.md":   contextArtifact("policy"),
	})

	fsTarget := t.TempDir()
	resFS := runPodium(t, "", nil, "sync",
		"--registry", registry, "--target", fsTarget, "--harness", "none")
	if resFS.Exit != 0 {
		t.Fatalf("filesystem sync exit=%d\nstderr: %s", resFS.Exit, resFS.Stderr)
	}

	srv := startServer(t, registry)
	serverTarget := t.TempDir()
	resSrv := runPodium(t, "", nil, "sync",
		"--registry", srv.BaseURL, "--target", serverTarget, "--harness", "none")
	if resSrv.Exit != 0 {
		t.Fatalf("server-source sync exit=%d\nstderr: %s", resSrv.Exit, resSrv.Stderr)
	}

	fsFiles := materializedFiles(t, fsTarget)
	srvFiles := materializedFiles(t, serverTarget)
	if len(fsFiles) == 0 {
		t.Fatal("filesystem sync materialized nothing")
	}
	for path, fsContent := range fsFiles {
		srvContent, ok := srvFiles[path]
		if !ok {
			t.Errorf("server sync missing %s that filesystem sync wrote", path)
			continue
		}
		if fsContent != srvContent {
			t.Errorf("content mismatch for %s:\nfs:     %q\nserver: %q", path, fsContent, srvContent)
		}
	}
	for path := range srvFiles {
		if _, ok := fsFiles[path]; !ok {
			t.Errorf("server sync wrote %s that filesystem sync did not", path)
		}
	}
}

// podium sync dispatches on the §7.5.2 registry source: a URL routes to a
// Podium server (server-source materialization, F-2.2.2), a filesystem path
// routes to local filesystem. A server URL must not be collapsed into a bogus
// filesystem path; pointed at a live server it materializes the effective
// view.
//
// spec: §2.2, §7.5.2
func TestSoloFS_ServerSourceURLMaterializes(t *testing.T) {
	t.Parallel()
	registry := writeRegistry(t, map[string]string{
		".registry-config":                 "multi_layer: true\n",
		"team-shared/glossary/ARTIFACT.md": contextArtifact("glossary"),
	})
	srv := startServer(t, registry)
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync",
		"--registry", srv.BaseURL, "--target", target, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("server-source sync exit=%d\nstderr: %s", res.Exit, res.Stderr)
	}
	// The pre-fix bug collapsed the URL into a bogus filesystem path.
	if strings.Contains(res.Stderr, "registry path does not exist") {
		t.Errorf("stderr leaked the mangled filesystem error:\n%s", res.Stderr)
	}
	// In multi-layer mode the leading path segment is the layer, so the
	// canonical id is "glossary" and materializes at <target>/glossary/.
	if _, err := os.Stat(filepath.Join(target, "glossary", "ARTIFACT.md")); err != nil {
		t.Errorf("server-source sync did not materialize glossary/ARTIFACT.md: %v", err)
	}
}

// materializedFiles returns a path->content map of every file under root,
// keyed by the slash-separated path relative to root, excluding sync state.
func materializedFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".podium/") {
			return nil // per-target sync.lock and state are not materialized output
		}
		out[rel] = readFile(t, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
