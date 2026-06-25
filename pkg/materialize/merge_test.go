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

// §7.8 marketplace emitter contract: a plugin that bundles two mcp-servers (and
// two hooks on the same native event) writes the emitter's per-plugin Component
// output through the materialize layer, and both servers and both hooks survive
// in the single per-plugin .mcp.json / hooks.json. The emitter emits OpMergeJSON
// fragments, so the merge accumulates the entries rather than the last write
// winning, and a re-render reconciles through the same path the project-files
// mode uses.
func TestWrite_MarketplacePluginAccumulatesMultipleArtifacts(t *testing.T) {
	dest := t.TempDir()
	plugin := adapter.PluginDescriptor{Name: "finance-pack", Prefix: "claude"}
	emitter := adapter.ClaudeMarketplace{}

	srcs := []adapter.Source{
		{
			ArtifactID:    "finance/pay-mcp",
			ArtifactBytes: []byte("---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: https://pay.acme.com\n---\n\nbody\n"),
			Plugin:        plugin,
		},
		{
			ArtifactID:    "finance/bill-mcp",
			ArtifactBytes: []byte("---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: https://bill.acme.com\n---\n\nbody\n"),
			Plugin:        plugin,
		},
		{
			ArtifactID:    "finance/start-a",
			ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\nhook_event: session_start\nhook_action: echo a\n---\n\nbody\n"),
			Plugin:        plugin,
		},
		{
			ArtifactID:    "finance/start-b",
			ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\nhook_event: session_start\nhook_action: echo b\n---\n\nbody\n"),
			Plugin:        plugin,
		},
	}
	var files []adapter.File
	for _, s := range srcs {
		comp, err := emitter.Component(t.Context(), s)
		if err != nil {
			t.Fatalf("Component(%s): %v", s.ArtifactID, err)
		}
		files = append(files, comp...)
	}
	if err := Write(dest, files); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Both mcp-servers accumulate into one .mcp.json. Under the broken OpWrite
	// path the second server's file would clobber the first, leaving only one.
	mcp := readString(t, filepath.Join(dest, "claude", "finance-pack", ".mcp.json"))
	for _, want := range []string{"pay-mcp", "bill-mcp"} {
		if !strings.Contains(mcp, want) {
			t.Errorf("both mcp-servers must survive in one .mcp.json, missing %q:\n%s", want, mcp)
		}
	}

	// Both hooks on the same native event accumulate into one event array in
	// hooks.json; OpWrite would have dropped the first.
	hooks := readString(t, filepath.Join(dest, "claude", "finance-pack", "hooks", "hooks.json"))
	for _, want := range []string{"echo a", "echo b"} {
		if !strings.Contains(hooks, want) {
			t.Errorf("both hooks on the same event must survive in one hooks.json, missing %q:\n%s", want, hooks)
		}
	}
}

// taggedHook builds a Podium-owned hook fragment for native event ev keyed by
// id, mirroring what adapter.hookFragmentJSON emits.
func taggedHook(ev, id, cmd string) []byte {
	return []byte(`{"hooks":{"` + ev + `":[{"hooks":[{"type":"command","command":"` + cmd + `"}],"` + adapter.PodiumOwnedKey + `":"` + id + `"}]}}`)
}

