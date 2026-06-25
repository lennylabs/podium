package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lennylabs/podium/pkg/publish"
	"github.com/lennylabs/podium/pkg/sync"
)

// publishCmd implements `podium publish` (§7.8): it resolves the marketplace
// outputs from publish.yaml and runs the prepare->render->publish pipeline per
// output. The flags mirror the §7.8 surface:
//
//	--output <id>    publish only the named marketplace output (default: all)
//	--config <path>  read this publish.yaml instead of the merged config scopes
//	--workdir <dir>  render into an existing checkout (single output only; default: allocate per output)
//	--dry-run        render into a temp dir and print each command; run no command
//	--check          validate the config only; render and run nothing
//	--json           emit a structured JSON envelope on stdout
//
// $PODIUM_WORKDIR is the per-output working and checkout directory, so --workdir
// names a single checkout and requires a single-output selection: pass it with
// --output, or against a config that resolves one output. A --workdir shared
// across multiple outputs would render them into one checkout where each output
// reconciles against the previous output's files, so the combination exits 2.
//
// Exit codes mirror syncCmd: 2 for a flag error or a config error (config.invalid,
// an unknown --output, a missing --config path, or a missing registry
// config.no_registry), 1 for a config-load failure (a malformed or unreadable
// config file) and for a runtime failure (a fetch error, a render error, a
// non-zero workflow command), 0 on success.
func publishCmd(args []string) int {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	setUsage(fs, "Render the catalog into harness-native marketplace repositories and push them to git remotes.")
	output := fs.String("output", "", "publish only the named marketplace output (default: every output)")
	configPath := fs.String("config", "", "read this publish.yaml instead of the merged config scopes")
	workdir := fs.String("workdir", "", "render into an existing checkout; single output only, pair with --output (default: allocate a working directory per output)")
	dryRun := fs.Bool("dry-run", false, "render into a temp dir and print each command; run no operator command and no publish phase")
	check := fs.Bool("check", false, "validate the config only; render and run nothing")
	asJSON := fs.Bool("json", false, "emit a structured JSON envelope on stdout")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}

	outputs, err := resolvePublishOutputs(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return publishExitCode(err)
	}
	// §7.8 --output selects one declared output by id; an unknown id is a config
	// error, like a flag error, so it exits 2.
	if *output != "" {
		selected, ok := selectOutput(outputs, *output)
		if !ok {
			fmt.Fprintf(os.Stderr, "error: no marketplace output named %q in publish.yaml\n", *output)
			return 2
		}
		outputs = []publish.ResolvedOutput{selected}
	}
	if len(outputs) == 0 {
		fmt.Fprintln(os.Stderr, "error: no marketplace outputs configured — add a marketplaces: entry to publish.yaml")
		return 2
	}
	// §7.8 names $PODIUM_WORKDIR as the per-output working and checkout directory,
	// and the only --workdir example pairs it with --output (a single output). A
	// --workdir shared across multiple outputs renders each output into the same
	// checkout, so output N reconciles against output N-1's freshly-written files
	// and the outputs clobber each other. Reject the combination as a config error
	// (exit 2): --workdir requires a single-output selection. With --output set the
	// selection above already narrowed outputs to one, so this only fires when the
	// operator passes --workdir against a config that resolves more than one output
	// without naming which.
	if *workdir != "" && len(outputs) > 1 {
		fmt.Fprintf(os.Stderr, "error: --workdir names a single per-output checkout but %d outputs resolved — pass --output <id> to select one\n", len(outputs))
		return 2
	}
	// spec: §7.8 publish.yaml resolves the registry by the same §7.5.2 rules as
	// sync.yaml, so an unset registry across all scopes is config.no_registry.
	// syncCmd maps that to exit 2 (main.go), and a --check run resolves the same
	// config, so guard it here for every output rather than letting the render's
	// sync.FetchRecords surface it as a runtime fetch failure (exit 1).
	for _, out := range outputs {
		if out.Registry == "" {
			fmt.Fprintf(os.Stderr, "error: config.no_registry: no registry configured for output %q — set defaults.registry in publish.yaml or pass PODIUM_REGISTRY\n", out.ID)
			return 2
		}
	}

	// Interrupting publish (SIGINT / SIGTERM) cancels the in-flight workflow
	// command so a long-running clone or push stops cleanly.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Under --json the structured envelope is the stdout contract, so the
	// pipeline's diagnostic output (the dry-run command preview, the streamed
	// workflow command output) goes to stderr to keep stdout valid JSON.
	pipeStdout := os.Stdout
	if *asJSON {
		pipeStdout = os.Stderr
	}

	results := make([]*publish.RunResult, 0, len(outputs))
	failures := 0
	for _, out := range outputs {
		// §7.8: a live render fetches the publishing identity's effective view
		// from a server-source registry, so it carries the caller credential the
		// read + sync paths use. --check renders nothing, so it needs no token.
		token := ""
		if !*check && sync.IsServerSource(out.Registry) {
			token = readCLIToken(out.Registry)
		}
		res, rerr := publish.Run(ctx, publish.RunOptions{
			Output:  out,
			Token:   token,
			Workdir: *workdir,
			DryRun:  *dryRun,
			Check:   *check,
			Stdout:  pipeStdout,
			Stderr:  os.Stderr,
		})
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "publish %s: %v\n", out.ID, rerr)
			failures++
			if errors.Is(rerr, publish.ErrConfigInvalid) || errors.Is(rerr, sync.ErrNoRegistry) {
				// A config error short-circuits the run: every output shares the
				// config, so the rest would fail the same way.
				return publishExitCode(rerr)
			}
			continue
		}
		results = append(results, res)
	}

	if *asJSON {
		printPublishJSON(results)
	} else {
		printPublishHuman(results, *check, *dryRun)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// resolvePublishOutputs loads publish.yaml and applies the merged defaults to
// each marketplace output (§7.8). An explicit --config path reads that file
// directly; otherwise the three §7.5.2 scopes merge by precedence. The registry
// resolves per the §7.5.2 ladder (PODIUM_REGISTRY over defaults.registry).
//
// The error classes match syncCmd's multi-output --config path
// (runMultiTargetSync). A parse or I/O failure reading the config file is a
// load failure that exits 1, mirroring sync.ReadConfigFile (main.go
// runMultiTargetSync); an explicit --config path that does not exist exits 2,
// mirroring the cfg == nil branch there; a structurally-invalid config from
// Resolve (a non-publish harness, a malformed glob or command) carries
// publish.ErrConfigInvalid and exits 2.
func resolvePublishOutputs(configPath string) ([]publish.ResolvedOutput, error) {
	var cfg *publish.PublishConfig
	if configPath != "" {
		c, err := publish.ReadConfigFile(configPath)
		if err != nil {
			// A parse or I/O failure is a config-load failure, not a validation
			// failure: syncCmd's runMultiTargetSync returns 1 for the analogous
			// sync.ReadConfigFile error, so leave it unwrapped for exit 1.
			return nil, err
		}
		if c == nil {
			// An explicit --config path that does not exist exits 2, mirroring
			// runMultiTargetSync's missing-file branch.
			return nil, fmt.Errorf("%w: config not found: %s", publish.ErrConfigInvalid, configPath)
		}
		cfg = c
	} else {
		ws, _ := os.Getwd()
		home, _ := os.UserHomeDir()
		merged, _, err := publish.LoadMergedConfig(ws, home)
		if err != nil {
			// A LoadMergedConfig failure is a config-load failure, matching
			// syncCmd's LoadMergedConfig branch, which returns 1.
			return nil, err
		}
		cfg = merged
	}
	return cfg.Resolve(os.Getenv)
}

// selectOutput returns the resolved output whose id matches name (§7.8 --output).
func selectOutput(outputs []publish.ResolvedOutput, name string) (publish.ResolvedOutput, bool) {
	for _, o := range outputs {
		if o.ID == name {
			return o, true
		}
	}
	return publish.ResolvedOutput{}, false
}

// publishExitCode maps a publish error to an exit code consistent with syncCmd.
// A config error exits 2: a structurally-invalid config (config.invalid) and an
// unset registry (config.no_registry) both exit 2, matching syncCmd, which
// returns 2 for a Resolve failure and for an unconfigured registry (main.go).
// A config-load failure (a malformed or unreadable config file) and every
// runtime failure exit 1, matching syncCmd's runMultiTargetSync, which returns
// 1 for a sync.ReadConfigFile error and for a per-target run failure.
func publishExitCode(err error) int {
	if errors.Is(err, publish.ErrConfigInvalid) || errors.Is(err, sync.ErrNoRegistry) {
		return 2
	}
	return 1
}

// printPublishHuman renders one block per output mirroring syncCmd's printHuman:
// the output id, the resolved working directory, whether the render changed and
// the changed artifacts, and whether the publish phase ran.
func printPublishHuman(results []*publish.RunResult, check, dryRun bool) {
	if check {
		fmt.Fprintln(os.Stdout, "publish.yaml: ok")
	}
	for _, r := range results {
		fmt.Fprintf(os.Stdout, "== output %s ==\n", r.OutputID)
		if r.Render == nil {
			// A --check run renders nothing.
			continue
		}
		if dryRun {
			fmt.Fprintln(os.Stdout, "(dry-run; nothing pushed)")
		}
		fmt.Fprintf(os.Stdout, "workdir:  %s\n", r.Workdir)
		fmt.Fprintf(os.Stdout, "changed:  %t\n", r.Render.Changed)
		if len(r.Render.ChangedArtifacts) > 0 {
			fmt.Fprintln(os.Stdout, "artifacts:")
			for _, id := range r.Render.ChangedArtifacts {
				fmt.Fprintf(os.Stdout, "  - %s\n", id)
			}
		}
		fmt.Fprintf(os.Stdout, "published: %t\n", r.Published)
	}
}

// printPublishJSON emits a structured envelope per output mirroring syncCmd's
// printJSON: {outputs: [{output, workdir, changed, changed_artifacts,
// published}]}. A jq consumer reads .outputs[].changed or
// .outputs[].changed_artifacts directly.
func printPublishJSON(results []*publish.RunResult) {
	type outEnv struct {
		Output           string   `json:"output"`
		Workdir          string   `json:"workdir"`
		Changed          bool     `json:"changed"`
		ChangedArtifacts []string `json:"changed_artifacts"`
		Published        bool     `json:"published"`
	}
	env := struct {
		Outputs []outEnv `json:"outputs"`
	}{Outputs: []outEnv{}}
	for _, r := range results {
		e := outEnv{Output: r.OutputID, Workdir: r.Workdir, ChangedArtifacts: []string{}}
		if r.Render != nil {
			e.Changed = r.Render.Changed
			e.Published = r.Published
			e.ChangedArtifacts = emptyIfNil(r.Render.ChangedArtifacts)
		}
		env.Outputs = append(env.Outputs, e)
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: encode json: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stdout, string(b))
}
