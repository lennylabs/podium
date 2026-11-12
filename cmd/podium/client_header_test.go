package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	gosync "sync"
	"testing"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/audit"
)

// cliClientServer is a registry stub that records the path and X-Podium-Client
// header of every request. It answers /healthz and /v1/scope/preview with
// valid bodies and every other path with an empty JSON object.
type cliClientServer struct {
	srv *httptest.Server

	// mu guards reqs, which the handler goroutines append to while the test
	// reads it.
	mu   gosync.Mutex
	reqs []recordedRequest
}

func newCLIClientServer(t *testing.T) *cliClientServer {
	t.Helper()
	s := &cliClientServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, recordedRequest{path: r.URL.Path, client: r.Header.Get(audit.ClientHeader)})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"mode":"ready","ready":true}`))
		case "/v1/scope/preview":
			_, _ = w.Write([]byte(`{"layers":["alice-personal"],"artifact_count":1,"by_type":{"skill":1},"by_sensitivity":{"low":1}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// clientOn returns the X-Podium-Client value of the single request recorded
// for path, failing the test when the path was not requested exactly once.
func (s *cliClientServer) clientOn(t *testing.T, path string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var found []string
	for _, r := range s.reqs {
		if r.path == path {
			found = append(found, r.client)
		}
	}
	if len(found) != 1 {
		t.Fatalf("requests to %s = %d, want 1 (recorded %+v)", path, len(found), s.reqs)
	}
	return found[0]
}

// isolateClientResolution keeps an ambient sync.yaml, harness, or credential
// out of the run: HOME and the working directory are empty temporary
// directories, PODIUM_HARNESS is empty, and a session token stands in for the
// keychain.
func isolateClientResolution(t *testing.T, fn func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_HARNESS", "")
	t.Setenv("PODIUM_REGISTRY", "")
	t.Setenv("PODIUM_SESSION_TOKEN", "tok")
	withCwd(t, t.TempDir(), fn)
}

// assertClient parses header and compares it to want, member by member.
func assertClient(t *testing.T, where, header string, want audit.CallerClient) {
	t.Helper()
	got := audit.ParseClientHeader(header)
	if got == nil || *got != want {
		t.Errorf("%s: X-Podium-Client %q parsed to %+v, want %+v", where, header, got, want)
	}
}

// cliWant is the value the podium CLI sends: kind cli and no harness.
func cliWant() audit.CallerClient {
	return audit.CallerClient{Kind: "cli", Name: "podium", Version: buildinfo.Version}
}

// Spec: §7.2.2 — the podium CLI request helpers send kind=cli, name podium,
// and the build version, with no harness member.
func TestClientHeader_CLIRequestHelpers(t *testing.T) {
	s := newCLIClientServer(t)
	isolateClientResolution(t, func() {
		_ = mustGetJSON(s.srv.URL, "/v1/search_artifacts", map[string]string{"query": "q"})
		if _, code := doJSON(s.srv.URL+"/v1/layers", http.MethodGet, nil); code != http.StatusOK {
			t.Fatalf("doJSON status = %d, want 200", code)
		}
	})
	assertClient(t, "mustGetJSON", s.clientOn(t, "/v1/search_artifacts"), cliWant())
	assertClient(t, "doJSON", s.clientOn(t, "/v1/layers"), cliWant())
}

// Spec: §7.2.2 — podium status identifies as the CLI on the §3.5 scope
// preview and sends no client value on the /healthz probe.
func TestClientHeader_StatusScopePreview(t *testing.T) {
	s := newCLIClientServer(t)
	isolateClientResolution(t, func() {
		t.Setenv("PODIUM_REGISTRY", s.srv.URL)
		_ = captureStdout(t, func() {
			if rc := statusCmd(nil); rc != 0 {
				t.Errorf("statusCmd rc = %d, want 0", rc)
			}
		})
	})
	assertClient(t, "status scope preview", s.clientOn(t, "/v1/scope/preview"), cliWant())
	if got := s.clientOn(t, "/healthz"); got != "" {
		t.Errorf("/healthz X-Podium-Client = %q, want none", got)
	}
}

// Spec: §7.2.2 — podium sync --preview identifies as podium sync with the
// resolved harness, which falls back to none (§7.5.2) when nothing names one.
func TestClientHeader_SyncPreview(t *testing.T) {
	cases := []struct {
		name        string
		extra       []string
		wantHarness string
	}{
		{name: "harness flag", extra: []string{"--harness", "cursor"}, wantHarness: "cursor"},
		{name: "no harness", wantHarness: "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newCLIClientServer(t)
			args := append([]string{"--preview", "--registry", s.srv.URL}, tc.extra...)
			isolateClientResolution(t, func() {
				_ = captureStdout(t, func() {
					if rc := syncCmd(args); rc != 0 {
						t.Errorf("syncCmd rc = %d, want 0", rc)
					}
				})
			})
			want := audit.CallerClient{Kind: "sync", Name: "podium", Version: buildinfo.Version, Harness: tc.wantHarness}
			assertClient(t, "sync --preview", s.clientOn(t, "/v1/scope/preview"), want)
		})
	}
}

// Spec: §7.2.2 — a presigned manifest link is not a control-plane request,
// so fetchManifestDocument sends no client value.
func TestClientHeader_ManifestLinkSendsNone(t *testing.T) {
	s := newCLIClientServer(t)
	if _, err := fetchManifestDocument(context.Background(), s.srv.URL+"/objects/manifest", "tok"); err != nil {
		t.Fatalf("fetchManifestDocument: %v", err)
	}
	if got := s.clientOn(t, "/objects/manifest"); got != "" {
		t.Errorf("manifest link X-Podium-Client = %q, want none", got)
	}
}
