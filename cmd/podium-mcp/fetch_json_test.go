package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// fetchJSON forwards the request, attaches Authorization + Tenant
// headers, and skips the "harness" key when building the query. It also
// names podium-mcp in X-Podium-Client: with no initialize the header carries
// no host members, the harness member takes the per-call argument only on
// /v1/load_artifact, and the always-revalidate HEAD carries the same value as
// the GET it revalidates.
//
// Spec: §7.2.2, §8.1, §8.3
func TestFetchJSON_AttachesAuthAndTenant(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var gotAuth, gotTenant string
	var gotQuery string
	clients := map[string]string{} // "<method> <path>" -> X-Podium-Client
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		clients[r.Method+" "+r.URL.Path] = r.Header.Get(audit.ClientHeader)
		if r.URL.Path == "/v1/x" {
			gotAuth = r.Header.Get("Authorization")
			gotTenant = r.Header.Get("X-Podium-Tenant")
			gotQuery = r.URL.RawQuery
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		sessionToken: "abc",
		tenantID:     "tenant-1",
		harness:      "claude-code",
		verifyPolicy: sign.PolicyNever,
	})
	if _, err := s.fetchJSON("/v1/x", map[string]any{
		"q": "search-me", "harness": "claude-code",
	}); err != nil {
		t.Fatalf("fetchJSON: %v", err)
	}
	cursorArgs := map[string]any{"id": "team/x", "harness": "cursor"}
	for _, path := range []string{"/v1/load_artifact", "/v1/search_artifacts"} {
		if _, err := s.fetchJSON(path, cursorArgs); err != nil {
			t.Fatalf("fetchJSON %s: %v", path, err)
		}
	}
	if _, err := s.headContentHash("/v1/load_artifact", cursorArgs); err != nil {
		t.Fatalf("headContentHash: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer abc" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotTenant != "tenant-1" {
		t.Errorf("X-Podium-Tenant = %q", gotTenant)
	}
	if strings.Contains(gotQuery, "harness") {
		t.Errorf("query forwards harness: %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "q=search-me") {
		t.Errorf("query missing q: %q", gotQuery)
	}
	for key, wantHarness := range map[string]string{
		"GET /v1/x":                "claude-code",
		"GET /v1/load_artifact":    "cursor",
		"GET /v1/search_artifacts": "claude-code",
		"HEAD /v1/load_artifact":   "cursor",
	} {
		assertClientMembers(t, key, clients[key], mcpClientMembers("", "", wantHarness))
	}
	if clients["HEAD /v1/load_artifact"] != clients["GET /v1/load_artifact"] {
		t.Errorf("HEAD header %q differs from GET header %q",
			clients["HEAD /v1/load_artifact"], clients["GET /v1/load_artifact"])
	}
}

// fetchJSON surfaces non-2xx as an error containing the status code.
func TestFetchJSON_Non2xxReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"upstream down"}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{registry: srv.URL, verifyPolicy: sign.PolicyNever})
	_, err := s.fetchJSON("/v1/x", nil)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v", err)
	}
}

// fetchJSON returns a wrapped error when the URL is invalid.
func TestFetchJSON_BadURLErrors(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{registry: "ht!@#$tp://", verifyPolicy: sign.PolicyNever})
	if _, err := s.fetchJSON("/v1/x", nil); err == nil {
		t.Errorf("expected URL parse error")
	}
}

// currentToken reads PODIUM_SESSION_TOKEN_FILE first, falling back to env.
func TestCurrentToken_FileSourceWins(t *testing.T) {
	dir := t.TempDir()
	tokFile := dir + "/tok.txt"
	if err := writeStr(tokFile, "from-file\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &mcpServer{cfg: &config{
		sessionToken:     "from-cfg",
		sessionTokenFile: tokFile,
	}}
	t.Setenv("PODIUM_SESSION_TOKEN", "from-env")
	if got := s.currentToken(); got != "from-file" {
		t.Errorf("got %q, want from-file", got)
	}
}

// currentToken returns empty when nothing is set.
func TestCurrentToken_NothingConfigured(t *testing.T) {
	s := &mcpServer{cfg: &config{}}
	t.Setenv("PODIUM_SESSION_TOKEN_ENV", "")
	t.Setenv("PODIUM_SESSION_TOKEN", "")
	if got := s.currentToken(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func writeStr(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
