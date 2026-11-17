package sync_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sync"
)

// listingWorkspace returns a workspace with a .podium directory and an empty
// home directory, for the listing_budgets scope tests.
func listingWorkspace(t *testing.T) (ws, home string) {
	t.Helper()
	ws, home = t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	return ws, home
}

// writePodiumFile writes name under ws/.podium and returns its path.
func writePodiumFile(t *testing.T, ws, name, content string) string {
	t.Helper()
	p := filepath.Join(ws, ".podium", name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Spec: §7.5.2, §7.5.8 — listing_budgets entries merge by adapter value, and
// within an entry each key resolves from the highest-precedence scope that
// sets it; a key no scope sets takes the §7.5.8 default.
func TestLoadMergedConfig_ListingBudgetsPerKey(t *testing.T) {
	t.Parallel()
	ws, home := listingWorkspace(t)
	writePodiumFile(t, ws, "sync.yaml", `listing_budgets:
  claude-code:
    max_chars: 12000
    entry_overhead_chars: 12
    types: [skill, agent]
    check: error
`)
	writePodiumFile(t, ws, "sync.local.yaml", "listing_budgets:\n  claude-code:\n    check: off\n")
	merged, _, err := sync.LoadMergedConfig(ws, home)
	if err != nil {
		t.Fatalf("LoadMergedConfig: %v", err)
	}
	got, err := merged.ListingBudgetFor("claude-code", adapter.DefaultRegistry())
	if err != nil {
		t.Fatalf("ListingBudgetFor: %v", err)
	}
	want := sync.EffectiveListingBudget{
		MaxChars: 12000, EntryOverheadChars: 12, MaxEntryChars: 0,
		Types: []string{"skill", "agent"}, Check: "off",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListingBudgetFor = %+v, want %+v", got, want)
	}
}

// Spec: §7.5.2 — the readers that do not decode listing_budgets load a file
// whose block holds a value of the wrong YAML type, keeping the rest of the
// file, while LoadMergedConfig fails naming the file and listing_budgets.
func TestReadConfig_MistypedListingBudgetsLoads(t *testing.T) {
	t.Parallel()
	ws, home := listingWorkspace(t)
	path := writePodiumFile(t, ws, "sync.yaml", `defaults:
  registry: https://podium.acme.com
listing_budgets:
  claude-code:
    max_chars: abc
`)
	cfg, err := sync.ReadConfig(ws)
	if err != nil || cfg == nil || cfg.Defaults.Registry != "https://podium.acme.com" {
		t.Errorf("ReadConfig = %+v, %v; want the registry intact", cfg, err)
	}
	cfg, err = sync.ReadConfigFile(path)
	if err != nil || cfg == nil || cfg.Defaults.Registry != "https://podium.acme.com" {
		t.Errorf("ReadConfigFile = %+v, %v; want the registry intact", cfg, err)
	}
	_, _, err = sync.LoadMergedConfig(ws, home)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "listing_budgets") {
		t.Errorf("LoadMergedConfig error = %v, want one naming %s and listing_budgets", err, path)
	}
}

// Spec: §7.5.2 — a listing_budgets value whose resolved YAML type does not
// match its key fails the merged load, whether written in place or reached
// through an alias or a << merge; a null leaves the key unset; a key outside
// the schema is ignored.
func TestLoadMergedConfig_ListingBudgetsMistyped(t *testing.T) {
	t.Parallel()
	reg := adapter.DefaultRegistry()
	mistyped := []struct {
		name, doc, key string
	}{
		{"word", "listing_budgets:\n  claude-code:\n    max_chars: abc\n", "max_chars"},
		{"float", "listing_budgets:\n  claude-code:\n    max_chars: 1.5\n", "max_chars"},
		{"small float", "listing_budgets:\n  claude-code:\n    max_chars: 0.5\n", "max_chars"},
		{"int check", "listing_budgets:\n  claude-code:\n    check: 1\n", "check"},
		{"bool check", "listing_budgets:\n  claude-code:\n    check: true\n", "check"},
		{"scalar types", "listing_budgets:\n  claude-code:\n    types: skill\n", "types"},
		{"int item", "listing_budgets:\n  claude-code:\n    types: [1]\n", "types"},
		{"merge", "x-base: &base {max_chars: 1.5}\nlisting_budgets:\n  claude-code:\n    <<: *base\n", "max_chars"},
		{"value alias", "x-half: &half 1.5\nlisting_budgets:\n  claude-code:\n    max_chars: *half\n", "max_chars"},
		{"entry alias", "x-base: &base {max_chars: 1.5}\nlisting_budgets:\n  claude-code: *base\n", "max_chars"},
		{"block alias", "x-all: &all {claude-code: {max_chars: 1.5}}\nlisting_budgets: *all\n", "max_chars"},
	}
	for _, tc := range mistyped {
		ws, home := listingWorkspace(t)
		path := writePodiumFile(t, ws, "sync.local.yaml", tc.doc)
		_, _, err := sync.LoadMergedConfig(ws, home)
		if err == nil {
			t.Errorf("%s: LoadMergedConfig loaded, want an error", tc.name)
			continue
		}
		for _, want := range []string{path, "listing_budgets", tc.key} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not name %q", tc.name, err, want)
			}
		}
	}

	loads := []struct {
		name, doc string
		want      sync.EffectiveListingBudget
	}{
		{"null", "listing_budgets:\n  claude-code:\n    max_chars: ~\n", sync.DefaultListingBudget()},
		{"off and underscores", "listing_budgets:\n  claude-code:\n    check: off\n    max_chars: 1_000\n    unknown_key: [1]\n",
			sync.EffectiveListingBudget{MaxChars: 1000, EntryOverheadChars: 5, Types: []string{"skill"}, Check: "off"}},
		{"valid merge", "x-ok: &ok {max_chars: 12000}\nlisting_budgets:\n  claude-code:\n    <<: *ok\n    check: error\n",
			sync.EffectiveListingBudget{MaxChars: 12000, EntryOverheadChars: 5, Types: []string{"skill"}, Check: "error"}},
	}
	for _, tc := range loads {
		ws, home := listingWorkspace(t)
		writePodiumFile(t, ws, "sync.local.yaml", tc.doc)
		merged, _, err := sync.LoadMergedConfig(ws, home)
		if err != nil {
			t.Errorf("%s: LoadMergedConfig: %v", tc.name, err)
			continue
		}
		got, err := merged.ListingBudgetFor("claude-code", reg)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ListingBudgetFor = %+v, %v; want %+v", tc.name, got, err, tc.want)
		}
	}
}

