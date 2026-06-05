package e2e

// Focused proof that the runtime-republish primitive stages multiple
// versions of an artifact against the standalone harness. These tests assert
// the capabilities the downstream journeys depend on:
//
//   - Two coexisting versions of one canonical id, each addressable by an
//     explicit version parameter (feeds version-selection).
//   - Default resolution returns the latest non-deprecated version, and a
//     deprecated successor is skipped by latest while staying addressable by
//     explicit version (feeds deprecation).
//   - A session that pins `latest` keeps serving the pinned version across a
//     mid-session republish while a fresh session sees the new version (feeds
//     the session-snapshot journey).
//
// Spec: §7.3.1 (manual reingest runs the ingest pipeline), §4.7.6 (immutable
// versions; latest resolves to the most recent non-deprecated version; a
// session pins its first latest lookup), §7.3.2 (a deprecated manifest
// supersedes a prior non-deprecated version on the deprecation transition).

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestRepublish_MultiVersionSelectionAndLatest stages two versions of one id
// through the runtime-republish helper and asserts each is addressable by
// explicit version while the default load resolves the latest.
func TestRepublish_MultiVersionSelectionAndLatest(t *testing.T) {
	t.Parallel()
	// Seed registry keeps the standalone boot non-empty; the layer under test is
	// registered and populated entirely at runtime.
	srv := startServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": contextArtifact("seed context"),
	}))
	layer := newRepublishLayer(t, srv, "versions")

	const id = "team/widget/render-chart"
	layer.publishVersion(t, versionSpec{ID: id, Version: "1.0.0", Description: "render a chart for the report"})
	layer.publishVersion(t, versionSpec{ID: id, Version: "2.0.0", Description: "render a chart for the report"})

	// Each explicit version resolves to its own bytes.
	if st, got := layer.loadVersion(t, id, "1.0.0"); st != 200 || got.Version != "1.0.0" {
		t.Errorf("load version=1.0.0: HTTP %d version=%q, want 200/1.0.0", st, got.Version)
	}
	if st, got := layer.loadVersion(t, id, "2.0.0"); st != 200 || got.Version != "2.0.0" {
		t.Errorf("load version=2.0.0: HTTP %d version=%q, want 200/2.0.0", st, got.Version)
	}
	// The default (latest) load resolves the most recently ingested version.
	if st, got := layer.loadVersion(t, id, ""); st != 200 || got.Version != "2.0.0" {
		t.Errorf("default load: HTTP %d version=%q, want 200/2.0.0 (latest)", st, got.Version)
	}
	// A version never published returns the structured not-found, proving the
	// store holds exactly the staged versions.
	if st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id="+id+"&version=9.9.9"); st == 200 || !strings.Contains(string(body), "not_found") {
		t.Errorf("load version=9.9.9: HTTP %d body=%s, want a not_found error", st, body)
	}
}

// TestRepublish_DeprecatedSuccessorSkippedByLatest publishes a non-deprecated
// version, then a higher deprecated version, and asserts latest resolution
// skips the deprecated version while it stays addressable by explicit version.
func TestRepublish_DeprecatedSuccessorSkippedByLatest(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": contextArtifact("seed context"),
	}))
	layer := newRepublishLayer(t, srv, "deprecation")

	const id = "team/payments/settle-batch"
	layer.publishVersion(t, versionSpec{ID: id, Version: "1.0.0", Description: "settle a batch of payments"})
	// A higher version that flips deprecated:true with a successor pointer.
	layer.publishVersion(t, versionSpec{
		ID: id, Version: "2.0.0", Description: "settle a batch of payments",
		Deprecated: true, ReplacedBy: "team/payments/settle-batch-v2",
	})

	// Latest skips the deprecated 2.0.0 and resolves the prior non-deprecated
	// 1.0.0 (§4.7.6).
	if st, got := layer.loadVersion(t, id, ""); st != 200 || got.Version != "1.0.0" {
		t.Errorf("default load: HTTP %d version=%q, want 200/1.0.0 (latest skips deprecated 2.0.0)", st, got.Version)
	}
	// The deprecated version is still addressable by explicit version and reports
	// its deprecated flag.
	st, got := layer.loadVersion(t, id, "2.0.0")
	if st != 200 || got.Version != "2.0.0" {
		t.Fatalf("load version=2.0.0: HTTP %d version=%q, want 200/2.0.0", st, got.Version)
	}
	if !got.Deprecated {
		t.Errorf("explicit load of 2.0.0 did not report deprecated=true (envelope=%+v)", got)
	}
}

// TestRepublish_SessionSnapshotStableAcrossRepublish opens a session, pins the
// latest version, republishes a newer version mid-session, and asserts the
// session keeps serving the pinned snapshot while a fresh session and a
// session-less caller see the new version. This is the session-snapshot
// consistency journey the primitive unblocks (§4.7.6).
func TestRepublish_SessionSnapshotStableAcrossRepublish(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": contextArtifact("seed context"),
	}))
	layer := newRepublishLayer(t, srv, "session")

	const id = "team/reports/run-variance"
	layer.publishVersion(t, versionSpec{ID: id, Version: "1.0.0", Description: "run the variance report"})

	// Session s1 pins its first latest lookup to 1.0.0.
	if v := sessionLoad(t, srv, id, "s1"); v != "1.0.0" {
		t.Fatalf("s1 first load version=%q, want 1.0.0", v)
	}

	// A newer version is republished after the pin.
	layer.publishVersion(t, versionSpec{ID: id, Version: "2.0.0", Description: "run the variance report"})

	// s1 still resolves the pinned 1.0.0 (session snapshot stability).
	if v := sessionLoad(t, srv, id, "s1"); v != "1.0.0" {
		t.Errorf("s1 second load version=%q, want 1.0.0 (pinned snapshot stable across republish)", v)
	}
	// A session-less caller sees the new latest, 2.0.0.
	if st, got := layer.loadVersion(t, id, ""); st != 200 || got.Version != "2.0.0" {
		t.Errorf("session-less load: HTTP %d version=%q, want 200/2.0.0", st, got.Version)
	}
	// A fresh session pins to the current latest, 2.0.0.
	if v := sessionLoad(t, srv, id, "s2"); v != "2.0.0" {
		t.Errorf("s2 first load version=%q, want 2.0.0", v)
	}
}

// sessionLoad GETs load_artifact for id within the given session and returns
// the resolved version, asserting HTTP 200. It drives the §5 session_id
// parameter the MCP bridge forwards.
func sessionLoad(t testing.TB, srv *serverProc, id, sessionID string) string {
	t.Helper()
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id="+id+"&session_id="+sessionID)
	if st != http.StatusOK {
		t.Fatalf("session %s load %s = HTTP %d\nbody: %s", sessionID, id, st, body)
	}
	var r exLoadResp
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode session load: %v\nbody: %s", err, body)
	}
	return r.Version
}
