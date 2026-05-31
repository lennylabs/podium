package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
)

// spec: §8.2 — the MCP server scrubs the free-text search query before
// writing the meta-tool event to its local audit sink. F-8.2.4.
func TestMCPAuditSearch_ScrubsQuery(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{cfg: &config{}, audit: sink, sessionID: "sess-1", scrubber: audit.NewPIIScrubber()}
	s.auditSearch(audit.EventArtifactsSearched, "find ssn 123-45-6789 and card 4111111111111111")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, leaked := range []string{"123-45-6789", "4111111111111111"} {
		if strings.Contains(got, leaked) {
			t.Errorf("MCP local audit leaked PII %q:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "artifacts.searched") {
		t.Errorf("missing event type:\n%s", got)
	}
}

// spec: §8.2 — callTool routes search_artifacts / search_domains through
// the scrubbing path before dispatching, so the query is redacted even
// when the downstream registry is unreachable. F-8.2.4.
func TestMCPCallTool_SearchQueryScrubbed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{
		cfg:       &config{registry: "http://127.0.0.1:1"},
		http:      &http.Client{},
		audit:     sink,
		scrubber:  audit.NewPIIScrubber(),
		sessionID: "sess-2",
	}
	_ = s.callTool([]byte(`{"name":"search_domains","arguments":{"query":"email bob@acme.com"}}`))

	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Contains(got, "bob@acme.com") {
		t.Errorf("search query PII leaked to local audit:\n%s", got)
	}
	if !strings.Contains(got, "[email-redacted]") {
		t.Errorf("missing email redaction placeholder:\n%s", got)
	}
}

// spec: §8.2 — a disabled scrubber (PODIUM_PII_REDACTION=false) writes the
// query unredacted; the toggle is honored.
func TestMCPAuditSearch_DisabledWritesRaw(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{cfg: &config{}, audit: sink, sessionID: "sess-3"} // scrubber nil
	s.auditSearch(audit.EventDomainsSearched, "ssn 123-45-6789")
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "123-45-6789") {
		t.Errorf("disabled scrub should write the raw query:\n%s", data)
	}
}

// spec: §8.2 — PODIUM_PII_REDACTION controls whether newServer builds a
// scrubber. Default-on; "false" disables.
func TestMCPNewServer_ScrubberToggle(t *testing.T) {
	t.Run("default on", func(t *testing.T) {
		c := &config{cacheDir: t.TempDir()}
		srv, err := newServer(c)
		if err != nil {
			t.Fatal(err)
		}
		if srv.scrubber == nil {
			t.Errorf("default-on config built no scrubber")
		}
	})
	t.Run("disabled", func(t *testing.T) {
		dis := false
		c := &config{cacheDir: t.TempDir(), piiRedaction: &dis}
		srv, err := newServer(c)
		if err != nil {
			t.Fatal(err)
		}
		if srv.scrubber != nil {
			t.Errorf("disabled config built a scrubber")
		}
	})
}

func TestPIIDisabledValue(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"false", "0", "no", "off", "FALSE", " off "} {
		if !piiDisabledValue(v) {
			t.Errorf("piiDisabledValue(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"true", "1", "yes", "", "on"} {
		if piiDisabledValue(v) {
			t.Errorf("piiDisabledValue(%q) = true, want false", v)
		}
	}
}
