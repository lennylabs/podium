package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
)

// stubArtifact is one artifact a stub registry serves over /v1/load_artifact.
type stubArtifact struct {
	typ           string
	layer         string
	frontmatter   string
	manifestBody  string
	skillRaw      string
	resources     map[string]string
	resourcesB64  bool
	largeResource map[string]string // path -> bytes served from a side endpoint
}

// newStubRegistry serves the §7.5 server-source endpoints podium sync reads:
// GET /v1/sync/manifest (the effective view) and GET /v1/load_artifact
// (per-artifact manifest + resources). Large resources are served from a
// /blob endpoint via presigned-style URLs.
func newStubRegistry(t *testing.T, arts map[string]stubArtifact) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/v1/sync/manifest", func(w http.ResponseWriter, r *http.Request) {
		type entry struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Version string `json:"version"`
			Layer   string `json:"layer"`
		}
		out := struct {
			Artifacts []entry `json:"artifacts"`
		}{}
		for id, a := range arts {
			out.Artifacts = append(out.Artifacts, entry{ID: id, Type: a.typ, Version: "1.0.0", Layer: a.layer})
		}
		writeJSONTest(w, out)
	})

	mux.HandleFunc("/v1/load_artifact", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		a, ok := arts[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			writeJSONTest(w, map[string]string{"code": "registry.not_found", "message": id})
			return
		}
		type link struct {
			URL string `json:"presigned_url"`
		}
		resp := map[string]any{
			"id":            id,
			"type":          a.typ,
			"layer":         a.layer,
			"manifest_body": a.manifestBody,
			"frontmatter":   a.frontmatter,
		}
		// spec: §4.3.4 / §11 — the registry delivers a skill's verbatim
		// SKILL.md so the consumer materializes it byte-for-byte.
		if a.skillRaw != "" {
			resp["skill_raw"] = a.skillRaw
		}
		if len(a.resources) > 0 {
			resp["resources"] = a.resources
			resp["resources_base64"] = a.resourcesB64
		}
		if len(a.largeResource) > 0 {
			large := map[string]link{}
			for path := range a.largeResource {
				large[path] = link{URL: srv.URL + "/blob?id=" + id + "&path=" + path}
			}
			resp["large_resources"] = large
		}
		writeJSONTest(w, resp)
	})

	mux.HandleFunc("/blob", func(w http.ResponseWriter, r *http.Request) {
		a := arts[r.URL.Query().Get("id")]
		_, _ = w.Write([]byte(a.largeResource[r.URL.Query().Get("path")]))
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSONTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// spec: §2.2, §7.5 (F-2.2.2) — podium sync server-source reads the caller's
// effective view over HTTP and materializes each artifact through the
// harness adapter, mirroring the MCP server's server-source delivery.
func TestRun_ServerSource_MaterializesEffectiveView(t *testing.T) {
	t.Parallel()
	srv := newStubRegistry(t, map[string]stubArtifact{
		"team/glossary": {
			typ:          "context",
			layer:        "team-shared",
			frontmatter:  contextArtifactSrc,
			manifestBody: "Glossary body.\n",
		},
	})
	target := t.TempDir()
	res, err := Run(Options{
		RegistryPath: srv.URL,
		Target:       target,
		AdapterID:    "none",
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("server-source Run: %v", err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].ID != "team/glossary" {
		t.Fatalf("Result.Artifacts = %+v, want one team/glossary entry", res.Artifacts)
	}
	if res.Artifacts[0].Layer != "team-shared" {
		t.Errorf("layer = %q, want team-shared", res.Artifacts[0].Layer)
	}
	got := readFileT(t, filepath.Join(target, "team", "glossary", "ARTIFACT.md"))
	if got != contextArtifactSrc {
		t.Errorf("ARTIFACT.md = %q, want the served frontmatter", got)
	}
}

// spec: §4.3.4 / §11 — a server-source skill materializes the verbatim
// SKILL.md the registry delivers in skill_raw, so its authored frontmatter
// (name, description, …) is preserved rather than replaced by ARTIFACT.md's.
func TestRun_ServerSource_SkillWritesSkillMD(t *testing.T) {
	t.Parallel()
	const skillFM = "---\ntype: skill\nversion: 1.0.0\n---\n"
	// The authored SKILL.md carries its own frontmatter, distinct from
	// ARTIFACT.md, plus the prose body.
	const skillMD = "---\nname: lint\ndescription: Run the project linter.\n---\n\nRun the linter.\n"
	srv := newStubRegistry(t, map[string]stubArtifact{
		"eng/lint": {
			typ:          "skill",
			layer:        "local",
			frontmatter:  skillFM,
			manifestBody: "Run the linter.\n",
			skillRaw:     skillMD,
		},
	})
	target := t.TempDir()
	if _, err := Run(Options{RegistryPath: srv.URL, Target: target, AdapterID: "none", HTTPClient: srv.Client()}); err != nil {
		t.Fatalf("server-source Run: %v", err)
	}
	root := filepath.Join(target, "eng", "lint")
	if got := readFileT(t, filepath.Join(root, "ARTIFACT.md")); got != skillFM {
		t.Errorf("ARTIFACT.md = %q", got)
	}
	if got := readFileT(t, filepath.Join(root, "SKILL.md")); got != skillMD {
		t.Errorf("SKILL.md = %q", got)
	}
}

// spec: §7.2 — large resources travel as presigned URLs the server-source
// sync fetches before writing the package to disk.
func TestRun_ServerSource_FetchesLargeResources(t *testing.T) {
	t.Parallel()
	srv := newStubRegistry(t, map[string]stubArtifact{
		"team/glossary": {
			typ:           "context",
			layer:         "local",
			frontmatter:   contextArtifactSrc,
			manifestBody:  "body",
			largeResource: map[string]string{"data/big.bin": "BIGDATA"},
		},
	})
	target := t.TempDir()
	if _, err := Run(Options{RegistryPath: srv.URL, Target: target, AdapterID: "none", HTTPClient: srv.Client()}); err != nil {
		t.Fatalf("server-source Run: %v", err)
	}
	if got := readFileT(t, filepath.Join(target, "team", "glossary", "data", "big.bin")); got != "BIGDATA" {
		t.Errorf("large resource = %q, want BIGDATA", got)
	}
}

// spec: §6.10 — a server error surfaces as a Run error rather than a partial
// or silent materialization.
func TestRun_ServerSource_PropagatesError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "registry.unavailable", "message": "down"})
	}))
	t.Cleanup(srv.Close)
	_, err := Run(Options{RegistryPath: srv.URL, Target: t.TempDir(), AdapterID: "none", HTTPClient: srv.Client()})
	if err == nil {
		t.Fatal("expected an error from an unavailable server source")
	}
}

