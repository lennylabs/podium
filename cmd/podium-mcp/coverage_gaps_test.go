package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// --- load_domain.go pure helpers ---------------------------------------------

// Spec: §4.5.2/§6.4 — overlayHasDomainContent gates synthesizing a result for
// an overlay-only domain the registry 404s. It reports true when the overlay
// carries a DOMAIN.md at the path, a DOMAIN.md deeper under it, or an artifact
// under it, and false when nothing in the overlay touches the path.
func TestOverlayHasDomainContent(t *testing.T) {
	t.Parallel()
	mkDomain := func() *manifest.Domain { return &manifest.Domain{Description: "d"} }
	rec := func(id string) filesystem.ArtifactRecord {
		return filesystem.ArtifactRecord{ID: id, Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Name: "n", Version: "1.0.0"}}
	}
	for _, tc := range []struct {
		name    string
		path    string
		domains map[string]*manifest.Domain
		records []filesystem.ArtifactRecord
		want    bool
	}{
		{
			name:    "domain at path",
			path:    "finance",
			domains: map[string]*manifest.Domain{"finance": mkDomain()},
			want:    true,
		},
		{
			name:    "deeper domain under path",
			path:    "finance",
			domains: map[string]*manifest.Domain{"finance/ap": mkDomain()},
			want:    true,
		},
		{
			name:    "artifact under path",
			path:    "finance",
			records: []filesystem.ArtifactRecord{rec("finance/ap/pay")},
			want:    true,
		},
		{
			name:    "unrelated domain only",
			path:    "finance",
			domains: map[string]*manifest.Domain{"hr": mkDomain()},
			records: []filesystem.ArtifactRecord{rec("hr/onboard")},
			want:    false,
		},
		{
			name: "empty overlay",
			path: "finance",
			want: false,
		},
		{
			// A sibling whose name shares a literal prefix ("financex") is not
			// segment-aligned under "finance", so it does not count.
			name:    "prefix-not-segment sibling",
			path:    "finance",
			domains: map[string]*manifest.Domain{"financex": mkDomain()},
			want:    false,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := overlayHasDomainContent(tc.path, tc.domains, tc.records); got != tc.want {
				t.Errorf("overlayHasDomainContent(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// Spec: §4.5.5 — renderDepth reads the caller's requested depth in the
// float64 / int / string forms an MCP argument may take, falling back to the
// default when the value is absent, zero, negative, or unparsable.
func TestRenderDepth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args map[string]any
		want int
	}{
		{"absent", map[string]any{}, defaultRenderDepth},
		{"float positive", map[string]any{"depth": float64(5)}, 5},
		{"float zero falls back", map[string]any{"depth": float64(0)}, defaultRenderDepth},
		{"int positive", map[string]any{"depth": 2}, 2},
		{"int negative falls back", map[string]any{"depth": -1}, defaultRenderDepth},
		{"string positive", map[string]any{"depth": "4"}, 4},
		{"string zero falls back", map[string]any{"depth": "0"}, defaultRenderDepth},
		{"string non-numeric falls back", map[string]any{"depth": "deep"}, defaultRenderDepth},
		{"wrong type falls back", map[string]any{"depth": true}, defaultRenderDepth},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := renderDepth(tc.args); got != tc.want {
				t.Errorf("renderDepth(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

// Spec: §4.5.5 — overlayChildDescription returns the overlay DOMAIN.md
// frontmatter description when set, otherwise the synthesized basename
// fallback. The prose body is never surfaced for a non-requested entry.
func TestOverlayChildDescription(t *testing.T) {
	t.Parallel()
	domains := map[string]*manifest.Domain{
		"finance/ap":  {Description: "Accounts payable overlay", Body: "body never shown for a child"},
		"finance/raw": {Body: "body only, no description"},
	}
	if got := overlayChildDescription("finance/ap", domains); got != "Accounts payable overlay" {
		t.Errorf("frontmatter description = %q, want overlay value", got)
	}
	// No DOMAIN.md at the path: the de-slugged title-cased basename is used.
	if got := overlayChildDescription("finance/accounts-payable", domains); got != "Accounts Payable" {
		t.Errorf("missing-DOMAIN.md fallback = %q, want title-cased basename", got)
	}
	// A DOMAIN.md with a body but no description also takes the basename
	// fallback; the body is not promoted to the short description.
	if got := overlayChildDescription("finance/raw", domains); got != "Raw" {
		t.Errorf("body-only fallback = %q, want title-cased basename", got)
	}
}

// joinSeg joins a domain path and a child segment, special-casing the empty
// (root) path so a top-level child has no leading separator.
func TestJoinSeg(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		path, seg, want string
	}{
		{"", "finance", "finance"},
		{"finance", "ap", "finance/ap"},
		{"finance/ap", "invoices", "finance/ap/invoices"},
	} {
		if got := joinSeg(tc.path, tc.seg); got != tc.want {
			t.Errorf("joinSeg(%q, %q) = %q, want %q", tc.path, tc.seg, got, tc.want)
		}
	}
}

// underRest returns id's remainder beyond a segment-aligned prefix, or "" when
// id is not at or under prefix. An empty prefix returns id unchanged.
func TestUnderRest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id, prefix, want string
	}{
		{"finance/ap/pay", "", "finance/ap/pay"}, // empty prefix: id unchanged
		{"finance/ap/pay", "finance", "ap/pay"},  // under prefix
		{"finance", "finance", ""},               // exact match: no remainder
		{"financex/ap", "finance", ""},           // shared literal prefix, not segment-aligned
		{"finance/ap", "hr", ""},                 // unrelated prefix
		{"finance/ap/pay", "finance/ap", "pay"},  // multi-segment prefix
		{"finance-ap", "finance", ""},            // boundary char is not '/'
	} {
		if got := underRest(tc.id, tc.prefix); got != tc.want {
			t.Errorf("underRest(%q, %q) = %q, want %q", tc.id, tc.prefix, got, tc.want)
		}
	}
}

// Spec: §4.5.5 — renderOverlaySubtree renders the overlay-only subdomain tree
// under a path to a bounded depth. A non-positive depth yields no subtree, an
// unlisted child is dropped, and the result is sorted by path.
func TestRenderOverlaySubtree(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	domains := map[string]*manifest.Domain{
		"finance/team/sub": {Description: "deep node"},
		"finance/hidden":   {Unlisted: true},
	}
	records := []filesystem.ArtifactRecord{
		{ID: "finance/team/sub/draft", Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Name: "draft", Version: "0.1.0"}},
		{ID: "finance/hidden/secret", Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Name: "secret", Version: "0.1.0"}},
	}

	// Depth 0 short-circuits to no subtree.
	if got := srv.renderOverlaySubtree("finance", 0, domains, records); got != nil {
		t.Errorf("depth 0 = %+v, want nil", got)
	}

	// Depth 1 renders the immediate children only; the unlisted "hidden"
	// branch is pruned, and "team" appears with no rendered grandchildren.
	depth1 := srv.renderOverlaySubtree("finance", 1, domains, records)
	if len(depth1) != 1 || depth1[0].Path != "finance/team" {
		t.Fatalf("depth 1 = %+v, want only finance/team", depth1)
	}
	if depth1[0].Subdomains != nil {
		t.Errorf("depth 1 grandchildren = %+v, want none", depth1[0].Subdomains)
	}

	// Depth 2 descends one further level into the overlay directory tree.
	depth2 := srv.renderOverlaySubtree("finance", 2, domains, records)
	if len(depth2) != 1 || depth2[0].Path != "finance/team" {
		t.Fatalf("depth 2 top = %+v, want finance/team", depth2)
	}
	sub := depth2[0].Subdomains
	if len(sub) != 1 || sub[0].Path != "finance/team/sub" {
		t.Fatalf("depth 2 nested = %+v, want finance/team/sub", sub)
	}
	if sub[0].Description != "deep node" {
		t.Errorf("nested description = %q, want overlay frontmatter value", sub[0].Description)
	}
}

// Spec: §4.5.2/§6.4 — a domain that exists only in the workspace overlay is
// still part of the caller's effective view. When the registry 404s the path,
// loadDomain synthesizes an empty registry result and composes the overlay
// onto it, exercising overlayHasDomainContent's true branch and the overlay
// subtree renderer through the full meta-tool path.
func TestLoadDomain_OverlayOnlyDomainSynthesized(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/load_domain":
			// The registry never sees the overlay, so an overlay-only path 404s
			// with the §6.10 domain.not_found envelope.
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    "domain.not_found",
				"message": "no such domain",
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	srv.overlayDomains = map[string]*manifest.Domain{
		"drafts":         {Body: "Local drafts workspace"},
		"drafts/newteam": {Description: "New team area"},
	}
	srv.overlay = []filesystem.ArtifactRecord{{
		ID:       "drafts/newteam/helper",
		Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Name: "helper", Version: "0.1.0", Description: "overlay helper"},
	}}

	resp := decodeDomain(t, srv.loadDomain(map[string]any{"path": "drafts"}))
	if resp.Path != "drafts" {
		t.Errorf("path = %q, want drafts", resp.Path)
	}
	if resp.Description != "Local drafts workspace" {
		t.Errorf("description = %q, want overlay body", resp.Description)
	}
	sd, ok := subdomainByPath(resp, "drafts/newteam")
	if !ok {
		t.Fatalf("overlay subdomain drafts/newteam missing: %+v", resp.Subdomains)
	}
	if sd.Name != "newteam" || sd.Description != "New team area" {
		t.Errorf("synthesized subdomain = %+v, want overlay name/description", sd)
	}
}

