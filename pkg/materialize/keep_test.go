package materialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// Spec: §6.7, §13.11.3 — IndexedEntries reads the artifact ID -> entry path
// index of a config document or fragment, skipping an index value that is not
// a non-empty list of strings, and returns nothing for a document with no
// index or one that is not a JSON object.
func TestIndexedEntries(t *testing.T) {
	t.Parallel()
	doc := []byte(`{"mcpServers":{"alpha":{}},"x-podium":{"s/a":["mcpServers","alpha"],"s/b":"bad","s/c":[],"s/d":["mcpServers",1]}}`)
	got := IndexedEntries(doc)
	if len(got) != 1 || strings.Join(got["s/a"], "/") != "mcpServers/alpha" {
		t.Errorf("IndexedEntries = %v, want only s/a at mcpServers/alpha", got)
	}
	for _, in := range []string{`{"mcpServers":{}}`, `[1]`, `not json`, `{"x-podium":"bad"}`} {
		if got := IndexedEntries([]byte(in)); len(got) != 0 {
			t.Errorf("IndexedEntries(%s) = %v, want none", in, got)
		}
	}
}

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

// keepAllBut returns a keep predicate that reports every ID except id, the
// predicate podium-mcp passes for a load of id.
func keepAllBut(id string) func(string) bool {
	return func(got string) bool { return got != id }
}

// Spec: §6.6, §6.7 — a write that keeps every artifact except the one it
// writes replaces that artifact's earlier index-tracked entry and leaves the
// other artifacts' entries in place.
func TestWriteKeeping_ReplacesOnlyTheWrittenArtifact(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	mcp := filepath.Join(dest, ".mcp.json")
	for _, w := range []struct{ name, id string }{{"alpha", "s/a"}, {"bravo", "s/b"}, {"alpha2", "s/a"}} {
		if err := WriteKeeping(dest, []adapter.File{
			{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: taggedServer(w.name, w.id)},
		}, keepAllBut(w.id)); err != nil {
			t.Fatalf("WriteKeeping %s: %v", w.name, err)
		}
	}
	// The last write re-wrote s/a under a new entry name: the old alpha entry
	// goes, and bravo stays.
	raw := readString(t, mcp)
	got := IndexedEntries([]byte(raw))
	if len(got) != 2 || strings.Join(got["s/a"], "/") != "mcpServers/alpha2" || strings.Join(got["s/b"], "/") != "mcpServers/bravo" {
		t.Errorf("index = %v, want s/a at alpha2 and s/b at bravo", got)
	}
	if strings.Contains(raw, `"cmd-alpha"`) || !strings.Contains(raw, `"cmd-bravo"`) {
		t.Errorf("want the alpha entry replaced and the bravo entry kept:\n%s", raw)
	}
}

// Spec: §6.6, §6.7 — a kept artifact's keyed entry that a fragment of the batch
// records for a different artifact is replaced by that fragment: the kept
// entry's fields do not mix into the new entry, and the index names only the
// new owner. A kept entry at another path stays.
func TestWriteKeeping_TakenOverEntryIsReplaced(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	mcp := filepath.Join(dest, ".mcp.json")
	base := `{"mcpServers":{"shared":{"command":"old","env":{"TOKEN_FILE":"a"}},"other":{"command":"keep"}},` +
		`"` + adapter.PodiumIndexKey + `":{"s/a":["mcpServers","shared"],"s/k":["mcpServers","other"]}}`
	if err := os.WriteFile(mcp, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	frag := `{"mcpServers":{"shared":{"url":"https://mcp.acme.com"}},"` + adapter.PodiumIndexKey + `":{"s/b":["mcpServers","shared"]}}`
	if err := WriteKeeping(dest, []adapter.File{
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: []byte(frag)},
	}, keepAllBut("s/b")); err != nil {
		t.Fatalf("WriteKeeping: %v", err)
	}
	raw := readString(t, mcp)
	for _, gone := range []string{`"old"`, `TOKEN_FILE`, `"s/a"`} {
		if strings.Contains(raw, gone) {
			t.Errorf("%s of the taken-over entry survived:\n%s", gone, raw)
		}
	}
	got := IndexedEntries([]byte(raw))
	if strings.Join(got["s/b"], "/") != "mcpServers/shared" || strings.Join(got["s/k"], "/") != "mcpServers/other" {
		t.Errorf("index = %v, want s/b at shared and s/k at other", got)
	}
	if !strings.Contains(raw, "https://mcp.acme.com") || !strings.Contains(raw, `"keep"`) {
		t.Errorf("the new entry or the kept entry is missing:\n%s", raw)
	}
}

// Spec: §6.7, §13.11.3 — EntriesTakenOver names each document entry a fragment
// records for a different artifact, and nothing for the same artifact, a
// different path, or a fragment or document without an index.
func TestEntriesTakenOver(t *testing.T) {
	t.Parallel()
	doc := []byte(`{"x-podium":{"s/a":["mcpServers","alpha"],"s/b":["mcpServers","bravo"]}}`)
	cases := []struct {
		name  string
		doc   []byte
		frags [][]byte
		want  string
	}{
		{"other artifact", doc, [][]byte{taggedServer("alpha", "s/c")}, "s/a"},
		{"same artifact", doc, [][]byte{taggedServer("alpha", "s/a")}, ""},
		{"other path", doc, [][]byte{taggedServer("charlie", "s/c")}, ""},
		{"no fragment index", doc, [][]byte{[]byte(`{"mcpServers":{"alpha":{}}}`)}, ""},
		{"no document index", []byte(`{}`), [][]byte{taggedServer("alpha", "s/c")}, ""},
	}
	for _, c := range cases {
		ids := make([]string, 0, 1)
		for id := range EntriesTakenOver(c.doc, c.frags) {
			ids = append(ids, id)
		}
		if strings.Join(ids, ",") != c.want {
			t.Errorf("%s: EntriesTakenOver = %v, want %q", c.name, ids, c.want)
		}
	}
}
