package e2e

// Harness listing budget in `podium sync`, end to end.
//
// Spec §7.5.8 defines the listing estimate `podium sync` computes for each
// `kind: workspace` target, its defaults, the `listing` member of the --json
// envelope, and the effect of `check: off | warning | error` on the exit
// status. Spec §7.5.2 defines the `listing_budgets` block of sync.yaml, which
// entries are invalid, and which commands exit 2 or 1 on an invalid entry or a
// file that does not load. These tests drive the real `podium` binary. Unless
// a case states its own configuration, the workspace sync.yaml sets a
// claude-code entry with `max_chars: 200`, which the listing fixture exceeds.

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	podsync "github.com/lennylabs/podium/pkg/sync"
)

// listingSkill is one skill of the listing fixture.
type listingSkill struct {
	id   string
	desc string
}

// listingSkills is the listing fixture. ops/delta's description is exactly 20
// code points, so `max_entry_chars: 20` reports the other three and not it.
// ops/bravo's description holds a non-ASCII letter, so the cost counts code
// points rather than bytes.
var listingSkills = []listingSkill{
	{"ops/alpha", "Reconcile vendor invoices against purchase orders before the month-end close."},
	{"ops/bravo", "Summarize the café supplier contracts that renew within the next quarter."},
	{"ops/charlie", "Prepare the weekly cash forecast from the bank feeds and the payables ledger."},
	{"ops/delta", "Draft weekly report."},
}

// listingEnv blanks the ambient registry and harness variables so the
// workspace sync.yaml and the flags decide them.
var listingEnv = []string{"PODIUM_REGISTRY=", "PODIUM_HARNESS="}

// listingBudget200 is the claude-code entry most cases use, under check.
func listingBudget200(check string) string {
	return "    max_chars: 200\n    check: " + check + "\n"
}

// listingCost is the §7.5.8 cost of skills at the default overhead of 5: the
// code points of each name and description plus the overhead per entry.
func listingCost(skills []listingSkill) int {
	cost := 0
	for _, s := range skills {
		cost += utf8.RuneCountInString(path.Base(s.id)) + utf8.RuneCountInString(s.desc) + 5
	}
	return cost
}

// writeListingRegistry writes a filesystem registry holding skills and one
// agent artifact, which the default `types: [skill]` leaves out of the
// listing.
func writeListingRegistry(t *testing.T, skills []listingSkill) string {
	t.Helper()
	files := map[string]string{
		"finance/policy/ARTIFACT.md": agentArtifact("Travel and expense policy reference for the finance team and its approvers."),
	}
	for _, s := range skills {
		name := path.Base(s.id)
		files[s.id+"/ARTIFACT.md"] = "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
		files[s.id+"/SKILL.md"] = "---\nname: " + name + "\ndescription: " + s.desc + "\n---\n\nFollow the " + name + " procedure.\n"
	}
	return writeRegistry(t, files)
}

// listingWorkspace writes a workspace whose sync.yaml sets defaults.registry
// to reg and holds entry as the claude-code `listing_budgets` entry, and
// returns the workspace directory.
func listingWorkspace(t *testing.T, reg, entry string) string {
	t.Helper()
	ws := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\nlisting_budgets:\n  claude-code:\n"+entry)
	return ws
}

// writeLocalConfig writes ws/.podium/sync.local.yaml.
func writeLocalConfig(t *testing.T, ws, content string) {
	t.Helper()
	solofsWriteFile(t, filepath.Join(ws, ".podium", "sync.local.yaml"), content)
}

// listingMember is the §7.5.8 `listing` member of the sync --json envelope.
type listingMember struct {
	Harness     string `json:"harness"`
	BudgetChars int    `json:"budget_chars"`
	CostChars   int    `json:"cost_chars"`
	Entries     int    `json:"entries"`
	Over        bool   `json:"over"`
	Check       string `json:"check"`
	Oversized   []struct {
		ID    string `json:"id"`
		Chars int    `json:"chars"`
	} `json:"oversized"`
}

// syncEnvelope is the part of the sync --json envelope these tests read.
type syncEnvelope struct {
	Harness string         `json:"harness"`
	Listing *listingMember `json:"listing"`
}

