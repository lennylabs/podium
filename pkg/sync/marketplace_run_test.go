package sync

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests exercise the §7.8 prepare->render->publish marketplace runner
// against the filesystem-source fixture registry (no live server) with stub
// commands that touch marker files: variable injection, skip_if_no_changes
// suppression, fail-fast on the first non-zero exit, continue_on_error, the
// per-command timeout, the per-phase on_error cleanup, --dry-run, --check, and
// the supplied-workdir versus allocated-workdir paths.

// runOutput returns a resolved output for the fixture registry with the standard
// finance plugins and the given harness set and workflow.
func runOutput(reg string, harnesses []string, wf Workflow) ResolvedOutput {
	return ResolvedOutput{
		ID:            "acme-agents",
		Registry:      reg,
		Identity:      "publisher@acme.com",
		Git:           GitRemote{Remote: "git@example.com:acme/agents.git", Branch: "main"},
		Harnesses:     harnesses,
		CommitMessage: "Sync ({{.ChangedCount}} changes)",
		Plugins:       financePlugins(),
		Workflow:      wf,
	}
}

// touch returns an sh: command that writes its variable-expanded marker line to a
// marker file in $PODIUM_WORKDIR, so a test can assert the command ran and inspect
// the injected variables it saw. It appends so a sequence of commands records its
// order.
func touch(marker, line string) Command {
	return Command{Sh: "printf '%s\\n' \"" + line + "\" >> \"$PODIUM_WORKDIR/" + marker + "\""}
}

func readMarker(t *testing.T, workdir, marker string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workdir, marker))
	if err != nil {
		t.Fatalf("read marker %q: %v", marker, err)
	}
	return string(data)
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC) }
}

// Spec: §7.8 — the pipeline injects $PODIUM_WORKDIR, $PODIUM_OUTPUT_ID,
// $PODIUM_GIT_REMOTE, $PODIUM_GIT_BRANCH, $PODIUM_REGISTRY, the harness set, plus
// the change-driven $PODIUM_CHANGED, $PODIUM_COMMIT_MESSAGE, and
// $PODIUM_CHANGE_SUMMARY, into every command's environment.
func TestRunMarketplace_VariableInjection(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("vars",
			"id=$PODIUM_OUTPUT_ID remote=$PODIUM_GIT_REMOTE branch=$PODIUM_GIT_BRANCH "+
				"changed=$PODIUM_CHANGED harnesses=$PODIUM_HARNESSES wd=$PODIUM_WORKDIR "+
				"msg=$PODIUM_COMMIT_MESSAGE summary=$PODIUM_CHANGE_SUMMARY identity=$PODIUM_IDENTITY")},
	})

	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("RunMarketplace: %v", err)
	}
	if !res.Published {
		t.Errorf("a live run must report Published=true")
	}

	got := readMarker(t, workdir, "vars")
	wantMsg := "msg=Sync (" + strconv.Itoa(len(res.Render.ChangedArtifacts)) + " changes)"
	for _, want := range []string{
		"id=acme-agents",
		"remote=git@example.com:acme/agents.git",
		"branch=main",
		"changed=true",
		"harnesses=claude-code",
		"wd=" + workdir,
		wantMsg,
		"identity=publisher@acme.com",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("injected vars missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "summary="+filepath.Join(workdir, ".podium", "publish-change-summary.json")) {
		t.Errorf("PODIUM_CHANGE_SUMMARY not pointed at the summary file:\n%s", got)
	}
}

