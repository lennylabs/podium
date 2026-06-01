package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/lennylabs/podium/internal/serverboot"
)

// configCmd dispatches `podium config <subcommand>`.
func configCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("config", "Inspect the merged configuration.", [][2]string{
			{"show", "Print the merged sync.yaml and the resolved server settings with per-key provenance (--server for server settings only)."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "show":
		return configShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand: %s\n", args[0])
		return 2
	}
}

// configShow prints the merged client sync.yaml with per-key provenance
// (§7.7). With --server it prints the resolved server configuration
// instead (§13.10, §13.12).
func configShow(args []string) int {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	setUsage(fs, "Print the merged sync.yaml with per-key provenance.")
	asJSON := fs.Bool("json", false, "emit JSON")
	server := fs.Bool("server", false, "print the resolved server configuration instead of the client sync.yaml")
	explain := fs.String("explain", "", "print the full resolution chain for one key")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *server {
		return configServerShow(*asJSON)
	}
	// --explain resolves a single client sync.yaml key (client-only).
	if *explain != "" {
		cwd, _ := os.Getwd()
		home, _ := os.UserHomeDir()
		return configClientShowAt(cwd, home, *asJSON, *explain)
	}
	return configEffectiveShow(*asJSON)
}

// configEffectiveShow prints the merged client sync.yaml together with the
// resolved server settings and their provenance. The client section answers
// "what registry / profiles will the CLI use" (§7.7); the settings section
// answers "what would a server resolve from this environment" (§13.10, §13.12).
// `config show --server` prints only the latter.
func configEffectiveShow(asJSON bool) int {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	settings := serverboot.LoadConfig().Settings()
	if asJSON {
		payload, err := clientPayloadAt(cwd, home)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		payload["settings"] = settings
		_ = json.NewEncoder(os.Stdout).Encode(payload)
		return 0
	}
	if rc := configClientShowAt(cwd, home, false, ""); rc != 0 {
		return rc
	}
	fmt.Fprintln(os.Stdout)
	printSettingsTable(settings)
	return 0
}

// configServerShow prints the resolved server configuration with the
// source of each value (env var name, registry.yaml, or "default").
// API keys / DSNs are redacted. spec: §13.10, §13.12.
func configServerShow(asJSON bool) int {
	settings := serverboot.LoadConfig().Settings()
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"settings": settings})
		return 0
	}
	printSettingsTable(settings)
	return 0
}

// printSettingsTable writes the name/value/source table for a settings slice.
func printSettingsTable(settings []serverboot.Setting) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "name\tvalue\tsource")
	for _, s := range settings {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.Value, s.Source)
	}
	tw.Flush()
}
