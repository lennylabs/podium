package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sync"
)

// ---- pure helpers -------------------------------------------------------

func TestDomainOf(t *testing.T) {
	cases := map[string]string{
		"finance/invoice":      "finance",
		"finance/ap/legacy":    "finance",
		"toplevel":             "(root)",
		"personal/notes/thing": "personal",
	}
	for id, want := range cases {
		if got := domainOf(id); got != want {
			t.Errorf("domainOf(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	cmd, arg := splitCommand("add-include  finance/**")
	if cmd != "add-include" || arg != "finance/**" {
		t.Errorf("splitCommand = %q/%q", cmd, arg)
	}
	if c, a := splitCommand("save"); c != "save" || a != "" {
		t.Errorf("splitCommand(save) = %q/%q", c, a)
	}
}

func TestAppendUnique(t *testing.T) {
	got := appendUnique([]string{"a", "b"}, "a")
	if len(got) != 2 {
		t.Errorf("appendUnique kept a duplicate: %v", got)
	}
	got = appendUnique(got, "c")
	if len(got) != 3 || got[2] != "c" {
		t.Errorf("appendUnique = %v", got)
	}
}

func TestDiffStrings(t *testing.T) {
	added, removed := diffStrings([]string{"a", "b"}, []string{"b", "c"})
	if strings.Join(added, ",") != "c" {
		t.Errorf("added = %v, want [c]", added)
	}
	if strings.Join(removed, ",") != "a" {
		t.Errorf("removed = %v, want [a]", removed)
	}
}

// ---- override checklist (§7.5.5) ---------------------------------------

func sampleView() []sync.EffectiveArtifact {
	return []sync.EffectiveArtifact{
		{ID: "finance/invoice", Type: "context", Layer: "acme-finance", Materialized: true},
		{ID: "finance/experimental/new", Type: "skill", Layer: "acme-finance", Materialized: false},
		{ID: "personal/note", Type: "context", Layer: "alice-personal", Materialized: false},
	}
}

// spec: §7.5.5 — checking an unmaterialized leaf records it as an add; the tree
// is rendered so the operator can see the leaf numbers.
func TestRunOverrideTUI_ToggleAddAndSave(t *testing.T) {
	var out bytes.Buffer
	// Toggle leaf 3 (personal/note, currently unmaterialized) then save.
	res := runOverrideTUI(strings.NewReader("3\nsave\n"), &out, sampleView(), false)
	if !res.Apply {
		t.Fatal("Apply = false, want true after save")
	}
	if strings.Join(res.Add, ",") != "personal/note" {
		t.Errorf("Add = %v, want [personal/note]", res.Add)
	}
	if len(res.Remove) != 0 {
		t.Errorf("Remove = %v, want empty", res.Remove)
	}
	if !strings.Contains(out.String(), "finance/invoice") {
		t.Errorf("tree did not render the effective view:\n%s", out.String())
	}
}

// spec: §7.5.5 — unchecking a materialized leaf records it as a remove.
func TestRunOverrideTUI_ToggleRemoveAndSave(t *testing.T) {
	var out bytes.Buffer
	// Toggle leaf 1 (finance/invoice, materialized) off, then save.
	res := runOverrideTUI(strings.NewReader("1\nsave\n"), &out, sampleView(), false)
	if !res.Apply {
		t.Fatal("Apply = false, want true")
	}
	if strings.Join(res.Remove, ",") != "finance/invoice" {
		t.Errorf("Remove = %v, want [finance/invoice]", res.Remove)
	}
	if len(res.Add) != 0 {
		t.Errorf("Add = %v, want empty", res.Add)
	}
}

// spec: §7.5.5 — quitting discards the diff.
func TestRunOverrideTUI_QuitDiscards(t *testing.T) {
	var out bytes.Buffer
	res := runOverrideTUI(strings.NewReader("3\nquit\n"), &out, sampleView(), false)
	if res.Apply {
		t.Errorf("Apply = true, want false after quit")
	}
	if len(res.Add)+len(res.Remove) != 0 {
		t.Errorf("quit produced a diff: add=%v remove=%v", res.Add, res.Remove)
	}
}

// spec: §7.5.5 — an empty (EOF) input from a non-terminal applies nothing and
// records zero commands so the caller can print actionable guidance.
func TestRunOverrideTUI_EmptyEOF(t *testing.T) {
	var out bytes.Buffer
	res := runOverrideTUI(strings.NewReader(""), &out, sampleView(), false)
	if res.Apply || res.Commands != 0 {
		t.Errorf("empty input: Apply=%v Commands=%d, want false/0", res.Apply, res.Commands)
	}
}

// spec: §7.5.5 — all/none bulk toggles, expand/collapse, and an unrecognized
// command are handled without crashing and without aborting the loop.
func TestRunOverrideTUI_BulkAndCollapse(t *testing.T) {
	var out bytes.Buffer
	res := runOverrideTUI(strings.NewReader("none\ncollapse finance\nexpand finance\nbogus\nall\nsave\n"), &out, sampleView(), false)
	if !res.Apply {
		t.Fatal("Apply = false, want true")
	}
	// "all" selects every leaf; finance/invoice was already materialized so it
	// is not re-added; the two unmaterialized leaves become adds.
	if strings.Join(res.Add, ",") != "finance/experimental/new,personal/note" {
		t.Errorf("Add = %v", res.Add)
	}
	if !strings.Contains(out.String(), "collapsed") {
		t.Errorf("collapse did not render a summary:\n%s", out.String())
	}
}

func TestRunOverrideTUI_EmptyView(t *testing.T) {
	var out bytes.Buffer
	res := runOverrideTUI(strings.NewReader("save\n"), &out, nil, false)
	if !res.Apply || len(res.Add)+len(res.Remove) != 0 {
		t.Errorf("empty view save: apply=%v add=%v remove=%v", res.Apply, res.Add, res.Remove)
	}
	if !strings.Contains(out.String(), "no artifacts visible") {
		t.Errorf("empty view not announced:\n%s", out.String())
	}
}

// ---- profile editor (§7.5.7) -------------------------------------------

// spec: §7.5.7 — add-include then save returns the augmented include list.
func TestRunProfileEditTUI_AddIncludeSave(t *testing.T) {
	var out bytes.Buffer
	cur := sync.Profile{Include: []string{"finance/**"}}
	res := runProfileEditTUI(strings.NewReader("add-include shared/policies/*\nsave\n"), &out, "team", cur, false)
	if !res.Apply {
		t.Fatal("Apply = false, want true")
	}
	if strings.Join(res.Include, ",") != "finance/**,shared/policies/*" {
		t.Errorf("Include = %v", res.Include)
	}
}

// spec: §7.5.7 — remove-include drops the entry at the 1-based index.
func TestRunProfileEditTUI_RemoveIncludeByIndex(t *testing.T) {
	var out bytes.Buffer
	cur := sync.Profile{Include: []string{"a", "b", "c"}}
	res := runProfileEditTUI(strings.NewReader("remove-include 2\nsave\n"), &out, "team", cur, false)
	if strings.Join(res.Include, ",") != "a,c" {
		t.Errorf("Include = %v, want [a c]", res.Include)
	}
}

// spec: §7.5.7 — add-exclude records an exclude pattern; quit discards.
func TestRunProfileEditTUI_QuitDiscards(t *testing.T) {
	var out bytes.Buffer
	cur := sync.Profile{}
	res := runProfileEditTUI(strings.NewReader("add-exclude drafts/*\nquit\n"), &out, "team", cur, false)
	if res.Apply {
		t.Errorf("Apply = true, want false after quit")
	}
}

func TestRunProfileEditTUI_EmptyEOF(t *testing.T) {
	var out bytes.Buffer
	res := runProfileEditTUI(strings.NewReader(""), &out, "team", sync.Profile{}, false)
	if res.Apply || res.Commands != 0 {
		t.Errorf("empty input: Apply=%v Commands=%d", res.Apply, res.Commands)
	}
}

// ---- CLI integration ----------------------------------------------------

func withInteractiveStdin(t *testing.T, script string, tty bool) {
	t.Helper()
	prevIn, prevTTY := interactiveStdin, interactiveIsTerminal
	interactiveStdin = strings.NewReader(script)
	interactiveIsTerminal = func() bool { return tty }
	t.Cleanup(func() { interactiveStdin = prevIn; interactiveIsTerminal = prevTTY })
}

// spec: §7.5.7 — `podium profile edit team` (no batch flags) opens the editor;
// scripted commands write the same result the --add-include flag would.
func TestRunProfileEditInteractive_WritesEdits(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir) // profile "team" with empty include/exclude
	withInteractiveStdin(t, "add-include personal/*\nadd-exclude drafts/*\nsave\n", true)

	withStderr(t, func() {
		captureStdout(t, func() {
			if rc := runProfileEditInteractive("team", dir, false); rc != 0 {
				t.Errorf("rc = %d, want 0", rc)
			}
		})
	})
	body, err := os.ReadFile(filepath.Join(dir, ".podium", "sync.yaml"))
	if err != nil {
		t.Fatalf("read sync.yaml: %v", err)
	}
	for _, want := range []string{"personal/*", "drafts/*"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("sync.yaml missing %q:\n%s", want, body)
		}
	}
}

// spec: §7.5.7 — a non-terminal invocation with no scripted commands prints
// guidance and writes nothing rather than blocking.
func TestRunProfileEditInteractive_NonTerminalGuidance(t *testing.T) {
	dir := t.TempDir()
	writeSyncYAML(t, dir)
	withInteractiveStdin(t, "", false)

	var rc int
	stderr := captureStderr(t, func() {
		captureStdout(t, func() { rc = runProfileEditInteractive("team", dir, false) })
	})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(stderr, "needs a terminal or piped commands") {
		t.Errorf("missing guidance: %s", stderr)
	}
}

// spec: §7.5.5 — `podium sync override` (no batch flags) checklist applied to a
// filesystem source materializes the checked artifact and records it in
// toggles.add, identical to `--add`.
func TestRunSyncOverrideInteractive_AddsAndMaterializes(t *testing.T) {
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team-shared/company-glossary/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\ndescription: glossary\n---\n\nBody.\n"},
	)
	target := t.TempDir()
	// View is sorted by ID; the lone artifact is leaf 1.
	withInteractiveStdin(t, "1\nsave\n", true)

	withCwd(t, t.TempDir(), func() {
		withStderr(t, func() {
			captureStdout(t, func() {
				if rc := runSyncOverrideInteractive(target, registry, "none", false); rc != 0 {
					t.Errorf("rc = %d, want 0", rc)
				}
			})
		})
	})

	lock := readLockString(t, target)
	if !strings.Contains(lock, "company-glossary") {
		t.Errorf("lock toggles.add missing the checked artifact:\n%s", lock)
	}
	if _, err := os.Stat(filepath.Join(target, "company-glossary", "ARTIFACT.md")); err != nil {
		t.Errorf("checked artifact not materialized: %v", err)
	}
}

func readLockString(t *testing.T, target string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(target, ".podium", "sync.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	return string(b)
}
