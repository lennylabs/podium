package integration

import (
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
		})
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
