package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// staleCacheLoad caches team/x 1.0.0 as the `latest` resolution, writes
// etagFile as its kept entity tag, and loads `latest` from a registry that
// now resolves 2.0.0. The registry answers 304 to `*`, to its current tag,
// and, when lax304 is set, to any If-None-Match. It returns the load result
// and the If-None-Match values the registry received.
func staleCacheLoad(t *testing.T, etagFile string, lax304 bool) (map[string]any, []string) {
	t.Helper()
	const fm1 = "---\ntype: agent\nversion: 1.0.0\n---\n"
	h1 := "sha256:" + version.CanonicalContentHash([]byte(fm1), nil, nil)
	const fm2 = "---\ntype: agent\nversion: 2.0.0\n---\n"
	h2 := "sha256:" + version.CanonicalContentHash([]byte(fm2), nil, nil)
	fresh := loadArtifactJSON(t, map[string]any{"id": "team/x", "type": "agent", "version": "2.0.0",
		"content_hash": h2, "manifest_body": "fresh-body", "frontmatter": fm2})
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("X-Podium-Content-Hash", h2)
			w.WriteHeader(http.StatusOK)
			return
		}
		inm := r.Header.Get("If-None-Match")
		seen = append(seen, inm)
		cur := `"` + h2 + `"`
		w.Header().Set("ETag", cur)
		if strings.TrimSpace(inm) == "*" || inm == cur || lax304 && inm != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fresh))
	}))
	t.Cleanup(ts.Close)
	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	resolutions := newResolutionCache(dir)
	t.Cleanup(func() { _ = resolutions.Close() })
	resolutions.PutVersion("team/x", "", h1, time.Now().Add(-time.Hour))
	srv := &mcpServer{
		cfg:         &config{cacheDir: dir, cacheMode: "always-revalidate", registry: ts.URL, harness: "none", verifyPolicy: sign.PolicyNever},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
	if err := srv.cacheVerifiedRecord(sealDelivery(loadArtifactResponse{ID: "team/x", Type: "agent", Version: "1.0.0",
		ContentHash: h1, Frontmatter: fm1, ManifestBody: "stale-body"})); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, sanitizeHash(h1), "delivery", deliverySegment("team/x"), "etag")
	if err := os.WriteFile(p, []byte(etagFile), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := srv.loadArtifact(map[string]any{"id": "team/x"}).(map[string]any)
	return out, seen
}

// Spec: §7.2 — a kept entity tag that is not in a form the registry writes
// for the cached content hash, such as a planted `*` or a tag with a line
// break, is replaced by the content-hash tag, so it cannot pin the bridge to
// a cached version that `latest` no longer resolves to; and a 304 whose
// entity tag names another content hash is a miss answered with a full
// load.
func TestLoadArtifact_KeptEntityTagIsValidated(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file string
		lax        bool
	}{
		{"wildcard", "*", false},
		{"line break", "\"x\"\r\nX-Injected: 1", false},
		{"304 for another hash", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, seen := staleCacheLoad(t, tc.file, tc.lax)
			if out["manifest_body"] != "fresh-body" || out["version"] != "2.0.0" {
				t.Errorf("result %v", out)
			}
			for _, inm := range seen {
				if inm == "*" || strings.ContainsAny(inm, "\r\n") {
					t.Errorf("sent If-None-Match %q", inm)
				}
			}
		})
	}
}

// Spec: §7.2 — the forms of a kept tag the bridge sends back.
func TestTagNamesHash(t *testing.T) {
	t.Parallel()
	h := "sha256:" + strings.Repeat("a", 64)
	for tag, want := range map[string]bool{
		`"` + h + `"`: true,
		`"` + h + `+` + strings.Repeat("0", 32) + `"`: true,
		`"` + h + `+` + strings.Repeat("A", 32) + `"`: false,
		`"` + h + `+` + strings.Repeat("0", 31) + `"`: false,
		`"` + h + `+0123"`: false,
		"*":                false,
		`W/"` + h + `"`:    false,
	} {
		if got := tagNamesHash(tag, h); got != want {
			t.Errorf("tagNamesHash(%q) = %v, want %v", tag, got, want)
		}
	}
	if tagNamesHash(`""`, "") {
		t.Error("an empty hash names a tag")
	}
}

// Spec: §7.2 — the kept tag is written through a temporary file and a
// rename: a missing directory and a target that cannot be replaced fail the
// write and leave no temporary file.
func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "etag")
	if err := writeFileAtomic(p, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "b" {
		t.Errorf("content %q", b)
	}
	if err := writeFileAtomic(filepath.Join(dir, "missing", "etag"), nil); err == nil {
		t.Error("a write into a missing directory passed")
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(blocked, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(blocked, []byte("x")); err == nil {
		t.Error("replacing a non-empty directory passed")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("left %s", e.Name())
		}
	}
}
