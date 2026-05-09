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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	case "search":
		os.Exit(searchCmd(os.Args[2:]))
	case "domain":
		os.Exit(domainCmd(os.Args[2:]))
	case "artifact":
		os.Exit(artifactCmd(os.Args[2:]))
	case "init":
		os.Exit(initCmd(os.Args[2:]))
	case "profile":
		os.Exit(profileCmd(os.Args[2:]))
	case "layer":
		os.Exit(layerCmd(os.Args[2:]))
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
  sync                Materialize the caller's effective view through a HarnessAdapter.
  sync override       Add or remove ephemeral artifact toggles.
  sync save-as        Capture the current target state as a sync.yaml profile.
  lint                Validate manifests in a filesystem-source registry.
  search              Hybrid search over artifacts (registry HTTP API).
  domain show         Show a domain map.
  domain search       Hybrid search over domains.
  artifact show       Print an artifact's manifest body and frontmatter.
  init                Write ~/.podium/sync.yaml or ./.podium/sync.yaml.
  profile edit        Add or remove patterns on a sync.yaml profile.
  layer register      Register a layer with the registry.
  layer list          List registered layers.
  layer reorder       Re-sequence the layer list.
  layer unregister    Remove a layer.
  layer reingest      Trigger a fresh ingest for a layer.
  version             Print the podium version.
  help                Print this message.