// decodeSyncEnvelope decodes s as exactly one JSON object.
func decodeSyncEnvelope(t *testing.T, s string) syncEnvelope {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var env syncEnvelope
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("decode sync envelope: %v\n%s", err, s)
	}
	if rest := strings.TrimSpace(s[dec.InputOffset():]); rest != "" {
		t.Fatalf("output carries more than the envelope; trailing %q", rest)
	}
	return env
}

// targetEnvelopes splits the standard output of a `--config --json` run into
// the envelope each `== target <id> ==` header precedes, keyed by target ID.
func targetEnvelopes(t *testing.T, stdout string) map[string]syncEnvelope {
	t.Helper()
	out := map[string]syncEnvelope{}
	for _, sec := range strings.Split(stdout, "== target ")[1:] {
		id, body, ok := strings.Cut(sec, " ==\n")
		if !ok {
			t.Fatalf("malformed target header in:\n%s", stdout)
		}
		out[id] = decodeSyncEnvelope(t, body)
	}
	return out
}

// overLine is the standard-error line a claude-code run over the listing
// fixture writes against budget, with prefix "warning: " under check: warning.
func overLine(prefix string, cost, budget, entries int) string {
	return fmt.Sprintf("%slisting: claude-code listing estimate is %d characters against a configured budget of %d (%d entries)",
		prefix, cost, budget, entries)
}

// assertOverBudget checks a claude-code listing member over the fixture
// against a budget of 200 under check.
func assertOverBudget(t *testing.T, l *listingMember, check string) {
	t.Helper()
	if l == nil {
		t.Fatal("listing = null, want a report")
	}
	if l.Harness != "claude-code" || l.BudgetChars != 200 || l.CostChars != listingCost(listingSkills) ||
		l.Entries != len(listingSkills) || !l.Over || l.Check != check || len(l.Oversized) != 0 {
		t.Errorf("listing = %+v, want claude-code budget 200 cost %d entries %d over check %s no oversized",
			*l, listingCost(listingSkills), len(listingSkills), check)
	}
}

// assertEmptyDir fails when dir holds any file.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	if files := readTreeAll(t, dir); len(files) != 0 {
		t.Errorf("%s holds %v, want nothing written", dir, keysOf(files))
	}
}

// Spec: §7.5.8 — a dry run over the budget writes the listing line on
// standard error and `listing.over: true` in the envelope; check: warning
// keeps exit 0 and check: error exits 1.
func TestListingBudget_DryRunJSONOverBudget(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	for _, tc := range []struct {
		check  string
		exit   int
		prefix string
	}{
		{podsync.ListingCheckWarning, 0, "warning: "},
		{podsync.ListingCheckError, 1, ""},
	} {
		t.Run(tc.check, func(t *testing.T) {
			t.Parallel()
			ws := listingWorkspace(t, reg, listingBudget200(tc.check))
			res := runPodium(t, ws, listingEnv, "sync", "--harness", "claude-code", "--target", t.TempDir(), "--dry-run", "--json")
			cliWantExit(t, res, tc.exit, "sync --dry-run --json over budget")
			cliContains(t, res.Stderr, overLine(tc.prefix, listingCost(listingSkills), 200, len(listingSkills)), "listing line")
			if tc.prefix == "" {
				cliNotContains(t, res.Stderr, "warning: listing:", "check: error carries no warning prefix")
			}
			assertOverBudget(t, decodeSyncEnvelope(t, res.Stdout).Listing, tc.check)
			cliContains(t, res.Stdout, `"oversized": []`, "oversized renders [] when unset")
		})
	}
}

