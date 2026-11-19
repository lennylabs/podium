package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/audit"
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
		{ID: "finance/invoice", Type: "agent", Layer: "acme-finance", Materialized: true},
		{ID: "finance/experimental/new", Type: "skill", Layer: "acme-finance", Materialized: false},
		{ID: "personal/note", Type: "agent", Layer: "alice-personal", Materialized: false},
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
		testharness.WriteTreeOption{Path: "team-shared/company-glossary/ARTIFACT.md", Content: "---\ntype: agent\nversion: 1.0.0\ndescription: glossary\n---\n\nBody.\n"},
	)
	target := t.TempDir()
	// View is sorted by ID; the lone artifact is leaf 1.
	withInteractiveStdin(t, "1\nsave\n", true)

	withCwd(t, t.TempDir(), func() {
		withStderr(t, func() {
			captureStdout(t, func() {
				if rc := runSyncOverrideInteractive(target, registry, "none", false, sync.OnUnparseableKeep); rc != 0 {
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

// Spec: §7.2.2, §7.5.5 — the interactive podium sync override resolves its
// harness once and sends it on every registry request: the view the checklist
// renders and the re-materialization after save both report it, so the view
// requests sent before the apply carry the harness too. An empty flag on a
// target with no lock falls back to none.
func TestRunSyncOverrideInteractive_SendsResolvedHarness(t *testing.T) {
	cases := []struct {
		name, harnessFlag, wantHarness string
	}{
		{name: "harness flag", harnessFlag: "cursor", wantHarness: "cursor"},
		{name: "no harness flag", harnessFlag: "", wantHarness: "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newClientRecordingRegistry(t)
			t.Setenv("PODIUM_VERIFY_SIGNATURES", "never")
			t.Setenv("PODIUM_HARNESS", "")
			t.Setenv("HOME", t.TempDir())
			withInteractiveStdin(t, "1\nsave\n", true)

			withCwd(t, t.TempDir(), func() {
				withStderr(t, func() {
					captureStdout(t, func() {
						if rc := runSyncOverrideInteractive(t.TempDir(), reg.srv.URL, tc.harnessFlag, false, sync.OnUnparseableKeep); rc != 0 {
							t.Errorf("rc = %d, want 0", rc)
						}
					})
				})
			})

			reqs := reg.seen()
			manifests := 0
			for _, req := range reqs {
				if req.path == "/v1/sync/manifest" {
					manifests++
				}
				got := audit.ParseClientHeader(req.client)
				if got == nil || got.Name != "cli" || got.Purpose != "sync" || got.Harness != tc.wantHarness {
					t.Errorf("%s X-Podium-Client %q parsed to %+v, want name cli, purpose sync, and harness %q", req.path, req.client, got, tc.wantHarness)
				}
			}
			// One manifest read serves the checklist view and one serves the
			// Override re-materialization.
			if manifests < 2 {
				t.Errorf("/v1/sync/manifest requests = %d, want at least 2", manifests)
			}
		})
	}
}

// clientRecordingRegistry is a server-source registry stub that serves one
// agent artifact and records the path and X-Podium-Client header of every
// request.
type clientRecordingRegistry struct {
	srv *httptest.Server

	// mu guards reqs, which the handler goroutines append to while the test
	// reads it.
	mu   gosync.Mutex
	reqs []recordedRequest
}

// recordedRequest is one request a clientRecordingRegistry received.
type recordedRequest struct {
	path, client string
}

// newClientRecordingRegistry serves /v1/sync/manifest and a sealed
// /v1/load_artifact response for company-glossary.
func newClientRecordingRegistry(t *testing.T) *clientRecordingRegistry {
	t.Helper()
	reg := &clientRecordingRegistry{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync/manifest", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"artifacts": []map[string]string{
			{"id": "company-glossary", "type": "agent", "version": "1.0.0", "layer": "team-shared"},
		}})
	})
	mux.HandleFunc("/v1/load_artifact", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, testharness.SealDelivery(map[string]any{
			"id": "company-glossary", "type": "agent", "layer": "team-shared",
			"frontmatter": "---\ntype: agent\nversion: 1.0.0\ndescription: glossary\n---\n\nBody.\n",
		}))
	})
	reg.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		reg.reqs = append(reg.reqs, recordedRequest{path: r.URL.Path, client: r.Header.Get(audit.ClientHeader)})
		reg.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(reg.srv.Close)
	return reg
}

