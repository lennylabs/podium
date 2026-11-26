package materialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// roundTripBases are operator files an inject block is added to and removed
// from. Each covers a line-ending or trailing-newline variant the strip has to
// restore byte for byte.
var roundTripBases = map[string]string{
	"empty":                    "",
	"trailing newline":         "# House rules\n\nKeep PRs small.\n",
	"no trailing newline":      "# House rules\n\nKeep PRs small.",
	"single line":              "operator line\n",
	"trailing blank lines":     "operator line\n\n\n",
	"crlf":                     "# House rules\r\n\r\nKeep PRs small.\r\n",
	"crlf no trailing":         "# House rules\r\n\r\nKeep PRs small.",
	"only a newline":           "\n",
	"lone carriage return":     "operator line\r",
	"toml-like key at the end": "model = \"gpt\"\n",
}

var roundTripPaths = []string{"GEMINI.md", "AGENTS.md", ".codex/config.toml"}

// Spec: §6.7 — removing Podium's inject block restores the operator's bytes
// outside it exactly, including the final newline, CRLF line endings, and a
// file that held nothing but the block.
func TestInjectStrip_RoundTripRestoresOperatorBytes(t *testing.T) {
	t.Parallel()
	for _, path := range roundTripPaths {
		cs := commentStyleFor(path)
		for name, base := range roundTripBases {
			one := injectBlock([]byte(base), "team/a", []byte("Rule A."), cs)
			if got := string(StripPodiumBlocks(one, path, nil)); got != base {
				t.Errorf("%s/%s: one block: strip(inject(base)) = %q, want %q", path, name, got, base)
			}
			two := injectBlock(one, "team/b", []byte("Rule B.\n"), cs)
			if got := string(StripPodiumBlocks(two, path, nil)); got != base {
				t.Errorf("%s/%s: two blocks: strip = %q, want %q", path, name, got, base)
			}
		}
	}
}

// Spec: §6.7, §13.11.3 — removing one block while another stays leaves the
// file as if only the remaining block had been added, whichever of the two
// blocks is first.
func TestInjectStrip_PartialRemovalMatchesSingleInject(t *testing.T) {
	t.Parallel()
	for _, path := range roundTripPaths {
		cs := commentStyleFor(path)
		for name, base := range roundTripBases {
			two := injectBlock(injectBlock([]byte(base), "team/a", []byte("Rule A."), cs), "team/b", []byte("Rule B."), cs)
			onlyA := string(injectBlock([]byte(base), "team/a", []byte("Rule A."), cs))
			onlyB := string(injectBlock([]byte(base), "team/b", []byte("Rule B."), cs))
			if got := string(StripPodiumBlocks(two, path, keepOnly("team/a"))); got != onlyA {
				t.Errorf("%s/%s: drop last block = %q, want %q", path, name, got, onlyA)
			}
			if got := string(StripPodiumBlocks(two, path, keepOnly("team/b"))); got != onlyB {
				t.Errorf("%s/%s: drop first block = %q, want %q", path, name, got, onlyB)
			}
		}
	}
}

// Spec: §6.7 — the block at the start, the middle, and the end of a file the
// operator edited around it is removed with its own line terminator and the
// one separator line Podium inserted, and nothing else.
func TestInjectStrip_BlockPositions(t *testing.T) {
	t.Parallel()
	md := commentStyleFor("GEMINI.md")
	block := string(injectBlock(nil, "team/a", []byte("Rule A."), md))
	cases := []struct {
		name, file, want string
	}{
		{"start", block + "operator line\n", "operator line\n"},
		{"start blank line after", block + "\noperator line\n", "\noperator line\n"},
		{"middle", "top\n\n" + block + "bottom\n", "top\nbottom\n"},
		{"middle blank lines", "top\n\n" + block + "\nbottom\n", "top\n\nbottom\n"},
		{"end", "top\n\n" + block, "top\n"},
		{"end no final newline", "top\n\n" + strings.TrimSuffix(block, "\n"), "top\n"},
		{"only block", block, ""},
		{"crlf middle", "top\r\n\r\n" + strings.ReplaceAll(block, "\n", "\r\n") + "bottom\r\n", "top\r\nbottom\r\n"},
		{"crlf end", "top\r\n\r\n" + strings.ReplaceAll(block, "\n", "\r\n"), "top\r\n"},
	}
	for _, c := range cases {
		if got := string(StripPodiumBlocks([]byte(c.file), "GEMINI.md", nil)); got != c.want {
			t.Errorf("%s: strip(%q) = %q, want %q", c.name, c.file, got, c.want)
		}
	}
}

