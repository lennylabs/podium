package materialize

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
// operator edited around it is removed with its own line terminator and at
// most one line break Podium inserted beside it. A line break that ends the
// operator's line directly above the block stays when operator text follows
// the block, so the lines above and below are not joined.
func TestInjectStrip_BlockPositions(t *testing.T) {
	t.Parallel()
	md := commentStyleFor("GEMINI.md")
	block := string(injectBlock(nil, "team/a", []byte("Rule A."), md))
	tomlBlock := string(injectBlock(nil, "ops/db", []byte("[mcp_servers.db]\ncommand = \"db-mcp\""), commentStyleFor(".codex/config.toml")))
	cases := []struct {
		name, path, file, want string
	}{
		{"start", "GEMINI.md", block + "operator line\n", "operator line\n"},
		{"start blank line after", "GEMINI.md", block + "\noperator line\n", "\noperator line\n"},
		{"middle", "GEMINI.md", "top\n\n" + block + "bottom\n", "top\nbottom\n"},
		{"middle blank lines", "GEMINI.md", "top\n\n" + block + "\nbottom\n", "top\n\nbottom\n"},
		{"middle directly below a line", "GEMINI.md", "# Team notes\n" + block + "Ask before force-pushing.\n", "# Team notes\nAsk before force-pushing.\n"},
		{"text appended after a file without a final newline", "GEMINI.md", string(injectBlock([]byte("top"), "team/a", []byte("Rule A."), md)) + "bottom\n", "top\nbottom\n"},
		{"toml directly between two lines", ".codex/config.toml", "model = \"o3\"\n" + tomlBlock + "[profiles.x]\n", "model = \"o3\"\n[profiles.x]\n"},
		{"end", "GEMINI.md", "top\n\n" + block, "top\n"},
		{"end no final newline", "GEMINI.md", "top\n\n" + strings.TrimSuffix(block, "\n"), "top\n"},
		{"only block", "GEMINI.md", block, ""},
		{"crlf middle", "GEMINI.md", "top\r\n\r\n" + strings.ReplaceAll(block, "\n", "\r\n") + "bottom\r\n", "top\r\nbottom\r\n"},
		{"crlf directly below a line", "GEMINI.md", "top\r\n" + strings.ReplaceAll(block, "\n", "\r\n") + "bottom\r\n", "top\r\nbottom\r\n"},
		{"crlf end", "GEMINI.md", "top\r\n\r\n" + strings.ReplaceAll(block, "\n", "\r\n"), "top\r\n"},
	}
	for _, c := range cases {
		if got := string(StripPodiumBlocks([]byte(c.file), c.path, nil)); got != c.want {
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

// Spec: §6.7 — a marker counts only as a whole line, and a begin marker pairs
// only with an end marker for the same key. A begin marker with no such end
// marker is left in place together with everything after it, and operator
// prose that mentions a marker is not a marker, so neither takes the
// operator's content up to an unrelated marker. A CRLF block after a line
// that ends with a bare line feed takes that line feed as its separator.
func TestInjectStrip_MarkerRecognition(t *testing.T) {
	t.Parallel()
	md := commentStyleFor("GEMINI.md")
	block := string(injectBlock(nil, "team/a", []byte("Rule A."), md))
	const stale = "<!-- podium:begin:team/a -->\nnotes\n"
	cases := []struct {
		name, path, file, want string
	}{
		{"no end marker", "GEMINI.md", "top\n\n<!-- podium:begin:a -->\nRule A.\n", "top\n\n<!-- podium:begin:a -->\nRule A.\n"},
		{"end marker without close", "GEMINI.md", "top\n\n<!-- podium:begin:a -->\nRule A.\n<!-- podium:end:a\n", "top\n\n<!-- podium:begin:a -->\nRule A.\n<!-- podium:end:a\n"},
		{"end marker for another key", "GEMINI.md", "top\n\n<!-- podium:begin:a -->\nx\n<!-- podium:end:b -->\nmore\n", "top\n\n<!-- podium:begin:a -->\nx\n<!-- podium:end:b -->\nmore\n"},
		{"marker with trailing text", "GEMINI.md", "<!-- podium:begin:a --> note\nx\n<!-- podium:end:a -->\n", "<!-- podium:begin:a --> note\nx\n<!-- podium:end:a -->\n"},
		{"inline mention above a block", "GEMINI.md", "Use <!-- podium:begin:team/x --> markers.\n\n" + block + "after\n", "Use <!-- podium:begin:team/x --> markers.\nafter\n"},
		{"inline toml mention above a block", ".codex/config.toml", "x = 1 # podium:begin:ops/x\n\n# podium:begin:ops/db\ny = 2\n# podium:end:ops/db\n", "x = 1 # podium:begin:ops/x\n"},
		{"unterminated begin above a block", "GEMINI.md", "<!-- podium:begin:old -->\nnotes\n\n" + block, "<!-- podium:begin:old -->\nnotes\n"},
		{"unterminated begin for the same key", "GEMINI.md", stale + "\n" + block, stale},
		{"toml end at end of file", ".codex/config.toml", "top\n\n# podium:begin:a\nx = 1\n# podium:end:a", "top\n"},
		{"crlf block after bare line feed", "GEMINI.md", "top\n<!-- podium:begin:a -->\r\nRule A.\r\n<!-- podium:end:a -->\r\n", "top"},
	}
	for _, c := range cases {
		if got := string(StripPodiumBlocks([]byte(c.file), c.path, nil)); got != c.want {
			t.Errorf("%s: strip(%q) = %q, want %q", c.name, c.file, got, c.want)
		}
	}
	// A re-inject replaces the complete block for the key and leaves the
	// unterminated begin marker above it, with the operator's notes, alone.
	got := string(injectBlock([]byte(stale+"\n"+block), "team/a", []byte("Rule A v2."), md))
	if want := stale + "\n" + string(injectBlock(nil, "team/a", []byte("Rule A v2."), md)); got != want {
		t.Errorf("re-inject past an unterminated begin = %q, want %q", got, want)
	}
}

// Spec: §6.7, §13.11.3 — a TOML marker has no closing text, so the marker for
// ops/db is a prefix of the marker for ops/db-admin. Each key matches only its
// own whole-line markers, in either write order, and the keep rule leaves the
// kept ops/db-admin block untouched while ops/db is rewritten or removed.
func TestInjectStrip_TOMLPrefixKeys(t *testing.T) {
	t.Parallel()
	const path = ".codex/config.toml"
	toml := commentStyleFor(path)
	base := []byte("model = \"o3\"\n")
	for _, order := range [][]string{{"ops/db", "ops/db-admin"}, {"ops/db-admin", "ops/db"}} {
		file := base
		for _, k := range order {
			file = injectBlock(file, k, []byte("[mcp_servers."+k+"]"), toml)
		}
		updated := string(injectBlock(file, "ops/db", []byte("[mcp_servers.ops/db v2]"), toml))
		for _, want := range []string{"[mcp_servers.ops/db v2]", "[mcp_servers.ops/db-admin]"} {
			if strings.Count(updated, want) != 1 {
				t.Errorf("%v: re-inject of ops/db: want one %q:\n%s", order, want, updated)
			}
		}
		onlyAdmin := string(injectBlock(base, "ops/db-admin", []byte("[mcp_servers.ops/db-admin]"), toml))
		if got := string(StripPodiumBlocks(file, path, keepOnly("ops/db-admin"))); got != onlyAdmin {
			t.Errorf("%v: keep ops/db-admin = %q, want %q", order, got, onlyAdmin)
		}
		onlyDB := string(injectBlock(base, "ops/db", []byte("[mcp_servers.ops/db]"), toml))
		if got := string(StripPodiumBlocks(file, path, keepOnly("ops/db"))); got != onlyDB {
			t.Errorf("%v: keep ops/db = %q, want %q", order, got, onlyDB)
		}
	}
}

// Spec: §6.7, §13.11.3 — WriteKeeping with ops/db-admin kept and ops/db
// injected leaves the kept block byte-identical across repeated syncs.
func TestWriteKeeping_TOMLPrefixKeyKeepsLongerKey(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	if err := Write(dest, []adapter.File{
		{Path: ".codex/config.toml", Op: adapter.OpInject, Key: "ops/db-admin", Content: []byte("[mcp_servers.db-admin]")},
		{Path: ".codex/config.toml", Op: adapter.OpInject, Key: "ops/db", Content: []byte("[mcp_servers.db]")},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	next := []adapter.File{{Path: ".codex/config.toml", Op: adapter.OpInject, Key: "ops/db", Content: []byte("[mcp_servers.db]\nargs = [\"v2\"]")}}
	for i := 0; i < 2; i++ {
		if err := WriteKeeping(dest, next, keepOnly("ops/db-admin")); err != nil {
			t.Fatalf("WriteKeeping %d: %v", i, err)
		}
		got := readString(t, filepath.Join(dest, ".codex", "config.toml"))
		want := "# podium:begin:ops/db-admin\n[mcp_servers.db-admin]\n# podium:end:ops/db-admin\n\n# podium:begin:ops/db\n[mcp_servers.db]\nargs = [\"v2\"]\n# podium:end:ops/db\n"
		if got != want {
			t.Errorf("sync %d: config.toml = %q, want %q", i, got, want)
		}
	}
}

// Spec: §6.7 — a marker line counts after a UTF-8 byte order mark that starts
// the file and with spaces or tabs around it, so three syncs in a row replace
// the block in place: the old content is gone, one block remains, and the
// operator's bytes, the byte order mark included, stay.
func TestWriteKeeping_DecoratedMarkersReplaceInPlace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, path, file, operator string
	}{
		{"byte order mark before the first block", "GEMINI.md",
			utf8BOM + "<!-- podium:begin:a -->\nold\n<!-- podium:end:a -->\nop\n", utf8BOM + "op\n"},
		{"trailing space after the begin marker", "GEMINI.md",
			"op\n\n<!-- podium:begin:a --> \nold\n<!-- podium:end:a -->\n", "op\n"},
		{"indented markers", "GEMINI.md",
			"op\n\n  <!-- podium:begin:a -->\nold\n\t<!-- podium:end:a -->\t\n", "op\n"},
		{"toml trailing whitespace and crlf", ".codex/config.toml",
			"x = 1\r\n\r\n# podium:begin:a \r\nold\r\n  # podium:end:a\r\n", "x = 1\r\n"},
	}
	for _, c := range cases {
		dest := t.TempDir()
		full := filepath.Join(dest, filepath.FromSlash(c.path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c.file), 0o644); err != nil {
			t.Fatal(err)
		}
		want := string(injectBlock([]byte(c.operator), "a", []byte("new"), commentStyleFor(c.path)))
		files := []adapter.File{{Path: c.path, Op: adapter.OpInject, Key: "a", Content: []byte("new")}}
		for i := 0; i < 3; i++ {
			if err := WriteKeeping(dest, files, nil); err != nil {
				t.Fatalf("%s: sync %d: %v", c.name, i, err)
			}
			if got := readString(t, full); got != want {
				t.Errorf("%s: sync %d = %q, want %q", c.name, i, got, want)
			}
		}
		if got := string(StripPodiumBlocks([]byte(c.file), c.path, nil)); got != c.operator {
			t.Errorf("%s: strip = %q, want %q", c.name, got, c.operator)
		}
	}
}

// Spec: §6.7 — reading the markers is linear in the file: a file with tens of
// thousands of unterminated begin markers, as a committed AGENTS.md or
// config.toml can carry, is stripped and injected into in one pass. The scan
// that paired each begin marker by reading on to the end of the file took
// close to a minute on this input. The bound sits far above the linear scan's
// milliseconds, the race detector included, and far below that.
func TestInjectStrip_UnterminatedBeginsScanLinearly(t *testing.T) {
	t.Parallel()
	const n = 32000
	for _, path := range []string{"GEMINI.md", ".codex/config.toml"} {
		cs := commentStyleFor(path)
		var b strings.Builder
		for i := 0; i < n; i++ {
			// Distinct keys, so no later begin marker for the same key
			// stops a begin marker's search for its end marker.
			b.WriteString(cs.open + "podium:begin:k" + strconv.Itoa(i) + cs.close + "\nnotes\n")
		}
		file := b.String()
		start := time.Now()
		if got := string(StripPodiumBlocks([]byte(file), path, nil)); got != file {
			t.Fatalf("%s: the strip changed a file with only unterminated begins", path)
		}
		if got := string(injectBlock([]byte(file), "k", []byte("Rule."), cs)); !strings.HasPrefix(got, file) {
			t.Fatalf("%s: the inject rewrote the unterminated begins", path)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("%s: %d unterminated begins took %v", path, n, elapsed)
		}
	}
}
