package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lennylabs/podium/internal/serverboot"
)

// serveCmd implements `podium serve`. Same code path as the
// `podium-server` binary, exposed here so a single Podium
// distribution gives operators an in-process registry server (§13.10
// standalone deployment).
//
// Flags map to env vars to keep the deployment story uniform: a
// flag set on the command line overrides the matching env var for
// the duration of the process.
func serveCmd(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	setUsage(fs, "Run the standalone registry server in-process.")
	bind := fs.String("bind", "", "address to listen on (overrides PODIUM_BIND)")
	publicMode := fs.Bool("public-mode", false, "run in public mode (overrides PODIUM_PUBLIC_MODE)")
	standalone := fs.Bool("standalone", false, "alias for the zero-flag standalone bootstrap")
	configFile := fs.String("config", "", "path to registry.yaml (overrides PODIUM_CONFIG_FILE)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *bind != "" {
		_ = os.Setenv("PODIUM_BIND", *bind)
	}
	if *publicMode {
		_ = os.Setenv("PODIUM_PUBLIC_MODE", "true")
	}
	if *configFile != "" {
		_ = os.Setenv("PODIUM_CONFIG_FILE", *configFile)
	}
	// --standalone is a documentation hint per §13.10; the bootstrap
	// is already the standalone default. Carry it on the env so
	// future flag-only tweaks have a single source of truth.
	if *standalone {
		_ = os.Setenv("PODIUM_STANDALONE", "true")
	}
	if err := serverboot.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "podium serve: %v\n", err)
		return 1
	}
	return 0
}
