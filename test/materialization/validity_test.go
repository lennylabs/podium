package materialization

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// TestValidity_HarnessOutput materializes the canonical artifact set through
// every adapter and checks that each produced file is not merely the expected
// bytes (the golden suite covers that) but actually parses and satisfies the
// schema the target harness consumes: JSON config files parse and carry the
// right top-level keys, TOML files parse and carry the right tables, SKILL.md
// frontmatter is valid YAML with name and description (the agentskills.io
// shape), Cursor .mdc frontmatter is valid YAML with only the native keys, and
// AGENTS.md / GEMINI.md inject markers are balanced. This guards "the output
// follows the format each harness expects" beyond byte-for-byte stability.
func TestValidity_HarnessOutput(t *testing.T) {
	for _, h := range harnesses {
		h := h
		t.Run(h, func(t *testing.T) {
			dir, _ := materializeCanonical(t, h)
			err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				rel, rerr := filepath.Rel(dir, p)
				if rerr != nil {
					return rerr
				}
				content := mustRead(t, p)
				validateMaterializedFile(t, h, filepath.ToSlash(rel), content)
				return nil
			})
			if err != nil {
				t.Fatalf("walk: %v", err)
			}
		})
	}
}

// validateMaterializedFile dispatches a file to the validator for its target
// format, keyed by the harness-native filename and location.
func validateMaterializedFile(t *testing.T, harness, rel string, content []byte) {
	t.Helper()
	base := path.Base(rel)
	ext := path.Ext(rel)
	switch {
	case base == "marketplace.json":
		requireMarketplaceJSON(t, rel, content)
	case base == "plugin.json":
		requireJSONStringField(t, rel, content, "name")
	case base == ".mcp.json" || base == "mcp.json":
		requireMCPServersJSON(t, rel, content)
	case base == "opencode.json":
		requireOpenCodeMCPJSON(t, rel, content)
	case base == "settings.json":
		requireSettingsJSON(t, rel, content)
	case base == "hooks.json" && harness == "cursor":
		requireCursorHooksJSON(t, rel, content)
	case base == "hooks.json":
		requireClaudeStyleHooksJSON(t, rel, content)
	case base == "config.toml":
		requireCodexConfigTOML(t, rel, content)
	case ext == ".toml" && strings.Contains(rel, "/agents/"):
		requireCodexAgentTOML(t, rel, content)
	case ext == ".toml" && strings.Contains(rel, "/commands/"):
		requireGeminiCommandTOML(t, rel, content)
	case base == "SKILL.md":
		requireSkillMD(t, rel, content)
	case ext == ".mdc":
		requireCursorMDC(t, rel, content)
	case base == "AGENTS.md" || base == "GEMINI.md":
		requireBalancedInjectMarkers(t, rel, content)
	case ext == ".md":
		requireValidFrontmatterIfPresent(t, rel, content)
	default:
		// Bundled resources (scripts, assets) are opaque bytes; nothing to check.
	}
}

// ---- JSON validators --------------------------------------------------------

func parseJSONObject(t *testing.T, rel string, content []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(content, &v); err != nil {
		t.Fatalf("%s: invalid JSON: %v\n%s", rel, err, content)
	}
	return v
}

func requireJSONStringField(t *testing.T, rel string, content []byte, key string) {
	t.Helper()
	obj := parseJSONObject(t, rel, content)
	if s, ok := obj[key].(string); !ok || s == "" {
		t.Errorf("%s: missing non-empty string %q: %v", rel, key, obj)
	}
}

// requireMCPServersJSON checks the {"mcpServers": {name: {command|url}}} shape
// used by .mcp.json (claude-code), .cursor/mcp.json, and the cowork per-plugin
// .mcp.json.
func requireMCPServersJSON(t *testing.T, rel string, content []byte) {
	t.Helper()
	obj := parseJSONObject(t, rel, content)
	servers, ok := obj["mcpServers"].(map[string]any)
	if !ok || len(servers) == 0 {
		t.Fatalf("%s: missing non-empty mcpServers object: %v", rel, obj)
	}
	for name, raw := range servers {
		requireServerEntry(t, rel+" mcpServers."+name, raw)
	}
}