`

func syncCmd(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "override":
			return syncOverrideCmd(args[1:])
		case "save-as":
			return syncSaveAsCmd(args[1:])
		}
	}
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

// stringSliceFlag is a flag.Value implementation for repeatable
// string flags such as --add and --remove.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string     { return fmt.Sprint([]string(*s)) }
func (s *stringSliceFlag) Set(v string) error { *s = append(*s, v); return nil }

func syncOverrideCmd(args []string) int {
	fs := flag.NewFlagSet("sync override", flag.ContinueOnError)
	target := fs.String("target", ".", "target directory")
	var add, remove stringSliceFlag
	fs.Var(&add, "add", "artifact id to materialize on top of the profile (repeatable)")
	fs.Var(&remove, "remove", "artifact id to drop from the profile (repeatable)")
	reset := fs.Bool("reset", false, "clear all toggles")
	dryRun := fs.Bool("dry-run", false, "resolve and report; write nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	abs, err := filepath.Abs(*target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	res, err := sync.Override(sync.OverrideOptions{
		Target: abs,
		Add:    []string(add), Remove: []string(remove),
		Reset: *reset, DryRun: *dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "override failed: %v\n", err)
		return 1
	}
	if *dryRun {
		fmt.Println("(dry-run; nothing written)")
	}
	fmt.Printf("toggles.add:    %s\n", formatToggles(res.Lock.Toggles.Add))
	fmt.Printf("toggles.remove: %s\n", formatToggles(res.Lock.Toggles.Remove))
	if !res.Changed {
		fmt.Println("(no change)")
	}
	return 0
}

func syncSaveAsCmd(args []string) int {
	fs := flag.NewFlagSet("sync save-as", flag.ContinueOnError)
	target := fs.String("target", ".", "target directory")
	profile := fs.String("profile", "", "profile name (required)")
	update := fs.Bool("update", false, "overwrite an existing profile")
	dryRun := fs.Bool("dry-run", false, "print the proposed YAML diff and write nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *profile == "" {
		fmt.Fprintln(os.Stderr, "error: --profile is required")
		return 2
	}
	abs, _ := filepath.Abs(*target)
	res, err := sync.SaveAs(sync.SaveAsOptions{
		Target: abs, Profile: *profile, Update: *update, DryRun: *dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "save-as failed: %v\n", err)
		return 1
	}
	fmt.Printf("profile: %s\n", *profile)
	fmt.Printf("  include: %s\n", formatList(res.Profile.Include))
	fmt.Printf("  exclude: %s\n", formatList(res.Profile.Exclude))
	if res.Profile.Type != nil {
		fmt.Printf("  type:    %s\n", formatList(res.Profile.Type))
	}
	if *dryRun {
		fmt.Println("(dry-run; nothing written)")
	}
	return 0
}

func profileCmd(args []string) int {
	if len(args) < 1 || args[0] != "edit" {
		fmt.Fprintln(os.Stderr, "usage: podium profile edit [flags]")
		return 2
	}
	fs := flag.NewFlagSet("profile edit", flag.ContinueOnError)
	target := fs.String("target", ".", "target directory")
	profile := fs.String("profile", "", "profile name (required)")
	var addInc, removeInc, addExc, removeExc stringSliceFlag
	fs.Var(&addInc, "add-include", "include pattern to add (repeatable)")
	fs.Var(&removeInc, "remove-include", "include pattern to remove (repeatable)")
	fs.Var(&addExc, "add-exclude", "exclude pattern to add (repeatable)")
	fs.Var(&removeExc, "remove-exclude", "exclude pattern to remove (repeatable)")
	dryRun := fs.Bool("dry-run", false, "print the proposed YAML diff and write nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *profile == "" {
		fmt.Fprintln(os.Stderr, "error: --profile is required")
		return 2
	}
	abs, _ := filepath.Abs(*target)
	res, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target:        abs,
		Profile:       *profile,
		AddInclude:    []string(addInc),
		RemoveInclude: []string(removeInc),
		AddExclude:    []string(addExc),
		RemoveExclude: []string(removeExc),
		DryRun:        *dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "profile edit failed: %v\n", err)
		return 1
	}
	fmt.Printf("profile: %s\n", *profile)
	fmt.Printf("  include: %s\n", formatList(res.Profile.Include))
	fmt.Printf("  exclude: %s\n", formatList(res.Profile.Exclude))
	if *dryRun {
		fmt.Println("(dry-run; nothing written)")
	}
	return 0
}

func formatToggles(toggles []sync.LockToggle) string {
	if len(toggles) == 0 {
		return "(none)"
	}
	out := []string{}
	for _, t := range toggles {
		out = append(out, t.ID)
	}
	return strings.Join(out, ", ")
}

func formatList(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
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

// ----- Read CLI (§7.6.1) ---------------------------------------------------

func searchCmd(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	typeFilter := fs.String("type", "", "filter by artifact type")
	scope := fs.String("scope", "", "constrain results to a path prefix")
	topK := fs.Int("top-k", 10, "max results")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: podium search <query> [flags]")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	params := map[string]string{"query": fs.Arg(0)}
	if *typeFilter != "" {
		params["type"] = *typeFilter
	}
	if *scope != "" {
		params["scope"] = *scope
	}
	if *topK > 0 {
		params["top_k"] = fmt.Sprintf("%d", *topK)
	}
	body := mustGetJSON(*registry, "/v1/search_artifacts", params)
	if *asJSON {
		fmt.Println(string(body))
		return 0
	}
	printSearchHuman(body)
	return 0
}

func domainCmd(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: podium domain show|search [flags]")
		return 2
	}
	switch args[0] {
	case "show":
		return domainShow(args[1:])
	case "search":
		return domainSearch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown domain subcommand: %s\n", args[0])
		return 2
	}
}

func domainShow(args []string) int {
	fs := flag.NewFlagSet("domain show", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	params := map[string]string{}
	if fs.NArg() > 0 {
		params["path"] = fs.Arg(0)
	}
	body := mustGetJSON(*registry, "/v1/load_domain", params)
	if *asJSON {
		fmt.Println(string(body))
		return 0
	}
	fmt.Println(string(body))
	return 0
}

func domainSearch(args []string) int {
	fs := flag.NewFlagSet("domain search", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	scope := fs.String("scope", "", "constrain results")
	topK := fs.Int("top-k", 10, "max results")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: podium domain search <query> [flags]")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	params := map[string]string{"query": fs.Arg(0), "top_k": fmt.Sprintf("%d", *topK)}
	if *scope != "" {
		params["scope"] = *scope
	}
	body := mustGetJSON(*registry, "/v1/search_domains", params)
	fmt.Println(string(body))
	return 0
}

func artifactCmd(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: podium artifact show <id> [flags]")
		return 2
	}
	switch args[0] {
	case "show":
		return artifactShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown artifact subcommand: %s\n", args[0])
		return 2
	}
}

func artifactShow(args []string) int {
	fs := flag.NewFlagSet("artifact show", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: podium artifact show <id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	body := mustGetJSON(*registry, "/v1/load_artifact",
		map[string]string{"id": fs.Arg(0)})
	fmt.Println(string(body))
	return 0
}

func initCmd(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	scopeGlobal := fs.Bool("global", false, "write ~/.podium/sync.yaml")
	registry := fs.String("registry", "", "registry URL or filesystem path")
	harness := fs.String("harness", "", "default harness")
	target := fs.String("target", "", "default target")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}

	dir := ".podium"
	if *scopeGlobal {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		dir = filepath.Join(home, ".podium")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	yaml := "defaults:\n  registry: " + *registry + "\n"
	if *harness != "" {
		yaml += "  harness: " + *harness + "\n"
	}
	if *target != "" {
		yaml += "  target: " + *target + "\n"
	}
	dest := filepath.Join(dir, "sync.yaml")
	if err := os.WriteFile(dest, []byte(yaml), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Wrote %s\n", dest)
	return 0
}

func mustGetJSON(base, path string, params map[string]string) []byte {
	u, err := url.Parse(base + path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	resp, err := http.Get(u.String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "error: HTTP %d: %s\n", resp.StatusCode, body)
		os.Exit(1)
	}
	return body
}

// printSearchHuman renders the search response in a human-friendly format.
func printSearchHuman(body []byte) {
	var resp struct {
		TotalMatched int `json:"total_matched"`
		Results      []struct {
			ID          string `json:"id"`
			Type        string `json:"type"`
			Version     string `json:"version"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Println(string(body))
		return
	}
	fmt.Printf("Showing %d of %d results\n\n", len(resp.Results), resp.TotalMatched)
	for _, r := range resp.Results {
		fmt.Printf("  %s  [%s]\n", r.ID, r.Type)
		if r.Description != "" {
			fmt.Printf("      %s\n", r.Description)
		}
	}
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