// Spec: §7.5.8 — with no listing_budgets block a sync applies the defaults:
// a budget of 8000 under check: warning, so a listing above 8000 code points
// warns and exits 0.
func TestListingBudget_DefaultBudgetWarns(t *testing.T) {
	t.Parallel()
	var bulk []listingSkill
	for i := 0; i < 30; i++ {
		bulk = append(bulk, listingSkill{
			id:   fmt.Sprintf("bulk/skill-%02d", i),
			desc: fmt.Sprintf("Bulk skill %02d walks the reviewer through ", i) + strings.Repeat("each ledger review step ", 12) + "in order.",
		})
	}
	cost := listingCost(bulk)
	if cost <= 8000 {
		t.Fatalf("fixture cost %d does not exceed the default budget", cost)
	}
	reg := writeListingRegistry(t, bulk)
	ws := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\n")
	res := runPodium(t, ws, withIsolatedHome(listingEnv, t.TempDir()), "sync", "--harness", "claude-code", "--target", t.TempDir(), "--dry-run", "--json")
	cliWantExit(t, res, 0, "sync over the default budget")
	cliContains(t, res.Stderr, overLine("warning: ", cost, 8000, len(bulk)), "default budget warning")
	l := decodeSyncEnvelope(t, res.Stdout).Listing
	if l == nil || l.BudgetChars != 8000 || l.Check != podsync.ListingCheckWarning || !l.Over || l.CostChars != cost {
		t.Errorf("listing = %+v, want budget 8000 check warning over cost %d", l, cost)
	}
}

// Spec: §7.5.8 — the check never changes what sync writes: a materializing
// sync under check: error writes the tree check: off writes, and exits 1.
func TestListingBudget_ErrorWritesSameTreeAsOff(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)

	offTarget := t.TempDir()
	off := runPodium(t, listingWorkspace(t, reg, listingBudget200(podsync.ListingCheckOff)), listingEnv,
		"sync", "--harness", "claude-code", "--target", offTarget)
	cliWantExit(t, off, 0, "sync under check: off")
	cliNotContains(t, off.Stderr, "listing:", "check: off reports no listing")

	errTarget := t.TempDir()
	failed := runPodium(t, listingWorkspace(t, reg, listingBudget200(podsync.ListingCheckError)), listingEnv,
		"sync", "--harness", "claude-code", "--target", errTarget)
	cliWantExit(t, failed, 1, "sync under check: error")
	cliContains(t, failed.Stderr, overLine("", listingCost(listingSkills), 200, len(listingSkills)), "listing line")

	offTree, errTree := readTreeFiltered(t, offTarget), readTreeFiltered(t, errTarget)
	if len(offTree) == 0 {
		t.Fatal("check: off wrote no file")
	}
	if !reflect.DeepEqual(offTree, errTree) {
		t.Errorf("check: error wrote %v, check: off wrote %v", keysOf(errTree), keysOf(offTree))
	}
}

// Spec: §7.5.8 — with max_entry_chars set, each description above it is
// reported in oversized in identifier order and on standard error, and a
// description at the bound is not.
func TestListingBudget_MaxEntryChars(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	ws := listingWorkspace(t, reg, "    max_entry_chars: 20\n")
	res := runPodium(t, ws, listingEnv, "sync", "--harness", "claude-code", "--target", t.TempDir(), "--dry-run", "--json")
	cliWantExit(t, res, 0, "sync with max_entry_chars 20 under check: warning")
	l := decodeSyncEnvelope(t, res.Stdout).Listing
	if l == nil || l.Over {
		t.Fatalf("listing = %+v, want a report within the default budget", l)
	}
	var want []string
	for _, s := range listingSkills {
		n := utf8.RuneCountInString(s.desc)
		if n <= 20 {
			cliNotContains(t, res.Stderr, "listing: "+s.id+" ", "a description at the bound is not oversized")
			continue
		}
		want = append(want, fmt.Sprintf("%s:%d", s.id, n))
		cliContains(t, res.Stderr,
			fmt.Sprintf("warning: listing: %s description is %d characters, above max_entry_chars 20\n", s.id, n), "oversized line")
	}
	var got []string
	for _, o := range l.Oversized {
		got = append(got, fmt.Sprintf("%s:%d", o.ID, o.Chars))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("oversized = %v, want %v", got, want)
	}
	cliNotContains(t, res.Stderr, "listing estimate is", "no excess within the default budget")
}

