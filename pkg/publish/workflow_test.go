package publish

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests exercise the §7.8 prepare->render->publish workflow runner against
// the filesystem-source fixture registry (no live server) with stub commands that
// touch marker files: variable injection, skip_if_no_changes suppression,
// fail-fast on the first non-zero exit, continue_on_error, the per-command
// timeout, the on_error cleanup, --dry-run, --check, and the --workdir versus
// allocated-workdir paths.

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

// touch returns a run: command that writes its variable-expanded marker line to a
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
func TestRun_VariableInjection(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("vars",
			"id=$PODIUM_OUTPUT_ID remote=$PODIUM_GIT_REMOTE branch=$PODIUM_GIT_BRANCH "+
				"changed=$PODIUM_CHANGED harnesses=$PODIUM_HARNESSES wd=$PODIUM_WORKDIR "+
				"msg=$PODIUM_COMMIT_MESSAGE summary=$PODIUM_CHANGE_SUMMARY identity=$PODIUM_IDENTITY")},
	})

	res, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
	if err != nil {
		t.Fatalf("Run: %v", err)
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

// Spec: §7.8 — the commit_message template is rendered with the change count and
// a timestamp into $PODIUM_COMMIT_MESSAGE.
func TestRun_CommitMessageTemplate(t *testing.T) {
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
func TestRun_SkipIfNoChanges(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Sh: "printf ran >> \"$PODIUM_WORKDIR/committed\"", SkipIfNoChanges: true},
		},
	})

	// First run: the render changes the tree, so the command runs.
	first, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
	if err != nil {
		t.Fatalf("first Run: %v", err)
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
	second, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
	if err != nil {
		t.Fatalf("second Run: %v", err)
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
func TestRun_FailFast(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Run: []string{"false"}},
			touch("after", "should-not-run"),
		},
	})

	_, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
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
func TestRun_ContinueOnError(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Run: []string{"false"}, ContinueOnError: true},
			touch("after", "ran-after-error"),
		},
	})

	res, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
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
func TestRun_Timeout(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{
			{Run: []string{"sleep", "10"}, Timeout: Duration(50 * time.Millisecond)},
		},
	})

	start := time.Now()
	_, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
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

// Spec: §7.8 — the on_error cleanup list runs when a phase command fails, before
// the failure propagates.
func TestRun_OnErrorCleanup(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{{Run: []string{"false"}}},
		OnError: []Command{touch("cleanup", "cleanup-ran")},
	})

	_, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
	if err == nil {
		t.Fatalf("the failing publish command must fail the run")
	}
	if got := readMarker(t, workdir, "cleanup"); !strings.Contains(got, "cleanup-ran") {
		t.Errorf("on_error cleanup must run on a phase failure: %q", got)
	}
}

// Spec: §7.8 — --dry-run renders into a temporary directory and prints each
// prepare and publish command with variables substituted, without running the
// publish phase. The operator's checkout is untouched.
func TestRun_DryRun(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Prepare: []Command{{Run: []string{"git", "clone", "$PODIUM_GIT_REMOTE", "$PODIUM_WORKDIR"}}},
		Publish: []Command{
			touch("must-not-run", "ran"),
			{Run: []string{"git", "commit", "-m", "$PODIUM_COMMIT_MESSAGE"}, SkipIfNoChanges: true},
		},
	})

	var stdout bytes.Buffer
	res, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, DryRun: true, Now: fixedNow(), Stdout: &stdout, Stderr: io_Discard()})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if res.Published {
		t.Errorf("a dry run must not publish")
	}
	if res.Workdir == workdir {
		t.Errorf("a dry run must render into a temporary directory, not the operator's --workdir")
	}

	// The publish command must not have run against the operator's checkout.
	if _, statErr := os.Stat(filepath.Join(workdir, "must-not-run")); !os.IsNotExist(statErr) {
		t.Errorf("a dry run must not run a publish command: stat err=%v", statErr)
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

// Spec: §7.8 — --check validates the config only and runs neither the render nor
// any command.
func TestRun_Check(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("must-not-run", "ran")},
	})

	res, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Check: true, Stdout: io_Discard(), Stderr: io_Discard()})
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
	if _, err := Run(context.Background(), RunOptions{Output: bad, Check: true, Stdout: io_Discard(), Stderr: io_Discard()}); err == nil {
		t.Errorf("--check must reject a non-publish harness")
	}
}