// Spec: §7.8 — an sh: command is handed to sh -c verbatim, and the pipeline
// inherits the ambient environment, so an ambient credential reference such as
// $GH_TOKEN that Podium does not inject is expanded by the shell rather than
// blanked by a Go-side pre-expansion. This is the credential pass-through the
// spec calls out (git authentication relies on SSH_AUTH_SOCK and GH_TOKEN). The
// command writes the shell-expanded value of an ambient variable to a marker
// file, alongside an injected PODIUM_* variable, so the test asserts the shell
// expands both.
func TestRunMarketplace_ShAmbientEnvSurvives(t *testing.T) {
	// t.Setenv forbids t.Parallel, so this test runs serially.
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()
	t.Setenv("GH_TOKEN", "ghp-secret-value")

	// The sh: string references an ambient $GH_TOKEN and the injected
	// $PODIUM_OUTPUT_ID. A correct pipeline runs it verbatim under sh -c, so the
	// shell expands both. A pipeline that pre-expands in Go blanks $GH_TOKEN,
	// because GH_TOKEN is not in the injected PODIUM_* set.
	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Sh: `printf 'token=%s id=%s\n' "$GH_TOKEN" "$PODIUM_OUTPUT_ID" >> "$PODIUM_WORKDIR/ambient"`},
		},
	})

	if _, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()}); err != nil {
		t.Fatalf("RunMarketplace: %v", err)
	}

	got := readMarker(t, workdir, "ambient")
	if !strings.Contains(got, "token=ghp-secret-value") {
		t.Errorf("sh: command must expand the ambient $GH_TOKEN through the shell, got:\n%s", got)
	}
	if !strings.Contains(got, "id=acme-agents") {
		t.Errorf("sh: command must expand the injected $PODIUM_OUTPUT_ID through the shell, got:\n%s", got)
	}
}

// Spec: §7.8 — the --dry-run preview prints each command with variables
// substituted. The injected PODIUM_* variables are expanded in the preview, and a
// reference Podium does not inject (an ambient $GH_TOKEN) is left as its literal
// $name so the printed command shows the ambient credential reference the live
// shell will expand rather than blanking it.
func TestRunMarketplace_DryRunPreservesAmbientReference(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)

	// The credential reference sits in the publish phase, which a dry run prints
	// but does not execute, so the test asserts the printed preview without a
	// reachable remote.
	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Sh: `git -C "$PODIUM_WORKDIR" push https://$GH_TOKEN@github.com/acme/agents.git`},
		},
	})

	var stdout bytes.Buffer
	if _, err := RunMarketplace(context.Background(), RunOptions{Output: out, DryRun: true, Now: fixedNow(), Stdout: &stdout, Stderr: discardBuf()}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}

	printed := stdout.String()
	// The injected $PODIUM_WORKDIR is expanded; the ambient $GH_TOKEN is left
	// literal so the credential reference survives into the printed command.
	if !strings.Contains(printed, "https://$GH_TOKEN@github.com/acme/agents.git") {
		t.Errorf("dry-run preview must preserve the literal $GH_TOKEN reference, got:\n%s", printed)
	}
	if strings.Contains(printed, "https://@github.com/acme/agents.git") {
		t.Errorf("dry-run preview must not blank the ambient $GH_TOKEN reference, got:\n%s", printed)
	}
}

// Spec: §7.8 — the commit_message template is rendered with the change count and
// a timestamp into $PODIUM_COMMIT_MESSAGE.
func TestRunMarketplace_CommitMessageTemplate(t *testing.T) {
	t.Parallel()
	out := runOutput("x", []string{"claude-code"}, Workflow{})
	out.CommitMessage = "msg {{.ChangedCount}} at {{.Timestamp}} for {{.OutputID}}"
	render := &RenderResult{OutputID: "acme-agents", Changed: true, ChangedArtifacts: []string{"a", "b"}}

	got, err := renderCommitMessage(out.CommitMessage, render, fixedNow()())
	if err != nil {
		t.Fatalf("renderCommitMessage: %v", err)
	}
	want := "msg 2 at 2026-06-25T12:00:00Z for acme-agents"
	if got != want {
		t.Errorf("commit message = %q, want %q", got, want)
	}

	// An empty template yields a default message naming the output and count.
	def, err := renderCommitMessage("", render, fixedNow()())
	if err != nil {
		t.Fatalf("renderCommitMessage default: %v", err)
	}
	if !strings.Contains(def, "acme-agents") || !strings.Contains(def, "2 changes") {
		t.Errorf("default commit message %q must name the output and the count", def)
	}

	// A malformed template is a config-level error.
	if _, err := renderCommitMessage("{{.Nope", render, fixedNow()()); err == nil {
		t.Errorf("malformed commit_message template must error")
	}
}

