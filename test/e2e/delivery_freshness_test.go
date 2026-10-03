package e2e

// End-to-end coverage of the §6.5 freshness check on `latest` loads. Each
// load runs a new podium-mcp process over one PODIUM_CACHE_DIR against one
// signed fixture URL, so the persisted revision mark carries across processes
// and every step reaches the same mark key. The fixture serves validly signed
// records, so a refusal comes from the freshness check rather than from the
// delivery-hash or signature checks.
//
// Spec: §6.5, §4.7.10.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/version"
)

// revisionAt returns the artifact_revision string for n Unix microseconds.
func revisionAt(n int64) string {
	return version.FormatArtifactRevision(time.UnixMicro(n))
}

// TestDeliveryFreshness_PersistedMarkRefusesStaleLatest loads 2.0.0 at
// revision 200, then serves a validly signed 1.0.0 at revision 100 from the
// same URL. A `latest` load in a new process is refused with
// materialize.stale_resolution against the persisted mark and leaves the
// materialized 2.0.0 files untouched, and a load that names version 1.0.0 is
// never compared and succeeds. After `podium cache reset-revisions` deletes
// the mark, a new-process `latest` load of 1.0.0 at revision 100 succeeds.
func TestDeliveryFreshness_PersistedMarkRefusesStaleLatest(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{Version: "2.0.0", ArtifactRevision: revisionAt(200)})
	env := append(f.Env(t, "always"), "PODIUM_MATERIALIZE_ROOT="+t.TempDir())
	cacheDir := envValue(env, "PODIUM_CACHE_DIR")

	errStr, result := loadSignedArtifact(t, env, f.ID())
	if errStr != "" {
		t.Fatalf("first latest load: %s\nresult=%v", errStr, result)
	}
	before := readMaterialized(t, result)

	f.setRecord("1.0.0", revisionAt(100))
	errStr, result = loadSignedArtifact(t, env, f.ID())
	if errStr == "" {
		t.Fatalf("stale latest load should be refused, got %v", result)
	}
	wantStaleRefusal(t, result, f.ID(), "1.0.0", revisionAt(100), revisionAt(200))
	wantFilesUnchanged(t, before)

	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": f.ID(), "version": "1.0.0"}))
	if pinned := rpcResult(t, res.Stdout, 1); pinned["error"] != nil {
		t.Fatalf("pinned load of 1.0.0 should succeed, got %v", pinned)
	}

	reset := runPodium(t, "", nil, "cache", "reset-revisions", "--dir", cacheDir, f.ID())
	cliWantExit(t, reset, 0, "cache reset-revisions "+f.ID())
	cliContains(t, reset.Stdout, "cache: reset 1 revision mark(s)", "reset count after the stale refusal")

	errStr, result = loadSignedArtifact(t, env, f.ID())
	if errStr != "" {
		t.Fatalf("latest load after the reset should succeed: %s\nresult=%v", errStr, result)
	}
	if result["version"] != "1.0.0" {
		t.Errorf("version after the reset = %v, want 1.0.0", result["version"])
	}
}

// wantStaleRefusal asserts that result is a non-retryable
// materialize.stale_resolution refusal whose §6.10 details name the artifact,
// the served version and revision, and the reference revision.
func wantStaleRefusal(t *testing.T, result map[string]any, id, servedVersion, served, reference string) {
	t.Helper()
	if result["code"] != "materialize.stale_resolution" || result["retryable"] != false {
		t.Fatalf("code=%v retryable=%v, want materialize.stale_resolution and false", result["code"], result["retryable"])
	}
	details, _ := result["details"].(map[string]any)
	want := map[string]any{
		"artifact_id":        id,
		"served_version":     servedVersion,
		"served_revision":    served,
		"reference_revision": reference,
	}
	for k, v := range want {
		if details[k] != v {
			t.Errorf("details[%s] = %v, want %v", k, details[k], v)
		}
	}
}

// readMaterialized returns the contents of every path a successful load lists
// in materialized_at, keyed by path. It fails the test when the load wrote no
// file, because the later unchanged-content check would then assert nothing.
func readMaterialized(t *testing.T, result map[string]any) map[string]string {
	t.Helper()
	paths, _ := result["materialized_at"].([]any)
	if len(paths) == 0 {
		t.Fatalf("load materialized no files: %v", result)
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		path, _ := p.(string)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read materialized %s: %v", path, err)
		}
		out[path] = string(data)
	}
	return out
}

// wantFilesUnchanged asserts that every file in before still holds the
// recorded contents, so a refused load wrote nothing to the destination.
func wantFilesUnchanged(t *testing.T, before map[string]string) {
	t.Helper()
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s after the refused load: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("%s changed after the refused load:\n%s", path, got)
		}
		if !strings.Contains(want, "version: 2.0.0") {
			t.Errorf("%s does not hold the 2.0.0 record:\n%s", path, want)
		}
	}
}

// TestDeliveryFreshness_MissingRevisionRefused serves a signed `latest` record
// with no artifact_revision. The answer carries no canonical ingest time, so
// the bridge fails closed with materialize.stale_resolution and an empty
// reference_revision.
func TestDeliveryFreshness_MissingRevisionRefused(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{})
	f.setRecord("1.0.0", "")
	env := f.Env(t, "always")

	_, result := loadSignedArtifact(t, env, f.ID())
	if result["code"] != "materialize.stale_resolution" {
		t.Fatalf("code = %v, want materialize.stale_resolution; result=%v", result["code"], result)
	}
	details, _ := result["details"].(map[string]any)
	if details["served_revision"] != "" || details["reference_revision"] != "" {
		t.Errorf("details = %v, want empty served_revision and reference_revision", details)
	}
}
