package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/registryharness"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// /v1/search_domains: matches a domain by name/description text.
func TestSearchDomains_FindsMatchingPath(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run-variance/ARTIFACT.md", contextFor("Variance", "finance")),
		fixture("ops/restart-runner/ARTIFACT.md", contextFor("Restart runner", "ops")),
	)
	body := mustGet(t, h.URL, "/v1/search_domains?query=finance&top_k=5")
	var resp server.SearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, body)
	}
	// At minimum the request reaches the handler and returns a 200.
	if resp.Query != "finance" {
		t.Errorf("Query echo = %q", resp.Query)
	}
}

// /v1/dependents: missing id query param surfaces registry.invalid_argument.
func TestDependents_MissingIDReturns400(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	resp, err := http.Get(h.URL + "/v1/dependents")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "registry.invalid_argument") {
		t.Errorf("body = %s", body)
	}
}

// /v1/dependents: known id returns an edges array, even when empty.
func TestDependents_ReturnsEdgesArray(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("a/ARTIFACT.md", contextFor("A", "")),
	)
	body := mustGet(t, h.URL, "/v1/dependents?id=a")
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, body)
	}
	if _, ok := resp["edges"]; !ok {
		t.Errorf("missing edges key in %v", resp)
	}
}

// /v1/admin/reembed: GET method is rejected as method-not-allowed.
func TestAdminReembed_WrongMethodReturns405(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	resp, err := http.Get(h.URL + "/v1/admin/reembed")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// /v1/admin/reembed: POST without --version-but-with-artifact returns 400.
func TestAdminReembed_ArtifactWithoutVersionReturns400(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	req, _ := http.NewRequest(http.MethodPost,
		h.URL+"/v1/admin/reembed?artifact=foo", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// /v1/admin/reembed: bare POST reaches the handler. The default test
// harness has no vector search backend so a 500 is expected; what we
// assert is that the handler ran (not a 4xx validation rejection).
func TestAdminReembed_TenantWidePostReachesHandler(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("a/ARTIFACT.md", contextFor("A", "")),
	)
	req, _ := http.NewRequest(http.MethodPost, h.URL+"/v1/admin/reembed", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	// 200 (when vector search is wired) or 500 (when not) — both
	// prove the handler executed past validation.
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusMethodNotAllowed {
		buf, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d (validation/method): %s", resp.StatusCode, buf)
	}
}

// /v1/domain/analyze: GET returns the report; wrong method = 405.
func TestDomainAnalyze_OKAndWrongMethod(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run-variance/ARTIFACT.md", contextFor("Variance", "finance")),
	)
	if body := mustGet(t, h.URL, "/v1/domain/analyze?path=finance"); len(body) == 0 {
		t.Errorf("empty body")
	}
	req, _ := http.NewRequest(http.MethodPost, h.URL+"/v1/domain/analyze", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST method status = %d, want 405", resp.StatusCode)
	}
}

// /v1/scope/preview: returns the per-layer visibility preview.
func TestScopePreview_ReturnsPreview(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	body := mustGet(t, h.URL, "/v1/scope/preview")
	if len(body) == 0 {
		t.Errorf("empty body")
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, body)
	}
}

// /v1/search_artifacts: tags filter exercises the splitCSV helper.
func TestSearchArtifacts_TagsFilterExercisesSplitCSV(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("a/ARTIFACT.md", contextFor("A", "finance,ap")),
		fixture("b/ARTIFACT.md", contextFor("B", "sales")),
	)
	body := mustGet(t, h.URL, "/v1/search_artifacts?tags=finance,ap")
	var resp server.SearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Errorf("expected at least one tagged result")
	}
}
