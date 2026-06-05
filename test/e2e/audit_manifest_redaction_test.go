package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// spec: §8.2 — a manifest that names a sensitive frontmatter field
// (bank_account) in audit_redact has that field surfaced into the audit context
// and masked to [redacted] before any event reaches a sink. The standalone
// registry forwards each catalogue event to an in-test SIEM recorder; the
// artifact.loaded event carries bank_account as [redacted], and the raw value
// never appears anywhere in the audit stream (covering the boot publish event
// and the read event alike).
func TestAudit_ManifestDeclaredRedaction(t *testing.T) {
	t.Parallel()

	const secret = "AC-9999-8888"
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

	reg := writeRegistry(t, map[string]string{
		"finance/payroll/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Payroll record.\nsensitivity: low\nbank_account: \"" + secret + "\"\naudit_redact:\n  - bank_account\n---\n\nbody\n",
	})
	home := t.TempDir()
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_AUDIT_LOG_PATH=" + recorder.URL + "/sink",
	}, "serve", "--standalone", "--layer-path", reg)

	// A read emits artifact.loaded, forwarded to the recorder.
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/payroll", nil)

	deadline := time.Now().Add(5 * time.Second)
	var loaded string
	for time.Now().Before(deadline) && loaded == "" {
		mu.Lock()
		for _, b := range received {
			if strings.Contains(b, "artifact.loaded") && strings.Contains(b, "bank_account") {
				loaded = b
				break
			}
		}
		mu.Unlock()
		if loaded == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}

	mu.Lock()
	all := strings.Join(received, "\n")
	mu.Unlock()

	if loaded == "" {
		t.Fatalf("no artifact.loaded event carrying bank_account; got:\n%s", all)
	}
	if !strings.Contains(loaded, "[redacted]") {
		t.Errorf("artifact.loaded event did not redact bank_account:\n%s", loaded)
	}
	// The raw value must not leak through any forwarded event (publish or read).
	if strings.Contains(all, secret) {
		t.Errorf("raw bank_account value %q leaked into the audit stream:\n%s", secret, all)
	}
}