// Spec: §7.5.2 — an invalid entry for the run's harness exits 2 before any
// file is written, and `podium sync --check` reports it as a warning and
// exits 0.
func TestListingBudget_InvalidCheckValue(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	ws := listingWorkspace(t, reg, "    check: maybe\n")
	target := t.TempDir()
	res := runPodium(t, ws, listingEnv, "sync", "--harness", "claude-code", "--target", target)
	cliWantExit(t, res, 2, "sync with check: maybe")
	cliContains(t, res.Stderr, "listing_budgets.claude-code.check", "message names the key")
	assertEmptyDir(t, target)

	chk := runPodium(t, ws, listingEnv, "sync", "--check")
	cliWantExit(t, chk, 0, "sync --check with check: maybe")
	cliContains(t, chk.Stderr, "warning: sync.yaml: listing_budgets.claude-code.check", "--check warns")
}

// Spec: §7.5.2 — a sync that sets no harness runs the none adapter, so a
// listing_budgets.none entry is invalid for it: the run exits 2 and writes
// nothing.
func TestListingBudget_NoneEntryWithoutHarness(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	ws := t.TempDir()
	writeWorkspaceConfig(t, ws, "listing_budgets:\n  none:\n    max_chars: 100\n")
	target := t.TempDir()
	res := runPodium(t, ws, withIsolatedHome(listingEnv, t.TempDir()), "sync", "--registry", reg, "--target", target)
	cliWantExit(t, res, 2, "sync with no harness over a none entry")
	cliContains(t, res.Stderr, "listing_budgets.none", "message names the entry")
	cliContains(t, res.Stderr, "the none adapter feeds no harness listing", "message names the reason")
	assertEmptyDir(t, target)
}

// Spec: §7.5.2, §3.5 — `podium sync --preview` validates the entry first, so
// an invalid entry exits 2 before the preview's server-source check.
func TestListingBudget_PreviewInvalidEntry(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	ws := listingWorkspace(t, reg, "    check: maybe\n")
	res := runPodium(t, ws, listingEnv, "sync", "--harness", "claude-code", "--preview")
	cliWantExit(t, res, 2, "sync --preview with check: maybe")
	cliContains(t, res.Stderr, "listing_budgets.claude-code.check", "message names the key")
	cliNotContains(t, res.Stderr, "--preview requires a server registry URL", "validation precedes the preview branch")
}

// Spec: §7.5.2 — a value of the wrong YAML type in sync.local.yaml makes the
// file fail to load, so `podium sync` and `podium sync --check` exit 1 and
// name the file.
func TestListingBudget_LocalLoadError(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	ws := t.TempDir()
	writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\n")
	writeLocalConfig(t, ws, "listing_budgets:\n  claude-code:\n    max_chars: abc\n")
	target := t.TempDir()
	res := runPodium(t, ws, listingEnv, "sync", "--harness", "claude-code", "--target", target)
	cliWantExit(t, res, 1, "sync beside a mistyped sync.local.yaml")
	cliContains(t, res.Stderr, "sync.local.yaml: listing_budgets", "load error names the file")
	assertEmptyDir(t, target)

	chk := runPodium(t, ws, listingEnv, "sync", "--check")
	cliWantExit(t, chk, 1, "sync --check beside a mistyped sync.local.yaml")
	cliContains(t, chk.Stderr, "sync.local.yaml: listing_budgets", "--check prints the load error")
}

// listingConfigWorkspace writes a --config workspace with two kind: workspace
// targets over reg, cc-ws on claude-code and codex-ws on codex, beside the
// given listing_budgets block. It returns the config path and the two target
// directories.
func listingConfigWorkspace(t *testing.T, reg, budgets string) (cfg, ccTarget, codexTarget string) {
	t.Helper()
	ws := t.TempDir()
	ccTarget = filepath.Join(ws, "out", "cc")
	codexTarget = filepath.Join(ws, "out", "codex")
	cfg = writeWorkspaceConfig(t, ws, "defaults:\n  registry: "+reg+"\n"+
		"listing_budgets:\n"+budgets+
		"targets:\n"+
		"  - id: cc-ws\n    kind: workspace\n    harness: claude-code\n    target: "+ccTarget+"\n"+
		"  - id: codex-ws\n    kind: workspace\n    harness: codex\n    target: "+codexTarget+"\n")
	return cfg, ccTarget, codexTarget
}