// Spec: §6.10 — a registry error other than domain.not_found (here a generic
// 500) is not eligible for overlay-only synthesis and passes through as a
// structured error result even when the overlay carries the path.
func TestLoadDomain_OverlayPresentButRegistryErrorPassesThrough(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "internal.error", "message": "boom"})
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	srv.overlayDomains = map[string]*manifest.Domain{"drafts": {Body: "Local drafts"}}

	got := srv.loadDomain(map[string]any{"path": "drafts"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want error map", got)
	}
	if _, has := m["error"]; !has {
		t.Errorf("expected an error result for a non-404 registry failure: %v", m)
	}
}

// --- cliconfig.go applyConfigKV ----------------------------------------------

// Spec: §6.1/§6.2 — applyConfigKV assigns each kebab-case parameter to its
// config field and ignores keys it does not recognize, so an unrelated flag
// does not abort startup. The table covers every recognized key, exercising
// the CSV, TTL, and tri-state-set branches.
func TestApplyConfigKV_AllKeys(t *testing.T) {
	t.Parallel()
	c := &config{}
	pairs := [][2]string{
		{"registry", "https://reg.example"},
		{"harness", "claude-code"},
		{"cache-dir", "/var/cache/podium"},
		{"cache-mode", "offline-first"},
		{"prefetch", "a/x, b/y ,c/z"},
		{"cache-resolution-ttl-seconds", "45"},
		{"materialize-root", "/tmp/mat"},
		{"overlay-path", "/work/.podium"},
		{"audit-sink", "/var/log/podium-audit.log"},
		{"tenant-id", "acme"},
		{"identity-provider", "oauth-device-code"},
		{"verify-signatures", "always"},
		{"signature-provider", "sigstore-keyless"},
		{"oauth-audience", "podium-registry"},
		{"session-token", "tok-123"},
		{"session-token-file", "/run/secrets/token"},
		{"metrics-addr", "127.0.0.1:9090"},
		{"min-client-version", "1.2.3"},
	}
	for _, kv := range pairs {
		applyConfigKV(c, kv[0], kv[1])
	}

	if c.registry != "https://reg.example" || c.harness != "claude-code" {
		t.Errorf("registry/harness = %q/%q", c.registry, c.harness)
	}
	if c.cacheDir != "/var/cache/podium" || c.cacheMode != "offline-first" {
		t.Errorf("cache-dir/mode = %q/%q", c.cacheDir, c.cacheMode)
	}
	if strings.Join(c.prefetchIDs, "|") != "a/x|b/y|c/z" {
		t.Errorf("prefetch = %v, want trimmed CSV split", c.prefetchIDs)
	}
	if c.resolutionTTL.Seconds() != 45 {
		t.Errorf("resolution TTL = %v, want 45s", c.resolutionTTL)
	}
	if c.materializeRoot != "/tmp/mat" || c.overlayPath != "/work/.podium" {
		t.Errorf("materialize-root/overlay-path = %q/%q", c.materializeRoot, c.overlayPath)
	}
	if c.auditSink != "/var/log/podium-audit.log" || !c.auditSinkSet {
		t.Errorf("audit-sink = %q set=%v, want value and set=true", c.auditSink, c.auditSinkSet)
	}
	if c.tenantID != "acme" || c.identityProvider != "oauth-device-code" {
		t.Errorf("tenant/identity = %q/%q", c.tenantID, c.identityProvider)
	}
	if string(c.verifyPolicy) != "always" || c.signatureProvider != "sigstore-keyless" {
		t.Errorf("verify/sig-provider = %q/%q", c.verifyPolicy, c.signatureProvider)
	}
	if c.oauthAudience != "podium-registry" || c.sessionToken != "tok-123" {
		t.Errorf("audience/token = %q/%q", c.oauthAudience, c.sessionToken)
	}
	if c.sessionTokenFile != "/run/secrets/token" || c.metricsAddr != "127.0.0.1:9090" {
		t.Errorf("token-file/metrics = %q/%q", c.sessionTokenFile, c.metricsAddr)
	}
	if c.minClientVersion != "1.2.3" {
		t.Errorf("min-client-version = %q", c.minClientVersion)
	}
}

