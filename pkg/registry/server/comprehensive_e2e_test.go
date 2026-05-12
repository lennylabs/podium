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

// search_domains with query + scope + top_k populates the registry's
// SearchDomains and returns a 200 response.
func TestSearchDomains_AllParams(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run-variance/ARTIFACT.md", contextFor("Variance", "finance")),
		fixture("finance/pay-invoice/ARTIFACT.md", contextFor("Pay invoice", "finance,ap")),
		fixture("ops/restart/ARTIFACT.md", contextFor("Restart runner", "ops")),
	)
	body := mustGet(t, h.URL, "/v1/search_domains?query=finance&scope=finance&top_k=5")
	var resp server.SearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, body)
	}
}

func TestSearchDomains_InvalidArgumentTopK(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	resp, err := http.Get(h.URL + "/v1/search_domains?top_k=999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

// scope/preview returns the per-layer preview for the caller.
func TestScopePreview_HappyPath(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run/ARTIFACT.md", contextFor("Run", "finance")),
	)
	resp, err := http.Get(h.URL + "/v1/scope/preview")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d: %s", resp.StatusCode, buf)
	}
}

// load_artifact missing id returns 400.
func TestLoadArtifact_MissingIDReturns400(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	resp, err := http.Get(h.URL + "/v1/load_artifact")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// load_artifact unknown id returns 404.
func TestLoadArtifact_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run/ARTIFACT.md", contextFor("Run", "finance")),
	)
	resp, err := http.Get(h.URL + "/v1/load_artifact?id=does/not/exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "registry.not_found") {
		t.Errorf("error code missing: %s", body)
	}
}

// load_domain on a missing path returns 404.
func TestLoadDomain_UnknownPathReturns404(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run/ARTIFACT.md", contextFor("Run", "finance")),
	)
	resp, err := http.Get(h.URL + "/v1/load_domain?path=does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// search_artifacts with a tag filter that matches nothing.
func TestSearchArtifacts_TagFilterNoMatches(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("a/ARTIFACT.md", contextFor("A", "finance")),
	)
	body := mustGet(t, h.URL, "/v1/search_artifacts?tags=nonexistent")
	var resp server.SearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected zero results, got %d", len(resp.Results))
	}
}

// search_artifacts with empty query and no filters returns all visible
// artifacts.
func TestSearchArtifacts_EmptyQuery(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("a/ARTIFACT.md", contextFor("A", "")),
		fixture("b/ARTIFACT.md", contextFor("B", "")),
	)
	body := mustGet(t, h.URL, "/v1/search_artifacts?query=")
	var resp server.SearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
