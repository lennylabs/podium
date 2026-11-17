package materialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// keepOnly returns a keep predicate that reports exactly id.
func keepOnly(id string) func(string) bool {
	return func(got string) bool { return got == id }
}

// Spec: §6.7, §13.11.3 — WriteKeeping exempts a kept artifact's index-tracked
// entry from the config-merge strip, so the entry and its index record stay
// beside the fragments the batch merges, while the entry of an artifact that
// is neither kept nor written is removed and the operator's entry survives.
func TestWriteKeeping_KeepsIndexedEntry(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	if err := Write(dest, []adapter.File{
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: taggedServer("alpha", "s/a")},
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: taggedServer("bravo", "s/b")},
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: []byte(`{"mcpServers":{"op":{"command":"op"}}}`)},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := WriteKeeping(dest, []adapter.File{
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: taggedServer("charlie", "s/c")},
	}, keepOnly("s/a")); err != nil {
		t.Fatalf("WriteKeeping: %v", err)
	}
	got := readString(t, filepath.Join(dest, ".mcp.json"))
	for _, want := range []string{`"cmd-alpha"`, `"s/a"`, `"cmd-charlie"`, `"s/c"`, `"op"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s after a keeping write:\n%s", want, got)
		}
	}
	if strings.Contains(got, "bravo") || strings.Contains(got, "s/b") {
		t.Errorf("unkept bravo survived:\n%s", got)
	}

	// A plain Write keeps nothing, so the next sync without alpha removes it.
	if err := Write(dest, []adapter.File{
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: taggedServer("charlie", "s/c")},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readString(t, filepath.Join(dest, ".mcp.json")); strings.Contains(got, "alpha") {
		t.Errorf("alpha survived a write that keeps nothing:\n%s", got)
	}
}

// Spec: §6.7, §13.11.3 — the strip leaves an in-entry-tagged array element
// whose owner is kept, removes the other tagged elements, and treats a tag
// whose value is not a string as Podium-owned with no owner.
func TestStripPodiumOwnedBytes_KeepsTaggedElement(t *testing.T) {
	t.Parallel()
	base := []byte(`{"hooks":{"Pre":[` +
		`{"command":"a","x-podium-id":"h/a"},` +
		`{"command":"b","x-podium-id":"h/b"},` +
		`{"command":"n","x-podium-id":7},` +
		`{"command":"op"}]},` +
		`"solo":{"command":"s","x-podium-id":"h/s"},` +
		`"other":{"command":"o","x-podium-id":"h/o"}}`)
	keep := func(id string) bool { return id == "h/a" || id == "h/s" }
	got := string(StripPodiumOwnedBytes(base, keep))
	for _, want := range []string{`"a"`, `"h/a"`, `"op"`, `"solo"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s:\n%s", want, got)
		}
	}
	for _, gone := range []string{`"h/b"`, `"n"`, `"other"`} {
		if strings.Contains(got, gone) {
			t.Errorf("%s survived the strip:\n%s", gone, got)
		}
	}
	if all := string(StripPodiumOwnedBytes(base, nil)); strings.Contains(all, "x-podium-id") {
		t.Errorf("a nil keep left a tagged entry:\n%s", all)
	}
}

// Spec: §6.7, §13.11.3 — the inject strip leaves a kept artifact's block in
// place in both marker styles and removes the blocks around it.
func TestStripPodiumBlocks_KeepsKeyedBlock(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"AGENTS.md", ".codex/config.toml"} {
		cs := commentStyleFor(path)
		base := injectBlock([]byte("operator line\n"), "team/a", []byte("Rule A."), cs)
		base = injectBlock(base, "team/b", []byte("Rule B."), cs)
		base = injectBlock(base, "team/c", []byte("Rule C."), cs)
		got := string(StripPodiumBlocks(base, path, keepOnly("team/b")))
		for _, want := range []string{"operator line", "podium:begin:team/b", "Rule B.", "podium:end:team/b"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: missing %q:\n%s", path, want, got)
			}
		}
		for _, gone := range []string{"team/a", "Rule A.", "team/c", "Rule C."} {
			if strings.Contains(got, gone) {
				t.Errorf("%s: %q survived the strip:\n%s", path, gone, got)
			}
		}
	}
}

// Spec: §6.7, §13.11.3 — WriteKeeping keeps a kept artifact's inject block in
// a shared text file while the batch re-injects the other blocks.
func TestWriteKeeping_KeepsInjectBlock(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	agents := filepath.Join(dest, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("# House rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dest, []adapter.File{
		{Path: "AGENTS.md", Op: adapter.OpInject, Key: "team/a", Content: []byte("Rule A.")},
		{Path: "AGENTS.md", Op: adapter.OpInject, Key: "team/b", Content: []byte("Rule B.")},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := WriteKeeping(dest, []adapter.File{
		{Path: "AGENTS.md", Op: adapter.OpInject, Key: "team/b", Content: []byte("Rule B v2.")},
	}, keepOnly("team/a")); err != nil {
		t.Fatalf("WriteKeeping: %v", err)
	}
	got := readString(t, agents)
	for _, want := range []string{"# House rules", "Rule A.", "Rule B v2."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Rule B.\n") {
		t.Errorf("the old team/b block survived:\n%s", got)
	}
}
