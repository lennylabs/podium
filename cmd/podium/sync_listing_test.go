package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sync"
)

// oversizedReport is a report over its budget with one entry of 101 code
// points above a max_entry_chars of 100.
func oversizedReport(check string) *sync.ListingReport {
	return &sync.ListingReport{
		Harness:       "claude-code",
		BudgetChars:   50,
		CostChars:     120,
		Entries:       2,
		MaxEntryChars: 100,
		Over:          true,
		Check:         check,
		Oversized:     []sync.OversizedEntry{{ID: "docs/writer", Chars: 101}},
	}
}

// Spec: §7.5.8 — the run writes each excess and each oversized entry on
// standard error, naming the configured budget and max_entry_chars; under
// `check: warning` each line carries the warning prefix and the run does not
// fail, and under `check: error` it fails. The --json member carries the
// §7.5.8 keys alone, with no max_entry_chars member.
func TestReportListing(t *testing.T) {
	const (
		excess    = "listing: claude-code listing estimate is 120 characters against a configured budget of 50 (2 entries)\n"
		oversized = "listing: docs/writer description is 101 characters, above max_entry_chars 100\n"
	)

	var errOut bytes.Buffer
	if !reportListing(&errOut, oversizedReport(sync.ListingCheckError)) {
		t.Errorf("check: error: reportListing = false, want true")
	}
	if got, want := errOut.String(), excess+oversized; got != want {
		t.Errorf("check: error output =\n%s\nwant\n%s", got, want)
	}

	var warnOut bytes.Buffer
	if reportListing(&warnOut, oversizedReport(sync.ListingCheckWarning)) {
		t.Errorf("check: warning: reportListing = true, want false")
	}
	if got, want := warnOut.String(), "warning: "+excess+"warning: "+oversized; got != want {
		t.Errorf("check: warning output =\n%s\nwant\n%s", got, want)
	}

	var quiet bytes.Buffer
	within := &sync.ListingReport{Harness: "claude-code", BudgetChars: 8000, CostChars: 10, Entries: 1, Check: sync.ListingCheckError}
	if reportListing(&quiet, within) || reportListing(&quiet, nil) || quiet.Len() != 0 {
		t.Errorf("a report within budget or a nil report wrote %q or failed the run", quiet.String())
	}

	b, err := json.Marshal(listingOut(oversizedReport(sync.ListingCheckWarning)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var member map[string]any
	if err := json.Unmarshal(b, &member); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{
		"harness":      "claude-code",
		"budget_chars": float64(50),
		"cost_chars":   float64(120),
		"entries":      float64(2),
		"over":         true,
		"check":        "warning",
		"oversized":    []any{map[string]any{"id": "docs/writer", "chars": float64(101)}},
	}
	if !reflect.DeepEqual(member, want) {
		t.Errorf("listing member = %v, want %v", member, want)
	}
}

// Spec: §7.5.8 — oversized is [] when no entry is reported, and a run that
// computes no listing carries listing: null.
func TestListingOut_EmptyAndNil(t *testing.T) {
	if listingOut(nil) != nil {
		t.Errorf("listingOut(nil) is not nil")
	}
	b, err := json.Marshal(listingOut(&sync.ListingReport{Harness: "codex", Check: sync.ListingCheckWarning}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"oversized":[]`) {
		t.Errorf("oversized not normalized to []: %s", b)
	}
}

// listingOverBudgetLine is the excess line the listingOverrideFixture skill
// writes against a max_chars of 10.
const listingOverBudgetLine = "listing: claude-code listing estimate is 57 characters against a configured budget of 10 (1 entries)"

// Spec: §7.5.8, §7.5 — a one-shot --dry-run --json sync carries the listing
// member in its envelope, writes the excess on standard error, and exits 1
// under `check: error`.
func TestSyncCmd_ListingCheckErrorExits1(t *testing.T) {
	registry, ws := listingOverrideFixture(t, "    max_chars: 10\n    check: error\n")
	var rc int
	var stdout, stderr string
	withCwd(t, ws, func() {
		stderr = captureStderr(t, func() {
			stdout = captureStdout(t, func() {
				rc = syncCmd([]string{"--registry", registry, "--target", t.TempDir(), "--harness", "claude-code", "--dry-run", "--json"})
			})
		})
	})
	if rc != 1 {
		t.Errorf("rc = %d, want 1\nstderr:\n%s", rc, stderr)
	}
	if !strings.Contains(stderr, listingOverBudgetLine) {
		t.Errorf("stderr missing %q:\n%s", listingOverBudgetLine, stderr)
	}
	var env struct {
		Listing *listingJSON `json:"listing"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, stdout)
	}
	if env.Listing == nil || !env.Listing.Over || env.Listing.CostChars != 57 || env.Listing.Check != "error" {
		t.Errorf("listing member = %+v, want over with cost 57 under check: error", env.Listing)
	}
}

// Spec: §7.5.2 — a sync that sets no harness runs the none adapter, so a
// listing_budgets.none entry exits 2 before the view resolves.
func TestSyncCmd_ListingNoneEntryExits2(t *testing.T) {
	registry, ws := listingOverrideFixture(t, "    max_chars: 9000\n")
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{
		Path:    ".podium/sync.yaml",
		Content: "listing_budgets:\n  none:\n    max_chars: 9000\n",
	})
	target := t.TempDir()
	var rc int
	var stderr string
	withCwd(t, ws, func() {
		stderr = captureStderr(t, func() {
			captureStdout(t, func() {
				rc = syncCmd([]string{"--registry", registry, "--target", target})
			})
		})
	})
	if rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
	if !strings.Contains(stderr, "listing_budgets.none: the none adapter feeds no harness listing") {
		t.Errorf("stderr does not name listing_budgets.none:\n%s", stderr)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("target holds %d entries, want none", len(entries))
	}
}

// writeListingTargetsConfig writes a --config sync.yaml with a claude-code
// workspace target whose publish phase touches a marker and a codex workspace
// target, and returns its path and the marker path.
func writeListingTargetsConfig(t *testing.T, ws, registry, claudeEntry string) (cfg, marker string) {
	t.Helper()
	marker = filepath.Join(ws, "publish-ran")
	body := "defaults:\n  registry: " + registry + "\n" +
		"listing_budgets:\n  claude-code:\n" + claudeEntry +
		"targets:\n" +
		"  - id: claude-workspace\n    kind: workspace\n    harness: claude-code\n" +
		"    target: " + filepath.Join(ws, "out", "claude") + "\n" +
		"    workflow:\n      publish:\n        - sh: \"touch " + marker + "\"\n" +
		"  - id: codex-workspace\n    kind: workspace\n    harness: codex\n" +
		"    target: " + filepath.Join(ws, "out", "codex") + "\n"
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{Path: ".podium/sync.yaml", Content: body})
	return filepath.Join(ws, ".podium", "sync.yaml"), marker
}

