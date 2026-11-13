package e2e

import (
	"encoding/json"
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
// each catalogue event (here domain.loaded) to that receiver as NDJSON
// batches rather than writing a local file. The receiver is an in-test
// recorder.
//
// Spec: §8.1 — the read carries X-Podium-Client, so the forwarded
// domain.loaded record's caller.client records the client the request named.
func TestAuditSink_RegistryEndpointRedirect(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var received []string
	var contentTypes []string
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(body))
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
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

	// A read emits domain.loaded, which the registry forwards to the receiver.
	getWithClient(t, srv.BaseURL+"/v1/load_domain?path=finance", "kind=cli&name=podium")

	deadline := time.Now().Add(10 * time.Second)
	forwarded := ""
	for time.Now().Before(deadline) && forwarded == "" {
		mu.Lock()
		forwarded = findRecordLine(received, "domain.loaded")
		mu.Unlock()
		if forwarded == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	mu.Lock()
	bodies := strings.Join(received, "")
	types := append([]string(nil), contentTypes...)
	mu.Unlock()
	if forwarded == "" {
		t.Fatalf("receiver did not get a forwarded domain.loaded record; got:\n%s", bodies)
	}
	for i, ct := range types {
		if ct != "application/x-ndjson" {
			t.Errorf("request %d Content-Type = %q, want application/x-ndjson", i, ct)
		}
	}

	// A batch can carry the standalone boot's records beside domain.loaded,
	// so the record is found by line, and its caller.client records the kind
	// the request named.
	if kind := lineClientKind(t, forwarded); kind != "cli" {
		t.Errorf("forwarded domain.loaded caller.client.kind = %q, want cli\nline: %s", kind, forwarded)
	}

	// The redirect replaces the file sink: no local audit.log is written at
	// the default ~/.podium/audit.log location.
	if _, err := os.Stat(filepath.Join(home, ".podium", "audit.log")); err == nil {
		t.Errorf("redirected registry wrote a local audit.log under %s/.podium", home)
	}
}

// findRecordLine splits each NDJSON body on newlines and returns the first
// record line whose type is typ, or "".
func findRecordLine(bodies []string, typ string) string {
	for _, b := range bodies {
		for _, line := range strings.Split(b, "\n") {
			var ev struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == typ {
				return line
			}
		}
	}
	return ""
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

// lineClientKind decodes one NDJSON record line and returns its
// caller.client.kind.
func lineClientKind(t *testing.T, line string) string {
	t.Helper()
	var ev struct {
		Caller struct {
			Client struct {
				Kind string `json:"kind"`
			} `json:"client"`
		} `json:"caller"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("decode forwarded record: %v\nline: %s", err, line)
	}
	return ev.Caller.Client.Kind
}