// §6.7 config-merge contract: two Podium hooks on the same native event both
// land in the event's array (no clobber), the operator's untagged entry is
// preserved, a re-sync is idempotent, and dropping one hook removes only its
// entry.
func TestConfigMerge_AccumulateIdempotentRemove(t *testing.T) {
	dest := t.TempDir()
	settings := filepath.Join(dest, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	// Operator-owned entry the merge must never disturb.
	operator := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"op"}]}]},"theme":"dark"}`
	if err := os.WriteFile(settings, []byte(operator), 0o644); err != nil {
		t.Fatal(err)
	}

	syncHooks := func(ids ...string) {
		files := make([]adapter.File, 0, len(ids))
		for _, id := range ids {
			files = append(files, adapter.File{Path: ".claude/settings.json", Op: adapter.OpMergeJSON, Content: taggedHook("PreToolUse", id, "cmd-"+id)})
		}
		if err := Write(dest, files); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	syncHooks("h/a", "h/b")
	got := readString(t, settings)
	for _, want := range []string{`"op"`, `"theme"`, "cmd-h/a", "cmd-h/b"} {
		if !strings.Contains(got, want) {
			t.Errorf("after A+B sync, missing %q:\n%s", want, got)
		}
	}

	// Re-sync the same set: idempotent (no duplicate entries).
	syncHooks("h/a", "h/b")
	got = readString(t, settings)
	if n := strings.Count(got, "cmd-h/a"); n != 1 {
		t.Errorf("re-sync duplicated h/a (count=%d):\n%s", n, got)
	}

	// Drop h/b: its entry is removed, h/a and the operator entry remain.
	syncHooks("h/a")
	got = readString(t, settings)
	if strings.Contains(got, "cmd-h/b") {
		t.Errorf("removed hook h/b still present:\n%s", got)
	}
	for _, want := range []string{`"op"`, `"theme"`, "cmd-h/a"} {
		if !strings.Contains(got, want) {
			t.Errorf("after removing h/b, missing %q:\n%s", want, got)
		}
	}
}

// §6.7 config-merge reconciliation: when a hook's hook_event changes, the prior
// translated native-event entry is stripped, and the now-empty native-event key
// is dropped rather than left as a stale empty array. A re-merge under the new
// event then carries only the new key. An array the operator left empty (no
// Podium element) is preserved, since stripping removed nothing from it.
func TestConfigMerge_EmptyEventKeyDroppedAfterEventChange(t *testing.T) {
	dest := t.TempDir()
	settings := filepath.Join(dest, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	// Operator carries a settings key plus a deliberately empty native-event
	// array; reconciliation must leave both untouched.
	operator := `{"hooks":{"OperatorEvent":[]},"theme":"dark"}`
	if err := os.WriteFile(settings, []byte(operator), 0o644); err != nil {
		t.Fatal(err)
	}

	syncHook := func(ev string) {
		f := adapter.File{Path: ".gemini/settings.json", Op: adapter.OpMergeJSON, Content: taggedHook(ev, "audit/guard", "echo guard")}
		if err := Write(dest, []adapter.File{f}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	// First sync under BeforeTool.
	syncHook("BeforeTool")
	got := readString(t, settings)
	if !strings.Contains(got, "BeforeTool") {
		t.Fatalf("first sync missing BeforeTool entry:\n%s", got)
	}

	// Re-sync under AfterTool (the event changed): the BeforeTool key is dropped,
	// AfterTool carries the single entry, and the operator's empty array and key
	// survive.
	syncHook("AfterTool")
	got = readString(t, settings)
	if strings.Contains(got, "BeforeTool") {
		t.Errorf("stale empty BeforeTool key not dropped after the event change:\n%s", got)
	}
	if !strings.Contains(got, "AfterTool") || strings.Count(got, "echo guard") != 1 {
		t.Errorf("AfterTool entry not present exactly once:\n%s", got)
	}
	if !strings.Contains(got, "OperatorEvent") || !strings.Contains(got, `"theme"`) {
		t.Errorf("operator empty-array key or theme lost:\n%s", got)
	}
}

// taggedServer builds a Podium-owned mcpServers fragment for server name keyed
// by id, mirroring adapter.mcpFragmentJSON: the entry carries no in-entry tag and
// ownership lives in the top-level index.
func taggedServer(name, id string) []byte {
	return []byte(`{"mcpServers":{"` + name + `":{"command":"cmd-` + name + `"}},"` +
		adapter.PodiumIndexKey + `":{"` + id + `":["mcpServers","` + name + `"]}}`)
}