// Spec: §7.5.8, §7.5.2, §13.11.3 — a --config run counts only the target
// whose listing fails under `check: error` as failed, after its workflow
// publishes; an invalid entry for a workspace target's harness exits 2
// before the first target runs, with or without --check.
func TestRunMultiTargetSync_Listing(t *testing.T) {
	registry, ws := listingOverrideFixture(t, "    max_chars: 9000\n")

	t.Run("over budget", func(t *testing.T) {
		cfg, marker := writeListingTargetsConfig(t, ws, registry, "    max_chars: 10\n    check: error\n")
		var rc int
		stderr := captureStderr(t, func() {
			captureStdout(t, func() { rc = runMultiTargetSync(cfg, "", false, false, false, false) })
		})
		if rc != 1 {
			t.Errorf("rc = %d, want 1\nstderr:\n%s", rc, stderr)
		}
		for _, want := range []string{listingOverBudgetLine, "target claude-workspace: " + errListingOverBudget.Error()} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q:\n%s", want, stderr)
			}
		}
		if strings.Contains(stderr, "target codex-workspace:") {
			t.Errorf("the codex target within its budget counted as failed:\n%s", stderr)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("publish phase did not run before the failure: %v", err)
		}
	})
	// Spec: §7.5.2 — a listing_budgets value of the wrong YAML type fails the
	// merged load, so a --config sync exits 1.
	t.Run("mistyped value", func(t *testing.T) {
		cfg, _ := writeListingTargetsConfig(t, ws, registry, "    max_chars: abc\n")
		var rc int
		stderr := captureStderr(t, func() {
			captureStdout(t, func() { rc = runMultiTargetSync(cfg, "", false, false, false, false) })
		})
		if rc != 1 || !strings.Contains(stderr, "listing_budgets") {
			t.Errorf("rc = %d, want 1 naming listing_budgets\nstderr:\n%s", rc, stderr)
		}
	})
	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid entry check=%v", check), func(t *testing.T) {
			cfg, _ := writeListingTargetsConfig(t, ws, registry, "    types: []\n")
			var rc int
			var stdout string
			stderr := captureStderr(t, func() {
				stdout = captureStdout(t, func() { rc = runMultiTargetSync(cfg, "", false, check, false, false) })
			})
			if rc != 2 {
				t.Errorf("rc = %d, want 2", rc)
			}
			if !strings.Contains(stderr, "error: target claude-workspace: sync.yaml: listing_budgets.claude-code.types") {
				t.Errorf("stderr does not name the target and key:\n%s", stderr)
			}
			if strings.Contains(stdout, "== target") {
				t.Errorf("a target ran before the validation:\n%s", stdout)
			}
		})
	}
}

