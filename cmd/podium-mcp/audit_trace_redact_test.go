package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// ctxWithTraceID returns a context carrying a recording-equivalent span
// context with the given 32-hex trace id, so the local-audit trace-id
// stamping (§8.1) can be exercised without standing up a real exporter.
func ctxWithTraceID(t *testing.T, hexID string) context.Context {
	t.Helper()
	tid, err := oteltrace.TraceIDFromHex(hexID)
	if err != nil {
		t.Fatalf("TraceIDFromHex(%q): %v", hexID, err)
	}
	sid, err := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex: %v", err)
	}
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: oteltrace.FlagsSampled,
	})
	return oteltrace.ContextWithSpanContext(context.Background(), sc)
}

// Spec: §8.1 — the MCP local audit event carries the in-flight
// call's trace id so the registry and local streams share one id per call.
// auditMeta (load_domain / load_artifact target events) stamps it.
func TestMCPAuditMeta_StampsTraceID(t *testing.T) {
	t.Parallel()
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{cfg: &config{}, audit: sink, sessionID: "sess-1"}
	s.activeCtx = ctxWithTraceID(t, traceID)

	s.auditMeta(audit.EventDomainLoaded, "finance/close")

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, `"trace_id":"`+traceID+`"`) {
		t.Errorf("local audit event missing shared trace id %q:\n%s", traceID, got)
	}
}

// Spec: §8.1 — the search meta-tool local event also carries the
// trace id, alongside the §8.2 query scrub.
func TestMCPAuditSearch_StampsTraceID(t *testing.T) {
	t.Parallel()
	const traceID = "0af7651916cd43dd8448eb211c80319c"
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{cfg: &config{}, audit: sink, sessionID: "sess-2", scrubber: audit.NewPIIScrubber()}
	s.activeCtx = ctxWithTraceID(t, traceID)

	s.auditSearch(audit.EventArtifactsSearched, "quarterly forecast")

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, `"trace_id":"`+traceID+`"`) {
		t.Errorf("search local audit event missing trace id %q:\n%s", traceID, got)
	}
}

// Spec: §8.1 — with no active span (tracing off) no trace id is
// written, so the local stream does not emit a placeholder id that would
// not match the registry's freshly minted one.
func TestMCPAudit_NoTraceIDWhenTracingOff(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{cfg: &config{}, audit: sink, sessionID: "sess-3"} // no activeCtx
	s.auditMeta(audit.EventDomainLoaded, "finance/close")

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "trace_id") {
		t.Errorf("expected no trace_id when tracing is off:\n%s", data)
	}
}

// Spec: §8.2 — the MCP server applies the manifest audit_redact
// directive before writing the artifact.loaded event to its local sink: the
// named sensitive frontmatter field is surfaced and masked, never leaking
// its raw value.
func TestMCPAuditLoadArtifact_RedactsManifestFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{cfg: &config{}, audit: sink, sessionID: "sess-4"}
	frontmatter := "---\n" +
		"name: payroll\n" +
		"type: skill\n" +
		"version: 1.0.0\n" +
		"audit_redact: [bank_account, ssn]\n" +
		"bank_account: \"12345678\"\n" +
		"ssn: \"078-05-1120\"\n" +
		"---\n"

	s.auditLoadArtifact("finance/payroll", frontmatter)

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "artifact.loaded") {
		t.Fatalf("missing artifact.loaded event:\n%s", got)
	}
	for _, leaked := range []string{"12345678", "078-05-1120"} {
		if strings.Contains(got, leaked) {
			t.Errorf("audit_redact directive failed: leaked %q:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "[redacted]") {
		t.Errorf("expected [redacted] placeholder for masked fields:\n%s", got)
	}
}

// Spec: §8.2 — an artifact with no audit_redact directive writes
// only the structural source key; the directive is inert by design, not by
// omission.
func TestMCPAuditLoadArtifact_NoDirectiveNoRedaction(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{cfg: &config{}, audit: sink, sessionID: "sess-5"}
	frontmatter := "---\nname: notes\ntype: skill\nversion: 1.0.0\n---\n"

	s.auditLoadArtifact("team/notes", frontmatter)

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "artifact.loaded") || !strings.Contains(got, "team/notes") {
		t.Fatalf("missing artifact.loaded for team/notes:\n%s", got)
	}
	if strings.Contains(got, "[redacted]") {
		t.Errorf("no directive should produce no redaction:\n%s", got)
	}
}