// seen returns a copy of the requests recorded so far.
func (r *clientRecordingRegistry) seen() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.reqs...)
}

// writeTestJSON writes v as a JSON response body.
func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// Spec: §13.11.3
// Matrix: §6.10 (ingest.collision)
// TestRunSyncOverrideInteractive_CollisionExits1 pins that applying a toggle
// from the TUI over a registry whose layers collide reports
// the dropped artifact on standard error and exits 1, with the toggle kept.
func TestRunSyncOverrideInteractive_CollisionExits1(t *testing.T) {
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\nlayer_order:\n  - base\n  - high\n"},
		testharness.WriteTreeOption{Path: "base/shared/note/ARTIFACT.md", Content: "---\ntype: agent\nversion: 1.0.0\ndescription: from-base\n---\n\nfrom-base\n"},
		testharness.WriteTreeOption{Path: "high/shared/note/ARTIFACT.md", Content: "---\ntype: agent\nversion: 1.0.0\ndescription: from-high\n---\n\nfrom-high\n"},
	)
	target := t.TempDir()
	withInteractiveStdin(t, "1\nsave\n", true)

	var stderr string
	withCwd(t, t.TempDir(), func() {
		stderr = captureStderr(t, func() {
			captureStdout(t, func() {
				if rc := runSyncOverrideInteractive(target, registry, "none", false, sync.OnUnparseableKeep); rc != 1 {
					t.Errorf("rc = %d, want 1", rc)
				}
			})
		})
	})

	if !strings.Contains(stderr, "rejected: shared/note (ingest.collision): cross-layer collision") {
		t.Errorf("stderr missing the rejected line:\n%s", stderr)
	}
	if !strings.Contains(readLockString(t, target), "shared/note") {
		t.Errorf("lock toggles.add missing the checked artifact")
	}
	got, err := os.ReadFile(filepath.Join(target, "shared", "note", "ARTIFACT.md"))
	if err != nil {
		t.Fatalf("lower copy not materialized: %v", err)
	}
	if !strings.Contains(string(got), "from-base") {
		t.Errorf("materialized copy = %s, want the lower layer's", got)
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

// listingOverrideFixture writes a filesystem registry holding one skill and a
// workspace whose sync.yaml sets the claude-code listing_budgets entry to
// entry. It isolates HOME and the PODIUM_* harness and registry variables so
// only the workspace scope applies, and returns the registry and workspace.
func listingOverrideFixture(t *testing.T, entry string) (registry, ws string) {
	t.Helper()
	registry = t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team/docs/writer/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\ndescription: Draft release notes from merged pull requests.\n---\n\n"},
		testharness.WriteTreeOption{Path: "team/docs/writer/SKILL.md",
			Content: "---\nname: writer\ndescription: Draft release notes from merged pull requests.\n---\n\nBody.\n"},
	)
	ws = t.TempDir()
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{
		Path:    ".podium/sync.yaml",
		Content: "listing_budgets:\n  claude-code:\n" + entry,
	})
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_HARNESS", "")
	t.Setenv("PODIUM_REGISTRY", "")
	return registry, ws
}

// runInteractiveOverride runs runSyncOverrideInteractive from ws with the
// scripted input and returns its exit code, standard output, and standard
// error.
func runInteractiveOverride(t *testing.T, ws, script, target, registry string, dryRun bool) (rc int, stdout, stderr string) {
	t.Helper()
	withInteractiveStdin(t, script, true)
	withCwd(t, ws, func() {
		stderr = captureStderr(t, func() {
			stdout = captureStdout(t, func() {
				rc = runSyncOverrideInteractive(target, registry, "claude-code", dryRun, sync.OnUnparseableKeep)
			})
		})
	})
	return rc, stdout, stderr
}

