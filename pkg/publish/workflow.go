package publish

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// This file implements the §7.8 prepare->render->publish pipeline that
// `podium publish` runs per marketplace output. Podium owns config resolution,
// the effective view, plugin assignment, rendering, reconciliation, change
// detection, variable injection, command sequencing, logging, and dry-run; the
// operator's prepare and publish commands own getting the destination repository
// to the working directory and taking the rendered tree to the remote.
//
// Trust boundary. The prepare, publish, and per-phase on_error cleanup commands
// run as subprocesses with the operator's privileges and ambient credentials,
// inheriting the publish process environment plus the injected PODIUM_* variables,
// because git authentication relies on SSH_AUTH_SOCK, GH_TOKEN, and similar. They
// come from operator-authored publish.yaml, the same trust boundary as a Makefile
// or a CI script, so a catalog author cannot inject a command. They are unrelated to
// the sandboxed MaterializationHook SPI (§6.6) and to the hook artifact type. A
// command is either an argv list under run:, executed directly without a shell
// (no shell metacharacter interpretation, no injection through artifact content),
// or a string under sh:, executed through sh -c. This is why publishing runs in
// an operator CLI rather than a multi-tenant server process.

// RunOptions configures one Run of a single resolved marketplace output through
// the prepare->render->publish pipeline. Token is the publishing identity's
// registry credential, forwarded to the render's effective-view fetch and exposed
// to commands. Workdir, when set, points the render at an existing checkout (the
// --workdir flag); when empty, Run allocates a per-output working directory and
// the prepare phase clones into it. DryRun runs the prepare commands into a
// temporary directory, renders against that checkout, and prints each prepare and
// publish command with its variables substituted without running the publish
// phase. Check validates the config only and runs neither the render nor any
// command. HTTPClient and Now are injected by tests; a nil client
// uses the pkg/sync default, and a nil Now uses time.Now.
type RunOptions struct {
	Output     ResolvedOutput
	Token      string
	Workdir    string
	DryRun     bool
	Check      bool
	Stdout     io.Writer
	Stderr     io.Writer
	HTTPClient *http.Client
	Now        func() time.Time
}

// RunResult reports the outcome of one Run. Workdir is the working directory the
// render wrote into (the allocated directory, the --workdir checkout, or the
// dry-run temp directory). Render is the §7.8 render result; it is nil for a
// --check run, which does not render. Published reports whether the publish phase
// ran, which is false for a --dry-run or --check run.
type RunResult struct {
	OutputID  string
	Workdir   string
	Render    *RenderResult
	Published bool
}

