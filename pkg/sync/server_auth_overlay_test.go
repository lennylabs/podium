package sync

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// spec: §6.3.2, §14.11 (F-14.11.5) — a server-source sync attaches the caller
// credential as Authorization: Bearer on every registry API request so CI's
// runtime-issued JWT authenticates the materialization. A filesystem source
// has no request and ignores the token.
func TestRun_ServerSource_ForwardsBearerToken(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync/manifest", func(w http.ResponseWriter, r *http.Request) {
		seen["manifest"] = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artifacts":[{"id":"team/glossary","type":"context","version":"1.0.0","layer":"org"}]}`))
	})
	mux.HandleFunc("/v1/load_artifact", func(w http.ResponseWriter, r *http.Request) {
		seen["load"] = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"team/glossary","type":"context","layer":"org","frontmatter":` + jsonQuote(contextArtifactSrc) + `}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := Run(Options{
		RegistryPath: srv.URL,
		Target:       t.TempDir(),
		AdapterID:    "none",
		HTTPClient:   srv.Client(),
		Token:        "jwt-xyz",
	}); err != nil {
		t.Fatalf("server-source Run: %v", err)
	}
	for _, ep := range []string{"manifest", "load"} {
		if seen[ep] != "Bearer jwt-xyz" {
			t.Errorf("%s Authorization = %q, want %q", ep, seen[ep], "Bearer jwt-xyz")
		}
	}
}

// spec: §14.11 (F-14.11.5) — an empty Token reaches the registry anonymously;
// no Authorization header is sent.
func TestRun_ServerSource_NoTokenSendsNoAuth(t *testing.T) {
	t.Parallel()
	var gotAuth string
	var present bool
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync/manifest", func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header["Authorization"]
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artifacts":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := Run(Options{RegistryPath: srv.URL, Target: t.TempDir(), AdapterID: "none", HTTPClient: srv.Client()}); err != nil {
		t.Fatalf("server-source Run: %v", err)
	}
	if present || gotAuth != "" {
		t.Errorf("anonymous sync sent Authorization=%q (present=%v), want none", gotAuth, present)
	}
}

// spec: §6.4 (F-14.6.2) — the workspace overlay merges as the highest-precedence
// layer for a server source too. The consumer merges it client-side because the
// developer's overlay directory is local and the server cannot see it.
func TestRun_ServerSource_OverlayOverridesServer(t *testing.T) {
	t.Parallel()
	srv := newStubRegistry(t, map[string]stubArtifact{
		"finance/intro": {
			typ:          "context",
			layer:        "org",
			frontmatter:  "---\ntype: context\nversion: 1.0.0\ndescription: from server\nsensitivity: low\n---\n\nfrom server\n",
			manifestBody: "from server\n",
		},
	})

	// Stage a workspace overlay at the same canonical ID with a different body.
	overlayDir := filepath.Join(t.TempDir(), "overlay")
	if err := os.MkdirAll(filepath.Join(overlayDir, "finance", "intro"), 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	overlayBody := "---\ntype: context\nversion: 1.0.0\ndescription: from overlay\nsensitivity: low\n---\n\nfrom overlay\n"
	if err := os.WriteFile(filepath.Join(overlayDir, "finance", "intro", "ARTIFACT.md"), []byte(overlayBody), 0o644); err != nil {
		t.Fatalf("write overlay artifact: %v", err)
	}

	target := t.TempDir()
	if _, err := Run(Options{
		RegistryPath: srv.URL,
		Target:       target,
		AdapterID:    "none",
		HTTPClient:   srv.Client(),
		OverlayPath:  overlayDir,
	}); err != nil {
		t.Fatalf("server-source Run with overlay: %v", err)
	}
	got := readFileT(t, filepath.Join(target, "finance", "intro", "ARTIFACT.md"))
	if got != overlayBody {
		t.Errorf("ARTIFACT.md = %q, want the overlay body (overlay must win over the server)", got)
	}
}

// jsonQuote renders s as a JSON string literal for inline test fixtures.
func jsonQuote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, string(r)...)
		}
	}
	out = append(out, '"')
	return string(out)
}
