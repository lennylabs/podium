// Command podium is the unified Podium CLI. Stage 2 ships the sync
// subcommand against a filesystem-source registry (spec §13.11). Other
// subcommands (init, login, layer, lint, search, domain, artifact, vuln,
// admin, status, profile) land in subsequent phases.
//
// Usage:
//
//	podium sync [flags]
//
// Flags supported in Stage 2:
//
//	--registry <path>   Filesystem registry path (required for filesystem source).
//	--target   <path>   Destination directory (default: cwd).
//	--harness  <name>   Adapter name (default: none).
//	--dry-run           Resolve and report; write nothing.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sync"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "sync":
		os.Exit(syncCmd(os.Args[2:]))
	case "lint":
		os.Exit(lintCmd(os.Args[2:]))
	case "version":
		fmt.Println("podium 0.0.0-dev")
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

const usage = `usage: podium <command> [flags]

Commands:
  sync     Materialize the caller's effective view through a HarnessAdapter.
  lint     Validate manifests in a filesystem-source registry.
  version  Print the podium version.
  help     Print this message.
`

func syncCmd(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	registry := fs.String("registry", "", "filesystem registry path (required)")
	target := fs.String("target", ".", "destination directory")
	harness := fs.String("harness", "none", "harness adapter")
	dryRun := fs.Bool("dry-run", false, "resolve and report; write nothing")
	asJSON := fs.Bool("json", false, "emit a structured JSON envelope on stdout")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required (filesystem path)")
		return 2
	}
	abs, err := filepath.Abs(*target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot resolve target: %v\n", err)
		return 2
	}
	res, err := sync.Run(sync.Options{
		RegistryPath: *registry,
		Target:       abs,
		AdapterID:    *harness,
		DryRun:       *dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync failed: %v\n", err)
		return 1
	}
	if *asJSON {
		printJSON(res)
	} else {
		printHuman(res, *dryRun)
	}
	return 0
}

func printHuman(res *sync.Result, dryRun bool) {
	if dryRun {
		fmt.Fprintln(os.Stdout, "(dry-run; nothing written)")
	}
	fmt.Fprintf(os.Stdout, "adapter: %s\n", res.Adapter)
	fmt.Fprintf(os.Stdout, "target:  %s\n", res.Target)
	fmt.Fprintf(os.Stdout, "artifacts:\n")
	for _, a := range res.Artifacts {
		fmt.Fprintf(os.Stdout, "  - %s  [%s]\n", a.ID, a.Layer)
		for _, f := range a.Files {
			fmt.Fprintf(os.Stdout, "      %s\n", f)
		}
	}
}

func lintCmd(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	registry := fs.String("registry", "", "filesystem registry path (required)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}

	reg, err := filesystem.Open(*registry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	diags := (&lint.Linter{}).Lint(reg, records)
	if len(diags) == 0 {
		fmt.Println("lint: no issues.")
		return 0
	}
	exit := 0
	for _, d := range diags {
		fmt.Fprintln(os.Stdout, d.String())
		if d.Severity == lint.SeverityError {
			exit = 1
		}
	}
	return exit
}

// printJSON emits a stable JSON envelope. Stage 2 keeps this tiny and
// dependency-free; the schema can grow as more fields land.
func printJSON(res *sync.Result) {
	fmt.Fprintf(os.Stdout, "{\n  \"adapter\": %q,\n  \"target\": %q,\n  \"artifacts\": [", res.Adapter, res.Target)
	for i, a := range res.Artifacts {
		if i > 0 {
			fmt.Fprint(os.Stdout, ",")
		}
		fmt.Fprintf(os.Stdout, "\n    {\"id\": %q, \"layer\": %q, \"files\": [", a.ID, a.Layer)
		for j, f := range a.Files {
			if j > 0 {
				fmt.Fprint(os.Stdout, ", ")
			}
			fmt.Fprintf(os.Stdout, "%q", f)
		}
		fmt.Fprint(os.Stdout, "]}")
	}
	fmt.Fprintln(os.Stdout, "\n  ]\n}")
}
