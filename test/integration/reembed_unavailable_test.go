package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// reembedEnvelope posts a re-embed with the given query to a server over reg
// that authenticates no caller, and returns the status and the decoded §6.10
// envelope.
func reembedEnvelope(t *testing.T, reg *core.Registry, query string) (int, server.ErrorResponse) {
	t.Helper()
	api := httptest.NewServer(server.New(reg, server.WithUnauthenticatedReembed()).Handler())
	t.Cleanup(api.Close)
	resp, err := http.Post(api.URL+"/v1/admin/reembed"+query, "application/json", nil)
	if err != nil {
		t.Fatalf("POST reembed%s: %v", query, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env server.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode reembed envelope: %v", err)
	}
	return resp.StatusCode, env
}

// Spec: §4.7, §6.10 — a registry with no vector search configured answers
// every arm of the re-embed endpoint with 404 registry.reembed_unavailable,
// retryable false, and a suggested_action naming the settings to change. The
// tenant-wide pass, a scoped pass, and the single-artifact arm all take this
// path, because the same request fails until the operator changes the
// configuration.
//
// Matrix: §6.10 (registry.reembed_unavailable)
func TestReembed_NoVectorSearchAnswersReembedUnavailable(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	storageOnly := core.New(st, "t", nil).WithVectorSearch(vector.NewMemory(4), nil)
	for name, tc := range map[string]struct {
		reg   *core.Registry
		query string
	}{
		"no vector backend, full pass":        {core.New(st, "t", nil), ""},
		"no vector backend, only missing":     {core.New(st, "t", nil), "?only_missing=true"},
		"no vector backend, one artifact":     {core.New(st, "t", nil), "?artifact=a&version=1.0.0"},
		"backend without embedder, full pass": {storageOnly, ""},
	} {
		status, env := reembedEnvelope(t, tc.reg, tc.query)
		if status != http.StatusNotFound || env.Code != "registry.reembed_unavailable" || env.Retryable {
			t.Errorf("%s: status %d code %q retryable %v, want 404 registry.reembed_unavailable retryable false",
				name, status, env.Code, env.Retryable)
		}
		if !strings.Contains(env.SuggestedAction, "PODIUM_VECTOR_BACKEND") || !strings.Contains(env.SuggestedAction, "PODIUM_EMBEDDING_PROVIDER") {
			t.Errorf("%s: suggested_action %q, want the vector settings named", name, env.SuggestedAction)
		}
	}
}

// Spec: §4.7, §6.10 — with vector search configured, a re-embed that fails at
// the metadata store is a dependency failure: it keeps registry.unavailable,
// retryable true, and its suggested_action does not point at the vector
// configuration.
func TestReembed_StoreFailureStaysRetryable(t *testing.T) {
	t.Parallel()
	reg := core.New(unlistableStore{store.NewMemory()}, "t", nil).
		WithVectorSearch(vector.NewMemory(4), overlapEmbedder{model: "m1"})
	status, env := reembedEnvelope(t, reg, "")
	if status != http.StatusInternalServerError || env.Code != "registry.unavailable" || !env.Retryable {
		t.Errorf("store failure: status %d code %q retryable %v, want 500 registry.unavailable retryable true",
			status, env.Code, env.Retryable)
	}
	if strings.Contains(env.SuggestedAction, "PODIUM_VECTOR_BACKEND") {
		t.Errorf("store failure: suggested_action %q names the vector configuration", env.SuggestedAction)
	}
}