// Spec: §6.1/§6.2 — an unrecognized key is silently ignored and leaves the
// config untouched.
func TestApplyConfigKV_UnknownKeyIgnored(t *testing.T) {
	t.Parallel()
	c := &config{registry: "keep"}
	applyConfigKV(c, "test.timeout", "30s")
	applyConfigKV(c, "totally-unknown", "value")
	if c.registry != "keep" {
		t.Errorf("registry mutated by unknown key: %q", c.registry)
	}
}

// --- local_semantic.go buildLocalSemantic / mcpOverlayEmbedder ---------------

// Spec: §9.1 — buildLocalSemantic activates the overlay semantic index when
// both an embedding provider and a vector backend are named. The ollama
// embedder and the in-memory vector backend construct without any network
// call, so the wiring is exercised without reaching a provider.
func TestBuildLocalSemantic_ActivatesWithBothBackends(t *testing.T) {
	cfg := &config{localEmbeddingProvider: "ollama", localVectorBackend: "memory"}
	idx, err := buildLocalSemantic(cfg)
	if err != nil {
		t.Fatalf("buildLocalSemantic: %v", err)
	}
	if idx == nil {
		t.Fatalf("index = nil, want an activated overlay semantic index")
	}
	if idx.emb == nil || idx.vec == nil {
		t.Errorf("index wiring incomplete: %+v", idx)
	}
}

