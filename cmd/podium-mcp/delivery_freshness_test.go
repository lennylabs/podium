package main

import (
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/version"
)

// rev returns the artifact_revision string for n Unix microseconds.
func rev(n int64) string {
	return version.FormatArtifactRevision(time.UnixMicro(n))
}

// freshnessServer returns a bridge over a fresh index DB for registry.
func freshnessServer(t *testing.T) *mcpServer {
	t.Helper()
	r := newResolutionCache(t.TempDir())
	t.Cleanup(func() { _ = r.Close() })
	return &mcpServer{cfg: &config{registry: "http://registry.example"}, resolutions: r, sessionID: "bridge-session"}
}

// TestCheckFreshness_ComparesWithSessionReferenceOrMark pins the §6.5
// comparison: no reference admits any canonical revision, equality is
// admitted, a revision below the session reference or the mark is refused
// with the reference written in details, and a non-canonical revision is
// refused with an empty reference.
//
// Spec: §6.5, §4.7.10
func TestCheckFreshness_ComparesWithSessionReferenceOrMark(t *testing.T) {
	t.Parallel()
	s := freshnessServer(t)
	resp := loadArtifactResponse{ID: "team/x", Version: "1.0.0", ArtifactRevision: rev(100)}

	if got, env := s.checkFreshness(resp, "s1"); env != nil || got != 100 {
		t.Fatalf("no reference: got (%d, %v), want (100, nil)", got, env)
	}
	s.resolutions.PutLatestAdvancing(s.markKey("team/x"), "s1", "team/x", "2.0.0", "sha256:a", 200, time.Now())

	if _, env := s.checkFreshness(loadArtifactResponse{ID: "team/x", ArtifactRevision: rev(200)}, "s2"); env != nil {
		t.Fatalf("equal to the mark: refused with %v", env)
	}
	for name, session := range map[string]string{"session reference": "s1", "mark": "s2"} {
		_, env := s.checkFreshness(resp, session)
		if env == nil {
			t.Fatalf("%s: revision 100 below 200 was admitted", name)
		}
		if env["code"] != "materialize.stale_resolution" || env["retryable"] != false || env["suggested_action"] == "" {
			t.Errorf("%s: envelope = %v", name, env)
		}
		d, _ := env["details"].(map[string]any)
		if d["artifact_id"] != "team/x" || d["served_version"] != "1.0.0" ||
			d["served_revision"] != rev(100) || d["reference_revision"] != rev(200) {
			t.Errorf("%s: details = %v", name, d)
		}
	}

	_, env := freshnessServer(t).checkFreshness(loadArtifactResponse{ID: "team/x", ArtifactRevision: "2026-01-01"}, "s1")
	if env == nil {
		t.Fatal("non-canonical revision was admitted")
	}
	if d, _ := env["details"].(map[string]any); d["served_revision"] != "2026-01-01" || d["reference_revision"] != "" {
		t.Errorf("non-canonical details = %v", d)
	}
}

// TestEffectiveSessionID_PrefersTrimmedHostSession pins that the registry
// request and the §6.5 check share one session: the trimmed host session_id,
// else the bridge's own.
//
// Spec: §6.5
func TestEffectiveSessionID_PrefersTrimmedHostSession(t *testing.T) {
	t.Parallel()
	s := freshnessServer(t)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"session_id": " host "}, "host"},
		{map[string]any{"session_id": "  "}, "bridge-session"},
		{map[string]any{"session_id": nil}, "bridge-session"},
		{map[string]any{}, "bridge-session"},
	} {
		if got := s.effectiveSessionID(tc.args); got != tc.want {
			t.Errorf("effectiveSessionID(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
	req, err := s.newRegistryRequest("GET", "/v1/load_artifact", map[string]any{"id": "team/x", "session_id": nil})
	if err != nil {
		t.Fatalf("newRegistryRequest: %v", err)
	}
	if got := req.URL.Query().Get("session_id"); got != "bridge-session" {
		t.Errorf("request session_id = %q, want bridge-session", got)
	}
}

// TestNoteCachedSession_RecordsReferenceWithoutAdvancingMark pins the
// revalidated-cache path: it records the session reference from the cached
// revision, leaves the mark absent, and records nothing for a revision that
// does not parse.
//
// Spec: §6.5
func TestNoteCachedSession_RecordsReferenceWithoutAdvancingMark(t *testing.T) {
	t.Parallel()
	s := freshnessServer(t)
	s.noteCachedSession(&resolutionWrite{ID: "team/x", Session: "bad"}, loadArtifactResponse{ArtifactRevision: "x"})
	s.noteCachedSession(&resolutionWrite{ID: "team/x", Session: "s1"}, loadArtifactResponse{ArtifactRevision: rev(300)})

	if ref, ok := s.resolutions.Reference(s.markKey("team/x"), "s1"); !ok || ref != 300 {
		t.Errorf("session reference = (%d, %v), want (300, true)", ref, ok)
	}
	for _, session := range []string{"bad", "other"} {
		if ref, ok := s.resolutions.Reference(s.markKey("team/x"), session); ok {
			t.Errorf("session %s: reference %d recorded, want none (no mark advanced)", session, ref)
		}
	}
}