// §6.7 config-merge contract for a keyed map (mcpServers): the entry carries no
// in-entry tag, ownership is tracked by the top-level index, two servers
// accumulate, the operator's untagged server is preserved, a re-sync is
// idempotent, dropping one removes only its entry, and a legacy in-entry tag
// (written before the index existed) is still reconciled on the next sync.
func TestConfigMerge_MapIndexReconcile(t *testing.T) {
	dest := t.TempDir()
	mcp := filepath.Join(dest, ".mcp.json")
	// Operator server plus a legacy Podium entry tagged the old in-entry way.
	operator := `{"mcpServers":{"op":{"command":"op"},"legacy":{"command":"l","x-podium-id":"old/legacy"}}}`
	if err := os.WriteFile(mcp, []byte(operator), 0o644); err != nil {
		t.Fatal(err)
	}

	syncServers := func(specs ...[2]string) {
		files := make([]adapter.File, 0, len(specs))
		for _, s := range specs {
			files = append(files, adapter.File{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: taggedServer(s[0], s[1])})
		}
		if err := Write(dest, files); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	syncServers([2]string{"alpha", "s/a"}, [2]string{"bravo", "s/b"})
	got := readString(t, mcp)
	for _, want := range []string{`"op"`, `"alpha"`, `"bravo"`, `"s/a"`, `"s/b"`} {
		if !strings.Contains(got, want) {
			t.Errorf("after A+B sync, missing %q:\n%s", want, got)
		}
	}
	// The Podium entries carry no in-entry tag; the index carries the ownership.
	if strings.Contains(got, `"command":"cmd-alpha","x-podium-id"`) {
		t.Errorf("Podium map entry should not carry an in-entry tag:\n%s", got)
	}
	// The legacy in-entry-tagged entry is reconciled away (its artifact is gone).
	if strings.Contains(got, `"legacy"`) {
		t.Errorf("legacy in-entry-tagged entry should have been stripped:\n%s", got)
	}

	// Re-sync the same set: idempotent (no duplicate server entries). Count the
	// command value, which is unique to the server entry (the name also appears
	// in the index path).
	syncServers([2]string{"alpha", "s/a"}, [2]string{"bravo", "s/b"})
	got = readString(t, mcp)
	if n := strings.Count(got, `"cmd-alpha"`); n != 1 {
		t.Errorf("re-sync duplicated alpha (count=%d):\n%s", n, got)
	}

	// Drop bravo: only its entry and index record are removed; alpha and the
	// operator server remain.
	syncServers([2]string{"alpha", "s/a"})
	got = readString(t, mcp)
	if strings.Contains(got, "bravo") || strings.Contains(got, "s/b") {
		t.Errorf("removed server bravo still present:\n%s", got)
	}
	for _, want := range []string{`"op"`, `"alpha"`, `"s/a"`} {
		if !strings.Contains(got, want) {
			t.Errorf("after removing bravo, missing %q:\n%s", want, got)
		}
	}
}

// stripPodiumBlocks removes only the Podium-managed inject blocks and leaves
// the operator's surrounding content intact.
func TestStripPodiumBlocks_PreservesOperatorContent(t *testing.T) {
	md := commentStyleFor("AGENTS.md")
	base := injectBlock([]byte("# House rules\n\nKeep PRs small.\n"), "team/a", []byte("Rule A."), md)
	base = injectBlock(base, "team/b", []byte("Rule B."), md)

	out := string(stripPodiumBlocks(base, md))
	if !strings.Contains(out, "Keep PRs small.") || !strings.Contains(out, "# House rules") {
		t.Errorf("operator content lost:\n%s", out)
	}
	for _, gone := range []string{"podium:begin:team/a", "podium:begin:team/b", "Rule A.", "Rule B."} {
		if strings.Contains(out, gone) {
			t.Errorf("Podium block content %q survived the strip:\n%s", gone, out)
		}
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