// Spec: §9.1 — an embedding-provider selection error (a built-in provider
// named with no credential) propagates from buildLocalSemantic rather than
// silently disabling the index.
func TestBuildLocalSemantic_EmbedderErrorPropagates(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := &config{localEmbeddingProvider: "openai", localVectorBackend: "memory"}
	idx, err := buildLocalSemantic(cfg)
	if err == nil {
		t.Fatalf("missing OPENAI_API_KEY: no error (idx=%v)", idx)
	}
	if idx != nil {
		t.Errorf("index = %v, want nil on embedder error", idx)
	}
}

// Spec: §9.1 — a registered embedder that resolves to a nil provider leaves
// the overlay BM25-only: buildLocalSemantic returns (nil, nil) rather than
// constructing an index around a nil backend.
func TestBuildLocalSemantic_NilEmbedderDeactivates(t *testing.T) {
	id := "test-nil-overlay-embedder"
	if err := embedding.Default.Register(id, func(map[string]string) (embedding.Provider, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("register nil-returning embedder: %v", err)
	}
	idx, err := buildLocalSemantic(&config{localEmbeddingProvider: id, localVectorBackend: "memory"})
	if err != nil {
		t.Fatalf("buildLocalSemantic: %v", err)
	}
	if idx != nil {
		t.Errorf("index = %v, want nil when the embedder resolves to nil", idx)
	}
}

// Spec: §9.1 — mcpOverlayEmbedder selects the overlay EmbeddingProvider by id.
// The ollama provider needs no credential; the keyed built-ins error without
// their API key; an unknown id is rejected.
func TestMCPOverlayEmbedder_BuiltinSelection(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("VOYAGE_API_KEY", "")
	t.Setenv("COHERE_API_KEY", "")
	t.Setenv("PODIUM_EMBEDDING_MODEL", "")

	p, err := mcpOverlayEmbedder("ollama")
	if err != nil {
		t.Fatalf("ollama embedder: %v", err)
	}
	if p == nil || p.ID() != "ollama" {
		t.Errorf("ollama provider = %v, want ollama", p)
	}

	for _, id := range []string{"openai", "voyage", "cohere"} {
		if _, err := mcpOverlayEmbedder(id); err == nil {
			t.Errorf("%s embedder with no key: expected an error", id)
		}
	}

	if _, err := mcpOverlayEmbedder("bogus"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("bogus embedder err = %v, want an unknown-provider error", err)
	}
}

// Spec: §9.1/§9.2 — a custom embedding provider registered via
// embedding.Default.Register is consulted before the built-in switch, so an id
// the switch does not know resolves through the SPI.
func TestMCPOverlayEmbedder_CustomRegistryWins(t *testing.T) {
	id := "test-overlay-embedder"
	if err := embedding.Default.Register(id, func(map[string]string) (embedding.Provider, error) {
		return &stubEmbed{dim: 3, vecFor: orthoVec}, nil
	}); err != nil {
		t.Fatalf("register custom embedder: %v", err)
	}
	p, err := mcpOverlayEmbedder(id)
	if err != nil {
		t.Fatalf("custom embedder: %v", err)
	}
	if p == nil || p.ID() != "stub" {
		t.Errorf("custom provider = %v, want the registered stub", p)
	}
}

// Spec: §9.1/§9.2 — a custom embedder factory that fails surfaces its error
// from mcpOverlayEmbedder rather than falling through to the built-in switch.
func TestMCPOverlayEmbedder_CustomRegistryError(t *testing.T) {
	id := "test-overlay-embedder-err"
	if err := embedding.Default.Register(id, func(map[string]string) (embedding.Provider, error) {
		return nil, errEmbedderFactory
	}); err != nil {
		t.Fatalf("register failing embedder: %v", err)
	}
	if _, err := mcpOverlayEmbedder(id); err == nil {
		t.Errorf("custom factory error: expected propagation, got nil")
	}
}

// --- main.go manifestRedactKeys / manifestBodyRefresher ----------------------

// Spec: §8.2 — manifestRedactKeys returns the audit_redact field names declared
// in an artifact's frontmatter, and nil when the frontmatter is empty,
// unparsable, or declares no directive.
func TestManifestRedactKeys(t *testing.T) {
	t.Parallel()
	declared := "---\ntype: context\nversion: 1.0.0\naudit_redact: [ssn, account_number]\n---\nbody\n"
	got := manifestRedactKeys(declared)
	if strings.Join(got, ",") != "ssn,account_number" {
		t.Errorf("declared keys = %v, want [ssn account_number]", got)
	}

	if got := manifestRedactKeys(""); got != nil {
		t.Errorf("empty frontmatter = %v, want nil", got)
	}
	// No directive present: the field is absent, so the result is nil.
	if got := manifestRedactKeys("---\ntype: context\nversion: 1.0.0\n---\nbody\n"); got != nil {
		t.Errorf("no-directive frontmatter = %v, want nil", got)
	}
	// Unparsable YAML frontmatter yields nil rather than a parse error.
	if got := manifestRedactKeys("---\n\tbad: : :\n---\n"); got != nil {
		t.Errorf("unparsable frontmatter = %v, want nil", got)
	}
}

// Spec: §6.6 step 1 — manifestBodyRefresher re-requests /v1/load_artifact and
// yields a freshly presigned manifest_body_url keyed under the refresh
// sentinel, so the manifest-body channel can replace an expired URL.
func TestManifestBodyRefresher_YieldsFreshURL(t *testing.T) {
	t.Parallel()
	const freshURL = "https://blob.example/manifest-body?sig=fresh"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                "finance/glossary",
			"type":              "context",
			"version":           "1.0.0",
			"content_hash":      "sha256:abc",
			"manifest_body_url": map[string]any{"presigned_url": freshURL, "content_hash": "sha256:abc", "size": 10},
		})
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}

	refresh := srv.manifestBodyRefresher(map[string]any{"id": "finance/glossary"})
	links, err := refresh()
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	link, ok := links[manifestBodyRefreshKey]
	if !ok {
		t.Fatalf("refresh links missing %q sentinel: %+v", manifestBodyRefreshKey, links)
	}
	if link.URL != freshURL {
		t.Errorf("refreshed url = %q, want %q", link.URL, freshURL)
	}
}

