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
	allowPublicBind := fs.Bool("allow-public-bind", false, "allow public mode to bind a non-loopback address (overrides PODIUM_ALLOW_PUBLIC_BIND)")
	standalone := fs.Bool("standalone", false, "alias for the zero-flag standalone bootstrap")
	configFile := fs.String("config", "", "path to registry.yaml (overrides PODIUM_CONFIG_FILE)")
	layerPath := fs.String("layer-path", "", "filesystem registry root to ingest at startup (§13.10; overrides PODIUM_LAYER_PATH)")
	// §13.10 Web UI: opt-in single-page UI at /ui/. The non-loopback bind is
	// refused unless --web-ui-allow-public-bind is also set and an identity
	// provider is configured (serverboot validates this).
	webUI := fs.Bool("web-ui", false, "mount the bundled web UI at /ui/ (overrides PODIUM_WEB_UI)")
	webUIAllowPublicBind := fs.Bool("web-ui-allow-public-bind", false, "allow the web UI on a non-loopback bind when an identity provider is configured (overrides PODIUM_WEB_UI_ALLOW_PUBLIC_BIND)")
	// §13.10 hybrid search: force BM25-only regardless of any configured
	// vector backend or embedding provider.
	noEmbeddings := fs.Bool("no-embeddings", false, "disable embeddings and fall back to BM25-only search (overrides PODIUM_NO_EMBEDDINGS)")
	// §13.10 signing: standalone signing is disabled by default; opt in to
	// registry-managed-key signing on ingest with --sign registry-key.
	signMode := fs.String("sign", "", "enable ingest signing; the only accepted value is registry-key (overrides PODIUM_SIGN)")
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
	if *allowPublicBind {
		_ = os.Setenv("PODIUM_ALLOW_PUBLIC_BIND", "true")
	}
	if *configFile != "" {
		_ = os.Setenv("PODIUM_CONFIG_FILE", *configFile)
	}
	if *layerPath != "" {
		_ = os.Setenv("PODIUM_LAYER_PATH", *layerPath)
	}
	if *webUI {
		_ = os.Setenv("PODIUM_WEB_UI", "true")
	}
	if *webUIAllowPublicBind {
		_ = os.Setenv("PODIUM_WEB_UI_ALLOW_PUBLIC_BIND", "true")
	}
	if *noEmbeddings {
		_ = os.Setenv("PODIUM_NO_EMBEDDINGS", "true")
	}
	if *signMode != "" {
		_ = os.Setenv("PODIUM_SIGN", *signMode)
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
