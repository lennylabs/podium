package integration

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sync"
)

// referenceRegistryPath returns the absolute path to the shared reference
// fixture, resolved from this test file's location so it runs from any cwd.
func referenceRegistryPath(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "testdata", "registries", "reference")
}

// Spec: §11 (Filesystem ↔ server equivalence test) / §2.2 (Shared library
// code) — a `podium sync` against a filesystem-source registry and a
// `podium sync` against a server pointed at the same directory produce
// byte-identical materialized output (manifest bodies, bundled resources,
// harness-adapter output) and the same lock-file artifacts list for the same
// target and profile. The server consumer runs in-process through
// server.NewFromFilesystem, the same shared library bootstrap the standalone
// `--layer-path` server uses, so the test owns its lifecycle and never blocks.
func TestSyncEquivalence_FilesystemVsServerByteIdentical(t *testing.T) {
	t.Parallel()
	dir := referenceRegistryPath(t)

	for _, adapterID := range []string{"none", "claude-code"} {
		adapterID := adapterID
		t.Run(adapterID, func(t *testing.T) {
			t.Parallel()

			// Filesystem-source sync.
			fsTarget := t.TempDir()
			fsRes, err := sync.Run(sync.Options{
				RegistryPath: dir,
				Target:       fsTarget,
				AdapterID:    adapterID,
			})
			if err != nil {
				t.Fatalf("filesystem sync.Run: %v", err)
			}

			// Server-source sync against the same directory. The standalone
			// bootstrap resolves an anonymous public identity, so visibility
			// is bypassed and both consumers see the same artifact set.
			srv, err := server.NewFromFilesystem(dir)
			if err != nil {
				t.Fatalf("NewFromFilesystem: %v", err)
			}
			ts := httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)

			srvTarget := t.TempDir()
			srvRes, err := sync.Run(sync.Options{
				RegistryPath: ts.URL,
				Target:       srvTarget,
				AdapterID:    adapterID,
			})
			if err != nil {
				t.Fatalf("server sync.Run: %v", err)
			}

			// The materialized trees must be byte-identical, excluding the
			// lock file (its target path, timestamps, and source provenance
			// legitimately differ between the two consumers).
			fsTree := materializedTree(t, fsTarget)
			srvTree := materializedTree(t, srvTarget)
			if len(fsTree) == 0 {
				t.Fatalf("filesystem sync materialized nothing")
			}
			assertTreesEqual(t, fsTree, srvTree)

			// The lock-file artifacts list (id + version) must match.
			if got, want := artifactKeys(srvRes), artifactKeys(fsRes); !equalStringSlices(got, want) {
				t.Errorf("artifacts list mismatch:\n filesystem=%v\n server=    %v", want, got)
			}

			// §11 names the lock-file artifacts: list among the four things the
			// two modes must produce identically, and the id and version alone do
			// not carry it. content_hash is what the §14.11 reproducibility triple
			// is pinned on and was computed from a different input on each side,
			// and the two consumers emitted the list in different orders. The
			// enclosing LockFile is not compared, because its target path,
			// timestamps, and source provenance legitimately differ.
			assertLockArtifactsEqual(t, fsTarget, srvTarget)
		})
	}
}