// Spec: §7.5.8, §7.5.2 — a --config run counts only the target over its
// budget as failed and writes both trees; under --dry-run --json each target
// writes its own envelope, the over-budget one with over: true and the other
// with a report within the codex default.
func TestListingBudget_ConfigTwoTargets(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	cfg, ccTarget, codexTarget := listingConfigWorkspace(t, reg, "  claude-code:\n"+listingBudget200(podsync.ListingCheckError))

	res := runPodium(t, "", listingEnv, "sync", "--config", cfg)
	cliWantExit(t, res, 1, "sync --config with one target over budget")
	cliContains(t, res.Stderr, "target cc-ws: "+"listing estimate exceeds the configured budget", "over-budget target fails")
	cliNotContains(t, res.Stderr, "target codex-ws:", "within-budget target does not fail")
	for _, dir := range []string{ccTarget, codexTarget} {
		if len(readTreeFiltered(t, dir)) == 0 {
			t.Errorf("%s: no file written", dir)
		}
	}

	dry := runPodium(t, "", listingEnv, "sync", "--config", cfg, "--dry-run", "--json")
	cliWantExit(t, dry, 1, "sync --config --dry-run --json")
	envs := targetEnvelopes(t, dry.Stdout)
	assertOverBudget(t, envs["cc-ws"].Listing, podsync.ListingCheckError)
	codex := envs["codex-ws"].Listing
	if codex == nil || codex.Harness != "codex" || codex.Over || codex.BudgetChars != 8000 || codex.Entries != len(listingSkills) {
		t.Errorf("codex-ws listing = %+v, want a codex report within the default budget", codex)
	}
}

// Spec: §7.5.2 — an invalid entry for any kind: workspace target's harness
// exits 2 before the first target runs, with or without --check.
func TestListingBudget_ConfigInvalidEntry(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	for _, extra := range [][]string{nil, {"--check"}} {
		t.Run(fmt.Sprintf("args=%v", extra), func(t *testing.T) {
			t.Parallel()
			cfg, ccTarget, codexTarget := listingConfigWorkspace(t, reg, "  codex:\n    check: maybe\n")
			res := runPodium(t, "", listingEnv, append([]string{"sync", "--config", cfg}, extra...)...)
			cliWantExit(t, res, 2, "sync --config with an invalid codex entry")
			cliContains(t, res.Stderr, "target codex-ws", "message names the target")
			cliContains(t, res.Stderr, "listing_budgets.codex.check", "message names the key")
			cliNotContains(t, res.Stdout, "== target", "no target runs")
			for _, dir := range []string{ccTarget, codexTarget} {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Errorf("%s written (stat err=%v)", dir, err)
				}
			}
		})
	}
}

// Spec: §7.5.8 — a --config --check run counts a kind: workspace target over
// its budget under check: error as failed and writes nothing.
func TestListingBudget_ConfigCheckFailsTarget(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	cfg, ccTarget, _ := listingConfigWorkspace(t, reg, "  claude-code:\n"+listingBudget200(podsync.ListingCheckError))
	res := runPodium(t, "", listingEnv, "sync", "--config", cfg, "--check")
	cliWantExit(t, res, 1, "sync --config --check over budget")
	cliContains(t, res.Stderr, overLine("", listingCost(listingSkills), 200, len(listingSkills)), "listing line")
	cliContains(t, res.Stderr, "target cc-ws: ", "target failure line")
	cliNotContains(t, res.Stderr, "target codex-ws:", "within-budget target does not fail")
	if _, err := os.Stat(ccTarget); !os.IsNotExist(err) {
		t.Errorf("--check wrote %s (stat err=%v)", ccTarget, err)
	}
}

// Spec: §7.5.8, §13.11.3 — a --config workspace target whose listing fails
// under check: error runs its publish phase on the written tree before the
// target counts as failed, as a drop does.
func TestListingBudget_WorkspaceWorkflowPublishesThenFails(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	ws := t.TempDir()
	target := filepath.Join(ws, "out", "claude")
	cfg := writeSyncConfigWorkspaceWorkflow(t, ws, reg, target, "")
	appendLine(t, cfg, "listing_budgets:\n  claude-code:\n"+listingBudget200(podsync.ListingCheckError))

	res := runPodium(t, "", listingEnv, "sync", "--config", cfg)
	cliWantExit(t, res, 1, "sync --config with a workflow over budget")
	mustExist(t, filepath.Join(ws, "publish-ran"))
	cliContains(t, res.Stderr, overLine("", listingCost(listingSkills), 200, len(listingSkills)), "listing line")
	cliContains(t, res.Stderr, "target claude-workspace:", "target failure line")
	if len(readTreeFiltered(t, target)) == 0 {
		t.Error("the materialized tree is not written")
	}
}

