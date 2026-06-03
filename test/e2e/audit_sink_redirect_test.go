package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Spec: §8.3 (F-8.3.1) — "Both the registry and local sinks can be
// redirected to external SIEM / log aggregation independently." When
// PODIUM_AUDIT_LOG_PATH is an http(s) URL the standalone registry forwards
// each catalogue event (here domain.loaded) to that endpoint as JSON rather
// than writing a local file. The endpoint is an in-test recorder.
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
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", nil)

	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		mu.Lock()
		for _, b := range received {
			if strings.Contains(b, "domain.loaded") {
				found = true
				break
			}
		}
		mu.Unlock()
		if !found {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !found {
		mu.Lock()
		got := strings.Join(received, "\n")
		mu.Unlock()
		t.Fatalf("SIEM endpoint did not receive a forwarded domain.loaded event; got:\n%s", got)
	}

	// The redirect replaces the file sink: no local audit.log is written at
	// the default ~/.podium/audit.log location.
	if _, err := os.Stat(filepath.Join(home, ".podium", "audit.log")); err == nil {
		t.Errorf("redirected registry wrote a local audit.log under %s/.podium", home)
	}
}
