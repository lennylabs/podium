package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
)

const deprecationNote = "version 2.0.0 of team/x is deprecated; replaced_by team/y; this load serves version 1.0.0"

// deprecationMCP returns an MCP server in mode over a cache in dir, against
// the registry at url.
func deprecationMCP(t *testing.T, dir, url, mode string) *mcpServer {
	t.Helper()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	resolutions := newResolutionCache(dir)
	t.Cleanup(func() { _ = resolutions.Close() })
	return &mcpServer{
		cfg:         &config{cacheDir: dir, cacheMode: mode, registry: url, harness: "none", verifyPolicy: sign.PolicyNever},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
}

func wantDeprecationMembers(t *testing.T, out any, label string) {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("%s: result %T (%v), want a map", label, out, out)
	}
	if m["deprecation_warning"] != deprecationNote || m["replaced_by"] != "team/y" {
		t.Errorf("%s: deprecation_warning=%v replaced_by=%v, want the registry's members: %v", label, m["deprecation_warning"], m["replaced_by"], m)
	}
	if _, set := m["deprecated"]; set {
		t.Errorf("%s: deprecated is set on a live served version", label)
	}
}

// Spec: §5 (Deprecation in the load_artifact result), §7.2 — a fresh load
// passes the registry's deprecation members to the tool result, and the
// cached record keeps them for an offline load (§6.5).
func TestLoadArtifact_ResultCarriesDeprecationMembers(t *testing.T) {
	t.Parallel()
	body := loadArtifactJSON(t, map[string]any{
		"id": "team/x", "type": "agent", "version": "1.0.0",
		"manifest_body": "body", "frontmatter": "---\ntype: agent\n---\n",
		"deprecation_warning": deprecationNote, "replaced_by": "team/y",
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	dir := t.TempDir()
	srv := deprecationMCP(t, dir, ts.URL, "always-revalidate")
	wantDeprecationMembers(t, srv.loadArtifact(map[string]any{"id": "team/x", "version": "1.0.0"}), "fresh load")

	// The same server, now offline-only, serves the cached record.
	srv.cfg.cacheMode, srv.cfg.registry = "offline-only", "http://127.0.0.1:1"
	wantDeprecationMembers(t, srv.loadArtifact(map[string]any{"id": "team/x", "version": "1.0.0"}), "offline-only cached load")
}

// Spec: §6.5, §7.2 — a HEAD revalidation that carries
// X-Podium-Deprecation-Warning does not confirm the cached record, and a
// cached record that carries a warning is not confirmed by a HEAD without
// one: both fetch the artifact in full.
func TestLoadArtifact_DeprecationWarningForcesARefetch(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: agent\nversion: 1.0.0\n---\n"
	for _, c := range []struct {
		name          string
		headWarns     bool
		cachedWarning string
	}{
		{"head reports a warning", true, ""},
		{"cached record carries a stale warning", false, deprecationNote},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cache, _ := newContentCache(dir)
			rec := primeCachedRecord(t, cache, "team/x", fm, "cached-body")
			if c.cachedWarning != "" {
				rec.DeprecationWarning = c.cachedWarning
				if err := (&mcpServer{cache: cache}).cacheVerifiedRecord(rec); err != nil {
					t.Fatal(err)
				}
			}
			fresh := map[string]any{
				"id": "team/x", "type": "agent", "version": "1.0.0",
				"manifest_body": "fresh-body", "frontmatter": fm,
			}
			if c.headWarns {
				fresh["deprecation_warning"] = deprecationNote
				fresh["replaced_by"] = "team/y"
			}
			body := loadArtifactJSON(t, fresh)
			var gets atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.Header().Set("X-Podium-Content-Hash", rec.ContentHash)
					if c.headWarns {
						w.Header().Set(deprecationWarningHeader, deprecationNote)
					}
					return
				}
				gets.Add(1)
				_, _ = w.Write([]byte(body))
			}))
			defer ts.Close()
			srv := deprecationMCP(t, dir, ts.URL, "always-revalidate")
			srv.resolutions.PutVersion("team/x", "1.0.0", rec.ContentHash, time.Now())

			out, ok := srv.loadArtifact(map[string]any{"id": "team/x", "version": "1.0.0"}).(map[string]any)
			if !ok || out["manifest_body"] != "fresh-body" || gets.Load() != 1 {
				t.Fatalf("result = %v after %d GETs, want the refetched record from one GET", out, gets.Load())
			}
			if c.headWarns {
				wantDeprecationMembers(t, out, c.name)
			} else if _, set := out["deprecation_warning"]; set {
				t.Errorf("refetched result kept the stale warning: %v", out)
			}
		})
	}
}