// Spec: §13.11.2 / §7.5.2 — relative `defaults.registry`
// resolves against the workspace.
func TestResolveRegistryPath_RelativeUsesWorkspace(t *testing.T) {
	t.Parallel()
	got := sync.ResolveRegistryPath("/home/dev/project", "./.podium/registry/")
	want := "/home/dev/project/.podium/registry"
	if filepath.Clean(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Spec: §13.11.2 — absolute `defaults.registry` passes through.
func TestResolveRegistryPath_AbsolutePassesThrough(t *testing.T) {
	t.Parallel()
	got := sync.ResolveRegistryPath("/home/dev/project", "/opt/podium-artifacts/")
	if got != "/opt/podium-artifacts" {
		t.Errorf("got %q, want /opt/podium-artifacts", got)
	}
}

// Spec: §13.11.2 — http(s):// URLs pass through unchanged so the
// caller can distinguish filesystem from server source by
// inspecting the scheme.
func TestResolveRegistryPath_URLPassesThrough(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"https://podium.example/", "http://localhost:8080"} {
		if got := sync.ResolveRegistryPath("/ws", in); got != in {
			t.Errorf("got %q for %q, want passthrough", got, in)
		}
	}
}

// Spec: §13.11.2 — empty input returns empty (callers detect
// "not configured" and surface config.no_registry separately).
func TestResolveRegistryPath_EmptyReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := sync.ResolveRegistryPath("/ws", ""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