// requireServerEntry checks an MCP launch entry carries a command (string) or a
// url (string).
func requireServerEntry(t *testing.T, where string, raw any) {
	t.Helper()
	entry, ok := raw.(map[string]any)
	if !ok {
		t.Errorf("%s: not an object: %v", where, raw)
		return
	}
	_, hasCmd := entry["command"].(string)
	_, hasURL := entry["url"].(string)
	if !hasCmd && !hasURL {
		t.Errorf("%s: entry has neither a string command nor url: %v", where, entry)
	}
}

// requireOpenCodeMCPJSON checks the {"mcp": {name: {type, enabled, command[]|url}}}
// shape OpenCode consumes.
func requireOpenCodeMCPJSON(t *testing.T, rel string, content []byte) {
	t.Helper()
	obj := parseJSONObject(t, rel, content)
	mcp, ok := obj["mcp"].(map[string]any)
	if !ok || len(mcp) == 0 {
		t.Fatalf("%s: missing non-empty mcp object: %v", rel, obj)
	}
	for name, raw := range mcp {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Errorf("%s mcp.%s: not an object", rel, name)
			continue
		}
		typ, _ := entry["type"].(string)
		switch typ {
		case "local":
			if cmd, ok := entry["command"].([]any); !ok || len(cmd) == 0 {
				t.Errorf("%s mcp.%s: local entry missing non-empty command array", rel, name)
			}
		case "remote":
			if _, ok := entry["url"].(string); !ok {
				t.Errorf("%s mcp.%s: remote entry missing string url", rel, name)
			}
		default:
			t.Errorf("%s mcp.%s: type must be local or remote, got %q", rel, name, typ)
		}
	}
}

// requireSettingsJSON validates a harness settings.json that may carry hooks
// (claude-style) and/or mcpServers (gemini settings.json holds both).
func requireSettingsJSON(t *testing.T, rel string, content []byte) {
	t.Helper()
	obj := parseJSONObject(t, rel, content)
	if _, ok := obj["hooks"]; ok {
		requireClaudeStyleHooks(t, rel, obj)
	}
	if _, ok := obj["mcpServers"]; ok {
		servers, ok := obj["mcpServers"].(map[string]any)
		if !ok {
			t.Errorf("%s: mcpServers is not an object", rel)
		}
		for name, raw := range servers {
			requireServerEntry(t, rel+" mcpServers."+name, raw)
		}
	}
}

// requireClaudeStyleHooksJSON validates the nested
// {"hooks": {Event: [{"hooks": [{type, command}]}]}} structure (claude-code,
// codex, claude-cowork).
func requireClaudeStyleHooksJSON(t *testing.T, rel string, content []byte) {
	t.Helper()
	requireClaudeStyleHooks(t, rel, parseJSONObject(t, rel, content))
}

