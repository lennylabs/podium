package e2e

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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

// Spec: §8.1 — with tracing off, every local audit event of podium-mcp still
// carries a trace ID, each meta-tool call gets its own, and the registry
// record of the same call carries the same ID, so the two streams share trace
// IDs whether or not spans are exported.
func TestMCPAudit_StreamsShareTraceIDWithTracingOff(t *testing.T) {
	t.Parallel()

	reg := writeRegistry(t, map[string]string{
		"finance/ledger/ARTIFACT.md": agentArtifact("quarterly ledger for finance"),
	})
	registryAudit := filepath.Join(t.TempDir(), "registry-audit.log")
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_AUDIT_LOG_PATH=" + registryAudit,
	}, "serve", "--standalone", "--layer-path", reg)

	localAudit := filepath.Join(t.TempDir(), "local-audit.log")
	env := []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_AUDIT_SINK=" + localAudit,
		"PODIUM_MATERIALIZE_ROOT=" + t.TempDir(),
		"PODIUM_CACHE_DIR=" + t.TempDir(),
		"PODIUM_VERIFY_SIGNATURES=never",
		"PODIUM_TRACING=",
		"OTEL_EXPORTER_OTLP_ENDPOINT=",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=",
	}
	res := mcpExec(t, env,
		toolCall(1, "search_artifacts", map[string]any{"query": "ledger"}),
		toolCall(2, "load_artifact", map[string]any{"id": "finance/ledger"}),
	)
	cliWantExit(t, res, 0, "podium-mcp search_artifacts, load_artifact")
	rpcResult(t, res.Stdout, 1)
	rpcResult(t, res.Stdout, 2)

	types := []string{string(audit.EventArtifactsSearched), string(audit.EventArtifactLoaded)}
	awaitClientAuditTypes(t, registryAudit, types...)
	awaitClientAuditTypes(t, localAudit, types...)
	seen := map[string]string{}
	for _, typ := range types {
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
