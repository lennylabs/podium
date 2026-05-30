package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// writeWorkspaceConfig writes a sync.yaml under ws/.podium and returns its
// absolute path.
func writeWorkspaceConfig(t *testing.T, ws, content string) string {
	t.Helper()
	pod := filepath.Join(ws, ".podium")
	if err := os.MkdirAll(pod, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pod, "sync.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// spec: §7.5.2 — `podium sync --profile <name>` loads the named scope from
// sync.yaml and applies its include/exclude/type (F-7.5.2).
func TestSync_ProfileResolvesScope(t *testing.T) {
	reg := cliReg(t)
	ws := t.TempDir()
	tgt := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\nprofiles:\n  finance:\n    include: [\"finance/**\"]\n")

	res := runPodium(t, ws, nil, "sync", "--profile", "finance", "--target", tgt, "--harness", "none")
	cliWantExit(t, res, 0, "sync --profile")
	files := readTreeFiltered(t, tgt)
	if _, ok := files["finance/invoice/ARTIFACT.md"]; !ok {
		t.Fatalf("profile finance must materialize finance/invoice: %v", keysOf(files))
	}
	if _, ok := files["personal/greet/ARTIFACT.md"]; ok {
		t.Fatalf("profile finance must exclude personal/greet: %v", keysOf(files))
	}
	// §7.5.3: the active profile and scope are recorded in the lock.
	lock := readFile(t, filepath.Join(tgt, ".podium/sync.lock"))
	cliContains(t, lock, "profile: finance", "lock records active profile")
}

// spec: §7.5.2 — registry is read from defaults.registry when --registry is
// omitted, and a stale --profile that names nothing fails (F-7.5.2).
func TestSync_UnknownProfileFails(t *testing.T) {
	reg := cliReg(t)
	ws := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\n")
	res := runPodium(t, ws, nil, "sync", "--profile", "ghost", "--target", t.TempDir(), "--harness", "none")
	cliWantNonZero(t, res, "sync --profile ghost")
}

// spec: §7.5.2 — `podium sync --config <path>` iterates targets: and runs one
// sync per entry, each with its own scope and target (F-7.5.2).
func TestSync_MultiTargetConfig(t *testing.T) {
	reg := cliReg(t)
	ws := t.TempDir()
	tgtA := t.TempDir()
	tgtB := t.TempDir()
	cfgPath := writeWorkspaceConfig(t, ws,
		"defaults:\n  registry: "+reg+"\n"+
			"profiles:\n  fin:\n    include: [\"finance/**\"]\n"+
			"targets:\n"+
			"  - id: a\n    target: "+tgtA+"\n    profile: fin\n"+
			"  - id: b\n    target: "+tgtB+"\n    include: [\"personal/**\"]\n")

	res := runPodium(t, ws, nil, "sync", "--config", cfgPath)
	cliWantExit(t, res, 0, "sync --config")

	a := readTreeFiltered(t, tgtA)
	if _, ok := a["finance/invoice/ARTIFACT.md"]; !ok {
		t.Fatalf("target a (profile fin) missing finance/invoice: %v", keysOf(a))
	}
	if _, ok := a["personal/greet/ARTIFACT.md"]; ok {
		t.Fatalf("target a must not include personal: %v", keysOf(a))
	}
	b := readTreeFiltered(t, tgtB)
	if _, ok := b["personal/greet/ARTIFACT.md"]; !ok {
		t.Fatalf("target b (inline personal/**) missing personal/greet: %v", keysOf(b))
	}
	if _, ok := b["finance/invoice/ARTIFACT.md"]; ok {
		t.Fatalf("target b must not include finance: %v", keysOf(b))
	}
}

// spec: §7.5.5 — `podium sync override --add <id>` writes the artifact through
// the active adapter just like a full sync, bringing in an out-of-scope but
// visible artifact (F-7.5.5).
func TestSyncOverride_AddMaterializes(t *testing.T) {
	reg := cliReg(t)
	tgt := t.TempDir()
	// Baseline sync scoped to personal/** so finance/invoice is out of scope.
	cliWantExit(t, runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt,
		"--harness", "none", "--include", "personal/**"), 0, "baseline sync")
	if _, ok := readTreeFiltered(t, tgt)["finance/invoice/ARTIFACT.md"]; ok {
		t.Fatalf("precondition: finance/invoice should be out of the baseline scope")
	}
	// Override adds finance/invoice with a registry, so it materializes.
	cliWantExit(t, runPodium(t, "", nil, "sync", "override", "--add", "finance/invoice",
		"--registry", reg, "--harness", "none", "--target", tgt), 0, "override --add materialize")
	if _, ok := readTreeFiltered(t, tgt)["finance/invoice/ARTIFACT.md"]; !ok {
		t.Fatalf("override --add must materialize finance/invoice: %v", keysOf(readTreeFiltered(t, tgt)))
	}
}

// spec: §7.5.5 — `podium sync override --remove <id>` deletes the artifact's
// materialized files from the target (F-7.5.5).
func TestSyncOverride_RemoveDeletes(t *testing.T) {
	reg := cliReg(t)
	tgt := t.TempDir()
	cliWantExit(t, runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt,
		"--harness", "none", "--include", "personal/**"), 0, "baseline sync")
	if _, ok := readTreeFiltered(t, tgt)["personal/greet/ARTIFACT.md"]; !ok {
		t.Fatalf("precondition: personal/greet should be materialized")
	}
	cliWantExit(t, runPodium(t, "", nil, "sync", "override", "--remove", "personal/greet",
		"--registry", reg, "--harness", "none", "--target", tgt), 0, "override --remove delete")
	if _, ok := readTreeFiltered(t, tgt)["personal/greet/ARTIFACT.md"]; ok {
		t.Fatalf("override --remove must delete personal/greet from disk")
	}
}

// spec: §7.5.2 — project-local sync.local.yaml overrides project-shared
// sync.yaml per key (F-7.5.3). The project-local registry points at a registry
// holding only finance, so the sync materializes finance, proving the
// higher-precedence scope file won.
func TestSync_ProjectLocalOverridesShared(t *testing.T) {
	shared := cliReg(t) // personal/greet, finance/invoice, personal/note
	localOnly := writeRegistry(t, map[string]string{
		"finance/invoice/ARTIFACT.md": contextArtifact("Only finance lives in the project-local registry override."),
	})
	ws := t.TempDir()
	tgt := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+shared+"\n")
	if err := os.WriteFile(filepath.Join(ws, ".podium", "sync.local.yaml"),
		[]byte("defaults:\n  registry: "+localOnly+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runPodium(t, ws, nil, "sync", "--target", tgt, "--harness", "none")
	cliWantExit(t, res, 0, "sync with project-local override")
	files := readTreeFiltered(t, tgt)
	if _, ok := files["personal/greet/ARTIFACT.md"]; ok {
		t.Fatalf("project-local registry (finance-only) should win; personal present: %v", keysOf(files))
	}
	if _, ok := files["finance/invoice/ARTIFACT.md"]; !ok {
		t.Fatalf("finance/invoice from project-local registry missing: %v", keysOf(files))
	}
}
