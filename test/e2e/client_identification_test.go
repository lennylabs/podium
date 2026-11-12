package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/audit"
)

// clientAuditRecord is the part of one JSON Lines audit record these tests
// read: the event type, the §8.1 caller.client object, and the context map.
type clientAuditRecord struct {
	Type   string `json:"type"`
	Caller struct {
		Client map[string]string `json:"client"`
	} `json:"caller"`
	Context map[string]string `json:"context"`
}

// readClientAuditRecords decodes every record in the JSON Lines audit log at
// path. A missing file reads as no records, so a caller can poll it.
func readClientAuditRecords(t *testing.T, path string) []clientAuditRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []clientAuditRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec clientAuditRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode audit record in %s: %v\nline: %s", path, err, line)
		}
		out = append(out, rec)
	}
	return out
}

// recordsOfType returns the records whose type is typ.
func recordsOfType(recs []clientAuditRecord, typ string) []clientAuditRecord {
	var out []clientAuditRecord
	for _, r := range recs {
		if r.Type == typ {
			out = append(out, r)
		}
	}
	return out
}

// awaitClientAuditTypes polls the audit log at path until it holds at least
// one record of each type in types, and returns every record it read. The
// registry appends read events after it answers the request, so the file
// can trail the response that caused the event.
func awaitClientAuditTypes(t *testing.T, path string, types ...string) []clientAuditRecord {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		recs := readClientAuditRecords(t, path)
		missing := ""
		for _, typ := range types {
			if len(recordsOfType(recs, typ)) == 0 {
				missing = typ
				break
			}
		}
		if missing == "" {
			return recs
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit log %s has no %s record within the deadline\nlog:\n%s", path, missing, readOrEmpty(path))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// verifyAuditChain runs the §8.6 hash-chain verification over the file at
// path, the check an operator runs on an exported log.
func verifyAuditChain(t *testing.T, path string) {
	t.Helper()
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("open audit log %s: %v", path, err)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Fatalf("audit chain %s failed verification: %v\nlog:\n%s", path, err, readOrEmpty(path))
	}
}

// Spec: §8.1, §8.3, §8.6 — podium-mcp names itself, the MCP host from the
// initialize clientInfo, and the harness in X-Podium-Client. The registry
// records that value as caller.client on artifacts.searched and
// artifact.loaded, the MCP server's local stream records the identical object
// with no context.source key, and both hash chains still verify because the
// client members enter the §8.6 hashed body.
func TestClientIdentification_MCPBothStreams(t *testing.T) {
	t.Parallel()

	reg := writeRegistry(t, map[string]string{
		"finance/ledger/ARTIFACT.md": contextArtifact("quarterly ledger for finance"),
	})
	registryAudit := filepath.Join(t.TempDir(), "registry-audit.log")
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_AUDIT_LOG_PATH=" + registryAudit,
	}, "serve", "--standalone", "--layer-path", reg)

	localAudit := filepath.Join(t.TempDir(), "local-audit.log")
	env := []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_HARNESS=claude-code",
		"PODIUM_AUDIT_SINK=" + localAudit,
		"PODIUM_MATERIALIZE_ROOT=" + t.TempDir(),
		"PODIUM_CACHE_DIR=" + t.TempDir(),
		"PODIUM_VERIFY_SIGNATURES=never",
	}
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cursor", "version": "2.0"},
	}
	res := mcpExec(t, env,
		rpcReq{ID: 1, Method: "initialize", Params: initParams},
		toolCall(2, "search_artifacts", map[string]any{"query": "ledger"}),
		toolCall(3, "load_artifact", map[string]any{"id": "finance/ledger"}),
	)
	cliWantExit(t, res, 0, "podium-mcp initialize, search_artifacts, load_artifact")
	rpcResult(t, res.Stdout, 1)
	rpcResult(t, res.Stdout, 2)
	rpcResult(t, res.Stdout, 3)

	want := map[string]string{
		"kind":         "mcp",
		"name":         "podium-mcp",
		"version":      buildinfo.Version,
		"host":         "cursor",
		"host_version": "2.0",
		"harness":      "claude-code",
	}
	types := []string{string(audit.EventArtifactsSearched), string(audit.EventArtifactLoaded)}

	registryRecs := awaitClientAuditTypes(t, registryAudit, types...)
	localRecs := awaitClientAuditTypes(t, localAudit, types...)
	for _, typ := range types {
		for _, rec := range recordsOfType(registryRecs, typ) {
			if !reflect.DeepEqual(rec.Caller.Client, want) {
				t.Errorf("registry %s caller.client = %v, want %v", typ, rec.Caller.Client, want)
			}
		}
		for _, rec := range recordsOfType(localRecs, typ) {
			if !reflect.DeepEqual(rec.Caller.Client, want) {
				t.Errorf("local %s caller.client = %v, want %v", typ, rec.Caller.Client, want)
			}
		}
	}
	for _, rec := range localRecs {
		if v, ok := rec.Context["source"]; ok {
			t.Errorf("local %s record carries context.source = %q; the key is removed", rec.Type, v)
		}
	}

	verifyAuditChain(t, registryAudit)
	verifyAuditChain(t, localAudit)
}

// Spec: §8.1, §8.3, §8.6 — podium sync names itself with kind=sync and the
// run's harness, and podium search names itself with kind=cli and no harness.
// The registry records each value as caller.client on the read event the
// request raises.
func TestClientIdentification_SyncAndCLI(t *testing.T) {
	t.Parallel()

	reg := writeRegistry(t, map[string]string{
		"ts-style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: TypeScript style rule.\nsensitivity: low\n---\n\nUse tabs.\n",
	})
	registryAudit := filepath.Join(t.TempDir(), "registry-audit.log")
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_AUDIT_LOG_PATH=" + registryAudit,
	}, "serve", "--standalone", "--layer-path", reg)

	sync := runPodium(t, "", append(srvSyncEnv(srv), "HOME="+t.TempDir()),
		"sync", "--registry", srv.BaseURL, "--target", t.TempDir(), "--harness", "claude-code")
	cliWantExit(t, sync, 0, "podium sync")
	search := runPodium(t, "", nil, "search", "--registry", srv.BaseURL, "typescript")
	cliWantExit(t, search, 0, "podium search")

	recs := awaitClientAuditTypes(t, registryAudit,
		string(audit.EventArtifactLoaded), string(audit.EventArtifactsSearched))

	loaded := recordsOfType(recs, string(audit.EventArtifactLoaded))
	if len(loaded) != 1 {
		t.Fatalf("registry recorded %d artifact.loaded events, want 1\nlog:\n%s", len(loaded), readOrEmpty(registryAudit))
	}
	if c := loaded[0].Caller.Client; c["kind"] != "sync" || c["harness"] != "claude-code" {
		t.Errorf("artifact.loaded caller.client = %v, want kind=sync and harness=claude-code", c)
	}

	searched := recordsOfType(recs, string(audit.EventArtifactsSearched))
	if len(searched) != 1 {
		t.Fatalf("registry recorded %d artifacts.searched events, want 1\nlog:\n%s", len(searched), readOrEmpty(registryAudit))
	}
	c := searched[0].Caller.Client
	if c["kind"] != "cli" {
		t.Errorf("artifacts.searched caller.client = %v, want kind=cli", c)
	}
	if h, ok := c["harness"]; ok {
		t.Errorf("artifacts.searched caller.client carries harness %q; podium search sends none", h)
	}
}