// Spec: §7.8 — --workdir points the render at an existing checkout, while the
// default allocates and cleans up a per-output working directory.
func TestRun_WorkdirVsAllocated(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)

	// --workdir path: the render writes into the supplied directory.
	workdir := t.TempDir()
	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("done", "done")},
	})
	res, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
	if err != nil {
		t.Fatalf("Run with --workdir: %v", err)
	}
	if res.Workdir != workdir {
		t.Errorf("--workdir must render into the supplied directory: got %q want %q", res.Workdir, workdir)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".claude-plugin", "marketplace.json")); err != nil {
		t.Errorf("the render must populate the --workdir checkout: %v", err)
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
	allocRes, err := Run(context.Background(), RunOptions{Output: allocOut, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()})
	if err != nil {
		t.Fatalf("Run with allocated workdir: %v", err)
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
func TestRun_RejectsMalformedCommand(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()

	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{{}}, // neither run: nor sh:
	})
	if _, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()}); err == nil {
		t.Errorf("a command with neither run: nor sh: must error")
	}

	both := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{{Run: []string{"true"}, Sh: "true"}},
	})
	if _, err := Run(context.Background(), RunOptions{Output: both, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()}); err == nil {
		t.Errorf("a command with both run: and sh: must error")
	}
}

// Spec: §7.8 — the on_error cleanup is best effort: a cleanup command's non-zero
// exit is logged and does not mask the original failure, continue_on_error keeps
// the rest of the cleanup running, and a cleanup command without continue_on_error
// stops the remaining cleanup.
func TestRunCleanup_BestEffort(t *testing.T) {
	t.Parallel()
	opts := RunOptions{Output: ResolvedOutput{ID: "x"}, Stderr: io_Discard()}

	// continue_on_error: the failing first cleanup command is logged and the
	// second still runs.
	workdir := t.TempDir()
	runCleanup(context.Background(), opts, []Command{
		{Run: []string{"false"}, ContinueOnError: true},
		touch("second", "second-ran"),
	}, map[string]string{"PODIUM_WORKDIR": workdir})
	if got := readMarker(t, workdir, "second"); !strings.Contains(got, "second-ran") {
		t.Errorf("continue_on_error cleanup must run later cleanup commands: %q", got)
	}

	// Without continue_on_error: a failing cleanup command stops the remaining
	// cleanup, so the second command does not run.
	workdir2 := t.TempDir()
	runCleanup(context.Background(), opts, []Command{
		{Run: []string{"false"}},
		touch("second", "should-not-run"),
	}, map[string]string{"PODIUM_WORKDIR": workdir2})
	if _, err := os.Stat(filepath.Join(workdir2, "second")); !os.IsNotExist(err) {
		t.Errorf("a non-continue cleanup failure must stop the remaining cleanup: stat err=%v", err)
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

// Run defaults Stdout, Stderr, and Now when the caller leaves them nil. A --check
// run with all three nil exercises the default-init branches without producing
// output, because --check returns before any command or render.
func TestRun_NilDefaults(t *testing.T) {
	t.Parallel()
	out := runOutput("unused", []string{"claude-code"}, Workflow{})
	res, err := Run(context.Background(), RunOptions{Output: out, Check: true})
	if err != nil {
		t.Fatalf("--check with nil writers must succeed: %v", err)
	}
	if res.Render != nil || res.Published {
		t.Errorf("--check must neither render nor publish: %+v", res)
	}
}

// runPipeline surfaces a malformed commit_message template as a run error, so a
// bad template fails the publish rather than committing an unrendered message.
func TestRun_MalformedCommitMessage(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry(t)
	workdir := t.TempDir()
	out := runOutput(reg, []string{"claude-code"}, Workflow{
		Publish: []Command{touch("done", "done")},
	})
	out.CommitMessage = "{{.Nope" // unterminated action
	if _, err := Run(context.Background(), RunOptions{Output: out, Workdir: workdir, Now: fixedNow(), Stdout: io_Discard(), Stderr: io_Discard()}); err == nil {
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

// io_Discard returns a writer that drops output, so test runs do not print
// subprocess stdout and stderr.
func io_Discard() *bytes.Buffer { return &bytes.Buffer{} }
