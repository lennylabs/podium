package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// newSessionFixture seeds two public artifacts so search and batch load have
// something to resolve.
func newSessionFixture(t *testing.T) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, m := range []store.ManifestRecord{
		{TenantID: "default", ArtifactID: "team/a", Version: "1.0.0",
			ContentHash: "sha256:a", Type: "skill", Layer: "L"},
		{TenantID: "default", ArtifactID: "team/b", Version: "1.0.0",
			ContentHash: "sha256:b", Type: "skill", Layer: "L"},
	} {
		if err := st.PutManifest(context.Background(), m); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// spec: §7.6.2 — an item the caller cannot resolve comes back as
// visibility.denied, never registry.not_found, so the batch response does not
// reveal whether the artifact exists in some hidden layer. A missing
// id and a visibility-filtered id both reduce to core.ErrNotFound, so they
// share this one wire code.
func TestBatchLoad_UnresolvableReturnsVisibilityDenied(t *testing.T) {
	t.Parallel()
	ts := newSessionFixture(t)
	body, _ := json.Marshal(map[string]any{"ids": []string{"team/a", "team/missing"}})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var out []server.BatchLoadEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := map[string]server.BatchLoadEnvelope{}
	for _, e := range out {
		byID[e.ID] = e
	}
	if byID["team/a"].Status != "ok" {
		t.Errorf("team/a status = %q, want ok", byID["team/a"].Status)
	}
	env := byID["team/missing"]
	if env.Status != "error" || env.Error == nil || env.Error.Code != "visibility.denied" {
		t.Errorf("team/missing = %+v, want error code visibility.denied", env)
	}
}

// spec: §7.6 — search_artifacts accepts a session_id filter; the
// server threads it into the core options without rejecting the request.
func TestSearchArtifacts_AcceptsSessionID(t *testing.T) {
	t.Parallel()
	ts := newSessionFixture(t)
	resp, err := http.Get(ts.URL + "/v1/search_artifacts?query=team&session_id=sess-1&top_k=5")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (session_id must be accepted)", resp.StatusCode)
	}
	var out struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