// Spec: §11 (Filesystem ↔ server equivalence test) / §2.2 (Shared library
// code) — two artifacts contending for one §6.7 config-merge target and two
// contending for one inject target compose identically under both registry
// sources. The reference fixture cannot show it, because it carries one
// artifact per merge destination.
//
// Each colliding pair straddles the two layers with canonical IDs that sort
// opposite to layer_order:, which is what makes the arms non-vacuous. Walk
// emits alphabetically by canonical ID within one layer, which is the order
// dedupeLatest produces globally for the server source, so a pair sitting
// inside one layer folds in the same sequence under both sources whether or
// not the materialization set is ordered by §7.5.
func TestSyncEquivalence_SharedMergeTargetsAreByteIdentical(t *testing.T) {
	t.Parallel()
	dir := collidingRegistry(t)

	// codex cannot translate the §6.7.1 command cell, so the fixture carries
	// no type: command artifact and both harnesses materialize the whole set.
	for _, adapterID := range []string{"claude-code", "codex"} {
		adapterID := adapterID
		t.Run(adapterID, func(t *testing.T) {
			t.Parallel()

			fsTarget := t.TempDir()
			fsRes, err := sync.Run(sync.Options{
				RegistryPath: dir,
				Target:       fsTarget,
				AdapterID:    adapterID,
			})
			if err != nil {
				t.Fatalf("filesystem sync.Run: %v", err)
			}

			srv, err := server.NewFromFilesystem(dir)
			if err != nil {
				t.Fatalf("NewFromFilesystem: %v", err)
			}
			ts := httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)

			srvTarget := t.TempDir()
			if _, err := sync.Run(sync.Options{
				RegistryPath: ts.URL,
				Target:       srvTarget,
				AdapterID:    adapterID,
			}); err != nil {
				t.Fatalf("server sync.Run: %v", err)
			}

			fsTree := materializedTree(t, fsTarget)
			srvTree := materializedTree(t, srvTarget)
			if len(fsTree) == 0 {
				t.Fatalf("filesystem sync materialized nothing")
			}
			assertTreesEqual(t, fsTree, srvTree)
			assertLockArtifactsEqual(t, fsTarget, srvTarget)

			if len(fsRes.Artifacts) != 4 {
				t.Fatalf("expected the four fixture artifacts, got %d", len(fsRes.Artifacts))
			}

			// The shared targets carry the collision, so assert the order
			// inside them directly. Byte equality alone would also hold if
			// neither mode wrote the pair.
			if adapterID == "claude-code" {
				// Both hook fragments carry an array under the same native
				// event key and deepMerge concatenates them, so the collision
				// surfaces as the concatenated array's element order.
				assertClaudeHookOrder(t, fsTree, "a-hooks/audit", "z-hooks/notify")
			} else {
				// Codex splices whole marker-delimited blocks, so the
				// collision surfaces as the block order in each file.
				assertBlockOrder(t, fsTree, "AGENTS.md", "a-rules/policy", "z-rules/style")
				assertBlockOrder(t, fsTree, ".codex/config.toml", "a-hooks/audit", "z-hooks/notify")
			}

			// Spec: §11 (idempotency) — a second sync over the same target
			// reports no change.
			for target, mode := range map[string]string{fsTarget: "filesystem", srvTarget: "server"} {
				registryPath := dir
				if mode == "server" {
					registryPath = ts.URL
				}
				res, err := sync.Run(sync.Options{
					RegistryPath: registryPath,
					Target:       target,
					AdapterID:    adapterID,
				})
				if err != nil {
					t.Fatalf("second %s sync.Run: %v", mode, err)
				}
				if res.Changed {
					t.Errorf("the second %s sync reported the target as changed", mode)
				}
			}
		})
	}
}

// collidingRegistry writes a two-layer registry in which each colliding pair
// straddles the layers and the canonical IDs sort opposite to layer_order:.
// Both layers are public so the server source's anonymous identity sees the
// same set the filesystem source walks.
func collidingRegistry(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	hook := func(description, action string) string {
		return "---\ntype: hook\nversion: 1.0.0\ndescription: " + description +
			"\nsensitivity: low\nhook_event: pre_tool_use\nhook_action: " + action + "\n---\n\n" + description + "\n"
	}
	rule := func(description, globs string) string {
		return "---\ntype: rule\nversion: 1.0.0\ndescription: " + description +
			"\nsensitivity: low\nrule_mode: glob\nrule_globs: \"" + globs + "\"\n---\n\n" + description + "\n"
	}
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-finance\n",
		},
		testharness.WriteTreeOption{Path: "org-defaults/.layer-config", Content: "visibility:\n  public: true\n"},
		testharness.WriteTreeOption{Path: "team-finance/.layer-config", Content: "visibility:\n  public: true\n"},
		testharness.WriteTreeOption{
			Path:    "org-defaults/z-hooks/notify/ARTIFACT.md",
			Content: hook("Notify on every tool call.", "notify-send podium"),
		},
		testharness.WriteTreeOption{
			Path:    "team-finance/a-hooks/audit/ARTIFACT.md",
			Content: hook("Audit every tool call.", "audit-log podium"),
		},
		testharness.WriteTreeOption{
			Path:    "org-defaults/z-rules/style/ARTIFACT.md",
			Content: rule("Apply the org style rules.", "src/**/*.ts"),
		},
		testharness.WriteTreeOption{
			Path:    "team-finance/a-rules/policy/ARTIFACT.md",
			Content: rule("Apply the finance policy rules.", "src/finance/**/*.ts"),
		},
	)
	return dir
}