// Run executes the prepare->render->publish pipeline for one resolved output
// (§7.8). The phases exist for their ordering: the prepare checkout must precede
// the render so the render reconciles against existing repository content, and the
// publish commit must follow it.
//
//   - --check (Check) validates the config only and returns before rendering or
//     running any command.
//   - The default path allocates a working directory unless --workdir set one,
//     runs the prepare commands, renders, then runs the publish commands.
//   - --dry-run (DryRun) clones into a temporary directory by running the prepare
//     commands, renders against that checkout, prints each prepare and publish
//     command with its PODIUM_* variables substituted, and does not run the
//     publish phase. Running prepare lets the render reconcile against real
//     repository content, so the printed $PODIUM_CHANGED and skip_if_no_changes
//     markers match a live run.
//
// Run fails fast on the first command that exits non-zero, unless that command
// declares continue_on_error. On a failure it runs the failing phase's own
// on_error cleanup commands (best effort) before returning the original error: a
// prepare failure runs prepare_on_error, and a publish failure runs
// publish_on_error.
func Run(ctx context.Context, opts RunOptions) (*RunResult, error) {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	out := opts.Output
	res := &RunResult{OutputID: out.ID}

	// --check validates the resolved config and stops before any side effect.
	if opts.Check {
		if err := validateOutput(out); err != nil {
			return nil, err
		}
		return res, nil
	}

	workdir, cleanup, err := resolveWorkdir(opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	res.Workdir = workdir

	return runPipeline(ctx, opts, out, workdir, res)
}

// runPipeline sequences prepare, render, and publish for a resolved output. It is
// split from Run so the workdir allocation and the --check short-circuit stay in
// one place and the phase ordering stays in another.
func runPipeline(ctx context.Context, opts RunOptions, out ResolvedOutput, workdir string, res *RunResult) (*RunResult, error) {
	vars := baseVars(out, workdir)

	// prepare runs in both modes so the checkout precedes the render and the
	// render reconciles against existing repository content. A dry run clones
	// into its temporary working directory, so the dry-run change detection and
	// the printed skip_if_no_changes markers reflect what a live run would do.
	if err := runPhase(ctx, opts, "prepare", out.Workflow.Prepare, vars, out.Workflow.PrepareOnError); err != nil {
		return nil, err
	}

	render, err := Render(ctx, RenderOptions{
		OutputID:   out.ID,
		Registry:   out.Registry,
		Identity:   out.Identity,
		Token:      opts.Token,
		Workdir:    workdir,
		Harnesses:  out.Harnesses,
		Plugins:    out.Plugins,
		HTTPClient: opts.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	res.Render = render

	// The change-driven variables become available only after the render.
	summaryPath, err := writeChangeSummary(workdir, render)
	if err != nil {
		return nil, err
	}
	commitMsg, err := renderCommitMessage(out.CommitMessage, render, opts.Now())
	if err != nil {
		return nil, err
	}
	vars["PODIUM_CHANGED"] = strconv.FormatBool(render.Changed)
	vars["PODIUM_CHANGE_SUMMARY"] = summaryPath
	vars["PODIUM_COMMIT_MESSAGE"] = commitMsg

	if opts.DryRun {
		printPhase(opts.Stdout, "prepare", out.Workflow.Prepare, vars)
		printPhase(opts.Stdout, "publish", out.Workflow.Publish, vars)
		return res, nil
	}

	if err := runPhase(ctx, opts, "publish", out.Workflow.Publish, vars, out.Workflow.PublishOnError); err != nil {
		return nil, err
	}
	res.Published = true
	return res, nil
}

// resolveWorkdir returns the working directory the render writes into and a
// cleanup function. --dry-run always renders into a temporary directory cleanup
// removes, ignoring --workdir so a dry run never touches the operator's checkout
// (§7.8 "renders into a temporary directory"). Otherwise, with --workdir set it
// points at that existing checkout and cleanup is a no-op (the operator owns the
// directory). With neither, it allocates a per-output working directory the
// prepare clone populates and cleanup removes, so a live run does not leave a
// checkout behind.
func resolveWorkdir(opts RunOptions) (string, func(), error) {
	if !opts.DryRun && opts.Workdir != "" {
		return opts.Workdir, func() {}, nil
	}
	prefix := "podium-publish-" + sanitizeID(opts.Output.ID) + "-"
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", nil, fmt.Errorf("publish %q: allocate working directory: %w", opts.Output.ID, err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// sanitizeID reduces an output ID to a temp-dir-safe fragment so the allocated
// working directory names the output without admitting a path separator.
func sanitizeID(id string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == os.PathSeparator {
			return '-'
		}
		return r
	}, id)
}

// runPhase runs the commands of one phase in order, failing fast on the first
// non-zero exit unless the command declares continue_on_error. On a failure it
// runs that phase's own on_error cleanup commands (best effort, errors logged
// not returned) before returning the original failure, so a half-applied
// checkout can be reset by cleanup scoped to the phase that failed.
func runPhase(ctx context.Context, opts RunOptions, phase string, cmds []Command, vars map[string]string, onError []Command) error {
	for i, c := range cmds {
		if c.SkipIfNoChanges && vars["PODIUM_CHANGED"] == "false" {
			fmt.Fprintf(opts.Stderr, "publish %s[%d]: skipped (no changes): %s\n", phase, i, c.display())
			continue
		}
		err := runCommand(ctx, opts, c, vars)
		if err == nil {
			continue
		}
		if c.ContinueOnError {
			fmt.Fprintf(opts.Stderr, "publish %s[%d]: %v (continue_on_error)\n", phase, i, err)
			continue
		}
		runCleanup(ctx, opts, phase, onError, vars)
		return fmt.Errorf("publish %q %s[%d] (%s): %w", opts.Output.ID, phase, i, c.display(), err)
	}
	return nil
}

// runCleanup runs a phase's on_error cleanup commands best effort: a cleanup
// failure is logged under the failing phase and does not mask the original phase
// failure, and continue_on_error is honored so a benign cleanup non-zero (a
// "nothing to reset" git exit) does not abort the rest of the cleanup. phase
// names the failing phase so the log distinguishes prepare cleanup from publish
// cleanup.
func runCleanup(ctx context.Context, opts RunOptions, phase string, cmds []Command, vars map[string]string) {
	for i, c := range cmds {
		if err := runCommand(ctx, opts, c, vars); err != nil {
			fmt.Fprintf(opts.Stderr, "publish %s_on_error[%d]: %v\n", phase, i, err)
			if !c.ContinueOnError {
				return
			}
		}
	}
}

// runCommand executes one command with the injected variables. A run: argv list
// is executed directly without a shell; an sh: string is handed to sh -c
// verbatim, so the shell performs all variable expansion. The command inherits
// the ambient environment plus the PODIUM_* variables, so the shell expands
// $PODIUM_WORKDIR and an ambient $GH_TOKEN or $SSH_AUTH_SOCK alike; pre-expanding
// the string in Go would blank every ambient credential reference the §7.8
// pipeline relies on for git authentication. A non-zero timeout bounds the
// command's wall clock; a timeout expiry surfaces as a
// context.DeadlineExceeded-wrapped error.
func runCommand(ctx context.Context, opts RunOptions, c Command, vars map[string]string) error {
	if err := c.validate(); err != nil {
		return err
	}

	cmdCtx := ctx
	var cancel context.CancelFunc
	if c.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, c.Timeout.Duration())
		defer cancel()
	}

	var cmd *exec.Cmd
	if c.Sh != "" {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", c.Sh)
	} else {
		argv := substituteArgs(c.Run, vars)
		cmd = exec.CommandContext(cmdCtx, argv[0], argv[1:]...)
	}
	cmd.Env = commandEnv(vars)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr

	err := cmd.Run()
	if cmdCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out after %s: %w", c.Timeout.Duration(), cmdCtx.Err())
	}
	return err
}