// Spec: §7.8 — a command that declares skip_if_no_changes is skipped when the
// render produced no diff. The first render changes the tree; a second run into
// the same checkout produces no diff, so the marked command does not run.
func TestRunMarketplace_SkipIfNoChanges(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Sh: "printf ran >> \"$PODIUM_WORKDIR/committed\"", SkipIfNoChanges: true},
		},
	})

	// First run: the render changes the tree, so the command runs.
	first, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("first RunMarketplace: %v", err)
	}
	if !first.Render.Changed {
		t.Fatalf("first run must report Changed=true")
	}
	if _, err := os.Stat(filepath.Join(workdir, "committed")); err != nil {
		t.Fatalf("skip_if_no_changes command must run on a changed render: %v", err)
	}
	if err := os.Remove(filepath.Join(workdir, "committed")); err != nil {
		t.Fatalf("remove marker: %v", err)
	}

	// Second run into the same checkout: no diff, so the command is skipped.
	second, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("second RunMarketplace: %v", err)
	}
	if second.Render.Changed {
		t.Fatalf("second run into an unchanged checkout must report Changed=false")
	}
	if _, err := os.Stat(filepath.Join(workdir, "committed")); !os.IsNotExist(err) {
		t.Errorf("skip_if_no_changes command must be suppressed on an unchanged render: stat err=%v", err)
	}
}

// Spec: §7.8 — the pipeline fails fast on the first non-zero exit. A failing
// command stops the phase, so a later command does not run, and the error names
// the output and the phase.
func TestRunMarketplace_FailFast(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Run: []string{"false"}},
			touch("after", "should-not-run"),
		},
	})

	_, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err == nil {
		t.Fatalf("a non-zero command must fail the run")
	}
	if !strings.Contains(err.Error(), "acme-agents") || !strings.Contains(err.Error(), "publish") {
		t.Errorf("fail-fast error must name the output and phase: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(workdir, "after")); !os.IsNotExist(statErr) {
		t.Errorf("the command after a failure must not run: stat err=%v", statErr)
	}
}

// Spec: §7.8 — a command that declares continue_on_error lets the pipeline
// proceed past its non-zero exit, so a later command still runs and the run
// succeeds.
func TestRunMarketplace_ContinueOnError(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Run: []string{"false"}, ContinueOnError: true},
			touch("after", "ran-after-error"),
		},
	})

	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("continue_on_error must let the run succeed: %v", err)
	}
	if !res.Published {
		t.Errorf("run with continue_on_error must report Published=true")
	}
	if got := readMarker(t, workdir, "after"); !strings.Contains(got, "ran-after-error") {
		t.Errorf("the command after a continue_on_error failure must run: %q", got)
	}
}

// Spec: §7.8 — a per-command timeout bounds its wall clock. A command that sleeps
// past its timeout is killed and the run fails with a deadline-exceeded error.
func TestRunMarketplace_Timeout(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Run: []string{"sleep", "10"}, Timeout: Duration(50 * time.Millisecond)},
		},
	})

	start := time.Now()
	_, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("a command past its timeout must fail the run")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout error must name the timeout: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("timeout did not kill the command promptly: elapsed %s", elapsed)
	}
}

// Spec: §7.8 — the per-phase on_error cleanup list runs when a command in its
// phase fails, before the failure propagates. A publish-phase failure runs
// publish_on_error and leaves prepare_on_error untouched.
func TestRunMarketplace_PublishOnErrorCleanup(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish:        []Command{{Run: []string{"false"}}},
		PrepareOnError: []Command{touch("prepare-cleanup", "prepare-cleanup-ran")},
		PublishOnError: []Command{touch("publish-cleanup", "publish-cleanup-ran")},
	})

	_, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err == nil {
		t.Fatalf("the failing publish command must fail the run")
	}
	if got := readMarker(t, workdir, "publish-cleanup"); !strings.Contains(got, "publish-cleanup-ran") {
		t.Errorf("publish_on_error cleanup must run on a publish-phase failure: %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(workdir, "prepare-cleanup")); !os.IsNotExist(statErr) {
		t.Errorf("a publish-phase failure must not run the prepare_on_error cleanup: stat err=%v", statErr)
	}
}

