package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §8.3 — "Both the registry and local sinks can be
// redirected to external SIEM / log aggregation independently." When
// PODIUM_AUDIT_LOG_PATH is an http(s) URL the standalone registry forwards
// each catalogue event (here domain.loaded) to that endpoint as JSON rather
// than writing a local file. The endpoint is an in-test recorder.
//
// Spec: §8.1 — the read carries X-Podium-Client, so the forwarded event is one
// JSON object whose caller.client records the client the request named.
func TestAuditSink_RegistryEndpointRedirect(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var received []string
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recorder.Close)

	reg := writeRegistry(t, map[string]string{"finance/x/ARTIFACT.md": contextArtifact("x")})
	home := t.TempDir()
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_AUDIT_LOG_PATH=" + recorder.URL + "/sink",
	}, "serve", "--standalone", "--layer-path", reg)

	// A read emits domain.loaded, which the registry forwards to the endpoint.
	getWithClient(t, srv.BaseURL+"/v1/load_domain?path=finance", "kind=cli&name=podium")

	deadline := time.Now().Add(5 * time.Second)
	forwarded := ""
	for time.Now().Before(deadline) && forwarded == "" {
		mu.Lock()
		for _, b := range received {
			if strings.Contains(b, "domain.loaded") {
				forwarded = b
				break
			}
		}
		mu.Unlock()
		if forwarded == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if forwarded == "" {
		mu.Lock()
		got := strings.Join(received, "\n")
		mu.Unlock()
		t.Fatalf("SIEM endpoint did not receive a forwarded domain.loaded event; got:\n%s", got)
	}

	// The endpoint sink sends one event per POST, and its caller.client
	// records the kind the request named.
	if kind := singleObjectClientKind(t, forwarded); kind != "cli" {
		t.Errorf("forwarded domain.loaded caller.client.kind = %q, want cli\nbody: %s", kind, forwarded)
	}

	// The redirect replaces the file sink: no local audit.log is written at
	// the default ~/.podium/audit.log location.
	if _, err := os.Stat(filepath.Join(home, ".podium", "audit.log")); err == nil {
		t.Errorf("redirected registry wrote a local audit.log under %s/.podium", home)
	}
}

// getWithClient sends a GET to url carrying the given X-Podium-Client value
// and fails the test unless the registry answers 200.
func getWithClient(t *testing.T, url, client string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request %s: %v", url, err)
	}
	req.Header.Set(audit.ClientHeader, client)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = HTTP %d, want 200\nbody: %s", url, resp.StatusCode, body)
	}
}

// singleObjectClientKind decodes body as exactly one JSON object and returns
// its caller.client.kind. A second JSON value after the object fails the
// test.
func singleObjectClientKind(t *testing.T, body string) string {
	t.Helper()
	var ev struct {
		Caller struct {
			Client struct {
				Kind string `json:"kind"`
			} `json:"client"`
		} `json:"caller"`
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(body)))
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("decode forwarded event: %v\nbody: %s", err, body)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("forwarded body holds more than one JSON value (err=%v)\nbody: %s", err, body)
	}
	return ev.Caller.Client.Kind
}
