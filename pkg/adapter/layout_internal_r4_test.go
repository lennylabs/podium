package adapter

import (
	"testing"

	"github.com/BurntSushi/toml"
)

// Spec: §4.1 (MCP server names) — the Codex adapter writes a key as a bare
// key only from the safe set and quotes every other key, and a basic string
// escapes every control character, so a manifest string cannot close a
// table header or open another table.
func TestTOMLKeyAndBasicEncoding(t *testing.T) {
	t.Parallel()
	if got := tomlKey("db-1_X"); got != "db-1_X" {
		t.Fatalf("bare = %q", got)
	}
	for _, k := range []string{`"db"`, "a.b", "zz]\n[mcp_servers.db.env]", "", "x\x00y\x7f"} {
		var doc map[string]any
		if _, err := toml.Decode("[t."+tomlKey(k)+"]\nv = "+tomlBasic(k)+"\n", &doc); err != nil {
			t.Fatalf("key %q does not round-trip: %v", k, err)
		}
		tbl := doc["t"].(map[string]any)
		if len(tbl) != 1 || tbl[k].(map[string]any)["v"] != k {
			t.Fatalf("key %q decoded as %v", k, doc)
		}
	}
	if got := yamlDoubleQuote("a\"\nb"); got != `"a\"\nb"` {
		t.Fatalf("yaml = %q", got)
	}
}