// Spec: §7.8 — a prepare-phase failure runs prepare_on_error, scoped to the
// phase that failed, and leaves publish_on_error untouched. The render and the
// publish phase do not run after a prepare failure.
func TestRunMarketplace_PrepareOnErrorCleanup(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Prepare:        []Command{{Run: []string{"false"}}},
		Publish:        []Command{touch("published", "should-not-run")},
		PrepareOnError: []Command{touch("prepare-cleanup", "prepare-cleanup-ran")},
		PublishOnError: []Command{touch("publish-cleanup", "publish-cleanup-ran")},
	})

	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err == nil {
		t.Fatalf("the failing prepare command must fail the run")
	}
	if !strings.Contains(err.Error(), "prepare") {
		t.Errorf("a prepare-phase failure must name the prepare phase: %v", err)
	}
	if res != nil {
		t.Errorf("a failed run returns a nil result, got %+v", res)
	}
	if got := readMarker(t, workdir, "prepare-cleanup"); !strings.Contains(got, "prepare-cleanup-ran") {
		t.Errorf("prepare_on_error cleanup must run on a prepare-phase failure: %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(workdir, "publish-cleanup")); !os.IsNotExist(statErr) {
		t.Errorf("a prepare-phase failure must not run the publish_on_error cleanup: stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(workdir, "published")); !os.IsNotExist(statErr) {
		t.Errorf("a prepare-phase failure must stop before the publish phase: stat err=%v", statErr)
	}
}

// Spec: §7.8 — --dry-run renders into a temporary directory and prints each
// prepare and publish command with variables substituted, without running any
// operator command. A dry run is a preview: it runs neither the prepare phase
// (no network clone) nor the publish phase (no commit or push). The operator's
// supplied checkout is untouched.
func TestRunMarketplace_DryRun(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	// The prepare command stands in for a clone. A dry run must print it with its
	// variables substituted rather than execute it, so the side-effecting touch is
	// a sentinel: if the dry run executed prepare, the marker file would appear.
	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Prepare: []Command{
			{Run: []string{"git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"}},
			touch("prepare-ran", "ran"),
		},
		Publish: []Command{
			touch("publish-ran", "ran"),
			{Run: []string{"git", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"}, SkipIfNoChanges: true},
		},
	})

	var stdout bytes.Buffer
	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, DryRun: true, Now: fixedNow(), Stdout: &stdout, Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if res.Published {
		t.Errorf("a dry run must not publish")
	}
	if res.Workdir == workdir {
		t.Errorf("a dry run must render into a temporary directory, not the operator's supplied workdir")
	}

	// No operator command ran. The prepare sentinel did not execute, so the dry
	// run did not clone, and the publish sentinel did not execute either. Both
	// markers would land in the render's temp directory, which cleanup removes, so
	// the test asserts through fresh sentinels that the side effects never fired.
	if _, statErr := os.Stat(filepath.Join(res.Workdir, "prepare-ran")); !os.IsNotExist(statErr) {
		t.Errorf("a dry run must not run a prepare command: stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(res.Workdir, "publish-ran")); !os.IsNotExist(statErr) {
		t.Errorf("a dry run must not run a publish command: stat err=%v", statErr)
	}
	// The operator's supplied workdir is untouched by the dry run.
	if _, statErr := os.Stat(filepath.Join(workdir, "prepare-ran")); !os.IsNotExist(statErr) {
		t.Errorf("a dry run must not touch the operator's supplied workdir: stat err=%v", statErr)
	}

	printed := stdout.String()
	// The git remote variable is substituted in the printed prepare clone.
	if !strings.Contains(printed, "git clone git@example.com:acme/agents.git") {
		t.Errorf("dry-run output must substitute $PODIUM_GIT_REMOTE:\n%s", printed)
	}
	// The commit_message variable is substituted in the printed publish commit.
	wantMsg := "Sync (" + strconv.Itoa(len(res.Render.ChangedArtifacts)) + " changes)"
	if !strings.Contains(printed, wantMsg) {
		t.Errorf("dry-run output must substitute $PODIUM_COMMIT_MESSAGE (%q):\n%s", wantMsg, printed)
	}
	// Both phases are printed.
	if !strings.Contains(printed, "# prepare") || !strings.Contains(printed, "# publish") {
		t.Errorf("dry-run output must print both phases:\n%s", printed)
	}
}

// Spec: §7.8 — a dry run renders into a temporary directory that no prepare clone
// populates, so the render reconciles against an empty tree and reports the whole
// rendered set as changed. A skip_if_no_changes publish command therefore prints
// un-skipped in a dry run, because the preview cannot observe an already-published
// remote without executing the operator's clone.
func TestRunMarketplace_DryRunReportsRenderedTreeChanged(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Prepare: []Command{{Run: []string{"git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"}}},
		Publish: []Command{
			{Run: []string{"git", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"}, SkipIfNoChanges: true},
		},
	})

	var stdout bytes.Buffer
	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, DryRun: true, Now: fixedNow(), Stdout: &stdout, Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !res.Render.Changed {
		t.Fatalf("a dry run renders into an empty temp directory, so it must report Changed=true")
	}

	// Because the render saw the whole tree as changed, the dry-run preview does
	// not mark the skip_if_no_changes publish command as skipped.
	printed := stdout.String()
	if strings.Contains(printed, "skipped (no changes)") {
		t.Errorf("dry-run preview against an empty temp directory must not skip a skip_if_no_changes command:\n%s", printed)
	}
}

// Spec: §7.8 — --check validates the config only and runs neither the render nor
// any command.
func TestRunMarketplace_Check(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("must-not-run", "ran")},
	})

	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Check: true, Stdout: discardBuf(), Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("--check on a valid config must succeed: %v", err)
	}
	if res.Render != nil || res.Published {
		t.Errorf("--check must not render or publish: %+v", res)
	}
	if _, statErr := os.Stat(filepath.Join(workdir, "must-not-run")); !os.IsNotExist(statErr) {
		t.Errorf("--check must not run any command: stat err=%v", statErr)
	}

	// --check rejects an output naming a non-publish harness.
	bad := runOutput(reg, []string{"opencode"}, Workflow{})
	if _, err := RunMarketplace(context.Background(), RunOptions{Output: bad, Check: true, Stdout: discardBuf(), Stderr: discardBuf()}); err == nil {
		t.Errorf("--check must reject a non-publish harness")
	}

	// --check rejects a malformed workflow command (neither run: nor sh:) before
	// any side effect, so a config.invalid command does not slip past the
	// fail-closed gate and fail mid-pipeline after the prepare clone and render.
	neither := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("must-not-run", "ran"), {}},
	})
	checkRejectsMalformed(t, "a publish command with neither run: nor sh:", neither, workdir)

	// --check rejects a command that declares both run: and sh:.
	both := runOutput(reg, []string{"claude-code"}, Workflow{
		Prepare: []Command{{Run: []string{"true"}, Sh: "true"}},
	})
	checkRejectsMalformed(t, "a prepare command with both run: and sh:", both, workdir)

	// --check inspects the per-phase cleanup lists too, not only prepare/publish.
	badCleanup := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish:        []Command{{Run: []string{"git", "push"}}},
		PublishOnError: []Command{{}},
	})
	checkRejectsMalformed(t, "a publish_on_error command with neither run: nor sh:", badCleanup, workdir)
}