// Spec: §7.5.8, §7.5.5 — a toggle applied from the checklist re-materializes
// the target, and a listing over its budget under `check: error` writes the
// listing line on standard error and exits 1 with the toggle kept. The
// skill's cost is len("writer") + 46 description code points + 5 = 57.
func TestRunSyncOverrideInteractive_ListingOverBudgetExits1(t *testing.T) {
	registry, ws := listingOverrideFixture(t, "    max_chars: 10\n    check: error\n")
	target := t.TempDir()

	rc, _, stderr := runInteractiveOverride(t, ws, "1\nsave\n", target, registry, false)
	if rc != 1 {
		t.Errorf("rc = %d, want 1\nstderr:\n%s", rc, stderr)
	}
	want := "listing: claude-code listing estimate is 57 characters against a configured budget of 10 (1 entries)"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
	if strings.Contains(stderr, "warning: listing:") {
		t.Errorf("check: error line carries the warning prefix:\n%s", stderr)
	}
	if !strings.Contains(readLockString(t, target), "docs/writer") {
		t.Errorf("lock toggles.add missing the checked artifact")
	}
}

// Spec: §7.5.8, §7.5.5, §7.5.2 — a re-materializing override exits 2 on an
// invalid listing_budgets entry for its harness before it records a toggle.
// An override that does not re-materialize validates no entry: --dry-run, a
// quit checklist, and a save with no toggle exit 0.
func TestRunSyncOverrideInteractive_InvalidListingBudgetExits2(t *testing.T) {
	registry, ws := listingOverrideFixture(t, "    check: maybe\n")
	const keyErr = "listing_budgets.claude-code.check"

	t.Run("re-materializes", func(t *testing.T) {
		target := t.TempDir()
		rc, _, stderr := runInteractiveOverride(t, ws, "1\nsave\n", target, registry, false)
		if rc != 2 {
			t.Errorf("rc = %d, want 2", rc)
		}
		if !strings.Contains(stderr, keyErr) {
			t.Errorf("stderr does not name %s:\n%s", keyErr, stderr)
		}
		if _, err := os.Stat(filepath.Join(target, ".podium", "sync.lock")); !os.IsNotExist(err) {
			t.Errorf("a toggle was recorded: sync.lock stat err = %v", err)
		}
	})
	t.Run("dry-run", func(t *testing.T) {
		rc, _, stderr := runInteractiveOverride(t, ws, "1\nsave\n", t.TempDir(), registry, true)
		if rc != 0 || strings.Contains(stderr, keyErr) {
			t.Errorf("rc = %d, want 0 with no %s error\nstderr:\n%s", rc, keyErr, stderr)
		}
	})
	t.Run("quit", func(t *testing.T) {
		rc, _, stderr := runInteractiveOverride(t, ws, "1\nquit\n", t.TempDir(), registry, false)
		if rc != 0 || strings.Contains(stderr, keyErr) {
			t.Errorf("rc = %d, want 0 with no %s error\nstderr:\n%s", rc, keyErr, stderr)
		}
		if !strings.Contains(stderr, "override: cancelled; no changes written") {
			t.Errorf("stderr missing the cancel line:\n%s", stderr)
		}
	})
	t.Run("save with no toggle", func(t *testing.T) {
		rc, stdout, stderr := runInteractiveOverride(t, ws, "save\n", t.TempDir(), registry, false)
		if rc != 0 || strings.Contains(stderr, keyErr) {
			t.Errorf("rc = %d, want 0 with no %s error\nstderr:\n%s", rc, keyErr, stderr)
		}
		if !strings.Contains(stdout, "(no change)") {
			t.Errorf("stdout missing (no change):\n%s", stdout)
		}
	})
}