// Spec: §6.7, §13.11.3 — a TOML block whose lines a checkout converted to CRLF
// still matches its key, so a kept artifact's block stays.
func TestInjectStrip_TOMLKeyIgnoresCarriageReturn(t *testing.T) {
	t.Parallel()
	toml := commentStyleFor(".codex/config.toml")
	file := strings.ReplaceAll(string(injectBlock([]byte("model = \"gpt\"\n"), "ops/db", []byte("[mcp_servers.db]"), toml)), "\n", "\r\n")
	if got := string(StripPodiumBlocks([]byte(file), ".codex/config.toml", keepOnly("ops/db"))); got != file {
		t.Errorf("kept CRLF block changed:\n got %q\nwant %q", got, file)
	}
	if got := string(StripPodiumBlocks([]byte(file), ".codex/config.toml", nil)); got != "model = \"gpt\"\r\n" {
		t.Errorf("CRLF strip = %q", got)
	}
}

// Spec: §6.7 — a resync through the writer rebuilds the same bytes and
// reproduces the operator's file once the block is gone, for a file without
// a final newline.
func TestWrite_InjectResyncAndRemovalKeepsOperatorBytes(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	const operator = "# House rules\n\nKeep PRs small."
	path := filepath.Join(dest, "GEMINI.md")
	if err := os.WriteFile(path, []byte(operator), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []adapter.File{{Path: "GEMINI.md", Op: adapter.OpInject, Key: "team/a", Content: []byte("Rule A.")}}
	if err := Write(dest, files); err != nil {
		t.Fatalf("Write: %v", err)
	}
	first := readString(t, path)
	if err := Write(dest, files); err != nil {
		t.Fatalf("re-Write: %v", err)
	}
	if again := readString(t, path); again != first {
		t.Errorf("resync changed the file:\n got %q\nwant %q", again, first)
	}
	if err := RewriteStale(dest, "GEMINI.md", func(b []byte) []byte { return StripPodiumBlocks(b, "GEMINI.md", nil) }); err != nil {
		t.Fatalf("RewriteStale: %v", err)
	}
	if got := readString(t, path); got != operator {
		t.Errorf("after removal = %q, want %q", got, operator)
	}
}

// Spec: §6.7 — a JSON config file already in the merge's output format
// (two-space indent, sorted keys, final newline) is restored byte for byte
// when Podium's entry is removed. mergeJSON re-serializes the document, so a
// file in another format takes that format on the first merge.
func TestMergeJSON_RoundTripCanonicalFile(t *testing.T) {
	t.Parallel()
	const operator = "{\n  \"mcpServers\": {\n    \"mine\": {\n      \"command\": \"x\"\n    }\n  },\n  \"theme\": \"dark\"\n}\n"
	frag := []byte(`{"mcpServers":{"db":{"command":"db-mcp","` + adapter.PodiumOwnedKey + `":"ops/db"}}}`)
	merged, err := mergeJSON([]byte(operator), frag)
	if err != nil {
		t.Fatalf("mergeJSON: %v", err)
	}
	if !strings.Contains(string(merged), "db-mcp") {
		t.Fatalf("setup: fragment not merged:\n%s", merged)
	}
	if got := string(StripPodiumOwnedBytes(merged, nil)); got != operator {
		t.Errorf("strip(merge(base)) = %q, want %q", got, operator)
	}
}

// Spec: §6.7 — an unterminated Podium block is left in place, because the
// strip cannot tell where the operator's content resumes, and a CRLF block
// after a line that ends with a bare line feed takes that line feed as its
// separator.
func TestInjectStrip_UnterminatedAndMixedEndings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, path, file, want string
	}{
		{"no end marker", "GEMINI.md", "top\n\n<!-- podium:begin:a -->\nRule A.\n", "top\n\n<!-- podium:begin:a -->\nRule A.\n"},
		{"end marker without close", "GEMINI.md", "top\n\n<!-- podium:begin:a -->\nRule A.\n<!-- podium:end:a\n", "top\n\n<!-- podium:begin:a -->\nRule A.\n<!-- podium:end:a\n"},
		{"toml end at end of file", ".codex/config.toml", "top\n\n# podium:begin:a\nx = 1\n# podium:end:a", "top\n"},
		{"crlf block after bare line feed", "GEMINI.md", "top\n<!-- podium:begin:a -->\r\nRule A.\r\n<!-- podium:end:a -->\r\n", "top"},
	}
	for _, c := range cases {
		if got := string(StripPodiumBlocks([]byte(c.file), c.path, nil)); got != c.want {
			t.Errorf("%s: strip(%q) = %q, want %q", c.name, c.file, got, c.want)
		}
	}
}
