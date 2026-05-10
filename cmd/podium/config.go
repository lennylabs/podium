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
		printGroupHelp("config", "Inspect the resolved server configuration.", [][2]string{
			{"show", "Print the resolved server configuration with sources."},
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

// configShow prints the resolved server configuration with the
// source of each value (env var name, registry.yaml, or
// "default"). API keys / DSNs are redacted.
func configShow(args []string) int {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	setUsage(fs, "Print the resolved server configuration with sources.")
	asJSON := fs.Bool("json", false, "emit JSON")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := serverboot.LoadConfig()
	settings := cfg.Settings()
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"settings": settings})
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "name\tvalue\tsource")
	for _, s := range settings {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.Value, s.Source)
	}
	tw.Flush()
	return 0
}
