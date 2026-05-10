package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
	"github.com/lennylabs/podium/pkg/registry/server"
)

const (
	contextArtifact = `---
type: context
version: 1.0.0
description: %s
tags: [%s]
---

Body for %s.
`
	skillArtifact = `---
type: skill
version: 1.0.0
description: %s
---

`
	skillBody = `---
name: %s
description: %s
---

Body.
`
)

func mustGet(t testing.TB, base, path string) []byte {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status %d, body=%s", path, resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

// Spec: §5 / §13.9 — /healthz reports mode: ready when the registry is
// reachable; clients use this to confirm a server-source registry is up.
func TestHealth_ReturnsReady(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	body := mustGet(t, h.URL, "/healthz")
	var resp server.HealthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Ready || resp.Mode != "ready" {
		t.Errorf("got %+v, want ready", resp)
	}
}

// Spec: §5 load_domain — root call (no path) returns subdomains plus
// notable artifacts directly under root.
func TestLoadDomain_RootMap(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/ap/pay-invoice/ARTIFACT.md", contextFor("Pay invoice", "finance,ap")),
		fixture("finance/close/run-variance/ARTIFACT.md", contextFor("Variance", "finance,close")),
		fixture("company-glossary/ARTIFACT.md", contextFor("Glossary", "company")),
	)
	body := mustGet(t, h.URL, "/v1/load_domain")
	var resp server.LoadDomainResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Subdomains) == 0 {
		t.Errorf("expected at least one subdomain, got: %+v", resp)
	}
	foundFinance := false
	for _, s := range resp.Subdomains {
		if s.Path == "finance" {
			foundFinance = true
		}
	}
	if !foundFinance {
		t.Errorf("expected finance subdomain, got %+v", resp.Subdomains)
	}
}

// Spec: §5 load_domain — drilling into a path returns the subdomains
// under that path plus notable artifacts directly under it.
func TestLoadDomain_DrillIn(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/ap/pay-invoice/ARTIFACT.md", contextFor("Pay invoice", "finance,ap")),
		fixture("finance/close/run-variance/ARTIFACT.md", contextFor("Variance", "finance,close")),
	)
	body := mustGet(t, h.URL, "/v1/load_domain?path=finance")
	var resp server.LoadDomainResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantPaths := []string{"finance/ap", "finance/close"}
	gotPaths := []string{}
	for _, s := range resp.Subdomains {
		gotPaths = append(gotPaths, s.Path)
	}
	for _, want := range wantPaths {
		if !containsString(gotPaths, want) {
			t.Errorf("expected subdomain %q in %v", want, gotPaths)
		}
	}
}

// Spec: §5 search_artifacts — basic substring match returns the
// artifact's descriptor; no manifest body is included.
func TestSearchArtifacts_Substring(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run-variance/ARTIFACT.md", contextFor("Variance analysis", "finance")),
		fixture("finance/pay-invoice/ARTIFACT.md", contextFor("Pay an invoice", "finance,ap")),
	)
	body := mustGet(t, h.URL, "/v1/search_artifacts?query=variance")
	var resp server.SearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(resp.Results), resp)
	}
	if resp.Results[0].ID != "finance/run-variance" {
		t.Errorf("ID = %q", resp.Results[0].ID)
	}
}

// Spec: §5 search_artifacts — type filter narrows to the requested type.
func TestSearchArtifacts_TypeFilter(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run/ARTIFACT.md", skillFor("Skill")),
		fixture("finance/run/SKILL.md", skillBodyFor("run", "Run")),
		fixture("notes/glossary/ARTIFACT.md", contextFor("Glossary", "notes")),
	)
	body := mustGet(t, h.URL, "/v1/search_artifacts?type=context")
	var resp server.SearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != "notes/glossary" {
		t.Errorf("type filter failed: %+v", resp.Results)
	}
}

// Spec: §5 search_artifacts — top_k > 50 is rejected with
// registry.invalid_argument.
// Matrix: §6.10 (registry.invalid_argument)
func TestSearchArtifacts_TopKBoundary(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	resp, err := http.Get(h.URL + "/v1/search_artifacts?top_k=51")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "registry.invalid_argument") {
		t.Errorf("body missing registry.invalid_argument: %s", body)
	}
}

// Spec: §5 load_artifact — returns the manifest body and the bundled
// resources inline (Stage 3; presigned URLs land in Phase 5).
func TestLoadArtifact_ReturnsManifestAndResources(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("finance/run/ARTIFACT.md", skillFor("Run")),
		fixture("finance/run/SKILL.md", skillBodyFor("run", "Run a thing")),
		fixture("finance/run/scripts/run.py", "print('run')\n"),
	)
	body := mustGet(t, h.URL, "/v1/load_artifact?id=finance/run")
	var resp server.LoadArtifactResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != "finance/run" {
		t.Errorf("ID = %q", resp.ID)
	}
	if !strings.Contains(resp.ManifestBody, "Body.") {
		t.Errorf("manifest_body did not contain SKILL.md body: %q", resp.ManifestBody)
	}
	if resp.Resources["scripts/run.py"] != "print('run')\n" {
		t.Errorf("resource missing: %+v", resp.Resources)
	}
}

// Spec: §6.10 / §5 — load_artifact for an unknown ID returns 404 with
// registry.not_found.
// Matrix: §6.10 (registry.not_found)
func TestLoadArtifact_NotFound(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	resp, err := http.Get(h.URL + "/v1/load_artifact?id=does/not/exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "registry.not_found") {
		t.Errorf("body missing registry.not_found: %s", body)
	}
}

// helpers

func fixture(p, content string) testharness.WriteTreeOption {
	return testharness.WriteTreeOption{Path: p, Content: content}
}

func contextFor(desc, tags string) string {
	return fmtString(contextArtifact, desc, tags, desc)
}

func skillFor(desc string) string { return fmtString(skillArtifact, desc) }

func skillBodyFor(name, desc string) string {
	return fmtString(skillBody, name, desc)
}

func fmtString(format string, args ...string) string {
	out := format
	for _, a := range args {
		idx := strings.Index(out, "%s")
		if idx < 0 {
			break
		}
		out = out[:idx] + a + out[idx+2:]
	}
	return out
}

func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
