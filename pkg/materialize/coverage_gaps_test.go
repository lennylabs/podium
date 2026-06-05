package materialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// Spec: §6.7 inject reconciliation — StripPodiumBlocks is the exported
// wrapper that selects the marker style from the path and removes every
// Podium-managed inject block. It round-trips with injectBlock: a block
// written by injectBlock is removed by StripPodiumBlocks, leaving the
// operator's surrounding content intact.
func TestStripPodiumBlocks_Exported_MarkdownRoundTrip(t *testing.T) {
	t.Parallel()
	md := commentStyleFor("AGENTS.md")
	base := injectBlock([]byte("# House rules\n\nKeep PRs small.\n"), "team/a", []byte("Rule A."), md)
	base = injectBlock(base, "team/b", []byte("Rule B."), md)

	out := string(StripPodiumBlocks(base, "AGENTS.md"))
	if !strings.Contains(out, "# House rules") || !strings.Contains(out, "Keep PRs small.") {
		t.Errorf("operator content lost:\n%s", out)
	}
	for _, gone := range []string{"podium:begin:team/a", "podium:begin:team/b", "Rule A.", "Rule B."} {
		if strings.Contains(out, gone) {
			t.Errorf("Podium block %q survived the strip:\n%s", gone, out)
		}
	}
}

// Spec: §6.7 inject reconciliation — StripPodiumBlocks selects the TOML
// comment style for a .toml path, so a block written with the "# " marker
// is removed.
func TestStripPodiumBlocks_Exported_TOMLMarkers(t *testing.T) {
	t.Parallel()
	toml := commentStyleFor(".codex/config.toml")
	base := injectBlock([]byte("model = \"gpt\"\n"), "ops/db", []byte("[mcp_servers.db]"), toml)
	if !strings.Contains(string(base), "# podium:begin:ops/db") {
		t.Fatalf("setup: expected toml markers:\n%s", base)
	}
	out := string(StripPodiumBlocks(base, ".codex/config.toml"))
	if strings.Contains(out, "podium:begin:ops/db") || strings.Contains(out, "[mcp_servers.db]") {
		t.Errorf("toml Podium block survived the strip:\n%s", out)
	}
	if !strings.Contains(out, "model = \"gpt\"") {
		t.Errorf("operator toml content lost:\n%s", out)
	}
}

// Spec: §6.7 inject reconciliation — a document with no Podium block is
// returned unchanged, and empty input yields empty output.
func TestStripPodiumBlocks_Exported_NoMatchAndEmpty(t *testing.T) {
	t.Parallel()
	plain := []byte("# House rules\n\nKeep PRs small.\n")
	if got := string(StripPodiumBlocks(plain, "AGENTS.md")); got != string(plain) {
		t.Errorf("no-block input changed:\nwant %q\ngot  %q", plain, got)
	}
	if got := string(StripPodiumBlocks(nil, "AGENTS.md")); got != "" {
		t.Errorf("empty input = %q, want empty", got)
	}
}

// Spec: §6.7 — modeOf maps a zero adapter mode to the 0o644 default and
// preserves a non-zero mode verbatim.
func TestModeOf_DefaultAndExplicit(t *testing.T) {
	t.Parallel()
	if got := modeOf(adapter.File{Mode: 0}); got != 0o644 {
		t.Errorf("modeOf(0) = %o, want 0644", got)
	}
	if got := modeOf(adapter.File{Mode: 0o600}); got != 0o600 {
		t.Errorf("modeOf(0o600) = %o, want 0600", got)
	}
	if got := modeOf(adapter.File{Mode: 0o755}); got != 0o755 {
		t.Errorf("modeOf(0o755) = %o, want 0755", got)
	}
}

// Spec: §4.4.1 — trimSpace strips leading and trailing spaces and tabs
// used to normalize runtime-requirement version strings before comparison.
// A string with no surrounding whitespace is returned unchanged, and an
// all-whitespace string collapses to empty.
func TestTrimSpace_LeadingTrailingTabsAndSpaces(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", ""},
		{"x", "x"},
		{"  x", "x"},
		{"x  ", "x"},
		{"  x  ", "x"},
		{"\t\tx\t", "x"},
		{" \t x \t ", "x"},
		{"   ", ""},
		{"\t\t", ""},
		{"a b", "a b"},
		{"  a b  ", "a b"},
	}
	for _, c := range cases {
		if got := trimSpace(c.in); got != c.want {
			t.Errorf("trimSpace(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Spec: §6.6 — writeAtomic stages content at "<path>.tmp" then renames it
// into place, creating intermediate directories, and leaves no temp file
// behind on success.
func TestWriteAtomic_WritesAndCommits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.json")
	if err := writeAtomic(path, []byte("payload"), 0o644); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("content = %q, want payload", got)
	}
	if _, statErr := os.Stat(path + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("leftover temp file: %v", statErr)
	}
}

// Spec: §6.6 — writeAtomic replaces an existing file in place; a reader
// sees either the prior or the new content.
func TestWriteAtomic_ReplacesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := writeAtomic(path, []byte("v1"), 0o644); err != nil {
		t.Fatalf("writeAtomic v1: %v", err)
	}
	if err := writeAtomic(path, []byte("v2"), 0o644); err != nil {
		t.Fatalf("writeAtomic v2: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("content = %q, want v2", got)
	}
}

// Spec: §6.6 — when the parent directory cannot be created (a path
// component is an existing regular file), writeAtomic returns the MkdirAll
// error and writes nothing.
func TestWriteAtomic_MkdirAllErrorPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// blocker is a regular file; using it as a path component makes MkdirAll fail.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "child", "out.txt")
	if err := writeAtomic(target, []byte("payload"), 0o644); err == nil {
		t.Fatalf("writeAtomic should fail when a path component is a file")
	}
	// The blocker file is untouched; the failed write created nothing under it.
	got, err := os.ReadFile(blocker)
	if err != nil {
		t.Fatalf("read blocker: %v", err)
	}
	if string(got) != "x" {
		t.Errorf("blocker content = %q, want x (writeAtomic should not have written)", got)
	}
}

// Spec: §6.6 — when the rename into place fails (the target is a non-empty
// directory, which a same-directory rename cannot overwrite), writeAtomic
// returns the error and removes the staged temporary rather than leaving it
// behind.
func TestWriteAtomic_RenameErrorCleansTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The target path is an existing non-empty directory; renaming the temp
	// onto it fails.
	target := filepath.Join(dir, "out")
	if err := os.MkdirAll(filepath.Join(target, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "inner", "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(target, []byte("payload"), 0o644); err == nil {
		t.Fatalf("writeAtomic should fail renaming onto a non-empty directory")
	}
	if _, statErr := os.Stat(target + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("staged temp not cleaned up after rename failure: %v", statErr)
	}
}
