// Command podium is the unified Podium CLI. The `sync` subcommand
// materializes the caller's effective view through a HarnessAdapter
// (spec §13.11); `init`, `login`, `layer`, `lint`, `search`,
// `domain`, `artifact`, `admin`, `status`, and `profile` cover the
// rest of the read + author surface. Run `podium help` for the
// full list.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/lint"
	overlaypkg "github.com/lennylabs/podium/pkg/overlay"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sync"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(serveCmd(os.Args[2:]))
	case "config":
		os.Exit(configCmd(os.Args[2:]))
	case "cache":
		os.Exit(cacheCmd(os.Args[2:]))
	case "import":
		os.Exit(importCmd(os.Args[2:]))
	case "sync":
		os.Exit(syncCmd(os.Args[2:]))
	case "publish":
		os.Exit(publishCmd(os.Args[2:]))
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
	case "impact":
		os.Exit(impactCmd(os.Args[2:]))
	case "admin":
		os.Exit(adminCmd(os.Args[2:]))
	case "login":
		os.Exit(loginCmd(os.Args[2:]))
	case "logout":
		os.Exit(logoutCmd(os.Args[2:]))
	case "status":
		os.Exit(statusCmd(os.Args[2:]))
	case "sign":
		os.Exit(signCmd(os.Args[2:]))
	case "verify":
		os.Exit(verifyCmd(os.Args[2:]))
	case "quota":
		os.Exit(quotaCmd(os.Args[2:]))
	case "version", "-v", "--version":
		fmt.Println("podium " + buildinfo.String())
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// setUsage attaches a description to the FlagSet's --help output. The
// FlagSet's Name() supplies the subcommand path; description is the one-
// line summary mirrored from the top-level help block.
func setUsage(fs *flag.FlagSet, description string) {
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "podium %s - %s\n\nFlags:\n", fs.Name(), description)
		fs.PrintDefaults()
	}
}

// printGroupHelp writes a help block for a dispatcher group to stdout.
func printGroupHelp(group, description string, items [][2]string) {
	fprintGroupHelp(os.Stdout, group, description, items)
}

// fprintGroupHelp is the testable form: writes the group help block to w.
func fprintGroupHelp(w io.Writer, group, description string, items [][2]string) {
	fmt.Fprintf(w, "podium %s - %s\n\nSubcommands:\n", group, description)
	width := 0
	for _, it := range items {
		if l := len(it[0]); l > width {
			width = l
		}
	}
	for _, it := range items {
		fmt.Fprintf(w, "  %-*s  %s\n", width, it[0], it[1])
	}
}

// isHelpArg returns true when s is one of the recognized help tokens.
func isHelpArg(s string) bool {
	return s == "help" || s == "-h" || s == "--help"
}

// parseExit translates a non-nil fs.Parse error into an exit code:
// 0 when --help was requested (flag.ErrHelp), 2 for any other parse
// failure. The flag package already printed the usage or error message
// to fs.Output().
func parseExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

