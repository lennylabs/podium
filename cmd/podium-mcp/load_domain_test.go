package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// fakeRegistry serves /v1/load_domain (the supplied payload) and /v1/catalog
// (the supplied artifacts, filtered to the requested scope prefix), the two
// endpoints the client-side load_domain merge consults.
func fakeRegistry(t *testing.T, loadDomain map[string]any, catalog []catalogEntry) *mcpServer {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/load_domain":
			_ = json.NewEncoder(w).Encode(loadDomain)
		case "/v1/catalog":
			scope := r.URL.Query().Get("scope")
			ids := []string{}
			arts := []catalogEntry{}
			for _, e := range catalog {
				if scope == "" || e.ID == scope || hasSegPrefix(e.ID, scope) {
					ids = append(ids, e.ID)
					arts = append(arts, e)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ids": ids, "artifacts": arts})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	return &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
}

func hasSegPrefix(id, prefix string) bool {
	return len(id) > len(prefix) && id[:len(prefix)] == prefix && id[len(prefix)] == '/'
}

// decodeDomain runs the merge result (a jsonAny map) back through JSON into the
// typed envelope so tests assert against the wire schema.
func decodeDomain(t *testing.T, out any) ldResponse {
	t.Helper()
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var resp ldResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("decode result %s: %v", b, err)
	}
	return resp
}

func notableIDs(resp ldResponse) map[string]ldArtifact {
	m := map[string]ldArtifact{}
	for _, a := range resp.Notable {
		m[a.ID] = a
	}
	return m
}

func subdomainByPath(resp ldResponse, path string) (ldSubdomain, bool) {
	for _, s := range resp.Subdomains {
		if s.Path == path {
			return s, true
		}
	}
	return ldSubdomain{}, false
}

// spec: §4.5.2/§4.5.4/§6.4 (F-4.5.2/F-6.4.2) — with no workspace overlay the
// load_domain result passes through the registry untouched.
func TestLoadDomain_NoOverlayPassthrough(t *testing.T) {
	t.Parallel()
	srv := fakeRegistry(t, map[string]any{
		"path":        "finance",
		"description": "Registry finance",
		"keywords":    []string{"money"},
		"subdomains":  []map[string]any{{"path": "finance/ap", "name": "ap", "description": "AP"}},
		"notable":     []map[string]any{{"id": "finance/x", "type": "skill", "summary": "x", "source": "signal"}},
	}, nil)
	resp := decodeDomain(t, srv.loadDomain(map[string]any{"path": "finance"}))
	if resp.Description != "Registry finance" {
		t.Errorf("description = %q, want registry value", resp.Description)
	}
	if len(resp.Notable) != 1 || resp.Notable[0].ID != "finance/x" {
		t.Errorf("notable = %+v, want only finance/x", resp.Notable)
	}
	if len(resp.Subdomains) != 1 || resp.Subdomains[0].Path != "finance/ap" {
		t.Errorf("subdomains = %+v, want only finance/ap", resp.Subdomains)
	}
}

// spec: §4.5.4 — an overlay DOMAIN.md description/body wins over the registry's
// (highest-precedence layer) and keywords append-unique.
func TestLoadDomain_OverlayDescriptionAndKeywords(t *testing.T) {
	t.Parallel()
	srv := fakeRegistry(t, map[string]any{
		"path":        "finance",
		"description": "Registry finance",
		"keywords":    []string{"money", "ledger"},
		"subdomains":  []map[string]any{},
		"notable":     []map[string]any{},
	}, nil)
	srv.overlayDomains = map[string]*manifest.Domain{
		"finance": {
			Body:      "Local working notes for finance",
			Discovery: &manifest.DomainDiscovery{Keywords: []string{"ledger", "draft"}},
		},
	}
	resp := decodeDomain(t, srv.loadDomain(map[string]any{"path": "finance"}))
	if resp.Description != "Local working notes for finance" {
		t.Errorf("description = %q, want overlay body", resp.Description)
	}
	want := []string{"money", "ledger", "draft"}
	if len(resp.Keywords) != len(want) {
		t.Fatalf("keywords = %v, want %v", resp.Keywords, want)
	}
	for i := range want {
		if resp.Keywords[i] != want[i] {
			t.Errorf("keywords = %v, want %v", resp.Keywords, want)
		}
	}
}

// spec: §4.5.5 — an overlay artifact that is a direct child of the requested
// domain joins the notable candidate pool, tagged overlay-sourced.
func TestLoadDomain_OverlayDirectChildNotable(t *testing.T) {
	t.Parallel()
	srv := fakeRegistry(t, map[string]any{
		"path":       "finance",
		"subdomains": []map[string]any{},
		"notable":    []map[string]any{{"id": "finance/x", "type": "skill", "summary": "x", "source": "signal"}},
	}, nil)
	srv.overlay = []filesystem.ArtifactRecord{{
		ID: "finance/draft-helper",
		Artifact: &manifest.Artifact{
			Type: manifest.TypeSkill, Name: "draft-helper", Version: "0.1.0",
			Description: "in-progress finance helper",
		},
	}}
	resp := decodeDomain(t, srv.loadDomain(map[string]any{"path": "finance"}))
	got := notableIDs(resp)
	d, ok := got["finance/draft-helper"]
	if !ok {
		t.Fatalf("overlay draft missing from notable: %+v", resp.Notable)
	}
	if !d.Overlay || d.Summary != "in-progress finance helper" || d.Source != "signal" {
		t.Errorf("overlay notable = %+v, want overlay/signal with summary", d)
	}
	if _, ok := got["finance/x"]; !ok {
		t.Errorf("registry notable finance/x dropped: %+v", resp.Notable)
	}
}

// spec: §4.5.5 — an overlay artifact below an immediate child introduces that
// child as a subdomain of the requested domain.
func TestLoadDomain_OverlayNewSubdomain(t *testing.T) {
	t.Parallel()
	srv := fakeRegistry(t, map[string]any{
		"path":       "finance",
		"subdomains": []map[string]any{{"path": "finance/ap", "name": "ap", "description": "AP"}},
		"notable":    []map[string]any{},
	}, nil)
	srv.overlay = []filesystem.ArtifactRecord{{
		ID:       "finance/newteam/draft",
		Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Name: "draft", Version: "0.1.0"},
	}}
	srv.overlayDomains = map[string]*manifest.Domain{
		"finance/newteam": {Description: "New team workspace"},
	}
	resp := decodeDomain(t, srv.loadDomain(map[string]any{"path": "finance"}))
	sd, ok := subdomainByPath(resp, "finance/newteam")
	if !ok {
		t.Fatalf("overlay subdomain finance/newteam missing: %+v", resp.Subdomains)
	}
	if sd.Name != "newteam" || sd.Description != "New team workspace" {
		t.Errorf("subdomain = %+v, want name/desc from overlay", sd)
	}
	if _, ok := subdomainByPath(resp, "finance/ap"); !ok {
		t.Errorf("registry subdomain finance/ap dropped: %+v", resp.Subdomains)
	}
}