// assertClaudeHookOrder asserts that the hook entries concatenated into
// .claude/settings.json under the native PreToolUse key appear in the given
// artifact-ID order. Each entry carries its artifact ID under x-podium-id.
func assertClaudeHookOrder(t *testing.T, tree map[string]string, want ...string) {
	t.Helper()
	body, ok := tree[".claude/settings.json"]
	if !ok {
		t.Fatalf(".claude/settings.json was not materialized")
	}
	var settings struct {
		Hooks map[string][]struct {
			ID string `json:"x-podium-id"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		t.Fatalf("parsing .claude/settings.json: %v", err)
	}
	entries := settings.Hooks["PreToolUse"]
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.ID)
	}
	if !equalStringSlices(got, want) {
		t.Errorf("PreToolUse hook order:\n got= %v\n want=%v", got, want)
	}
}

// assertBlockOrder asserts that the Podium-managed inject blocks in a
// marker-delimited file appear in the given artifact-ID order.
func assertBlockOrder(t *testing.T, tree map[string]string, path string, want ...string) {
	t.Helper()
	body, ok := tree[path]
	if !ok {
		t.Fatalf("%s was not materialized", path)
	}
	at := make([]int, 0, len(want))
	for _, id := range want {
		i := strings.Index(body, "podium:begin:"+id)
		if i < 0 {
			t.Fatalf("%s carries no block for %q", path, id)
		}
		at = append(at, i)
	}
	for i := 1; i < len(at); i++ {
		if at[i-1] > at[i] {
			t.Errorf("%s block order: %q must precede %q", path, want[i-1], want[i])
		}
	}
}

// Spec: §6.4 — the workspace overlay merges as the highest-precedence
// layer for a server source. The consumer merges it client-side because the
// developer's overlay directory is local to the machine running podium sync; the
// server pointed at the reference fixture cannot see it. The overlay overrides an
// artifact the server also serves, and that override must win in the materialized
// output. The server runs in-process so the test owns its lifecycle.
func TestSyncServerSource_WorkspaceOverlayWins(t *testing.T) {
	t.Parallel()
	dir := referenceRegistryPath(t)

	srv, err := server.NewFromFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFromFilesystem: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Stage a workspace overlay overriding notes/journal (served by the
	// reference fixture's personal layer) with a distinct body.
	overlayDir := t.TempDir()
	overlayBody := "---\ntype: context\nversion: 9.9.9\ndescription: overlay override\nsensitivity: low\n---\n\noverlay wins\n"
	testharness.WriteTree(t, overlayDir, testharness.WriteTreeOption{
		Path:    "notes/journal/ARTIFACT.md",
		Content: overlayBody,
	})

	target := t.TempDir()
	if _, err := sync.Run(sync.Options{
		RegistryPath: ts.URL,
		Target:       target,
		AdapterID:    "none",
		OverlayPath:  overlayDir,
	}); err != nil {
		t.Fatalf("server sync.Run with overlay: %v", err)
	}
	got := testharness.ReadTree(t, target)["notes/journal/ARTIFACT.md"]
	if got != overlayBody {
		t.Errorf("overlay did not override the server artifact:\n got=%q\n want=%q", got, overlayBody)
	}
}

// materializedTree reads the target tree minus the lock file.
func materializedTree(t testing.TB, target string) map[string]string {
	t.Helper()
	full := testharness.ReadTree(t, target)
	out := make(map[string]string, len(full))
	for path, content := range full {
		if strings.HasPrefix(path, ".podium/sync.lock") {
			continue
		}
		out[path] = content
	}
	return out
}

// assertTreesEqual fails with a focused diff when the two trees differ.
func assertTreesEqual(t testing.TB, want, got map[string]string) {
	t.Helper()
	for path, wantContent := range want {
		gotContent, ok := got[path]
		if !ok {
			t.Errorf("server tree missing %q present in filesystem tree", path)
			continue
		}
		if gotContent != wantContent {
			t.Errorf("content mismatch at %q:\n filesystem=%q\n server=    %q", path, wantContent, gotContent)
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("server tree has extra %q absent from filesystem tree", path)
		}
	}
}

// artifactKeys returns the sorted "id@version" keys for a sync result, the
// in-memory equivalent of the lock-file artifacts list.
func artifactKeys(res *sync.Result) []string {
	out := make([]string, 0, len(res.Artifacts))
	for _, a := range res.Artifacts {
		out = append(out, a.ID+"@"+a.Version)
	}
	sort.Strings(out)
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertLockArtifactsEqual compares the artifacts: list each consumer wrote,
// position for position and field for field: the id, the version, the content
// hash, the layer that supplied it, and where it landed. The lists are compared
// as written, because §7.5.3 states the list's order as a property of the lock
// file and §7.5 gives both consumers the same materialization order, so a
// divergence in either the order or the records fails here.
func assertLockArtifactsEqual(t *testing.T, fsTarget, srvTarget string) {
	t.Helper()
	fsLock, err := sync.ReadLock(fsTarget)
	if err != nil {
		t.Fatalf("reading the filesystem lock: %v", err)
	}
	srvLock, err := sync.ReadLock(srvTarget)
	if err != nil {
		t.Fatalf("reading the server lock: %v", err)
	}
	if len(fsLock.Artifacts) == 0 {
		t.Fatalf("the filesystem lock recorded no artifacts")
	}
	if len(fsLock.Artifacts) != len(srvLock.Artifacts) {
		t.Fatalf("lock artifact count: filesystem=%d server=%d", len(fsLock.Artifacts), len(srvLock.Artifacts))
	}
	for i, want := range fsLock.Artifacts {
		got := srvLock.Artifacts[i]
		if got != want {
			t.Errorf("lock artifact %d differs between the modes:\n filesystem=%+v\n server=    %+v", i, want, got)
		}
	}
}
