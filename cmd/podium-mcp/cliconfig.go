package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lennylabs/podium/pkg/sign"
	synccfg "github.com/lennylabs/podium/pkg/sync"
	"gopkg.in/yaml.v3"
)

// applyFlagsAndConfig overlays command-line flags and an optional config
// file onto an env-derived config, satisfying the §6.1 / §6.2 contract
// that the bridge "is configured via env vars, command-line flags, or a
// config file". Precedence is flag > config file > env > built-in default:
// the env-derived values already populated c, the config file overrides
// them, and explicit flags override the config file.
//
// The parser recognizes only the documented parameter flags and silently
// ignores everything else (for example the Go test runner's own -test.*
// flags), so it is safe to call unconditionally from loadConfig.
//
// spec: §6.1, §6.2.
func applyFlagsAndConfig(c *config, args []string) error {
	flags, configPath := parseFlags(args)
	// A config file named on the command line wins over the env hint.
	if configPath == "" {
		configPath = os.Getenv("PODIUM_CONFIG")
	}
	if configPath != "" {
		kv, err := readConfigFile(configPath)
		if err != nil {
			return err
		}
		for k, v := range kv {
			applyConfigKV(c, k, v)
		}
	}
	for k, v := range flags {
		applyConfigKV(c, k, v)
	}
	return nil
}

// parseFlags returns the recognized flag assignments and the --config
// path (empty when absent). It accepts both --key=value and --key value
// forms and a single- or double-dash prefix. Unknown flags are kept in
// the returned map; applyConfigKV ignores keys it does not recognize.
func parseFlags(args []string) (map[string]string, string) {
	out := map[string]string{}
	configPath := ""
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if !strings.HasPrefix(tok, "-") {
			continue
		}
		tok = strings.TrimLeft(tok, "-")
		key, val := tok, ""
		hasVal := false
		if eq := strings.IndexByte(tok, '='); eq >= 0 {
			key, val, hasVal = tok[:eq], tok[eq+1:], true
		} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			// Space form: consume the next token as the value.
			val, hasVal = args[i+1], true
			i++
		}
		if !hasVal {
			continue
		}
		if key == "config" {
			configPath = val
			continue
		}
		out[key] = val
	}
	return out, configPath
}

// readConfigFile parses a YAML config file into a flat key→value map.
// Keys match the flag names (kebab-case); values are scalars.
func readConfigFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}
	kv := map[string]string{}
	if err := yaml.Unmarshal(data, &kv); err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	return kv, nil
}

// applyConfigKV assigns one kebab-case parameter to the matching config
// field. Unrecognized keys are ignored so unrelated flags (test runner,
// future parameters) do not abort startup.
func applyConfigKV(c *config, key, val string) {
	switch key {
	case "registry":
		c.registry = val
	case "harness":
		c.harness = val
	case "cache-dir":
		c.cacheDir = val
	case "cache-mode":
		c.cacheMode = val
	case "prefetch":
		c.prefetchIDs = splitCSV(val)
	case "cache-resolution-ttl-seconds":
		c.resolutionTTL = parseTTLSeconds(val)
	case "materialize-root":
		c.materializeRoot = val
	case "overlay-path":
		c.overlayPath = val
	case "audit-sink":
		c.auditSink, c.auditSinkSet = val, true
	case "tenant-id":
		c.tenantID = val
	case "identity-provider":
		c.identityProvider = val
	case "verify-signatures":
		c.verifyPolicy = sign.VerificationPolicy(val)
	case "signature-provider":
		c.signatureProvider = val
	case "oauth-audience":
		c.oauthAudience = val
	case "session-token":
		c.sessionToken = val
	case "session-token-file":
		c.sessionTokenFile = val
	case "metrics-addr":
		c.metricsAddr = val
	case "min-client-version":
		c.minClientVersion = val
	}
}

// registryFromSyncYAML resolves defaults.registry from sync.yaml per
// §7.5.2: the workspace overlay (<cwd>/.podium/sync.yaml) is consulted
// first, then the home-global ~/.podium/sync.yaml that the standalone
// recipe bootstraps (§6.11, §13.10). Returns "" when neither supplies a
// registry.
//
// spec: §6.2, §7.5.2, §6.11.
func registryFromSyncYAML() string {
	if ws, err := os.Getwd(); err == nil {
		if reg := registryFromWorkspace(ws); reg != "" {
			return reg
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if reg := registryFromWorkspace(home); reg != "" {
			return reg
		}
	}
	return ""
}

// registryFromWorkspace reads <workspace>/.podium/sync.yaml and returns
// its resolved defaults.registry, or "" when the file is absent, invalid,
// or carries no registry.
func registryFromWorkspace(workspace string) string {
	cfg, err := synccfg.ReadConfig(workspace)
	if err != nil || cfg == nil || cfg.Defaults.Registry == "" {
		return ""
	}
	return synccfg.ResolveRegistryPath(workspace, cfg.Defaults.Registry)
}

// verifySignaturesFromSyncYAML resolves defaults.verify_signatures from
// sync.yaml using the same scope order as the registry lookup: the workspace
// overlay first, then the home-global ~/.podium/sync.yaml a standalone
// deployment writes. Returns "" when no scope sets it (§13.10).
func verifySignaturesFromSyncYAML() string {
	if ws, err := os.Getwd(); err == nil {
		if v := verifySignaturesFromWorkspace(ws); v != "" {
			return v
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if v := verifySignaturesFromWorkspace(home); v != "" {
			return v
		}
	}
	return ""
}

// verifySignaturesFromWorkspace reads <workspace>/.podium/sync.yaml and
// returns its defaults.verify_signatures, or "" when absent or invalid.
func verifySignaturesFromWorkspace(workspace string) string {
	cfg, err := synccfg.ReadConfig(workspace)
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.Defaults.VerifySignatures
}

// checkServerVersionFromSyncYAML enforces the §6.7 "Versioning" pin: if any
// sync.yaml scope (workspace overlay first, then the home-global
// ~/.podium/sync.yaml) pins a min_server_version above this binary's version,
// the bridge refuses to start. The bridge can serve any profile, so it must
// satisfy the highest pin across defaults and every profile in each scope.
// Returns the first config.server_version_too_old error found, or nil.
//
// spec: §6.7 "Versioning", §6.9 "older binaries refuse to start".
func checkServerVersionFromSyncYAML(binaryVersion string) error {
	scopes := []string{}
	if ws, err := os.Getwd(); err == nil {
		scopes = append(scopes, ws)
	}
	if home, err := os.UserHomeDir(); err == nil {
		scopes = append(scopes, home)
	}
	for _, workspace := range scopes {
		cfg, err := synccfg.ReadConfig(workspace)
		if err != nil || cfg == nil {
			continue
		}
		profiles := make([]string, 0, len(cfg.Profiles))
		for name := range cfg.Profiles {
			profiles = append(profiles, name)
		}
		if verr := cfg.CheckServerVersion(binaryVersion, profiles...); verr != nil {
			return verr
		}
	}
	return nil
}
