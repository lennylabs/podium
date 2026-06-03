package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// spec: §8.2 (F-8.2.2) — "the MCP server applies the same directives before
// writing to its local audit sink." A load_artifact for an artifact whose
// manifest names a sensitive frontmatter field in audit_redact must record the
// artifact.loaded event in the MCP local sink with that field masked, and the
// raw value must never appear. Driven end-to-end through the real podium-mcp
// binary against a standalone registry, reading the local JSON-Lines sink.
func TestMCP_LocalAuditAppliesManifestRedaction(t *testing.T) {
	t.Parallel()

	const secret = "AC-7777-6666"
	reg := writeRegistry(t, map[string]string{
		"finance/payroll/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Payroll record.\nsensitivity: low\nbank_account: \"" + secret + "\"\naudit_redact:\n  - bank_account\n---\n\nbody\n",
	})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()}, "serve", "--standalone", "--layer-path", reg)

	auditPath := filepath.Join(t.TempDir(), "local-audit.log")
	env := []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_HARNESS=none",
		"PODIUM_AUDIT_SINK=" + auditPath,
		"PODIUM_VERIFY_SIGNATURES=never",
	}
	res := mcpExec(t, env, toolCall(1, "load_artifact", map[string]any{"id": "finance/payroll"}))
	cliWantExit(t, res, 0, "mcp load_artifact")

	loaded := pollAuditLine(t, auditPath, "artifact.loaded", 5*time.Second)
	if loaded == "" {
		t.Fatalf("MCP local sink missing artifact.loaded:\n%s", readOrEmpty(auditPath))
	}
	all := readOrEmpty(auditPath)
	if strings.Contains(all, secret) {
		t.Errorf("raw bank_account value %q leaked into the MCP local sink:\n%s", secret, all)
	}
	if !strings.Contains(loaded, "[redacted]") {
		t.Errorf("MCP local artifact.loaded did not redact bank_account:\n%s", loaded)
	}
}

// pollAuditLine returns the first JSON-Lines audit record containing substr,
// polling up to the deadline. Empty result means none appeared in time.
func pollAuditLine(t *testing.T, path, substr string, within time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line != "" && strings.Contains(line, substr) {
					return line
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}
