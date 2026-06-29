package sync

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// This file implements the §7.8 prepare->render->publish pipeline that
// `podium sync` runs per kind: marketplace target. Podium owns config
// resolution, the effective view, plugin assignment, rendering, reconciliation,
// change detection, variable injection, command sequencing, logging, and
// dry-run; the operator's prepare and publish commands own getting the
// destination repository to the working directory and taking the rendered tree
// to the remote.
//
// Trust boundary. The prepare, publish, and per-phase on_error cleanup commands
// run as subprocesses with the operator's privileges and ambient credentials,
// inheriting the podium sync process environment plus the injected PODIUM_*
// variables, because git authentication relies on SSH_AUTH_SOCK, GH_TOKEN, and
// similar. A workflow executes only on the podium sync CLI path and never inside
// the registry server or the MCP server (§7.5.2 workflow-execution boundary).
// The commands are unrelated to the sandboxed MaterializationHook SPI (§6.6) and
// to the hook artifact type. A command is either an argv list under run:,
// executed directly without a shell (no shell metacharacter interpretation, no
// injection through artifact content), or a string under sh:, executed through
// sh -c.

// RunOptions configures one RunMarketplace of a single resolved marketplace
// output through the prepare->render->publish pipeline. Token is the publishing
// identity's registry credential, forwarded to the render's effective-view fetch
// and exposed to commands. Workdir, when set, points the render at an existing
// checkout; when empty, RunMarketplace allocates a per-output working directory
// and the prepare phase clones into it. DryRun renders into a temporary directory
// and prints each prepare and publish command with its variables substituted,
// running no operator command and no publish phase. Check validates the config
// only and runs neither the render nor any command. HTTPClient and Now are
// injected by tests; a nil client uses the default, and a nil Now uses time.Now.
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

// RunResult reports the outcome of one RunMarketplace. Workdir is the working
// directory the render wrote into (the allocated directory, the supplied
// checkout, or the dry-run temp directory). Render is the §7.8 render result; it
// is nil for a --check run, which does not render. Published reports whether the
// publish phase ran, which is false for a --dry-run or --check run.
type RunResult struct {
	OutputID  string
	Workdir   string
	Render    *RenderResult
	Published bool
}

// RunMarketplace executes the prepare->render->publish pipeline for one resolved
// marketplace output (§7.8). The phases exist for their ordering: the prepare
// checkout must precede the render so the render reconciles against existing
// repository content, and the publish commit must follow it.
//
//   - --check (Check) validates the config only and returns before rendering or
//     running any command.
//   - The default path allocates a working directory unless Workdir set one,
//     runs the prepare commands, renders, then runs the publish commands.
//   - --dry-run (DryRun) is a preview. It renders into a temporary directory and
//     prints each prepare and publish command with its PODIUM_* variables
//     substituted, running no operator command and no publish phase. Because no
//     prepare clone populates the temporary directory, the render reconciles
//     against an empty tree, so a dry run reports the whole rendered tree as
//     changed.
//
// RunMarketplace fails fast on the first command that exits non-zero, unless that
// command declares continue_on_error. On a failure it runs the failing phase's
// own on_error cleanup commands (best effort) before returning the original
// error: a prepare failure runs prepare_on_error, and a publish failure runs
// publish_on_error.
func RunMarketplace(ctx context.Context, opts RunOptions) (*RunResult, error) {
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
		if err := ValidateOutput(out); err != nil {
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
// split from RunMarketplace so the workdir allocation and the --check
// short-circuit stay in one place and the phase ordering stays in another. A dry
// run prints both phases instead of executing them, so it runs no operator
// command and performs no network clone or push; only the render runs, into a
// temporary directory.
func runPipeline(ctx context.Context, opts RunOptions, out ResolvedOutput, workdir string, res *RunResult) (*RunResult, error) {
	vars := baseVars(out, workdir)

	// A live run executes the prepare commands so the checkout precedes the
	// render and the render reconciles against existing repository content. A dry
	// run prints them instead: it is a preview that renders into a temporary
	// directory and runs no operator command (§7.8 "prints each command with
	// variables substituted"). The render then reconciles against the empty temp
	// directory, so a dry run reports the whole rendered tree as changed.
	if !opts.DryRun {
		if err := runPhase(ctx, opts, "prepare", out.Workflow.Prepare, vars, out.Workflow.PrepareOnError); err != nil {
			return nil, err
		}
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
// removes, ignoring the supplied Workdir so a dry run never touches the
// operator's checkout (§7.8 "renders into a temporary directory"). Otherwise,
// with Workdir set it points at that existing checkout and cleanup is a no-op
// (the operator owns the directory). With neither, it allocates a per-output
// working directory the prepare clone populates and cleanup removes, so a live
// run does not leave a checkout behind.
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

// runPhase runs one marketplace workflow phase through the shared WorkflowRunner,
// labeling failures and skips with the marketplace output id so a multi-output
// publish names the failing output.
func runPhase(ctx context.Context, opts RunOptions, phase string, cmds []Command, vars map[string]string, onError []Command) error {
	r := WorkflowRunner{Label: "publish " + strconv.Quote(opts.Output.ID), Stdout: opts.Stdout, Stderr: opts.Stderr}
	return r.Phase(ctx, phase, cmds, vars, onError)
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

// substituted returns the command rendered with variables expanded, the dry-run
// representation. An sh: command is shown as the sh -c invocation; a run: command
// is shown as its expanded argv.
func (c Command) substituted(vars map[string]string) string {
	if c.Sh != "" {
		return "sh -c " + strconv.Quote(substitute(c.Sh, vars))
	}
	return strings.Join(substituteArgs(c.Run, vars), " ")
}