const usage = `usage: podium <command> [flags]

Commands:
  serve               Run the standalone registry server in-process.
  config show         Print the resolved server configuration with sources.
  cache prune         Remove content-cache buckets older than N days.
  import              Convert a skills/* tree into a Podium-shaped layer.
  sync                Materialize the caller's effective view through a HarnessAdapter.
  sync override       Add or remove ephemeral artifact toggles.
  sync save-as        Capture the current target state as a sync.yaml profile.
  publish             Render the catalog into marketplace repositories and push them to git remotes.
  lint                Validate manifests in a filesystem-source registry.
  search              Hybrid search over artifacts (registry HTTP API).
  domain show         Show a domain map.
  domain search       Hybrid search over domains.
  domain analyze      Print domain-discovery metrics and split/fold candidates for a subtree.
  artifact show       Print an artifact's manifest body and frontmatter.
  artifact scaffold   Write a new artifact directory at a path on disk.
  init                Write ~/.podium/sync.yaml or ./.podium/sync.yaml.
  profile edit        Add or remove patterns on a sync.yaml profile.
  layer register      Register a layer with the registry.
  layer list          List registered layers.
  layer reorder       Re-sequence the layer list.
  layer unregister    Remove a layer.
  layer reingest      Trigger a fresh ingest for a layer.
  layer update        Patch a registered layer's mutable fields.
  layer watch         Poll a layer's source on an interval.
  impact              List artifacts that depend on a given artifact.
  admin erase         GDPR right-to-be-forgotten on the local audit log.
  admin retention     Apply audit retention policies to the local audit log.
  admin reembed       Re-run vector embeddings against the configured registry.
  admin grant         Grant tenant admin role to a user.
  admin revoke        Revoke tenant admin role from a user.
  admin show-effective  Print the per-layer visibility for a user identity.
  admin runtime register  Register a trusted runtime signing key.
  admin runtime list      List registered runtimes.
  admin migrate-to-standard  Pump standalone state (SQLite + filesystem) into a standard deployment (Postgres + S3).
  login               Run the OAuth Device Code flow and persist the token to the keychain.
  logout              Remove the cached token for the configured registry.
  status              Print a diagnostic summary of the current Podium client setup.
  sign                Sign a content hash via the configured signature provider.
  verify              Verify a signature envelope against a content hash.
  quota               Print the tenant's quotas and current usage.
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
	setUsage(fs, "Materialize the caller's effective view through a HarnessAdapter.")
	registry := fs.String("registry", "", "registry URL (server source) or filesystem path (required)")
	target := fs.String("target", "", "destination directory (default: current directory)")
	// §7.5.2: an unset --harness falls through to PODIUM_HARNESS, then the
	// sync.yaml harness, then the built-in "none" adapter. The default is
	// empty so an omitted flag does not pin "none" over the configured value.
	harness := fs.String("harness", "", "harness adapter (default: PODIUM_HARNESS, sync.yaml, then none)")
	dryRun := fs.Bool("dry-run", false, "resolve and report; write nothing")
	preview := fs.Bool("preview", false, "print the §3.5 scope-preview aggregate counts and exit; write nothing")
	asJSON := fs.Bool("json", false, "emit a structured JSON envelope on stdout")
	watch := fs.Bool("watch", false, "rerun sync whenever the registry changes")
	check := fs.Bool("check", false, "validate the merged sync.yaml and report warnings")
	overlay := fs.String("overlay", "", "workspace overlay path watched alongside the registry")
	profile := fs.String("profile", "", "load a named scope from sync.yaml profiles")
	configPath := fs.String("config", "", "run one sync per entry in a sync.yaml targets: list")
	typeStr := fs.String("type", "", "restrict to a comma-separated artifact type list")
	var include, exclude stringSliceFlag
	fs.Var(&include, "include", "glob over canonical IDs to include (repeatable)")
	fs.Var(&exclude, "exclude", "glob over canonical IDs to exclude (repeatable)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}

	// §7.5.2 validation: --check loads the merged config and reports warnings
	// (unresolved profiles, malformed globs, target/profile collisions).
	if *check {
		return runSyncCheck()
	}

	// §7.5.2 multi-target: --config iterates a targets: list, one sync each.
	if *configPath != "" {
		return runMultiTargetSync(*configPath, *registry, *dryRun, *asJSON)
	}

	// §7.5.2 resolution: merge the three sync.yaml scopes by per-key
	// precedence, then overlay CLI flags and PODIUM_* env. The merged
	// config also resolves the active profile's scope.
	ws, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	merged, workspace, merrr := sync.LoadMergedConfig(ws, home)
	if merrr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", merrr)
		return 1
	}
	if workspace == "" {
		workspace = ws
	}
	resolved, rerr := sync.Resolve(sync.ResolveInput{
		Registry: *registry,
		Target:   *target,
		Harness:  *harness,
		Profile:  *profile,
		Include:  []string(include),
		Exclude:  []string(exclude),
		Types:    splitCSV(*typeStr),
	}, merged, os.Getenv)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", rerr)
		return 2
	}
	if resolved.CollisionWarning != "" {
		fmt.Fprintln(os.Stderr, resolved.CollisionWarning)
	}
	// spec: §6.7 "Versioning" — refuse to run when the merged defaults or the
	// active profile pin a min_server_version above this binary. podium sync
	// runs the same versioned adapters as the MCP server, so an older binary
	// must not materialize with stale adapter behavior.
	if verr := merged.CheckServerVersion(buildinfo.Version, resolved.Profile); verr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", verr)
		return 2
	}

	registryPath := resolved.Registry
	if registryPath != "" {
		// Relative defaults.registry resolves against the workspace; URLs
		// and absolute paths pass through (§13.11.2).
		registryPath = sync.ResolveRegistryPath(workspace, registryPath)
	}
	if registryPath == "" {
		// spec: §13.11.2 — "If defaults.registry is unset across all scopes,
		// the client errors with config.no_registry and points the user at
		// podium init." Registry resolution above already applies the §7.5.2
		// precedence chain (CLI flag, PODIUM_REGISTRY, then the merged
		// user-global/project-shared/project-local scopes), so reaching here
		// means no scope set defaults.registry.
		fmt.Fprintln(os.Stderr, "error: config.no_registry: no registry configured — run `podium init` to set defaults.registry, or pass --registry")
		return 2
	}

	// §3.5: `podium sync --preview` is a transparency affordance. It prints
	// the caller's effective-view aggregate counts and writes nothing. The
	// preview is served by GET /v1/scope/preview, so it requires a
	// server-source registry; a filesystem source has no such endpoint.
	if *preview {
		if !sync.IsServerSource(registryPath) {
			fmt.Fprintln(os.Stderr, "error: --preview requires a server registry URL (the §3.5 scope preview is served by GET /v1/scope/preview)")
			return 2
		}
		return runScopePreview(registryPath, *asJSON)
	}

	targetDir := resolved.Target
	if targetDir == "" {
		targetDir = "."
	}
	abs, err := filepath.Abs(targetDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot resolve target: %v\n", err)
		return 2
	}
	// §7.4: podium sync applies the same cache modes as the MCP server. The
	// mode is read once from PODIUM_CACHE_MODE and threaded into the run; it
	// governs the server source (offline-only never contacts the registry,
	// offline-first tolerates an unreachable server) and is a no-op for a
	// filesystem source.
	cacheMode, cmErr := resolveCacheMode(os.Getenv("PODIUM_CACHE_MODE"))
	if cmErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cmErr)
		return 2
	}
	// §6.4 overlay resolution: an explicit --overlay wins; otherwise honor the
	// PODIUM_OVERLAY_PATH env var and the <CWD>/.podium/overlay/ fallback. A
	// disabled overlay (the directory is absent) leaves OverlayPath empty.
	overlayPath := *overlay
	if overlayPath == "" {
		if ovl, oerr := overlaypkg.ResolveWorkspaceOverlay(ws, os.Getenv("PODIUM_OVERLAY_PATH")); oerr == nil {
			overlayPath = ovl
		}
	}
	// §6.3.2 / §14.11: attach the caller credential (injected session token,
	// then a keychain oauth-device-code token) so a server-source sync reaches
	// an authenticated registry with the same identity the read CLI uses. A
	// filesystem source ignores it.
	token := ""
	if sync.IsServerSource(registryPath) {
		token = readCLIToken(registryPath)
	}
	syncOpts := sync.Options{
		RegistryPath: registryPath,
		Target:       abs,
		AdapterID:    resolved.Harness,
		DryRun:       *dryRun,
		OverlayPath:  overlayPath,
		Profile:      resolved.Profile,
		Scope:        resolved.Scope,
		CacheMode:    cacheMode,
		Token:        token,
	}
	if *watch {
		return runWatchLoop(syncOpts, overlayPath, *asJSON)
	}
	res, err := sync.Run(syncOpts)
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

// resolveCacheMode validates PODIUM_CACHE_MODE for the sync path (§7.4). An
// empty value defaults to always-revalidate, matching the MCP server (§6.2);
// an unrecognized value is rejected so a typo cannot silently change the
// degraded-network behavior.
func resolveCacheMode(v string) (string, error) {
	switch v {
	case "":
		return "always-revalidate", nil
	case "always-revalidate", "offline-first", "offline-only":
		return v, nil
	default:
		return "", fmt.Errorf("PODIUM_CACHE_MODE must be always-revalidate | offline-first | offline-only, got %q", v)
	}
}

// runWatchLoop drives sync.Watch until the user interrupts (SIGINT
// / SIGTERM). Each WatchEvent is printed in the same shape as a
// one-shot sync run so existing tooling can consume the stream.
func runWatchLoop(opts sync.Options, overlay string, asJSON bool) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	events, err := sync.Watch(ctx, sync.WatchOptions{
		Sync:        opts,
		OverlayPath: overlay,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch failed: %v\n", err)
		return 1
	}
	failures := 0
	for ev := range events {
		if ev.Err != nil {
			fmt.Fprintf(os.Stderr, "sync failed: %v\n", ev.Err)
			failures++
			continue
		}
		if asJSON {
			printJSON(ev.Result)
		} else {
			printHuman(ev.Result, opts.DryRun)
		}
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// runMultiTargetSync implements `podium sync --config <path>` (§7.5.2): it
// reads the config file's targets: list, resolves each entry's scope, target,
// and harness, and runs one sync per entry. Each target writes its own lock.
func runMultiTargetSync(configPath, registryOverride string, dryRun, asJSON bool) int {
	cfg, err := sync.ReadConfigFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "error: config not found: %s\n", configPath)
		return 2
	}
	workspace := filepath.Dir(filepath.Dir(configPath))
	plans, err := sync.PlanMultiTarget(cfg, sync.PlanInput{
		RegistryOverride: registryOverride,
		Workspace:        workspace,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	cacheMode, cmErr := resolveCacheMode(os.Getenv("PODIUM_CACHE_MODE"))
	if cmErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cmErr)
		return 2
	}
	failures := 0
	for _, p := range plans {
		abs, aerr := filepath.Abs(p.Target)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "target %s: %v\n", p.ID, aerr)
			failures++
			continue
		}
		res, rerr := sync.Run(sync.Options{
			RegistryPath: p.Registry,
			Target:       abs,
			AdapterID:    p.Harness,
			DryRun:       dryRun,
			Profile:      p.Profile,
			Scope:        p.Scope,
			CacheMode:    cacheMode,
		})
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "target %s: %v\n", p.ID, rerr)
			failures++
			continue
		}
		fmt.Printf("== target %s ==\n", p.ID)
		if asJSON {
			printJSON(res)
		} else {
			printHuman(res, dryRun)
		}
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// runSyncCheck implements `podium sync --check` (§7.5.2): it loads the merged
// config and prints validation warnings. Warnings are not errors, so a config
// with warnings still exits 0; only a config-load failure exits non-zero.
func runSyncCheck() int {
	ws, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	merged, _, err := sync.LoadMergedConfig(ws, home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	warns := sync.Check(merged)
	if len(warns) == 0 {
		fmt.Println("sync.yaml: ok (no warnings)")
		return 0
	}
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
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
	setUsage(fs, "Add or remove ephemeral artifact toggles.")
	target := fs.String("target", ".", "target directory")
	registry := fs.String("registry", "", "registry URL or filesystem path (default: sync.yaml)")
	harness := fs.String("harness", "", "harness adapter (default: PODIUM_HARNESS, lock, sync.yaml, then none)")
	var add, remove stringSliceFlag
	fs.Var(&add, "add", "artifact id to materialize on top of the profile (repeatable)")
	fs.Var(&remove, "remove", "artifact id to drop from the profile (repeatable)")
	reset := fs.Bool("reset", false, "clear all toggles")
	dryRun := fs.Bool("dry-run", false, "resolve and report; write nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	abs, err := filepath.Abs(*target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	// §7.5.5 TUI mode: no batch flags launches the interactive checklist over
	// the caller's effective view. The resulting toggles are applied through the
	// same Override path the --add/--remove flags use.
	if len(add) == 0 && len(remove) == 0 && !*reset {
		return runSyncOverrideInteractive(abs, *registry, *harness, *dryRun)
	}
	// §7.5.5: override writes/deletes files through the active adapter. Resolve
	// the registry the same way sync does so --add materializes and --remove
	// deletes. A missing registry leaves materialization off; the toggles are
	// still recorded.
	registryPath := resolveOverrideRegistry(*registry)
	res, err := sync.Override(sync.OverrideOptions{
		Target: abs,
		Add:    []string(add), Remove: []string(remove),
		Reset: *reset, DryRun: *dryRun,
		RegistryPath: registryPath,
		AdapterID:    resolveOverrideHarness(*harness, abs),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "override failed: %v\n", err)
		return 1
	}
	// spec: §7.5.5 — a redundant --add on an already-materialized artifact is a
	// no-op with a warning.
	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
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

// resolveOverrideRegistry resolves the registry source for `podium sync
// override` from the --registry flag, PODIUM_REGISTRY, or the merged
// sync.yaml, returning "" when none is configured (materialization off).
func resolveOverrideRegistry(flagVal string) string {
	ws, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	merged, workspace, _ := sync.LoadMergedConfig(ws, home)
	if workspace == "" {
		workspace = ws
	}
	reg := flagVal
	if reg == "" {
		reg = os.Getenv("PODIUM_REGISTRY")
	}
	if reg == "" && merged != nil {
		reg = merged.Defaults.Registry
	}
	if reg == "" {
		return ""
	}
	return sync.ResolveRegistryPath(workspace, reg)
}