// commandEnv returns the ambient process environment with the injected PODIUM_*
// variables appended, so a command sees both the credentials in the ambient
// environment (SSH_AUTH_SOCK, GH_TOKEN) and the publish context. A later
// assignment for the same key wins under exec, so the injected variables override
// any same-named ambient value.
func commandEnv(vars map[string]string) []string {
	env := os.Environ()
	for _, k := range sortedVarKeys(vars) {
		env = append(env, k+"="+vars[k])
	}
	return env
}

// baseVars builds the injected variables available before the render: the working
// directory, the output identifier, the git destination, the registry, the
// publishing identity, and the harness set. The change-driven variables
// (PODIUM_CHANGED, PODIUM_CHANGE_SUMMARY, PODIUM_COMMIT_MESSAGE) are added after
// the render.
func baseVars(out ResolvedOutput, workdir string) map[string]string {
	return map[string]string{
		"PODIUM_WORKDIR":    workdir,
		"PODIUM_OUTPUT_ID":  out.ID,
		"PODIUM_GIT_REMOTE": out.Git.Remote,
		"PODIUM_GIT_BRANCH": out.Git.Branch,
		"PODIUM_REGISTRY":   out.Registry,
		"PODIUM_IDENTITY":   out.Identity,
		"PODIUM_HARNESSES":  strings.Join(out.Harnesses, ","),
	}
}

// commitTemplateData is the data passed to the commit_message template. It
// exposes the change count and a timestamp, matching the §7.8 example
// "Sync Podium catalog ({{.ChangedCount}} changes) {{.Timestamp}}".
type commitTemplateData struct {
	ChangedCount int
	Timestamp    string
	OutputID     string
}

// renderCommitMessage renders the output's commit_message template against the
// render's change set and the supplied time. An empty commit_message yields a
// default message naming the output and the change count, so the commit always
// carries a message. A malformed template surfaces a config-level error.
func renderCommitMessage(tmpl string, render *RenderResult, now time.Time) (string, error) {
	count := len(render.ChangedArtifacts)
	ts := now.UTC().Format(time.RFC3339)
	if tmpl == "" {
		return fmt.Sprintf("Sync Podium catalog: %s (%d changes) %s", render.OutputID, count, ts), nil
	}
	t, err := template.New("commit_message").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("%w: marketplace %q commit_message template: %w", ErrConfigInvalid, render.OutputID, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, commitTemplateData{ChangedCount: count, Timestamp: ts, OutputID: render.OutputID}); err != nil {
		return "", fmt.Errorf("%w: marketplace %q commit_message render: %w", ErrConfigInvalid, render.OutputID, err)
	}
	return buf.String(), nil
}

