package sync

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// WorkflowRunner executes a target's workflow command lists (§7.5.2). It is the
// shared command machinery both the marketplace pipeline (RunMarketplace) and the
// workspace dispatch (a kind: workspace target carrying a workflow) run through,
// so the prepare/publish phases, the per-command flags, and the on_error cleanup
// behave identically for either kind. Label names the target in error messages
// and skip logs so the operator can tell which target a failure belongs to.
type WorkflowRunner struct {
	Label  string
	Stdout io.Writer
	Stderr io.Writer
}

// Phase runs the commands of one workflow phase in order (§7.5.2, §7.8), failing
// fast on the first non-zero exit unless the command declares continue_on_error.
// A skip_if_no_changes command is skipped when vars["PODIUM_CHANGED"] is "false".
// On a failure it runs that phase's own on_error cleanup commands (best effort,
// errors logged not returned) before returning the original failure, so a
// half-applied checkout can be reset by cleanup scoped to the failing phase.
func (r WorkflowRunner) Phase(ctx context.Context, phase string, cmds []Command, vars map[string]string, onError []Command) error {
	for i, c := range cmds {
		if c.SkipIfNoChanges && vars["PODIUM_CHANGED"] == "false" {
			fmt.Fprintf(r.Stderr, "%s %s[%d]: skipped (no changes): %s\n", r.Label, phase, i, c.display())
			continue
		}
		err := r.command(ctx, c, vars)
		if err == nil {
			continue
		}
		if c.ContinueOnError {
			fmt.Fprintf(r.Stderr, "%s %s[%d]: %v (continue_on_error)\n", r.Label, phase, i, err)
			continue
		}
		r.cleanup(ctx, phase, onError, vars)
		return fmt.Errorf("%s %s[%d] (%s): %w", r.Label, phase, i, c.display(), err)
	}
	return nil
}

// cleanup runs a phase's on_error commands best effort: a cleanup failure is
// logged under the failing phase and does not mask the original phase failure,
// and continue_on_error is honored so a benign cleanup non-zero (a "nothing to
// reset" git exit) does not abort the rest of the cleanup.
func (r WorkflowRunner) cleanup(ctx context.Context, phase string, cmds []Command, vars map[string]string) {
	for i, c := range cmds {
		if err := r.command(ctx, c, vars); err != nil {
			fmt.Fprintf(r.Stderr, "%s %s_on_error[%d]: %v\n", r.Label, phase, i, err)
			if !c.ContinueOnError {
				return
			}
		}
	}
}

// command executes one command with the injected variables. A run: argv list is
// executed directly without a shell; an sh: string is handed to sh -c verbatim,
// so the shell performs all variable expansion. The command inherits the ambient
// environment plus the PODIUM_* variables, so the shell expands $PODIUM_WORKDIR
// and an ambient $GH_TOKEN or $SSH_AUTH_SOCK alike; pre-expanding the string in Go
// would blank every ambient credential reference the §7.8 pipeline relies on for
// git authentication. A non-zero timeout bounds the command's wall clock; a
// timeout expiry surfaces as a context.DeadlineExceeded-wrapped error.
func (r WorkflowRunner) command(ctx context.Context, c Command, vars map[string]string) error {
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
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr

	err := cmd.Run()
	if cmdCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out after %s: %w", c.Timeout.Duration(), cmdCtx.Err())
	}
	return err
}

// commandEnv returns the ambient process environment with the injected PODIUM_*
// variables appended, so a command sees both the credentials in the ambient
// environment (SSH_AUTH_SOCK, GH_TOKEN) and the workflow context. A later
// assignment for the same key wins under exec, so the injected variables override
// any same-named ambient value.
func commandEnv(vars map[string]string) []string {
	env := os.Environ()
	for _, k := range sortedVarKeys(vars) {
		env = append(env, k+"="+vars[k])
	}
	return env
}