// Spec: §7.5.8, §7.5.5, §7.5.2 — a batch override that re-materializes exits
// 1 on a listing over its budget under `check: error` with the toggle kept,
// and exits 2 on an invalid entry with no toggle recorded; --dry-run
// validates no entry.
func TestSyncOverrideCmd_Listing(t *testing.T) {
	run := func(t *testing.T, ws string, args ...string) (int, string) {
		t.Helper()
		var rc int
		var stderr string
		withCwd(t, ws, func() {
			stderr = captureStderr(t, func() {
				captureStdout(t, func() { rc = syncOverrideCmd(args) })
			})
		})
		return rc, stderr
	}
	t.Run("over budget", func(t *testing.T) {
		registry, ws := listingOverrideFixture(t, "    max_chars: 10\n    check: error\n")
		target := t.TempDir()
		rc, stderr := run(t, ws, "--target", target, "--registry", registry, "--harness", "claude-code", "--add", "docs/writer")
		if rc != 1 || !strings.Contains(stderr, listingOverBudgetLine) {
			t.Errorf("rc = %d, want 1 with the listing line\nstderr:\n%s", rc, stderr)
		}
		if !strings.Contains(readLockString(t, target), "docs/writer") {
			t.Errorf("lock toggles.add missing the added artifact")
		}
	})
	t.Run("invalid entry", func(t *testing.T) {
		registry, ws := listingOverrideFixture(t, "    check: maybe\n")
		target := t.TempDir()
		rc, stderr := run(t, ws, "--target", target, "--registry", registry, "--harness", "claude-code", "--add", "docs/writer")
		if rc != 2 || !strings.Contains(stderr, "listing_budgets.claude-code.check") {
			t.Errorf("rc = %d, want 2 naming the key\nstderr:\n%s", rc, stderr)
		}
		if _, err := os.Stat(filepath.Join(target, ".podium", "sync.lock")); !os.IsNotExist(err) {
			t.Errorf("a toggle was recorded: sync.lock stat err = %v", err)
		}
		rc, stderr = run(t, ws, "--target", target, "--registry", registry, "--harness", "claude-code", "--add", "docs/writer", "--dry-run")
		if rc != 0 || strings.Contains(stderr, "listing_budgets") {
			t.Errorf("--dry-run: rc = %d, want 0 with no listing_budgets error\nstderr:\n%s", rc, stderr)
		}
	})
}

// Spec: §7.5.8, §13.11.3 — a --config target fails on a drop, on a listing
// report under `check: error`, or on both, and the joined error names each.
func TestListingErr_JoinsWithDrop(t *testing.T) {
	if err := errors.Join(droppedErr(false), listingErr(false)); err != nil {
		t.Errorf("no drop and no listing failure = %v, want nil", err)
	}
	err := errors.Join(droppedErr(true), listingErr(true))
	if !errors.Is(err, errDropped) || !errors.Is(err, errListingOverBudget) {
		t.Errorf("joined error = %v, want both sentinels", err)
	}
}