// checkRejectsMalformed asserts --check rejects out with a config.invalid error
// and runs no command, so the malformed command does not reach a live pipeline.
func checkRejectsMalformed(t *testing.T, label string, out ResolvedOutput, workdir string) {
	t.Helper()
	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Check: true, Stdout: discardBuf(), Stderr: discardBuf()})
	if err == nil {
		t.Errorf("--check must reject %s", label)
		return
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("--check rejection of %s must be config.invalid: %v", label, err)
	}
	if res != nil {
		t.Errorf("--check rejection of %s must return a nil result, got %+v", label, res)
	}
	if _, statErr := os.Stat(filepath.Join(workdir, "must-not-run")); !os.IsNotExist(statErr) {
		t.Errorf("--check rejection of %s must run no command: stat err=%v", label, statErr)
	}
}

// Spec: §7.8 — a supplied workdir points the render at an existing checkout,
// while the default allocates and cleans up a per-output working directory.
func TestRunMarketplace_WorkdirVsAllocated(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)

	// Supplied-workdir path: the render writes into the supplied directory.
	workdir := t.TempDir()
	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("done", "done")},
	})
	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("RunMarketplace with a supplied workdir: %v", err)
	}
	if res.Workdir != workdir {
		t.Errorf("a supplied workdir must render into the supplied directory: got %q want %q", res.Workdir, workdir)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".claude-plugin", "marketplace.json")); err != nil {
		t.Errorf("the render must populate the supplied checkout: %v", err)
	}

	// Allocated path: the prepare phase runs against the allocated directory, and
	// the directory is removed after the run. A publish command writes the
	// allocated $PODIUM_WORKDIR path to a marker file outside that directory (it is
	// removed after the run), so the test can read back the path the command saw.
	marker := filepath.Join(t.TempDir(), "allocated-path")
	allocOut := runOutput(reg, []string{"claude-code"}, Workflow{
		// A prepare command asserts the allocated $PODIUM_WORKDIR exists.
		Prepare: []Command{{Sh: "test -d \"$PODIUM_WORKDIR\" && printf prepared > \"$PODIUM_WORKDIR/.prep\""}},
		Publish: []Command{{Sh: "printf '%s' \"$PODIUM_WORKDIR\" > " + marker}},
	})
	allocRes, err := RunMarketplace(context.Background(), RunOptions{Output: allocOut, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()})
	if err != nil {
		t.Fatalf("RunMarketplace with an allocated workdir: %v", err)
	}
	if allocRes.Workdir == "" || allocRes.Workdir == workdir {
		t.Errorf("an allocated run must use its own temp directory, got %q", allocRes.Workdir)
	}
	// The allocated working directory is removed after the run.
	if _, err := os.Stat(allocRes.Workdir); !os.IsNotExist(err) {
		t.Errorf("the allocated working directory must be removed after the run: stat err=%v", err)
	}
	// The publish command saw the allocated directory.
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read allocated-path marker: %v", err)
	}
	if string(data) != allocRes.Workdir {
		t.Errorf("publish command saw workdir %q, want the allocated %q", string(data), allocRes.Workdir)
	}
}

