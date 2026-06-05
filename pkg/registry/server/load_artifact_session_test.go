package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// putVersion seeds one (id, version) manifest at the given ingest time so
// `latest` resolution (ordered by ingest time, ties by semver) is
// deterministic.
func putVersion(t *testing.T, st store.Store, id, version, hash string, at time.Time) {
	t.Helper()
	err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: id, Version: version,
		ContentHash: hash, Type: "skill", Layer: "L", IngestedAt: at,
	})
	if err != nil {
		t.Fatalf("PutManifest %s@%s: %v", id, version, err)
	}
}

func loadVersion(t *testing.T, base, query string) (int, string) {
	t.Helper()
	resp, err := http.Get(base + "/v1/load_artifact?" + query)
	if err != nil {
		t.Fatalf("GET %s: %v", query, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	var out server.LoadArtifactResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, out.Version
}

// Spec: §5 load_artifact "Optional session_id"; §4.7.6 — within a session
// the first `latest` lookup pins, so a later same-id lookup resolves to
// the same version even after a newer ingest, while a session-less (or
// different-session) lookup sees the new latest. This exercises the GET
// /v1/load_artifact path the MCP bridge drives, which previously
// ignored session_id.
func TestLoadArtifact_SessionPinsLatest(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t0 := time.Now().UTC()
	putVersion(t, st, "team/a", "1.0.0", "sha256:v1", t0)

	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	ts := httptest.NewServer(server.New(reg).Handler())
	t.Cleanup(ts.Close)

	// First lookup in session s1 pins to the only version, 1.0.0.
	if code, v := loadVersion(t, ts.URL, "id=team/a&session_id=s1"); code != 200 || v != "1.0.0" {
		t.Fatalf("first s1 load: code=%d version=%q, want 200/1.0.0", code, v)
	}

	// A newer version is ingested after the pin.
	putVersion(t, st, "team/a", "2.0.0", "sha256:v2", t0.Add(time.Hour))

	// s1 still resolves the pinned 1.0.0 (session consistency).
	if code, v := loadVersion(t, ts.URL, "id=team/a&session_id=s1"); code != 200 || v != "1.0.0" {
		t.Errorf("second s1 load: code=%d version=%q, want 200/1.0.0 (pinned)", code, v)
	}
	// A session-less lookup sees the new latest, 2.0.0.
	if code, v := loadVersion(t, ts.URL, "id=team/a"); code != 200 || v != "2.0.0" {
		t.Errorf("session-less load: code=%d version=%q, want 200/2.0.0", code, v)
	}
	// A fresh session pins to the current latest, 2.0.0.
	if code, v := loadVersion(t, ts.URL, "id=team/a&session_id=s2"); code != 200 || v != "2.0.0" {
		t.Errorf("s2 load: code=%d version=%q, want 200/2.0.0", code, v)
	}
}