// spec: §7.5.1, §9.4 (F-9.4.3) — programmatic curation against a server
// source: the include scope narrows the effective view fetched over HTTP, so
// only the curated ids are materialized. This is the server-source half of the
// §9.4 "search then podium sync --include" workflow.
func TestRun_ServerSource_ScopeIncludeNarrows(t *testing.T) {
	t.Parallel()
	srv := newStubRegistry(t, map[string]stubArtifact{
		"finance/invoice": {typ: "context", layer: "team-shared", frontmatter: contextArtifactSrc, manifestBody: "Invoice body.\n"},
		"personal/greet":  {typ: "context", layer: "local", frontmatter: contextArtifactSrc, manifestBody: "Greet body.\n"},
	})
	target := t.TempDir()
	res, err := Run(Options{
		RegistryPath: srv.URL,
		Target:       target,
		AdapterID:    "none",
		HTTPClient:   srv.Client(),
		Scope:        ScopeFilter{Include: []string{"finance/**"}},
	})
	if err != nil {
		t.Fatalf("server-source scoped Run: %v", err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].ID != "finance/invoice" {
		t.Fatalf("scoped server-source Result.Artifacts = %+v, want only finance/invoice", res.Artifacts)
	}
	if _, err := os.Stat(filepath.Join(target, "finance", "invoice", "ARTIFACT.md")); err != nil {
		t.Errorf("finance/invoice not materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "personal", "greet", "ARTIFACT.md")); !os.IsNotExist(err) {
		t.Errorf("personal/greet must be excluded by the include scope, stat err = %v", err)
	}
}

// spec: §7.5.2 — a filesystem path (and a file:// URI) is a filesystem
// source; the dispatch must route it to filesystem composition rather than
// the server path.
func TestRun_FilesystemSourceStillMaterializes(t *testing.T) {
	t.Parallel()
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team/glossary/ARTIFACT.md", Content: contextArtifactSrc},
	)
	if _, err := Run(Options{RegistryPath: registry, Target: t.TempDir(), AdapterID: "none"}); err != nil {
		t.Fatalf("filesystem Run: %v", err)
	}
}

// spec: §7.5.4 (F-2.2.2) — a server-source --watch subscribes to the
// registry change-event stream and reruns the sync on every event, rather
// than polling the filesystem.
func TestWatch_ServerSource_RerunsOnEvent(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync/manifest", func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, map[string]any{"artifacts": []map[string]string{
			{"id": "team/glossary", "type": "context", "version": "1.0.0", "layer": "local"},
		}})
	})
	mux.HandleFunc("/v1/load_artifact", func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, map[string]any{
			"id": "team/glossary", "type": "context", "layer": "local",
			"manifest_body": "body", "frontmatter": contextArtifactSrc,
		})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"event":"artifact.published"}` + "\n"))
		f.Flush()
		<-r.Context().Done() // hold the stream open until the watcher disconnects
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := Watch(ctx, WatchOptions{
		Sync:     Options{RegistryPath: srv.URL, Target: t.TempDir(), AdapterID: "none", HTTPClient: srv.Client()},
		Debounce: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// The initial sync plus the event-triggered rerun must both emit.
	deadline := time.After(8 * time.Second)
	syncs := 0
	for syncs < 2 {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("watch channel closed after %d syncs, want 2", syncs)
			}
			if ev.Err != nil {
				t.Fatalf("watch sync error: %v", ev.Err)
			}
			syncs++
		case <-deadline:
			t.Fatalf("server-source watch produced %d syncs in time, want >= 2", syncs)
		}
	}
	cancel()
}

// isServerSource is the §7.5.2 scheme classifier: only http:// and https://
// route to a server; bare paths and file:// URIs are filesystem sources.
func TestIsServerSource(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"https://podium.acme.com": true,
		"http://localhost:8080":   true,
		"HTTPS://podium.acme.com": false, // strict, lower-case scheme only
		"file:///srv/registry":    false,
		"/srv/registry":           false,
		"./.podium/registry":      false,
		"registry":                false,
		"":                        false,
	}
	for in, want := range cases {
		if got := isServerSource(in); got != want {
			t.Errorf("isServerSource(%q) = %v, want %v", in, got, want)
		}
	}
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