// writeChangeSummary writes the render's change set to a JSON file under workdir
// and returns its path, the $PODIUM_CHANGE_SUMMARY value (§7.8). The file lives in
// the .podium directory beside the sync lock so it is sync-local state the
// marketplace repository does not commit.
func writeChangeSummary(workdir string, render *RenderResult) (string, error) {
	dir := filepath.Join(workdir, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("publish %q: create change-summary dir: %w", render.OutputID, err)
	}
	path := filepath.Join(dir, "publish-change-summary.json")
	if err := os.WriteFile(path, render.ChangeSummaryJSON(), 0o644); err != nil {
		return "", fmt.Errorf("publish %q: write change summary: %w", render.OutputID, err)
	}
	return path, nil
}

// printPhase prints each command of a phase with its variables substituted, the
// --dry-run output. A skip_if_no_changes command that would be skipped is marked
// so the operator sees the suppression without the command running.
func printPhase(w io.Writer, phase string, cmds []Command, vars map[string]string) {
	fmt.Fprintf(w, "# %s\n", phase)
	for _, c := range cmds {
		skipped := ""
		if c.SkipIfNoChanges && vars["PODIUM_CHANGED"] == "false" {
			skipped = "  # skipped (no changes)"
		}
		fmt.Fprintf(w, "  %s%s\n", c.substituted(vars), skipped)
	}
}

// substitute expands the injected variables in s for the --dry-run preview. It
// uses os.Expand so $VAR and ${VAR} both resolve against vars, and a reference
// that names no injected variable is left as its literal $name. Preserving the
// literal keeps an ambient credential reference such as $GH_TOKEN or
// $SSH_AUTH_SOCK in the printed command, so the preview shows what the live shell
// will expand rather than blanking the reference. This is a preview-only helper:
// an sh: command is run verbatim by sh -c (runCommand), and a run: argv list is
// expanded against the injected variables only (substituteArgs).
func substitute(s string, vars map[string]string) string {
	return os.Expand(s, func(key string) string {
		if v, ok := vars[key]; ok {
			return v
		}
		return "$" + key
	})
}

// substituteArgs expands each argv element against the injected variables, so an
// argv command's $PODIUM_WORKDIR resolves the same way the live exec does. An
// argv element runs with no shell, so a reference Podium does not inject (an
// ambient $GH_TOKEN) is left as its literal $name rather than blanked, matching
// substitute.
func substituteArgs(args []string, vars map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = substitute(a, vars)
	}
	return out
}

// sortedVarKeys returns the variable keys in sorted order so the env append and
// the dry-run print are deterministic.
func sortedVarKeys(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// validate rejects a command that declares neither run: nor sh:, or both, so a
// malformed step surfaces at execution rather than running an empty command.
func (c Command) validate() error {
	switch {
	case len(c.Run) == 0 && c.Sh == "":
		return fmt.Errorf("%w: command declares neither run: nor sh:", ErrConfigInvalid)
	case len(c.Run) > 0 && c.Sh != "":
		return fmt.Errorf("%w: command declares both run: and sh:", ErrConfigInvalid)
	}
	return nil
}

// display returns a short human label for a command, used in error messages and
// skip logs. It does not substitute variables, so the label names the command as
// the operator wrote it.
func (c Command) display() string {
	if c.Sh != "" {
		return "sh -c " + strconv.Quote(c.Sh)
	}
	return strings.Join(c.Run, " ")
}

// substituted returns the command rendered with variables expanded, the dry-run
// representation. An sh: command is shown as the sh -c invocation; a run: command
// is shown as its expanded argv.
func (c Command) substituted(vars map[string]string) string {
	if c.Sh != "" {
		return "sh -c " + strconv.Quote(substitute(c.Sh, vars))
	}
	return strings.Join(substituteArgs(c.Run, vars), " ")
}
