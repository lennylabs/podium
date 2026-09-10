package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// orderRegistry writes a two-layer filesystem registry whose layer-major walk
// order and whose canonical ID order disagree. layer_order: puts org-defaults
// first, and Walk sorts within a layer, so the layer-major enumeration is
// a-audit/check, z-tools/lint, a-finance/close, while ascending canonical ID
// order is a-audit/check, a-finance/close, z-tools/lint.
func orderRegistry(t *testing.T) string {
	t.Helper()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-finance\n",
		},
		testharness.WriteTreeOption{Path: "org-defaults/z-tools/lint/ARTIFACT.md", Content: contextArtifactSrc},
		testharness.WriteTreeOption{Path: "org-defaults/a-audit/check/ARTIFACT.md", Content: contextArtifactSrc},
		testharness.WriteTreeOption{Path: "team-finance/a-finance/close/ARTIFACT.md", Content: contextArtifactSrc},
	)
	return registry
}

// Spec: §7.5 — the resolved set is materialized in ascending canonical
// artifact ID order whatever the registry source and whatever the layer order
// that composed it. Result.Artifacts is the witness for the materialization
// order, and the lock's artifacts: list is the §7.5.3 assertion.
func TestRun_MaterializesInCanonicalIDOrder(t *testing.T) {
	t.Parallel()
	registry := orderRegistry(t)
	target := t.TempDir()

	res, err := Run(Options{
		RegistryPath: registry,
		Target:       target,
		AdapterID:    "none",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"a-audit/check", "a-finance/close", "z-tools/lint"}
	if got := artifactIDs(res.Artifacts); !equalStrings(got, want) {
		t.Errorf("Result.Artifacts order = %v, want %v", got, want)
	}

	// §7.5.3: the lock's artifacts: list is ordered by id.
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock: %v (lock %v)", err, lock)
	}
	gotLock := make([]string, 0, len(lock.Artifacts))
	for _, a := range lock.Artifacts {
		gotLock = append(gotLock, a.ID)
	}
	if !equalStrings(gotLock, want) {
		t.Errorf("lock artifacts order = %v, want %v", gotLock, want)
	}

	// §7.5.5: the toggles.add tail is appended after the scoped set and after
	// the §6.4 overlay, so it is the last record that can reach selectRecords.
	// Scoping the run to the other two artifacts and pulling a-audit/check in
	// through toggles.add puts it at the tail of the appended slice; the sort
	// has to move it back to the head.
	if err := WriteLock(target, &LockFile{
		Version: 1, Target: target,
		Scope:   LockScope{Include: []string{"z-tools/**", "a-finance/**"}},
		Toggles: LockToggles{Add: []LockToggle{{ID: "a-audit/check"}}},
	}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	res, err = Run(Options{
		RegistryPath:    registry,
		Target:          target,
		AdapterID:       "none",
		Scope:           ScopeFilter{Include: []string{"z-tools/**", "a-finance/**"}},
		PreserveToggles: true,
	})
	if err != nil {
		t.Fatalf("Run with toggles.add: %v", err)
	}
	if got := artifactIDs(res.Artifacts); !equalStrings(got, want) {
		t.Errorf("toggles.add tail was not ordered: Result.Artifacts = %v, want %v", got, want)
	}
}

// mcpServerSrc builds an mcp-server ARTIFACT.md. Two of them config-merge into
// one .mcp.json under the claude-code adapter, which is the shared materialized
// path this test needs; the none adapter emits per-artifact OpWrite paths only.
func mcpServerSrc(name, desc string) string {
	return "---\ntype: mcp-server\nname: " + name +
		"\nversion: 1.0.0\ndescription: " + desc +
		"\nserver_identifier: npx:@acme/" + name + "\n---\n\nbody\n"
}

// Spec: §11 (idempotent re-sync), §7.5.3 — the change comparison is keyed by
// (artifact id, materialized path), so every artifact contributing to a shared
// materialized path participates in Result.Changed. Keyed by path alone, the
// two mcp-servers writing .mcp.json collapse to whichever entry the lock lists
// last, and an edit to the other one reports no change though the file was
// rewritten.
func TestRun_ChangedSeesEveryContributorToASharedPath(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	target := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{
			Path:    "team/a-alpha/server/ARTIFACT.md",
			Content: mcpServerSrc("alpha", "Alpha server."),
		},
		testharness.WriteTreeOption{
			Path:    "team/b-beta/server/ARTIFACT.md",
			Content: mcpServerSrc("beta", "Beta server."),
		},
	)

	opts := Options{RegistryPath: registry, Target: target, AdapterID: "claude-code"}
	if _, err := Run(opts); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if res.Changed {
		t.Fatalf("an unchanged re-sync reported Changed = true")
	}

	// Edit the artifact whose canonical ID sorts first, which is the entry a
	// path-only key discards.
	edited := filepath.Join(registry, "team", "a-alpha", "server", "ARTIFACT.md")
	if err := os.WriteFile(edited, []byte(mcpServerSrc("alpha", "Alpha server, revised.")), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", edited, err)
	}
	res, err = Run(opts)
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if !res.Changed {
		t.Errorf("editing a-alpha/server, which shares .mcp.json with b-beta/server, reported Changed = false")
	}
}

func artifactIDs(as []ArtifactResult) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
