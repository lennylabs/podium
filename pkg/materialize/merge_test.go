package materialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

func TestInjectBlock_AppendReplaceIdempotent(t *testing.T) {
	md := commentStyleFor("AGENTS.md")
	// Append into a file with pre-existing user content.
	base := []byte("# House rules\n\nKeep PRs small.\n")
	out := injectBlock(base, "team/style", []byte("Use tabs."), md)
	s := string(out)
	if !strings.Contains(s, "Keep PRs small.") {
		t.Errorf("user content lost:\n%s", s)
	}
	if !strings.Contains(s, "<!-- podium:begin:team/style -->") || !strings.Contains(s, "<!-- podium:end:team/style -->") {
		t.Errorf("markers missing:\n%s", s)
	}
	if !strings.Contains(s, "Use tabs.") {
		t.Errorf("block content missing:\n%s", s)
	}

	// Re-injecting the same key replaces the block in place (idempotent count).
	out2 := injectBlock(out, "team/style", []byte("Use spaces."), md)
	s2 := string(out2)
	if strings.Count(s2, "podium:begin:team/style") != 1 {
		t.Errorf("re-inject duplicated the block:\n%s", s2)
	}
	if strings.Contains(s2, "Use tabs.") || !strings.Contains(s2, "Use spaces.") {
		t.Errorf("re-inject did not replace content:\n%s", s2)
	}

	// A second key appends without disturbing the first.
	out3 := injectBlock(out2, "team/security", []byte("No secrets."), md)
	s3 := string(out3)
	if !strings.Contains(s3, "Use spaces.") || !strings.Contains(s3, "No secrets.") {
		t.Errorf("second key clobbered the first:\n%s", s3)
	}
}

func TestInjectBlock_TOMLMarkers(t *testing.T) {
	toml := commentStyleFor(".codex/config.toml")
	out := injectBlock(nil, "ops/db", []byte("[mcp_servers.db]\ncommand = \"db-mcp\""), toml)
	s := string(out)
	if !strings.Contains(s, "# podium:begin:ops/db") || !strings.Contains(s, "# podium:end:ops/db") {
		t.Errorf("toml comment markers missing:\n%s", s)
	}
	if strings.Contains(s, "<!--") {
		t.Errorf("toml file used markdown markers:\n%s", s)
	}
}

func TestMergeJSON_PreservesOtherKeys(t *testing.T) {
	base := []byte(`{"mcpServers":{"existing":{"command":"x"}},"theme":"dark"}`)
	frag := []byte(`{"mcpServers":{"podium":{"command":"podium-mcp"}}}`)
	out, err := mergeJSON(base, frag)
	if err != nil {
		t.Fatalf("mergeJSON: %v", err)
	}
	s := string(out)
	for _, want := range []string{`"existing"`, `"podium"`, `"theme"`, `"dark"`} {
		if !strings.Contains(s, want) {
			t.Errorf("merged JSON missing %s:\n%s", want, s)
		}
	}
	// Re-merging is stable.
	out2, err := mergeJSON(out, frag)
	if err != nil {
		t.Fatalf("re-merge: %v", err)
	}
	if string(out2) != string(out) {
		t.Errorf("re-merge not idempotent:\n%s\n---\n%s", out, out2)
	}
}

// Write end-to-end: two rules inject into one AGENTS.md, two mcp-servers merge
// into one .mcp.json, plus a plain standalone file, and a pre-existing file is
// preserved.
func TestWrite_InjectAndMergeIntegration(t *testing.T) {
	dest := t.TempDir()
	// Pre-existing user config the merge must preserve.
	if err := os.WriteFile(filepath.Join(dest, ".mcp.json"), []byte(`{"mcpServers":{"user":{"command":"u"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []adapter.File{
		{Path: "skills/x/SKILL.md", Content: []byte("skill body")},
		{Path: "AGENTS.md", Op: adapter.OpInject, Key: "a/rule-one", Content: []byte("Rule one.")},
		{Path: "AGENTS.md", Op: adapter.OpInject, Key: "a/rule-two", Content: []byte("Rule two.")},
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: []byte(`{"mcpServers":{"one":{"command":"a"}}}`)},
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: []byte(`{"mcpServers":{"two":{"command":"b"}}}`)},
	}
	if err := Write(dest, files); err != nil {
		t.Fatalf("Write: %v", err)
	}

	agents := readString(t, filepath.Join(dest, "AGENTS.md"))
	for _, want := range []string{"Rule one.", "Rule two.", "podium:begin:a/rule-one", "podium:begin:a/rule-two"} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md missing %q:\n%s", want, agents)
		}
	}
	mcp := readString(t, filepath.Join(dest, ".mcp.json"))
	for _, want := range []string{`"user"`, `"one"`, `"two"`} {
		if !strings.Contains(mcp, want) {
			t.Errorf(".mcp.json missing %s (lost user entry or a merge):\n%s", want, mcp)
		}
	}
	if got := readString(t, filepath.Join(dest, "skills/x/SKILL.md")); got != "skill body" {
		t.Errorf("standalone file = %q", got)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
