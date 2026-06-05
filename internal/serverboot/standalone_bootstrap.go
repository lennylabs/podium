package serverboot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// standaloneStartup applies the §13.10 zero-flag standalone policy before the
// server opens its backends. When the operator provided no explicit
// configuration, it refuses to start under --strict / PODIUM_NO_AUTOSTANDALONE,
// otherwise emits the documented first-run notice and writes the standalone
// default files. With explicit configuration it writes any missing default
// files (unless suppressed) and stays quiet. A non-nil error aborts startup.
func standaloneStartup(cfg *Config) error {
	// A standard (Postgres) deployment is never a zero-flag standalone first run.
	if cfg.storeType == "postgres" {
		return nil
	}
	if hasExplicitServerConfig() {
		bootstrapStandaloneFiles(cfg)
		return nil
	}
	// Zero-flag: no registry.yaml, no PODIUM_CONFIG_FILE, no --standalone, no
	// --layer-path, no server PODIUM_* env. §13.10: --strict / PODIUM_NO_AUTOSTANDALONE
	// makes a missing config a hard error rather than a cue to auto-bootstrap.
	if isTrue(os.Getenv("PODIUM_NO_AUTOSTANDALONE")) {
		return fmt.Errorf("no server configuration found at ~/.podium/registry.yaml and no PODIUM_* server settings are set; --strict (or PODIUM_NO_AUTOSTANDALONE) requires explicit setup. Run `podium serve` without --strict to auto-bootstrap a standalone deployment, or pass --config / --layer-path")
	}
	fmt.Fprintf(os.Stderr,
		"No config found at ~/.podium/registry.yaml. Starting in standalone mode at http://%s. Run `podium serve --strict` to require explicit setup.\n",
		cfg.bind)
	bootstrapStandaloneFiles(cfg)
	return nil
}

// hasExplicitServerConfig reports whether the operator supplied server
// configuration explicitly, so the §13.10 zero-flag auto-bootstrap should not
// engage (and the first-run notice should be suppressed).
func hasExplicitServerConfig() bool {
	if os.Getenv("PODIUM_CONFIG_FILE") != "" || isTrue(os.Getenv("PODIUM_STANDALONE")) {
		return true
	}
	if registryYAMLExists() {
		return true
	}
	for _, k := range []string{
		"PODIUM_BIND", "PODIUM_REGISTRY_STORE", "PODIUM_POSTGRES_DSN",
		"PODIUM_SQLITE_PATH", "PODIUM_OBJECT_STORE", "PODIUM_FILESYSTEM_ROOT",
		"PODIUM_S3_BUCKET", "PODIUM_S3_ENDPOINT", "PODIUM_VECTOR_BACKEND",
		"PODIUM_IDENTITY_PROVIDER", "PODIUM_PUBLIC_MODE", "PODIUM_LAYER_PATH",
	} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// registryYAMLExists reports whether ~/.podium/registry.yaml is present.
func registryYAMLExists() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".podium", "registry.yaml"))
	return err == nil
}

// bootstrapStandaloneFiles writes the §13.10 standalone default files on first
// run so a consumer (CLI, MCP server, SDK) resolves the registry from
// ~/.podium/sync.yaml without an extra environment variable:
//
//   - ~/.podium/sync.yaml      client pointer (defaults.registry: http://<bind>)
//   - ~/.podium/registry.yaml  server config (sqlite store + filesystem objects)
//   - ~/podium-artifacts/       default artifact directory
//
// Each file is written only when absent, so an operator's existing config is
// never overwritten. The whole step is suppressed when PODIUM_NO_AUTOSTANDALONE
// is set (CI, image builds, and test isolation) or when PODIUM_CONFIG_FILE
// (--config) names an explicit config: the operator chose the config, so a
// missing one is an error rather than a cue to invent defaults. Only the
// standalone (SQLite) deployment bootstraps these files; a standard (Postgres)
// deployment leaves client config to the operator.
//
// spec: §13.10 (lines 116, 223), §14.3 step 1, §14.10 step 1.
func bootstrapStandaloneFiles(cfg *Config) {
	if isTrue(os.Getenv("PODIUM_NO_AUTOSTANDALONE")) {
		return
	}
	if os.Getenv("PODIUM_CONFIG_FILE") != "" {
		return
	}
	// SQLite is the standalone default; a Postgres deployment is standard and
	// configures its clients out of band.
	if cfg.storeType == "postgres" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	podiumDir := filepath.Join(home, ".podium")
	if err := os.MkdirAll(podiumDir, 0o755); err != nil {
		log.Printf("warning: standalone bootstrap: %v", err)
		return
	}

	// ~/.podium/sync.yaml — the client registry pointer the §14.3/§14.10
	// flows depend on. The format mirrors `podium init --global`. §13.10: the
	// standalone deployment relaxes the consumer-side signature-verification
	// default to `never` here, so the MCP server and SDKs pick it up without an
	// env var; an operator who wants enforcement sets PODIUM_VERIFY_SIGNATURES.
	syncPath := filepath.Join(podiumDir, "sync.yaml")
	syncBody := []byte("defaults:\n  registry: " + cfg.publicURL + "\n  verify_signatures: never\n")
	if written, err := writeFileIfAbsent(syncPath, syncBody); err != nil {
		log.Printf("warning: standalone bootstrap %s: %v", syncPath, err)
	} else if written {
		log.Printf("standalone: wrote %s (defaults.registry: %s)", syncPath, cfg.publicURL)
	}

	// ~/.podium/registry.yaml — the server config matching the standalone
	// defaults already resolved into cfg.
	regPath := filepath.Join(podiumDir, "registry.yaml")
	if written, err := writeFileIfAbsent(regPath, standaloneRegistryYAML(cfg)); err != nil {
		log.Printf("warning: standalone bootstrap %s: %v", regPath, err)
	} else if written {
		log.Printf("standalone: wrote %s", regPath)
	}

	// ~/podium-artifacts/ — the default artifact directory the documented
	// `--layer-path ~/podium-artifacts/` graduation command points at.
	artifactsDir := filepath.Join(home, "podium-artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		log.Printf("warning: standalone bootstrap %s: %v", artifactsDir, err)
	}
}

// writeFileIfAbsent writes data to path only when no file already exists there.
// It returns (true, nil) when it wrote the file, (false, nil) when the path was
// already present, and a non-nil error for any other stat/write failure.
func writeFileIfAbsent(path string, data []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// standaloneRegistryYAML renders the §13.12 `registry:` document for a
// first-run standalone deployment from the resolved config: the bind address,
// the SQLite metadata store, and the filesystem object store.
func standaloneRegistryYAML(cfg *Config) []byte {
	var b strings.Builder
	b.WriteString("registry:\n")
	b.WriteString("  bind: " + cfg.bind + "\n")
	b.WriteString("  store:\n")
	b.WriteString("    type: sqlite\n")
	if cfg.sqlitePath != "" {
		b.WriteString("    sqlite_path: " + cfg.sqlitePath + "\n")
	}
	b.WriteString("  object_store:\n")
	b.WriteString("    type: filesystem\n")
	if cfg.filesystemRoot != "" {
		b.WriteString("    filesystem_root: " + cfg.filesystemRoot + "\n")
	}
	return []byte(b.String())
}
