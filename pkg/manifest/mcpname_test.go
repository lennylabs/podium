package manifest

import (
	"strings"
	"testing"
)

// Spec: §4.1 (MCP server names) — a declared name is 1 to 64 ASCII letters,
// digits, '_', or '-', and only a declared name of an mcp-server is refused.
func TestValidMCPServerName(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"db", "Finance-DB_2", strings.Repeat("a", 64)} {
		if !ValidMCPServerName(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", 65), `"db"`, "db.env", "a b", "db\n", "café", "a/b", "zz]\n[mcp_servers.db.env]"} {
		if ValidMCPServerName(bad) {
			t.Errorf("%q admitted", bad)
		}
	}
	if !MCPServerNameRefused(&Artifact{Type: TypeMCPServer, Name: "a.b"}) || MCPServerNameRefused(&Artifact{Type: TypeMCPServer}) ||
		MCPServerNameRefused(&Artifact{Type: TypeAgent, Name: "a.b"}) || MCPServerNameRefused(nil) {
		t.Fatal("MCPServerNameRefused misclassifies")
	}
}