// spec: §4.5.2 — a workspace-local DOMAIN.md include: resolves over the merged
// view (registry catalog ∪ overlay), pulling in both a registry artifact and an
// overlay artifact.
func TestLoadDomain_OverlayIncludeMergedView(t *testing.T) {
	t.Parallel()
	srv := fakeRegistry(t, map[string]any{
		"path":       "drafts",
		"subdomains": []map[string]any{},
		"notable":    []map[string]any{},
	}, []catalogEntry{
		{ID: "finance/ap/registry-pay", Type: "skill", Summary: "registry pay"},
		{ID: "other/unrelated", Type: "skill", Summary: "nope"},
	})
	srv.overlay = []filesystem.ArtifactRecord{{
		ID:       "finance/ap/overlay-pay",
		Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Name: "overlay-pay", Version: "0.1.0", Description: "overlay pay"},
	}}
	srv.overlayDomains = map[string]*manifest.Domain{
		"drafts": {Include: []string{"finance/ap/*"}},
	}
	resp := decodeDomain(t, srv.loadDomain(map[string]any{"path": "drafts"}))
	got := notableIDs(resp)
	if _, ok := got["finance/ap/registry-pay"]; !ok {
		t.Errorf("merged-view include missed registry artifact: %+v", resp.Notable)
	}
	if _, ok := got["finance/ap/overlay-pay"]; !ok {
		t.Errorf("merged-view include missed overlay artifact: %+v", resp.Notable)
	}
	if _, ok := got["other/unrelated"]; ok {
		t.Errorf("include pulled in out-of-scope artifact: %+v", resp.Notable)
	}
}

// spec: §4.5.3 — an overlay DOMAIN.md unlisted: true removes the folder and its
// subtree from the parent's enumeration.
func TestLoadDomain_OverlayUnlistedPrunesSubdomain(t *testing.T) {
	t.Parallel()
	srv := fakeRegistry(t, map[string]any{
		"path": "finance",
		"subdomains": []map[string]any{
			{"path": "finance/ap", "name": "ap", "description": "AP"},
			{"path": "finance/secret", "name": "secret", "description": "Secret"},
		},
		"notable": []map[string]any{},
	}, nil)
	srv.overlayDomains = map[string]*manifest.Domain{
		"finance/secret": {Unlisted: true},
	}
	resp := decodeDomain(t, srv.loadDomain(map[string]any{"path": "finance"}))
	if _, ok := subdomainByPath(resp, "finance/secret"); ok {
		t.Errorf("unlisted subdomain not pruned: %+v", resp.Subdomains)
	}
	if _, ok := subdomainByPath(resp, "finance/ap"); !ok {
		t.Errorf("non-unlisted subdomain finance/ap dropped: %+v", resp.Subdomains)
	}
}

// spec: §4.5.5 — an overlay DOMAIN.md at a child path overrides that child's
// short description (frontmatter description, never the body).
func TestLoadDomain_OverlayChildDescriptionOverride(t *testing.T) {
	t.Parallel()
	srv := fakeRegistry(t, map[string]any{
		"path":       "finance",
		"subdomains": []map[string]any{{"path": "finance/ap", "name": "ap", "description": "Registry AP"}},
		"notable":    []map[string]any{},
	}, nil)
	srv.overlayDomains = map[string]*manifest.Domain{
		"finance/ap": {Description: "Local AP overrides", Body: "body never shown for a child"},
	}
	resp := decodeDomain(t, srv.loadDomain(map[string]any{"path": "finance"}))
	sd, ok := subdomainByPath(resp, "finance/ap")
	if !ok {
		t.Fatalf("finance/ap subdomain missing")
	}
	if sd.Description != "Local AP overrides" {
		t.Errorf("child description = %q, want overlay frontmatter description", sd.Description)
	}
}