// Spec: §7.8 — a command that declares neither run: nor sh: is rejected at
// execution rather than running an empty command.
func TestRunMarketplace_RejectsMalformedCommand(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{{}}, // neither run: nor sh:
	})
	if _, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()}); err == nil {
		t.Errorf("a command with neither run: nor sh: must error")
	}

	both := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{{Run: []string{"true"}, Sh: "true"}},
	})
	if _, err := RunMarketplace(context.Background(), RunOptions{Output: both, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()}); err == nil {
		t.Errorf("a command with both run: and sh: must error")
	}
}

// Spec: §7.8 — the on_error cleanup is best effort: a cleanup command's non-zero
// exit is logged and does not mask the original failure, continue_on_error keeps
// the rest of the cleanup running, and a cleanup command without continue_on_error
// stops the remaining cleanup.
func TestRunCleanup_BestEffort(t *testing.T) {
	t.Parallel()
	r := WorkflowRunner{Label: "publish \"x\"", Stderr: discardBuf()}

	// continue_on_error: the failing first cleanup command is logged and the
	// second still runs.
	workdir := t.TempDir()
	r.cleanup(context.Background(), "publish", []Command{
		{Run: []string{"false"}, ContinueOnError: true},
		touch("second", "second-ran"),
	}, map[string]string{"PODIUM_WORKDIR": workdir})
	if got := readMarker(t, workdir, "second"); !strings.Contains(got, "second-ran") {
		t.Errorf("continue_on_error cleanup must run later cleanup commands: %q", got)
	}

	// Without continue_on_error: a failing cleanup command stops the remaining
	// cleanup, so the second command does not run.
	workdir2 := t.TempDir()
	r.cleanup(context.Background(), "prepare", []Command{
		{Run: []string{"false"}},
		touch("second", "should-not-run"),
	}, map[string]string{"PODIUM_WORKDIR": workdir2})
	if _, err := os.Stat(filepath.Join(workdir2, "second")); !os.IsNotExist(err) {
		t.Errorf("a non-continue cleanup failure must stop the remaining cleanup: stat err=%v", err)
	}
}