// Spec: §7.5.2 — a --config run beside a sync.local.yaml that does not load
// exits 1 before the first target runs.
func TestListingBudget_ConfigLocalLoadError(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	cfg, ccTarget, _ := listingConfigWorkspace(t, reg, "  claude-code:\n"+listingBudget200(podsync.ListingCheckWarning))
	writeLocalConfig(t, filepath.Dir(filepath.Dir(cfg)), "listing_budgets:\n  claude-code:\n    max_chars: abc\n")
	res := runPodium(t, "", listingEnv, "sync", "--config", cfg)
	cliWantExit(t, res, 1, "sync --config beside a mistyped sync.local.yaml")
	cliContains(t, res.Stderr, "sync.local.yaml: listing_budgets", "load error names the file")
	cliNotContains(t, res.Stdout, "== target", "no target runs")
	if _, err := os.Stat(ccTarget); !os.IsNotExist(err) {
		t.Errorf("%s written (stat err=%v)", ccTarget, err)
	}
}

// lockAdds returns the toggles.add IDs of target's sync.lock, or nil when no
// lock is written.
func lockAdds(t *testing.T, target string) []string {
	t.Helper()
	lock, err := podsync.ReadLock(target)
	if err != nil {
		t.Fatalf("read sync.lock: %v", err)
	}
	if lock == nil {
		return nil
	}
	var out []string
	for _, a := range lock.Toggles.Add {
		out = append(out, a.ID)
	}
	return out
}

// Spec: §7.5.8, §7.5.2 — `podium sync override` estimates the listing of its
// re-materialization and validates the entry only when it re-materializes.
func TestListingBudget_Override(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	override := func(t *testing.T, ws, target string, extra ...string) cliResult {
		t.Helper()
		args := append([]string{"sync", "override", "--target", target, "--add", "ops/alpha", "--harness", "claude-code"}, extra...)
		return runPodium(t, ws, withIsolatedHome(listingEnv, t.TempDir()), args...)
	}

	t.Run("check error", func(t *testing.T) {
		t.Parallel()
		ws := listingWorkspace(t, reg, listingBudget200(podsync.ListingCheckError))
		target := t.TempDir()
		res := override(t, ws, target)
		cliWantExit(t, res, 1, "override over budget under check: error")
		cliContains(t, res.Stderr, overLine("", listingCost(listingSkills), 200, len(listingSkills)), "listing line")
		if got := lockAdds(t, target); !reflect.DeepEqual(got, []string{"ops/alpha"}) {
			t.Errorf("toggles.add = %v, want [ops/alpha]", got)
		}
		dry := override(t, ws, t.TempDir(), "--dry-run")
		cliWantExit(t, dry, 0, "override --dry-run under check: error")
		cliNotContains(t, dry.Stderr, "listing:", "a dry run computes no listing")
	})

	t.Run("invalid entry", func(t *testing.T) {
		t.Parallel()
		ws := listingWorkspace(t, reg, "    check: maybe\n")
		target := t.TempDir()
		res := override(t, ws, target)
		cliWantExit(t, res, 2, "override with check: maybe")
		cliContains(t, res.Stderr, "listing_budgets.claude-code.check", "message names the key")
		if got := lockAdds(t, target); len(got) != 0 {
			t.Errorf("toggles.add = %v, want no toggle recorded", got)
		}
		dry := override(t, ws, t.TempDir(), "--dry-run")
		cliWantExit(t, dry, 0, "override --dry-run with check: maybe")
	})

	t.Run("unloadable local config", func(t *testing.T) {
		t.Parallel()
		ws := listingWorkspace(t, reg, listingBudget200(podsync.ListingCheckWarning))
		writeLocalConfig(t, ws, "listing_budgets:\n  claude-code:\n    max_chars: abc\n")
		target := t.TempDir()
		res := override(t, ws, target, "--registry", reg)
		cliWantExit(t, res, 0, "override beside a mistyped sync.local.yaml")
		if got := lockAdds(t, target); !reflect.DeepEqual(got, []string{"ops/alpha"}) {
			t.Errorf("toggles.add = %v, want [ops/alpha]", got)
		}
	})

	t.Run("no registry", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		writeWorkspaceConfig(t, ws, "listing_budgets:\n  claude-code:\n    check: maybe\n")
		target := t.TempDir()
		res := override(t, ws, target)
		cliWantExit(t, res, 0, "override with no registry and check: maybe")
		cliNotContains(t, res.Stderr, "listing_budgets", "no re-materialization validates no entry")
		if got := lockAdds(t, target); !reflect.DeepEqual(got, []string{"ops/alpha"}) {
			t.Errorf("toggles.add = %v, want [ops/alpha]", got)
		}
	})
}

