package materialize

import (
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

	out := string(StripPodiumBlocks(base, "AGENTS.md", nil))
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
	out := string(StripPodiumBlocks(base, ".codex/config.toml", nil))
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
	if got := string(StripPodiumBlocks(plain, "AGENTS.md", nil)); got != string(plain) {
		t.Errorf("no-block input changed:\nwant %q\ngot  %q", plain, got)
	}
	if got := string(StripPodiumBlocks(nil, "AGENTS.md", nil)); got != "" {
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
