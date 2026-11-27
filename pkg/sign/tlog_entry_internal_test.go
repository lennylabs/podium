package sign

import (
	"errors"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tlog"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// attestationLogEntry returns the parsed log entry of a harness bundle and the
// transparency logs of the harness root.
func attestationLogEntry(t *testing.T, h *sh.Harness, opts ...sh.RekorOpt) (*tlog.Entry, map[string]*root.TransparencyLog) {
	t.Helper()
	stmt := sh.Statement(t, "p", sh.Subject{Name: "a", SHA256: "00"})
	b := new(bundle.Bundle)
	if err := b.UnmarshalJSON(h.DSSEBundle(stmt, sh.WithRekorV1Entry(opts...))); err != nil {
		t.Fatal(err)
	}
	entries, err := b.TlogEntries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries: %v, %d", err, len(entries))
	}
	r, err := ParseAttestationRoot(h.TrustedRootJSON())
	if err != nil {
		t.Fatal(err)
	}
	return entries[0], r.material.RekorLogs()
}

// TestCheckLogEntryForm covers the §4.7.11 log entry forms on entries
// whose proof or log the bundle path cannot leave out: sigstore-go refuses
// a version 0.3 bundle whose entry has no proof before the check runs.
//
// Spec: §4.7.11
func TestCheckLogEntryForm(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	v1, logs := attestationLogEntry(t, h)
	if err := checkLogEntryForm(v1, logs); err != nil {
		t.Fatalf("Rekor v1 entry: %v", err)
	}
	v2, _ := attestationLogEntry(t, h, sh.WithRekorV2Body())
	if err := checkLogEntryForm(v2, logs); err != nil {
		t.Fatalf("Rekor v2 entry: %v", err)
	}
	for name, logs := range map[string]map[string]*root.TransparencyLog{
		"log outside the root": {},
		"log with no baseUrl":  withBaseURL(logs, ""),
		"log with a bad URL":   withBaseURL(logs, "https://[::1"),
	} {
		if err := checkLogEntryForm(v2, logs); !errors.Is(err, errLogEntryForm) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := checkLogKeyWindow(v1, map[string]*root.TransparencyLog{}, nil); !errors.Is(err, errLogEntryForm) {
		t.Errorf("window lookup of a log outside the root: err = %v", err)
	}
	noProof, _ := attestationLogEntry(t, h)
	noProof.TransparencyLogEntry().InclusionProof = nil
	if err := checkLogEntryForm(noProof, logs); !errors.Is(err, errLogEntryForm) {
		t.Errorf("no checkpoint: err = %v", err)
	}
	for name, body := range map[string]string{
		"body that is not JSON": "acme",
		"body with no kind":     `{"apiVersion":"0.0.1"}`,
	} {
		e, _ := attestationLogEntry(t, h)
		e.TransparencyLogEntry().CanonicalizedBody = []byte(body)
		if err := checkLogEntryForm(e, logs); !errors.Is(err, errLogEntryForm) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// withBaseURL returns a copy of logs whose every log has baseURL.
func withBaseURL(logs map[string]*root.TransparencyLog, baseURL string) map[string]*root.TransparencyLog {
	out := map[string]*root.TransparencyLog{}
	for k, v := range logs {
		c := *v
		c.BaseURL = baseURL
		out[k] = &c
	}
	return out
}
