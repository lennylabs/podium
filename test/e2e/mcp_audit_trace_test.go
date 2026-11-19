package e2e

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// traceIDPattern is the §8.1 trace ID: 32 lowercase hexadecimal characters.
var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// auditTraceIDs returns the trace_id of every record of type typ in the JSON
// Lines audit log at path, in file order.
func auditTraceIDs(t *testing.T, path, typ string) []string {
	t.Helper()
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(readOrEmpty(path)), "\n") {
		var rec struct {
			Type    string `json:"type"`
			TraceID string `json:"trace_id"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Type == typ {
			ids = append(ids, rec.TraceID)
		}
	}
	return ids
}

// traceAuditTypes are the read events a search_artifacts call and a
// load_artifact call raise in both audit streams.
var traceAuditTypes = []string{string(audit.EventArtifactsSearched), string(audit.EventArtifactLoaded)}

// runTracingOffBridge starts a registry with registryEnv, drives a podium-mcp
// with trace export off through one search_artifacts call and one
// load_artifact call, and returns the registry and the paths of the
// registry and local audit logs once both hold every traceAuditTypes record.
func runTracingOffBridge(t *testing.T, registryEnv ...string) (srv *serverProc, registryAudit, localAudit string) {
	t.Helper()
	reg := writeRegistry(t, map[string]string{
		"finance/ledger/ARTIFACT.md": agentArtifact("quarterly ledger for finance"),
	})
	registryAudit = filepath.Join(t.TempDir(), "registry-audit.log")
	srv = startServerArgs(t, append([]string{
		"HOME=" + t.TempDir(),
		"PODIUM_AUDIT_LOG_PATH=" + registryAudit,
	}, registryEnv...), "serve", "--standalone", "--layer-path", reg)

	localAudit = filepath.Join(t.TempDir(), "local-audit.log")
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_AUDIT_SINK=" + localAudit,
		"PODIUM_MATERIALIZE_ROOT=" + t.TempDir(),
		"PODIUM_CACHE_DIR=" + t.TempDir(),
		"PODIUM_VERIFY_SIGNATURES=never",
		"PODIUM_TRACING=",
		"OTEL_EXPORTER_OTLP_ENDPOINT=",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=",
	},
		toolCall(1, "search_artifacts", map[string]any{"query": "ledger"}),
		toolCall(2, "load_artifact", map[string]any{"id": "finance/ledger"}),
	)
	cliWantExit(t, res, 0, "podium-mcp search_artifacts, load_artifact")
	rpcResult(t, res.Stdout, 1)
	rpcResult(t, res.Stdout, 2)
	awaitClientAuditTypes(t, registryAudit, traceAuditTypes...)
	awaitClientAuditTypes(t, localAudit, traceAuditTypes...)
	return srv, registryAudit, localAudit
}

// assertStreamsShareTraceIDs fails the test unless each traceAuditTypes
// record appears once in each stream with one valid trace ID shared by both
// streams and distinct from the other call's.
func assertStreamsShareTraceIDs(t *testing.T, registryAudit, localAudit string) {
	t.Helper()
	seen := map[string]string{}
	for _, typ := range traceAuditTypes {
		local, remote := auditTraceIDs(t, localAudit, typ), auditTraceIDs(t, registryAudit, typ)
		if len(local) != 1 || len(remote) != 1 {
			t.Fatalf("%s: local trace IDs %q, registry trace IDs %q, want one each", typ, local, remote)
		}
		if !traceIDPattern.MatchString(local[0]) {
			t.Errorf("%s: local trace_id = %q, want 32 hexadecimal characters", typ, local[0])
		}
		if local[0] != remote[0] {
			t.Errorf("%s: local trace_id %q differs from the registry's %q", typ, local[0], remote[0])
		}
		if prev, dup := seen[local[0]]; dup {
			t.Errorf("%s reuses the trace ID of %s; each call gets its own", typ, prev)
		}
		seen[local[0]] = typ
	}
}

// Spec: §8.1 — with tracing off, every local audit event of podium-mcp still
// carries a trace ID, each meta-tool call gets its own, and the registry
// record of the same call carries the same ID, so the two streams share trace
// IDs whether or not spans are exported.
func TestMCPAudit_StreamsShareTraceIDWithTracingOff(t *testing.T) {
	t.Parallel()
	_, registryAudit, localAudit := runTracingOffBridge(t)
	assertStreamsShareTraceIDs(t, registryAudit, localAudit)
}

// Spec: §8.1, §13.8 — a registry with trace export on still records and
// exports its server spans for the calls of a podium-mcp whose export is
// off. The bridge carries its generated trace ID in X-Podium-Trace-Id and
// sends no traceparent, so the registry's parent-based sampler sees no
// not-sampled parent, and the audit streams still share the ID.
func TestMCPAudit_TracingRegistrySamplesTracingOffBridgeCalls(t *testing.T) {
	t.Parallel()
	srv, registryAudit, localAudit := runTracingOffBridge(t,
		"PODIUM_TRACING=stdout",
		"OTEL_BSP_SCHEDULE_DELAY=50",
	)
	assertStreamsShareTraceIDs(t, registryAudit, localAudit)
	for _, op := range []string{"search_artifacts", "load_artifact"} {
		span := regexp.MustCompile(`"Name":\s*"` + op + `"`)
		deadline := time.Now().Add(10 * time.Second)
		for !span.MatchString(readOrEmpty(srv.logPath)) {
			if time.Now().After(deadline) {
				t.Fatalf("the registry exported no %s span for the bridge call\nlog:\n%s", op, readOrEmpty(srv.logPath))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}
