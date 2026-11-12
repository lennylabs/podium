package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/audit"
)

// spec: §6.3.2, §14.11 — a server-source sync attaches the caller
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
		writeJSONTest(w, testharness.SealDelivery(map[string]any{
			"id": "team/glossary", "type": "context", "layer": "org", "frontmatter": contextArtifactSrc,
		}))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := Run(Options{
		RegistryPath: srv.URL,
		Delivery:     neverDelivery,
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

// Spec: §7.2.2, §7.5 — a server-source sync names itself on every
// control-plane request: X-Podium-Client carries kind sync, name podium, the
// build version, and the run's harness. A run with no AdapterID reports the
// none harness Run defaults to.
func TestRun_ServerSource_SendsClientHeader(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, adapterID, wantHarness string
	}{
		{name: "named harness", adapterID: "claude-code", wantHarness: "claude-code"},
		{name: "no harness", adapterID: "", wantHarness: "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()}, nil)
			if _, err := Run(Options{
				RegistryPath: srv.URL,
				Delivery:     neverDelivery,
				Target:       t.TempDir(),
				AdapterID:    tc.adapterID,
				HTTPClient:   srv.Client(),
			}); err != nil {
				t.Fatalf("server-source Run: %v", err)
			}
			assertSyncClientHeaders(t, counts.seen(), tc.wantHarness)
		})
	}
}

// Spec: §7.2.2, §7.5.5 — ResolveEffectiveView, the read behind the
// podium sync override checklist, forwards the harness it is given on the
// manifest request and on every load_artifact.
func TestResolveEffectiveView_ServerSourceSendsHarness(t *testing.T) {
	t.Parallel()
	arts := map[string]stubArtifact{"team/glossary": glossaryStub(), "team/onboarding": glossaryStub()}
	srv, counts := newCountedStubRegistry(t, arts, nil)
	view, err := ResolveEffectiveView(Options{
		RegistryPath: srv.URL,
		Delivery:     neverDelivery,
		Target:       t.TempDir(),
		AdapterID:    "claude-code",
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("ResolveEffectiveView: %v", err)
	}
	if len(view) != len(arts) {
		t.Fatalf("view has %d rows, want %d", len(view), len(arts))
	}
	reqs := counts.seen()
	assertSyncClientHeaders(t, reqs, "claude-code")
	if got := countPath(reqs, "/v1/load_artifact"); got != len(arts) {
		t.Errorf("load_artifact requests = %d, want %d", got, len(arts))
	}
}

// Spec: §7.2.2, §7.8 — a marketplace render reads the view through
// FetchRecords with no AdapterID, because it renders several harnesses from
// one read, so its header carries no harness member.
func TestRender_ServerSourceSendsClientHeaderWithoutHarness(t *testing.T) {
	t.Parallel()
	srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()}, nil)
	opts := RenderOptions{
		OutputID:   "acme-agents",
		Registry:   srv.URL,
		Workdir:    t.TempDir(),
		Harnesses:  []string{"claude-code"},
		Plugins:    financePlugins(),
		HTTPClient: srv.Client(),
		Delivery:   neverDelivery,
	}
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("Render: %v", err)
	}
	reqs := counts.seen()
	assertSyncClientHeaders(t, reqs, "")
	for _, req := range reqs {
		q, err := url.ParseQuery(req.client)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", req.client, err)
		}
		if _, ok := q["harness"]; ok {
			t.Errorf("%s header %q carries a harness member, want none", req.path, req.client)
		}
	}
}

// assertSyncClientHeaders checks that reqs include the manifest and at least
// one load_artifact request, and that every /v1/ request carries the
// X-Podium-Client value podium sync sends with wantHarness. Parsing compares
// members, because url.Values.Encode orders them by key.
func assertSyncClientHeaders(t *testing.T, reqs []stubRequest, wantHarness string) {
	t.Helper()
	for _, path := range []string{"/v1/sync/manifest", "/v1/load_artifact"} {
		if countPath(reqs, path) == 0 {
			t.Errorf("no %s request recorded", path)
		}
	}
	want := audit.CallerClient{Kind: "sync", Name: "podium", Version: buildinfo.Version, Harness: wantHarness}
	for _, req := range reqs {
		if !strings.HasPrefix(req.path, "/v1/") {
			continue
		}
		got := audit.ParseClientHeader(req.client)
		if got == nil || *got != want {
			t.Errorf("%s X-Podium-Client %q parsed to %+v, want %+v", req.path, req.client, got, want)
		}
	}
}

// countPath counts the recorded requests to path.
func countPath(reqs []stubRequest, path string) int {
	n := 0
	for _, req := range reqs {
		if req.path == path {
			n++
		}
	}
	return n
}

// spec: §14.11 — an empty Token reaches the registry anonymously;
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

	if _, err := Run(Options{RegistryPath: srv.URL, Delivery: neverDelivery, Target: t.TempDir(), AdapterID: "none", HTTPClient: srv.Client()}); err != nil {
		t.Fatalf("server-source Run: %v", err)
	}
	if present || gotAuth != "" {
		t.Errorf("anonymous sync sent Authorization=%q (present=%v), want none", gotAuth, present)
	}
}

// Spec: §6.4 — the workspace overlay merges as the highest-precedence
// layer for a server source too. The consumer merges it client-side because the
// developer's overlay directory is local and the server cannot see it. The
// overlay is the §6.4 exception to the §4.6 collision rule.
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
		Delivery:     neverDelivery,
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
