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

// spec: §8.2, §4.6 — a child that declares neither the sensitive frontmatter
// field nor the audit_redact directive inherits both from its parent, and the
// inherited field is masked to [redacted] before any event reaches a sink. The
// child is loaded through /v1/load_artifact against a standalone registry
// forwarding its catalogue events to an in-test SIEM recorder, so the assertion
// is on the same served surface TestAudit_ManifestDeclaredRedaction uses.
//
// The case reaches the sink only once the merged frontmatter preserves keys the
// manifest.Artifact struct does not declare: x_bank_account is a parent-declared
// extension field, and the closed round-trip dropped it from the served block
// before the load-path repair.
func TestAudit_ExtendsInheritedRedaction(t *testing.T) {
	t.Parallel()

	const secret = "GB29-NWBK-6016-1331-9268-19"
	const childID = "finance/inherited-payroll"
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

	// Two layers, so the parent is ingested and stored before the child that
	// pins it. A single-layer fixture depends on the walk order of one ingest
	// pass, where a parent that sorts after its child is not yet stored.
	reg := writeRegistry(t, map[string]string{
		".registry-config": "multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-foo\n",
		"org-defaults/shared/payroll-base/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\n" +
			"description: Payroll base.\nsensitivity: low\n" +
			"x_bank_account: \"" + secret + "\"\naudit_redact:\n  - x_bank_account\n---\n\nparent body\n",
		"team-foo/" + childID + "/ARTIFACT.md": "---\ntype: context\nversion: 2.0.0\n" +
			"description: Inherited payroll record.\nsensitivity: low\n" +
			"extends: shared/payroll-base@1.x\n---\n\nchild body\n",
	})
	home := t.TempDir()
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_AUDIT_LOG_PATH=" + recorder.URL + "/sink",
	}, "serve", "--standalone", "--layer-path", reg)

	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+childID, nil)

	// Wait for the read event of this fixture's own artifact: the boot publish
	// events and the parent's own events also carry the directive.
	deadline := time.Now().Add(5 * time.Second)
	var loaded string
	for time.Now().Before(deadline) && loaded == "" {
		mu.Lock()
		for _, b := range received {
			if strings.Contains(b, "artifact.loaded") && strings.Contains(b, childID) {
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
		t.Fatalf("no artifact.loaded event for %s; got:\n%s", childID, all)
	}
	if !strings.Contains(loaded, "x_bank_account") {
		t.Fatalf("artifact.loaded event does not carry the inherited x_bank_account field:\n%s", loaded)
	}
	if !strings.Contains(loaded, "[redacted]") {
		t.Errorf("artifact.loaded event did not redact the inherited x_bank_account:\n%s", loaded)
	}
	if strings.Contains(all, secret) {
		t.Errorf("raw x_bank_account value %q leaked into the audit stream:\n%s", secret, all)
	}
}