// Spec: §6.6 step 1 — when the registry response carries no manifest_body_url
// (the body now fits inline), the refresher returns no links and no error so
// the caller falls back to the inline body.
func TestManifestBodyRefresher_NilWhenNoBodyURL(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "finance/glossary", "type": "context", "version": "1.0.0", "content_hash": "sha256:abc",
		})
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}

	links, err := srv.manifestBodyRefresher(map[string]any{"id": "finance/glossary"})()
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if links != nil {
		t.Errorf("links = %+v, want nil when no manifest_body_url", links)
	}
}

// Spec: §6.6 step 1 — a transport failure and an undecodable body both
// surface as errors from the refresher rather than a partial link set.
func TestManifestBodyRefresher_ErrorPaths(t *testing.T) {
	t.Parallel()

	// Unreachable registry: the fetch error propagates.
	down := &mcpServer{cfg: &config{registry: "http://127.0.0.1:1"}, http: &http.Client{}}
	if _, err := down.manifestBodyRefresher(map[string]any{"id": "x"})(); err == nil {
		t.Errorf("unreachable registry: expected a fetch error")
	}

	// Malformed JSON body: the decode error propagates.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(ts.Close)
	bad := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	if _, err := bad.manifestBodyRefresher(map[string]any{"id": "x"})(); err == nil {
		t.Errorf("malformed body: expected a decode error")
	}
}

// errEmbedderFactory is the failure a registered embedder factory returns in
// the custom-registry error test, so mcpOverlayEmbedder has a concrete error
// to propagate.
var errEmbedderFactory = errors.New("test embedder factory failure")