// substitute expands an injected variable and leaves an uninjected reference as
// its literal $name, so the dry-run preview keeps an ambient credential reference
// rather than blanking it.
func TestSubstitute(t *testing.T) {
	t.Parallel()
	vars := map[string]string{"PODIUM_WORKDIR": "/w", "PODIUM_EMPTY": ""}
	for _, tc := range []struct{ in, want string }{
		{"$PODIUM_WORKDIR/x", "/w/x"},
		{"${PODIUM_WORKDIR}/x", "/w/x"},
		{"$GH_TOKEN", "$GH_TOKEN"},                         // uninjected: left literal
		{"https://$GH_TOKEN@h/r", "https://$GH_TOKEN@h/r"}, // ambient credential survives
		{"$PODIUM_EMPTY", ""},                              // injected-but-empty expands to empty
		{"$PODIUM_WORKDIR and $SSH_AUTH_SOCK", "/w and $SSH_AUTH_SOCK"},
	} {
		if got := substitute(tc.in, vars); got != tc.want {
			t.Errorf("substitute(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// sanitizeID replaces a path separator in an output ID so the allocated working
// directory name carries no separator.
func TestSanitizeID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"acme-agents", "acme-agents"},
		{"team/finance", "team-finance"},
		{"a/b\\c", "a-b-c"},
	} {
		if got := sanitizeID(tc.in); got != tc.want {
			t.Errorf("sanitizeID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// writeChangeSummary surfaces a structured error when the change-summary
// directory cannot be created, rather than a successful render with no summary.
func TestWriteChangeSummary_Error(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	// A regular file at <workdir>/.podium blocks the summary-directory creation.
	if err := os.WriteFile(filepath.Join(workdir, ".podium"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	_, err := writeChangeSummary(workdir, &RenderResult{OutputID: "acme-agents"})
	if err == nil {
		t.Fatalf("writeChangeSummary must error when the .podium dir cannot be created")
	}
	if !strings.Contains(err.Error(), "acme-agents") {
		t.Errorf("error must name the output id: %v", err)
	}
}

// RunMarketplace defaults Stdout, Stderr, and Now when the caller leaves them
// nil. A --check run with all three nil exercises the default-init branches
// without producing output, because --check returns before any command or render.
func TestRunMarketplace_NilDefaults(t *testing.T) {
	t.Parallel()
	out := runOutput("unused", []string{"claude-code"}, Workflow{})
	res, err := RunMarketplace(context.Background(), RunOptions{Output: out, Check: true})
	if err != nil {
		t.Fatalf("--check with nil writers must succeed: %v", err)
	}
	if res.Render != nil || res.Published {
		t.Errorf("--check must neither render nor publish: %+v", res)
	}
}

// runPipeline surfaces a malformed commit_message template as a run error, so a
// bad template fails the publish rather than committing an unrendered message.
func TestRunMarketplace_MalformedCommitMessage(t *testing.T) {
	t.Parallel()
	reg := renderFixtureRegistry(t)
	workdir := t.TempDir()
	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("done", "done")},
	})
	out.CommitMessage = "{{.Nope" // unterminated action
	if _, err := RunMarketplace(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()}); err == nil {
		t.Errorf("a malformed commit_message must fail the run")
	}
}

// printPhase marks a skip_if_no_changes command as skipped when the render
// produced no diff, so the dry-run output shows the suppression without running
// the command.
func TestPrintPhase_SkipMarker(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmds := []Command{
		{Run: []string{"git", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"}, SkipIfNoChanges: true},
		{Run: []string{"git", "push"}},
	}
	printPhase(&buf, "publish", cmds, map[string]string{"PODIUM_CHANGED": "false", "PODIUM_COMMIT_MESSAGE": "msg"})
	out := buf.String()
	if !strings.Contains(out, "skipped (no changes)") {
		t.Errorf("printPhase must mark a skip_if_no_changes command on a no-change render:\n%s", out)
	}
	if !strings.Contains(out, "git commit -m msg") {
		t.Errorf("printPhase must substitute variables:\n%s", out)
	}
}

// discardBuf returns a writer that drops output, so test runs do not print
// subprocess stdout and stderr.
func discardBuf() *bytes.Buffer { return &bytes.Buffer{} }