// resolveOverrideHarness resolves the adapter id for `podium sync override`
// per §7.5.2 precedence: the --harness flag, then PODIUM_HARNESS, then the
// harness recorded in the target's lock (the adapter the last sync used), then
// the merged sync.yaml default, then the built-in "none" adapter.
func resolveOverrideHarness(flagVal, target string) string {
	if flagVal != "" {
		return flagVal
	}
	if h := os.Getenv("PODIUM_HARNESS"); h != "" {
		return h
	}
	if lock, _ := sync.ReadLock(target); lock != nil && lock.Harness != "" {
		return lock.Harness
	}
	ws, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	if merged, _, _ := sync.LoadMergedConfig(ws, home); merged != nil && merged.Defaults.Harness != "" {
		return merged.Defaults.Harness
	}
	return "none"
}

func syncSaveAsCmd(args []string) int {
	fs := flag.NewFlagSet("sync save-as", flag.ContinueOnError)
	setUsage(fs, "Capture the current target state as a sync.yaml profile.")
	target := fs.String("target", ".", "target directory")
	profile := fs.String("profile", "", "profile name (required)")
	update := fs.Bool("update", false, "overwrite an existing profile")
	dryRun := fs.Bool("dry-run", false, "print the proposed YAML diff and write nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
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
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("profile", "Manage sync.yaml profiles.", [][2]string{
			{"edit", "Add or remove patterns on a sync.yaml profile."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	if args[0] != "edit" {
		fmt.Fprintf(os.Stderr, "unknown profile subcommand: %s\n", args[0])
		return 2
	}
	// §7.5.7: the profile name is positional (`podium profile edit finance-team`).
	// Pull it off before flag parsing; a leading flag means no name was given.
	rest := args[1:]
	var name string
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		name = rest[0]
		rest = rest[1:]
	}
	fs := flag.NewFlagSet("profile edit", flag.ContinueOnError)
	setUsage(fs, "Add or remove patterns on a sync.yaml profile.")
	target := fs.String("target", ".", "target directory")
	var addInc, removeInc, addExc, removeExc stringSliceFlag
	fs.Var(&addInc, "add-include", "include pattern to add (repeatable)")
	fs.Var(&removeInc, "remove-include", "include pattern to remove (repeatable)")
	fs.Var(&addExc, "add-exclude", "exclude pattern to add (repeatable)")
	fs.Var(&removeExc, "remove-exclude", "exclude pattern to remove (repeatable)")
	dryRun := fs.Bool("dry-run", false, "print the proposed YAML diff and write nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(rest); err != nil {
		return parseExit(err)
	}
	if name == "" {
		// §7.5.7: `podium profile edit` with no name errors and asks for one.
		fmt.Fprintln(os.Stderr, "error: profile name required (usage: podium profile edit <name> [--add-include ...])")
		return 2
	}
	abs, _ := filepath.Abs(*target)
	// §7.5.7 TUI mode: no batch flags opens the interactive editor over the
	// profile's include/exclude lists, writing the resulting deltas through the
	// same ProfileEdit path the batch flags use.
	if len(addInc) == 0 && len(removeInc) == 0 && len(addExc) == 0 && len(removeExc) == 0 {
		return runProfileEditInteractive(name, abs, *dryRun)
	}
	res, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target:        abs,
		Profile:       name,
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
	fmt.Printf("profile: %s\n", name)
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
	setUsage(fs, "Validate manifests in a filesystem-source registry.")
	registry := fs.String("registry", "", "filesystem registry path (required)")
	// §4.4: prose URL references are validated by an HTTP HEAD (200/3xx)
	// by default; --offline (or PODIUM_INGEST_OFFLINE=true) skips the
	// network probe and validates only bundled-file references.
	offline := fs.Bool("offline", false, "skip the §4.4 URL HEAD check (validate bundled files only)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
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
	// spec: §4.6 — lint validates the registry, so it surfaces a
	// same-canonical-ID cross-layer collision as an error ("silent
	// shadowing is never permitted"). The default policy honors the
	// extends exception, so a legitimate higher-precedence overlay that
	// declares extends: passes; only an unsanctioned shadow errors. sync
	// keeps CollisionPolicyHighestWins because it materializes the
	// caller's composed effective view rather than validating it.
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyDefault,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	linter := lint.NewIngestLinter(*offline || os.Getenv("PODIUM_INGEST_OFFLINE") == "true")
	diags := linter.Lint(context.Background(), reg, records)
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
	setUsage(fs, "Hybrid search over artifacts (registry HTTP API).")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	typeFilter := fs.String("type", "", "filter by artifact type")
	scope := fs.String("scope", "", "constrain results to a path prefix")
	topK := fs.Int("top-k", 10, "max results")
	// spec: §7.6.1 — podium search flag --tags mirrors the SDK arg.
	tagsFlag := fs.String("tags", "", "comma-separated tag filter")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
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
	if *tagsFlag != "" {
		params["tags"] = *tagsFlag
	}
	if *topK > 0 {
		params["top_k"] = fmt.Sprintf("%d", *topK)
	}
	body := mustGetJSON(*registry, "/v1/search_artifacts", params)
	if *asJSON {
		// spec: §7.6.1 — emit the documented {query, total_matched,
		// results:[{id, type, version, score, frontmatter}]} schema rather than
		// the raw wire descriptor.
		emitSearchJSON(body)
		return 0
	}
	printSearchHuman(body)
	return 0
}

func domainCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("domain", "Inspect and search the domain hierarchy.", [][2]string{
			{"show", "Show a domain map."},
			{"search", "Hybrid search over domains."},
			{"analyze", "Print domain-discovery metrics and split/fold candidates for a subtree."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "show":
		return domainShow(args[1:])
	case "search":
		return domainSearch(args[1:])
	case "analyze":
		return domainAnalyze(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown domain subcommand: %s\n", args[0])
		return 2
	}
}

// domainAnalyze hits /v1/domain/analyze and prints the §4.5.5
// report. Useful for ingest-time review of split / fold candidates.
func domainAnalyze(args []string) int {
	fs := flag.NewFlagSet("domain analyze", flag.ContinueOnError)
	setUsage(fs, "Print domain-discovery metrics and split/fold candidates for a subtree.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	path := fs.String("path", "", "subtree to analyze (empty = root)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	// Accept a positional <path> (consistent with `domain show` and
	// `domain search`, and the §4.5 CLI form `podium domain analyze [<path>]`).
	// The --path flag stays accepted for back-compat; an explicit flag wins.
	if *path == "" && fs.NArg() > 0 {
		*path = fs.Arg(0)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	endpoint := *registry + "/v1/domain/analyze"
	if *path != "" {
		endpoint += "?path=" + url.QueryEscape(*path)
	}
	out, status := doJSON(endpoint, "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "analyze failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func domainShow(args []string) int {
	fs := flag.NewFlagSet("domain show", flag.ContinueOnError)
	setUsage(fs, "Show a domain map.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	asJSON := fs.Bool("json", false, "JSON output")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
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
	// spec: §7.6.1 Output formats — the default is a human-readable
	// rendering (domain trees are nested bullets); --json is the structured
	// envelope.
	printDomainHuman(body)
	return 0
}

func domainSearch(args []string) int {
	fs := flag.NewFlagSet("domain search", flag.ContinueOnError)
	setUsage(fs, "Hybrid search over domains.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	scope := fs.String("scope", "", "constrain results")
	topK := fs.Int("top-k", 10, "max results")
	// spec: §7.6.1 — domain search exposes a --json structured envelope
	// alongside the default human-readable ranked table.
	asJSON := fs.Bool("json", false, "JSON output")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
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
	if *asJSON {
		// spec: §7.6.1 — the documented schema keys the ranked domains under
		// "results" (matching the artifact-search envelope); the wire response
		// keys them "domains". Map before emitting.
		emitDomainSearchJSON(body)
		return 0
	}
	printDomainSearchHuman(body)
	return 0
}

func artifactCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("artifact", "Inspect and scaffold artifacts.", [][2]string{
			{"show", "Print an artifact's manifest body and frontmatter."},
			{"scaffold", "Write a new artifact directory at a path on disk."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "show":
		return artifactShow(args[1:])
	case "scaffold":
		return artifactScaffold(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown artifact subcommand: %s\n", args[0])
		return 2
	}
}

func artifactShow(args []string) int {
	fs := flag.NewFlagSet("artifact show", flag.ContinueOnError)
	setUsage(fs, "Print an artifact's manifest body and frontmatter.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	// spec: §7.6.1 — podium artifact show flags --version and --session-id.
	version := fs.String("version", "", "specific version (default: latest)")
	sessionID := fs.String("session-id", "", "session id for consistent latest resolution")
	// spec: §7.6.1 — --json emits the structured {id, version, content_hash,
	// frontmatter, body} envelope; the default prints the markdown body with
	// frontmatter at the top.
	asJSON := fs.Bool("json", false, "JSON output")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: podium artifact show <id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	params := map[string]string{"id": fs.Arg(0)}
	if *version != "" {
		params["version"] = *version
	}
	if *sessionID != "" {
		params["session_id"] = *sessionID
	}
	body := mustGetJSON(*registry, "/v1/load_artifact", params)
	if *asJSON {
		// spec: §7.6.1 — emit the documented {id, version, content_hash,
		// frontmatter, body} schema. The wire response keys the manifest text
		// "manifest_body" and delivers frontmatter as a raw string.
		emitArtifactShowJSON(body)
		return 0
	}
	printArtifactHuman(body)
	return 0
}

func initCmd(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	setUsage(fs, "Write ~/.podium/sync.yaml or ./.podium/sync.yaml.")
	scopeGlobal := fs.Bool("global", false, "write ~/.podium/sync.yaml")
	scopeLocal := fs.Bool("local", false, "write <ws>/.podium/sync.local.yaml (gitignored override)")
	standalone := fs.Bool("standalone", false, "shortcut for --registry http://127.0.0.1:8080")
	registry := fs.String("registry", "", "registry URL or filesystem path")
	harness := fs.String("harness", "", "default harness")
	target := fs.String("target", "", "default target")
	force := fs.Bool("force", false, "overwrite an existing sync.yaml")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *scopeGlobal && *scopeLocal {
		fmt.Fprintln(os.Stderr, "error: --global and --local are mutually exclusive")
		return 2
	}
	if *standalone {
		if *registry != "" && *registry != "http://127.0.0.1:8080" {
			fmt.Fprintln(os.Stderr, "error: --standalone conflicts with --registry")
			return 2
		}
		*registry = "http://127.0.0.1:8080"
	}
	// (spec: §7.7) — with no value flags and an interactive
	// stdin, prompt for the registry (and optionally harness/target). A
	// non-terminal stdin (CI, tests, pipes) skips the wizard and falls
	// through to the required-flag error below, so init never blocks.
	if *registry == "" && initIsTerminal() {
		w, err := runInitWizard(initStdin, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		*registry = w.registry
		if *harness == "" {
			*harness = w.harness
		}
		if *target == "" {
			*target = w.target
		}
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry, --standalone, or interactive wizard required")
		return 2
	}

	// §7.7 scope resolution: --global → ~/.podium; --local →
	// <ws>/.podium/sync.local.yaml (gitignored); default →
	// <ws>/.podium/sync.yaml (committed).
	filename := "sync.yaml"
	// workspace is the directory holding `.podium/` for the workspace and
	// local scopes; it is empty for the user-global scope.
	var workspace string
	var dir string
	if *scopeGlobal {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		dir = filepath.Join(home, ".podium")
	} else {
		// (spec: §7.7 workspace-mode step 1) — walk up from CWD to
		// reuse an existing `.podium/` workspace so init from a subdirectory
		// does not create a second workspace; create one in CWD when none is
		// found. Both the committed default scope and the `--local` override
		// resolve against the discovered workspace.
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		ws, found := sync.DiscoverWorkspace(cwd)
		if !found {
			ws = cwd
		}
		workspace = ws
		dir = filepath.Join(ws, ".podium")
		if *scopeLocal {
			filename = "sync.local.yaml"
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	dest := filepath.Join(dir, filename)
	if !*force {
		if _, err := os.Stat(dest); err == nil {
			fmt.Fprintf(os.Stderr, "error: %s already exists; pass --force to overwrite\n", dest)
			return 2
		}
	}
	yaml := "defaults:\n  registry: " + *registry + "\n"
	if *harness != "" {
		yaml += "  harness: " + *harness + "\n"
	}
	if *target != "" {
		yaml += "  target: " + *target + "\n"
	}
	if err := os.WriteFile(dest, []byte(yaml), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	// §7.7 workspace mode adds the gitignored override file +
	// overlay dir to .gitignore so they don't accidentally land
	// in commits.
	if !*scopeGlobal && !*scopeLocal {
		_ = ensureGitignoreEntries(filepath.Join(workspace, ".gitignore"), []string{
			".podium/sync.local.yaml",
			".podium/overlay/",
		})
	}
	fmt.Printf("Wrote %s\n", dest)
	// (spec: §7.7 workspace-mode step 4) — print next-step hints.
	// The committed default scope suggests committing the file; every
	// workspace scope suggests running `podium sync` to materialize.
	if !*scopeGlobal {
		fmt.Println("Next steps:")
		if !*scopeLocal {
			fmt.Printf("  - commit %s to share the configuration with your team\n", dest)
		}
		fmt.Println("  - run `podium sync` to materialize artifacts")
	}
	return 0
}

// initWizardResult holds the values gathered from the interactive
// `podium init` wizard. spec: §7.7.
type initWizardResult struct {
	registry string
	harness  string
	target   string
}

// initStdin and initIsTerminal are indirections so tests can drive the
// interactive wizard without a real terminal. By default the wizard reads
// os.Stdin and runs only when stdin is a character device.
var (
	initStdin      io.Reader = os.Stdin
	initIsTerminal           = func() bool {
		fi, err := os.Stdin.Stat()
		if err != nil {
			return false
		}
		return fi.Mode()&os.ModeCharDevice != 0
	}
)

// runInitWizard prompts for the registry (required) and the optional
// harness and target defaults, returning the collected values. Empty
// answers leave the corresponding field unset. spec: §7.7.
func runInitWizard(in io.Reader, out io.Writer) (initWizardResult, error) {
	r := bufio.NewReader(in)
	ask := func(prompt string) (string, error) {
		fmt.Fprint(out, prompt)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	var res initWizardResult
	var err error
	if res.registry, err = ask("Registry URL or filesystem path: "); err != nil {
		return res, err
	}
	if res.harness, err = ask("Default harness (optional): "); err != nil {
		return res, err
	}
	if res.target, err = ask("Default target (optional): "); err != nil {
		return res, err
	}
	return res, nil
}

// ensureGitignoreEntries appends every missing entry to path,
// creating the file when absent. Existing entries are preserved
// verbatim. Best-effort: callers ignore the error to avoid
// failing init when the workspace is read-only.
func ensureGitignoreEntries(path string, entries []string) error {
	existing, _ := os.ReadFile(path)
	out := string(existing)
	for _, e := range entries {
		if strings.Contains(out, e) {
			continue
		}
		if len(out) > 0 && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += e + "\n"
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// readCLIToken resolves the read CLI's caller credential so its requests
// reach the registry with the same identity the MCP path uses (spec: §7.6,
// §7.6.1 — "uses the same identity ... server-side"). Resolution mirrors the
// MCP bridge: the §6.3.2 injected session token (file, then env) wins, then a
// §6.3.1 oauth-device-code access token cached in the OS keychain keyed by the
// registry URL (matching `podium login`). Returns "" when none is configured,
// in which case the caller reaches the registry anonymously.
func readCLIToken(registry string) string {
	if f := os.Getenv("PODIUM_SESSION_TOKEN_FILE"); f != "" {
		if data, err := os.ReadFile(f); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	src := os.Getenv("PODIUM_SESSION_TOKEN_ENV")
	if src == "" {
		src = "PODIUM_SESSION_TOKEN"
	}
	if v := os.Getenv(src); v != "" {
		return strings.TrimSpace(v)
	}
	if registry != "" {
		store := identity.KeychainStore{Service: envDefault("PODIUM_TOKEN_KEYCHAIN_NAME", "podium")}
		if tok, err := store.Load(registry); err == nil {
			return strings.TrimSpace(tok)
		}
	}
	return ""
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
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	// §7.6 / §7.6.1 — attach the resolved caller credential so visibility
	// filtering, layer composition, and audit apply to this identity
	// server-side, matching the MCP path.
	if tok := readCLIToken(base); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
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

// printArtifactHuman renders artifact show output as the markdown body with
// frontmatter at the top (spec: §7.6.1 Output formats). On a decode failure
// it falls back to the raw JSON body so output is never lost.
func printArtifactHuman(body []byte) {
	var a struct {
		Frontmatter  string `json:"frontmatter"`
		ManifestBody string `json:"manifest_body"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		fmt.Println(string(body))
		return
	}
	if a.Frontmatter != "" {
		fmt.Print(a.Frontmatter)
		if !strings.HasSuffix(a.Frontmatter, "\n") {
			fmt.Println()
		}
	}
	if a.ManifestBody != "" {
		fmt.Println(a.ManifestBody)
	}
}

// domainNode is one node in the load_domain subdomain tree, used by
// printDomainHuman to render the nested-bullet view.
type domainNode struct {
	Path        string       `json:"path"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Subdomains  []domainNode `json:"subdomains"`
}

// printDomainHuman renders a domain map as nested bullets (spec: §7.6.1
// Output formats). On a decode failure it falls back to the raw JSON body.
func printDomainHuman(body []byte) {
	var d struct {
		Path        string       `json:"path"`
		Description string       `json:"description"`
		Keywords    []string     `json:"keywords"`
		Subdomains  []domainNode `json:"subdomains"`
		Notable     []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Summary string `json:"summary"`
		} `json:"notable"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		fmt.Println(string(body))
		return
	}
	title := d.Path
	if title == "" {
		title = "(root)"
	}
	fmt.Println(title)
	if d.Description != "" {
		fmt.Printf("  %s\n", d.Description)
	}
	// spec: §4.5.5 — the requested domain's author-curated keywords are
	// returned verbatim in load_domain output; surface them in the human view.
	if len(d.Keywords) > 0 {
		fmt.Printf("  keywords: %s\n", strings.Join(d.Keywords, ", "))
	}
	var walk func(nodes []domainNode, depth int)
	walk = func(nodes []domainNode, depth int) {
		indent := strings.Repeat("  ", depth)
		for _, n := range nodes {
			// Show the full subdomain path so the user can drill in with
			// `podium domain show <path>`; the display name follows when set.
			label := n.Path
			if label == "" {
				label = n.Name
			} else if n.Name != "" {
				label = n.Path + "  " + n.Name
			}
			fmt.Printf("%s- %s\n", indent, label)
			if n.Description != "" {
				fmt.Printf("%s    %s\n", indent, n.Description)
			}
			walk(n.Subdomains, depth+1)
		}
	}
	if len(d.Subdomains) > 0 {
		fmt.Println("subdomains:")
		walk(d.Subdomains, 1)
	}
	if len(d.Notable) > 0 {
		fmt.Println("notable:")
		for _, a := range d.Notable {
			fmt.Printf("  - %s  [%s]\n", a.ID, a.Type)
			if a.Summary != "" {
				fmt.Printf("      %s\n", a.Summary)
			}
		}
	}
}

// printDomainSearchHuman renders a domain search result set as a ranked list
// (spec: §7.6.1 Output formats). On a decode failure it falls back to raw JSON.
func printDomainSearchHuman(body []byte) {
	var resp struct {
		TotalMatched int `json:"total_matched"`
		Domains      []struct {
			Path        string `json:"path"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Println(string(body))
		return
	}
	fmt.Printf("Showing %d of %d results\n\n", len(resp.Domains), resp.TotalMatched)
	for _, d := range resp.Domains {
		label := d.Name
		if label == "" {
			label = d.Path
		}
		fmt.Printf("  %s  (%s)\n", label, d.Path)
		if d.Description != "" {
			fmt.Printf("      %s\n", d.Description)
		}
	}
}

// printJSON emits the §7.5 dry-run envelope:
// {profile, target, harness, scope, artifacts: [{id, version, content_hash,
// type, layer}]}. A jq consumer reads .harness, .scope.include, or
// .artifacts[].version directly. The per-artifact content_hash lets a pre-flight
// check verify the full §14.11 (artifact_id, version, content_hash) triple
// before the lock file is committed (spec: §7.5, §14.11).
func printJSON(res *sync.Result) {
	type artOut struct {
		ID          string `json:"id"`
		Version     string `json:"version"`
		ContentHash string `json:"content_hash"`
		Type        string `json:"type"`
		Layer       string `json:"layer"`
	}
	type scopeOut struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
		Type    []string `json:"type"`
	}
	env := struct {
		Profile   string   `json:"profile"`
		Target    string   `json:"target"`
		Harness   string   `json:"harness"`
		Scope     scopeOut `json:"scope"`
		Artifacts []artOut `json:"artifacts"`
	}{
		Profile: res.Profile,
		Target:  res.Target,
		Harness: res.Adapter,
		Scope: scopeOut{
			Include: emptyIfNil(res.Scope.Include),
			Exclude: emptyIfNil(res.Scope.Exclude),
			Type:    emptyIfNil(res.Scope.Types),
		},
		Artifacts: []artOut{},
	}
	for _, a := range res.Artifacts {
		env.Artifacts = append(env.Artifacts, artOut{ID: a.ID, Version: a.Version, ContentHash: a.ContentHash, Type: a.Type, Layer: a.Layer})
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: encode json: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stdout, string(b))
}

// emptyIfNil normalizes a nil slice to a non-nil empty slice so the JSON
// envelope renders [] rather than null for an unset scope field.
func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
