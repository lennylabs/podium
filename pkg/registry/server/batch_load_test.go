package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func newBatchFixture(t *testing.T) (*httptest.Server, store.Store) {
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
	return ts, st
}

// Spec: §7.6.2 — POST /v1/artifacts:batchLoad accepts a list of
// IDs and returns an array of per-item envelopes carrying the
// canonical metadata.
func TestBatchLoad_ReturnsPerItemEnvelopes(t *testing.T) {
	t.Parallel()
	ts, _ := newBatchFixture(t)
	body, _ := json.Marshal(map[string]any{"ids": []string{"team/a", "team/b"}})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out []server.BatchLoadEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	for _, e := range out {
		if e.Status != "ok" {
			t.Errorf("%s status = %q, want ok", e.ID, e.Status)
		}
		if e.ContentHash == "" {
			t.Errorf("%s content_hash empty", e.ID)
		}
	}
}

// Spec: §7.6.2 — partial failure does not fail the batch. Items
// the caller cannot see come back as status=error with
// visibility.denied; missing items as registry.not_found.
func TestBatchLoad_PartialFailureSurfacesPerItemErrors(t *testing.T) {
	t.Parallel()
	ts, _ := newBatchFixture(t)
	body, _ := json.Marshal(map[string]any{
		"ids": []string{"team/a", "team/missing"},
	})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (partial failure shouldn't 4xx)", resp.StatusCode)
	}
	var out []server.BatchLoadEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&out)
	byID := map[string]server.BatchLoadEnvelope{}
	for _, e := range out {
		byID[e.ID] = e
	}
	if byID["team/a"].Status != "ok" {
		t.Errorf("team/a status = %q, want ok", byID["team/a"].Status)
	}
	if byID["team/missing"].Status != "error" {
		t.Errorf("team/missing status = %q, want error", byID["team/missing"].Status)
	}
	if byID["team/missing"].Error == nil ||
		byID["team/missing"].Error.Code != "registry.not_found" {
		t.Errorf("team/missing error = %+v, want registry.not_found", byID["team/missing"].Error)
	}
}

// Spec: §7.6.2 — hard cap of 50 IDs per batch. Larger requests
// fail with registry.invalid_argument.
func TestBatchLoad_RejectsBatchAboveCap(t *testing.T) {
	t.Parallel()
	ts, _ := newBatchFixture(t)
	ids := make([]string, 51)
	for i := range ids {
		ids[i] = "x"
	}
	body, _ := json.Marshal(map[string]any{"ids": ids})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body2, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body2), "registry.invalid_argument") {
		t.Errorf("body = %q", body2)
	}
}

// Spec: §7.6.2 — empty ids array is rejected.
func TestBatchLoad_RejectsEmptyIDs(t *testing.T) {
	t.Parallel()
	ts, _ := newBatchFixture(t)
	body, _ := json.Marshal(map[string]any{"ids": []string{}})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// Spec: §7.6.2 — version_pins map carries explicit versions per ID.
func TestBatchLoad_VersionPinsHonored(t *testing.T) {
	t.Parallel()
	ts, st := newBatchFixture(t)
	// Add a v2 of team/a.
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "team/a", Version: "2.0.0",
		ContentHash: "sha256:a2", Type: "skill", Layer: "L",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"ids":          []string{"team/a"},
		"version_pins": map[string]string{"team/a": "1.0.0"},
	})
	resp, err := http.Post(ts.URL+"/v1/artifacts:batchLoad", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var out []server.BatchLoadEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out) != 1 || out[0].Version != "1.0.0" {
		t.Errorf("envelope = %+v, want version 1.0.0", out)
	}
}
