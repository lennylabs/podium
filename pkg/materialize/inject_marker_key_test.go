package materialize

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// hostileID is an artifact ID whose last segment, a directory name, holds line
// breaks: written into a TOML marker comment as it is, the second and third
// lines would be a table and a key of .codex/config.toml.
const hostileID = "ops/x\n[mcp_servers.db.env]\nNODE_OPTIONS = \"--require /tmp/evil.js\""

// operatorConfig is a .codex/config.toml that the operator wrote, holding the
// server the hostile ID targets.
const operatorConfig = "[mcp_servers.db]\ncommand = \"db\"\n"

// Spec: §6.7 (Encoding) — an inject marker names an ID that holds a control
// character in the quoted form, so the ID adds no line to the file: the
// config decodes with the operator's db server unchanged. Re-injecting
// replaces the block in place, a keep that names the ID keeps it, and
// stripping every block restores the operator's bytes.
func TestInjectBlock_QuotedMarkerKey(t *testing.T) {
	t.Parallel()
	cs := commentStyleFor(".codex/config.toml")
	block := []byte("[mcp_servers.x]\ncommand = \"x\"\n")
	out := injectBlock([]byte(operatorConfig), hostileID, block, cs)
	var doc map[string]map[string]map[string]any
	if _, err := toml.Decode(string(out), &doc); err != nil {
		t.Fatalf("config does not decode: %v\n%s", err, out)
	}
	if want := map[string]any{"command": "db"}; !reflect.DeepEqual(doc["mcp_servers"]["db"], want) {
		t.Errorf("db = %v, want %v\n%s", doc["mcp_servers"]["db"], want, out)
	}
	if !strings.Contains(string(out), `# podium:begin-q:"ops/x\n[mcp_servers.db.env]\nNODE_OPTIONS = \"--require /tmp/evil.js\""`) {
		t.Errorf("begin marker not in the quoted form:\n%s", out)
	}
	if again := injectBlock(out, hostileID, block, cs); string(again) != string(out) {
		t.Errorf("re-inject changed the file:\n%s\nwant:\n%s", again, out)
	}
	if kept := stripPodiumBlocks(out, cs, func(id string) bool { return id == hostileID }); string(kept) != string(out) {
		t.Errorf("keep of the ID removed its block:\n%s", kept)
	}
	if stripped := stripPodiumBlocks(out, cs, nil); string(stripped) != operatorConfig {
		t.Errorf("strip = %q, want the operator's bytes", stripped)
	}
}

// Spec: §6.7 (Encoding) — a plain marker that names a key holding a control
// character, as an earlier release wrote a tab into one, is read as naming
// that key, so the next inject replaces it with the quoted form rather than
// adding a second block. A quoted marker whose string does not decode is no
// marker, and an ID without a control character keeps the plain form.
func TestInjectBlock_LegacyAndMalformedMarkers(t *testing.T) {
	t.Parallel()
	cs := commentStyleFor("AGENTS.md")
	legacy := "<!-- podium:begin:a\tb -->\nold\n<!-- podium:end:a\tb -->\n"
	out := string(injectBlock([]byte(legacy), "a\tb", []byte("new\n"), cs))
	want := "<!-- podium:begin-q:\"a\\tb\" -->\nnew\n<!-- podium:end-q:\"a\\tb\" -->\n"
	if out != want {
		t.Errorf("inject over a legacy marker = %q, want %q", out, want)
	}
	if _, ok := markerKey(`<!-- podium:begin-q:"unterminated -->`, true, cs); ok {
		t.Error("an undecodable quoted marker was read as a marker")
	}
	if _, ok := markerKey(`<!-- podium:begin-q:"" -->`, true, cs); ok {
		t.Error("a quoted marker with an empty key was read as a marker")
	}
	if got := markerName("begin", "team/a.b"); got != "podium:begin:team/a.b" {
		t.Errorf("markerName = %q, want the plain form", got)
	}
}
