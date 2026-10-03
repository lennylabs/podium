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
// materialize.stale_resolution against the persisted mark, and a load that
// names version 1.0.0 is never compared and succeeds.
func TestDeliveryFreshness_PersistedMarkRefusesStaleLatest(t *testing.T) {
	t.Parallel()
	f := newSignedArtifactFixture(t, signedArtifactSpec{Version: "2.0.0", ArtifactRevision: revisionAt(200)})
	env := f.Env(t, "always")

	if errStr, result := loadSignedArtifact(t, env, f.ID()); errStr != "" {
		t.Fatalf("first latest load: %s\nresult=%v", errStr, result)
	}

	f.setRecord("1.0.0", revisionAt(100))
	errStr, result := loadSignedArtifact(t, env, f.ID())
	if errStr == "" {
		t.Fatalf("stale latest load should be refused, got %v", result)
	}
	if result["code"] != "materialize.stale_resolution" || result["retryable"] != false {
		t.Fatalf("code=%v retryable=%v, want materialize.stale_resolution and false", result["code"], result["retryable"])
	}
	details, _ := result["details"].(map[string]any)
	want := map[string]any{
		"artifact_id":        f.ID(),
		"served_version":     "1.0.0",
		"served_revision":    revisionAt(100),
		"reference_revision": revisionAt(200),
	}
	for k, v := range want {
		if details[k] != v {
			t.Errorf("details[%s] = %v, want %v", k, details[k], v)
		}
	}

	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": f.ID(), "version": "1.0.0"}))
	if pinned := rpcResult(t, res.Stdout, 1); pinned["error"] != nil {
		t.Fatalf("pinned load of 1.0.0 should succeed, got %v", pinned)
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