func requireClaudeStyleHooks(t *testing.T, rel string, obj map[string]any) {
	t.Helper()
	hooks, ok := obj["hooks"].(map[string]any)
	if !ok || len(hooks) == 0 {
		t.Fatalf("%s: missing non-empty hooks object: %v", rel, obj)
	}
	for event, raw := range hooks {
		arr, ok := raw.([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("%s hooks.%s: not a non-empty array", rel, event)
			continue
		}
		for _, e := range arr {
			entry, _ := e.(map[string]any)
			inner, ok := entry["hooks"].([]any)
			if !ok || len(inner) == 0 {
				t.Errorf("%s hooks.%s: entry missing nested hooks array", rel, event)
				continue
			}
			for _, hi := range inner {
				h, _ := hi.(map[string]any)
				if h["type"] != "command" {
					t.Errorf("%s hooks.%s: handler type != command: %v", rel, event, h)
				}
				if cmd, ok := h["command"].(string); !ok || cmd == "" {
					t.Errorf("%s hooks.%s: handler missing string command", rel, event)
				}
			}
		}
	}
}

// requireCursorHooksJSON validates Cursor's flat
// {"version": N, "hooks": {event: [{command}]}} schema.
func requireCursorHooksJSON(t *testing.T, rel string, content []byte) {
	t.Helper()
	obj := parseJSONObject(t, rel, content)
	if _, ok := obj["version"].(float64); !ok {
		t.Errorf("%s: cursor hooks.json missing numeric version: %v", rel, obj["version"])
	}
	hooks, ok := obj["hooks"].(map[string]any)
	if !ok || len(hooks) == 0 {
		t.Fatalf("%s: missing non-empty hooks object", rel)
	}
	for event, raw := range hooks {
		arr, ok := raw.([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("%s hooks.%s: not a non-empty array", rel, event)
			continue
		}
		for _, e := range arr {
			entry, _ := e.(map[string]any)
			if cmd, ok := entry["command"].(string); !ok || cmd == "" {
				t.Errorf("%s hooks.%s: entry missing string command: %v", rel, event, entry)
			}
		}
	}
}

func requireMarketplaceJSON(t *testing.T, rel string, content []byte) {
	t.Helper()
	obj := parseJSONObject(t, rel, content)
	if s, ok := obj["name"].(string); !ok || s == "" {
		t.Errorf("%s: marketplace missing non-empty name", rel)
	}
	plugins, ok := obj["plugins"].([]any)
	if !ok || len(plugins) == 0 {
		t.Fatalf("%s: marketplace missing non-empty plugins array", rel)
	}
	for i, raw := range plugins {
		entry, _ := raw.(map[string]any)
		if s, ok := entry["name"].(string); !ok || s == "" {
			t.Errorf("%s plugins[%d]: missing non-empty name", rel, i)
		}
		if s, ok := entry["source"].(string); !ok || s == "" {
			t.Errorf("%s plugins[%d]: missing non-empty source", rel, i)
		}
	}
}

// ---- TOML validators --------------------------------------------------------

func parseTOML(t *testing.T, rel string, content []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := toml.Unmarshal(content, &v); err != nil {
		t.Fatalf("%s: invalid TOML: %v\n%s", rel, err, content)
	}
	return v
}

// requireCodexConfigTOML checks the [mcp_servers.<name>] tables Codex reads.
func requireCodexConfigTOML(t *testing.T, rel string, content []byte) {
	t.Helper()
	v := parseTOML(t, rel, content)
	servers, ok := v["mcp_servers"].(map[string]any)
	if !ok || len(servers) == 0 {
		t.Fatalf("%s: missing non-empty [mcp_servers] table: %v", rel, v)
	}
	for name, raw := range servers {
		entry, _ := raw.(map[string]any)
		_, hasCmd := entry["command"].(string)
		_, hasURL := entry["url"].(string)
		if !hasCmd && !hasURL {
			t.Errorf("%s mcp_servers.%s: neither command nor url", rel, name)
		}
	}
}

func requireCodexAgentTOML(t *testing.T, rel string, content []byte) {
	t.Helper()
	v := parseTOML(t, rel, content)
	for _, key := range []string{"name", "description", "developer_instructions"} {
		if s, ok := v[key].(string); !ok || s == "" {
			t.Errorf("%s: codex agent TOML missing non-empty %q", rel, key)
		}
	}
}

func requireGeminiCommandTOML(t *testing.T, rel string, content []byte) {
	t.Helper()
	v := parseTOML(t, rel, content)
	for _, key := range []string{"description", "prompt"} {
		if s, ok := v[key].(string); !ok || s == "" {
			t.Errorf("%s: gemini command TOML missing non-empty %q", rel, key)
		}
	}
}

// ---- Markdown / frontmatter validators --------------------------------------

// frontmatter returns the YAML frontmatter block (between the leading `---` and
// the next `---` line) and whether a well-formed block was found. An empty
// block (---\n---) returns ("", true).
func frontmatter(content []byte) (string, bool) {
	s := string(content)
	if !strings.HasPrefix(s, "---\n") {
		return "", false
	}
	rest := s[len("---\n"):]
	// The closing fence is a line that is exactly "---".
	for _, fence := range []string{"\n---\n", "\n---"} {
		if i := strings.Index(rest, fence); i >= 0 {
			return rest[:i], true
		}
	}
	if strings.HasPrefix(rest, "---") { // immediate empty block
		return "", true
	}
	return "", false
}

func parseYAML(t *testing.T, rel, block string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(block), &m); err != nil {
		t.Fatalf("%s: invalid YAML frontmatter: %v\n---\n%s\n---", rel, err, block)
	}
	return m
}

// requireSkillMD checks the agentskills.io SKILL.md shape: valid YAML
// frontmatter with non-empty name and description, and none of the
// Podium-internal artifact fields (type, version) leaking through.
func requireSkillMD(t *testing.T, rel string, content []byte) {
	t.Helper()
	block, ok := frontmatter(content)
	if !ok {
		t.Fatalf("%s: SKILL.md has no YAML frontmatter", rel)
	}
	m := parseYAML(t, rel, block)
	if s, ok := m["name"].(string); !ok || s == "" {
		t.Errorf("%s: SKILL.md missing non-empty name", rel)
	}
	if s, ok := m["description"].(string); !ok || s == "" {
		t.Errorf("%s: SKILL.md missing non-empty description", rel)
	}
	for _, leaked := range []string{"type", "version"} {
		if _, ok := m[leaked]; ok {
			t.Errorf("%s: SKILL.md leaks Podium artifact field %q into skill frontmatter", rel, leaked)
		}
	}
}

// requireCursorMDC checks a Cursor .mdc: a YAML frontmatter block that parses,
// with keys limited to the Cursor-native set and correct value types.
func requireCursorMDC(t *testing.T, rel string, content []byte) {
	t.Helper()
	block, ok := frontmatter(content)
	if !ok {
		t.Fatalf("%s: .mdc has no frontmatter fence", rel)
	}
	m := parseYAML(t, rel, block)
	for k, v := range m {
		switch k {
		case "alwaysApply":
			if _, ok := v.(bool); !ok {
				t.Errorf("%s: alwaysApply must be a bool, got %T", rel, v)
			}
		case "globs", "description":
			if _, ok := v.(string); !ok {
				t.Errorf("%s: %s must be a string, got %T", rel, k, v)
			}
		default:
			t.Errorf("%s: unexpected Cursor .mdc frontmatter key %q", rel, k)
		}
	}
}

// requireBalancedInjectMarkers checks every podium:begin:<key> in an AGENTS.md /
// GEMINI.md inject file has a matching podium:end:<key>.
func requireBalancedInjectMarkers(t *testing.T, rel string, content []byte) {
	t.Helper()
	s := string(content)
	begins := markerKeys(s, "podium:begin:")
	ends := markerKeys(s, "podium:end:")
	if len(begins) == 0 {
		t.Errorf("%s: inject file carries no podium markers", rel)
	}
	for _, k := range begins {
		if !contains(ends, k) {
			t.Errorf("%s: begin marker %q has no matching end", rel, k)
		}
	}
	if len(begins) != len(ends) {
		t.Errorf("%s: unbalanced markers: %d begins, %d ends", rel, len(begins), len(ends))
	}
}

func markerKeys(s, prefix string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, prefix); i >= 0 {
			rest := line[i+len(prefix):]
			rest = strings.TrimSuffix(strings.TrimSpace(rest), "-->")
			out = append(out, strings.TrimSpace(rest))
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// requireValidFrontmatterIfPresent checks any other .md (pass-through agents,
// commands, claude rules, context) that opens with a frontmatter fence parses
// as YAML.
func requireValidFrontmatterIfPresent(t *testing.T, rel string, content []byte) {
	t.Helper()
	if block, ok := frontmatter(content); ok {
		parseYAML(t, rel, block)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}