// Spec: §7.5.8, §13.11.4 — a --watch run reports each cycle's listing and
// keeps watching; under check: error the cycle counts toward exit 1 on
// interrupt, and under --json the cycle envelope carries the report.
func TestListingBudget_Watch(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	line := overLine("", listingCost(listingSkills), 200, len(listingSkills))

	t.Run("human", func(t *testing.T) {
		t.Parallel()
		ws := listingWorkspace(t, reg, listingBudget200(podsync.ListingCheckError))
		w := startWatchIn(t, ws, listingEnv, "--harness", "claude-code", "--target", t.TempDir())
		if !pollLog(w, line, 15*time.Second) {
			t.Fatalf("watch did not report the listing\nlog:\n%s", w.log())
		}
		if err := w.cmd.Process.Signal(syscall.Signal(0)); err != nil {
			t.Fatalf("watch exited after reporting the listing: %v\nlog:\n%s", err, w.log())
		}
		if code := w.stop(t); code != 1 {
			t.Errorf("watch exit=%d on SIGINT after a failed listing, want 1\nlog:\n%s", code, w.log())
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		ws := listingWorkspace(t, reg, listingBudget200(podsync.ListingCheckError))
		w := startWatchIn(t, ws, listingEnv, "--harness", "claude-code", "--target", t.TempDir(), "--json")
		if !pollLog(w, line, 15*time.Second) {
			t.Fatalf("watch --json did not report the listing\nlog:\n%s", w.log())
		}
		w.stop(t)
		log := w.log()
		start := strings.Index(log, "{\n")
		if start < 0 {
			t.Fatalf("no cycle envelope in the watch log:\n%s", log)
		}
		var env syncEnvelope
		if err := json.NewDecoder(strings.NewReader(log[start:])).Decode(&env); err != nil {
			t.Fatalf("decode cycle envelope: %v\nlog:\n%s", err, log)
		}
		assertOverBudget(t, env.Listing, podsync.ListingCheckError)
	})
}

// Spec: §7.5.8, §2.2 — a server-source sync and a filesystem-source sync of
// the same directory report the same listing.
func TestListingBudget_ServerSourceMatchesFilesystem(t *testing.T) {
	t.Parallel()
	reg := writeListingRegistry(t, listingSkills)
	srv := startServer(t, reg)
	ws := listingWorkspace(t, reg, listingBudget200(podsync.ListingCheckWarning))

	fsRes := runPodium(t, ws, listingEnv, "sync", "--harness", "claude-code", "--target", t.TempDir(), "--dry-run", "--json")
	cliWantExit(t, fsRes, 0, "filesystem-source sync")
	srvRes := runPodium(t, ws, append(append([]string{}, listingEnv...), srvSyncEnv(srv)...),
		"sync", "--registry", srv.BaseURL, "--harness", "claude-code", "--target", t.TempDir(), "--dry-run", "--json")
	cliWantExit(t, srvRes, 0, "server-source sync")

	fsListing := decodeSyncEnvelope(t, fsRes.Stdout).Listing
	srvListing := decodeSyncEnvelope(t, srvRes.Stdout).Listing
	assertOverBudget(t, fsListing, podsync.ListingCheckWarning)
	if !reflect.DeepEqual(fsListing, srvListing) {
		t.Errorf("server-source listing %+v differs from filesystem-source %+v", srvListing, fsListing)
	}
}
