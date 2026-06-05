package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/version"
	"gopkg.in/yaml.v3"
)

// Errors related to config files.
var (
	// ErrConfigNotFound signals that no sync.yaml was found in any
	// configured scope. Maps to config.no_registry in §6.10 when it
	// surfaces during a sync.
	ErrConfigNotFound = errors.New("config.not_found")
)

// SyncConfig is the in-memory representation of `.podium/sync.yaml`
// (spec §7.5.2). Fields not in the schema are preserved verbatim
// through Read/Write so handcrafted comments and scalar formatting
// stay intact across edits.
type SyncConfig struct {
	Defaults Defaults           `yaml:"defaults,omitempty"`
	Profiles map[string]Profile `yaml:"profiles,omitempty"`
	Targets  []TargetEntry      `yaml:"targets,omitempty"`
}

// Defaults is the `defaults:` block.
type Defaults struct {
	Registry string `yaml:"registry,omitempty"`
	Harness  string `yaml:"harness,omitempty"`
	Target   string `yaml:"target,omitempty"`
	Profile  string `yaml:"profile,omitempty"`
	// VerifySignatures is the consumer-side §4.7.9 signature-verification
	// policy (never | medium-and-above | always). A standalone deployment
	// writes `never` here on first run so consumers relax the default without
	// an env var (§13.10); PODIUM_VERIFY_SIGNATURES overrides it.
	VerifySignatures string `yaml:"verify_signatures,omitempty"`
	// MinServerVersion pins the minimum MCP server / CLI binary version this
	// configuration requires (§6.7 "Versioning": a profile or harness
	// combination that needs a newer adapter behavior pins a minimum, and an
	// older binary refuses to start). A profile pin overrides this default-wide
	// pin when it is higher.
	MinServerVersion string `yaml:"min_server_version,omitempty"`
}

// Profile is one entry under `profiles:`. Names without explicit
// values are normalized to empty slices on read.
type Profile struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	Type    []string `yaml:"type,omitempty"`
	Target  string   `yaml:"target,omitempty"`
	Harness string   `yaml:"harness,omitempty"`
	// MinServerVersion pins the minimum binary version this profile requires
	// (§6.7 "Versioning"). A binary below this refuses to start when the
	// profile is active.
	MinServerVersion string `yaml:"min_server_version,omitempty"`
}

// TargetEntry is one entry under `targets:` (multi-target mode).
type TargetEntry struct {
	ID      string   `yaml:"id"`
	Harness string   `yaml:"harness,omitempty"`
	Target  string   `yaml:"target,omitempty"`
	Profile string   `yaml:"profile,omitempty"`
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	Type    []string `yaml:"type,omitempty"`
}

// ConfigPath returns the canonical path to a workspace's sync.yaml.
func ConfigPath(workspace string) string {
	return filepath.Join(workspace, ".podium", "sync.yaml")
}

// ResolveRegistryPath resolves a sync.yaml `defaults.registry`
// value per §13.11.2: filesystem URLs return as-is, absolute
// paths return as-is, and relative paths resolve against the
// workspace. Empty input returns "".
func ResolveRegistryPath(workspace, registry string) string {
	if registry == "" {
		return ""
	}
	// HTTP(S) URLs and file:// URIs pass through unchanged; the
	// caller distinguishes filesystem from server source by
	// inspecting the scheme.
	if hasURLScheme(registry) {
		return registry
	}
	if filepath.IsAbs(registry) {
		return filepath.Clean(registry)
	}
	return filepath.Clean(filepath.Join(workspace, registry))
}

// hasURLScheme reports whether s begins with a URL scheme
// (http://, https://, file://). The check is intentionally
// strict: registry: ./relative is a path, not a URL.
func hasURLScheme(s string) bool {
	for _, prefix := range []string{"http://", "https://", "file://"} {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// isServerSource reports whether registry resolves to a Podium server
// under the §7.1 / §7.5.2 dispatch: an http:// or https:// URL routes to
// a server, and every other value (a bare path, a file:// URI) is a
// filesystem source. Run and Watch use it to reject a server URL with a
// canonical error instead of letting filesystem.Open mangle the URL into
// a bogus path under the working directory.
//
// spec: §7.1, §7.5.2 — "a URL routes to a Podium server, a filesystem
// path routes to local filesystem".
// IsServerSource is the exported form of isServerSource for callers outside
// the package (the CLI uses it to decide whether the §3.5 scope-preview
// endpoint is reachable over HTTP).
func IsServerSource(registry string) bool { return isServerSource(registry) }

func isServerSource(registry string) bool {
	for _, prefix := range []string{"http://", "https://"} {
		if len(registry) >= len(prefix) && registry[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// ReadConfig reads sync.yaml from the workspace's .podium/ directory.
// A missing file returns (nil, nil) so callers can distinguish "no
// config" from "invalid config" without an error type discriminator.
func ReadConfig(workspace string) (*SyncConfig, error) {
	data, err := os.ReadFile(ConfigPath(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cfg := &SyncConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("sync.yaml: %w", err)
	}
	return cfg, nil
}

// WriteConfig writes the SyncConfig back to the workspace's
// sync.yaml atomically via .tmp + rename.
func WriteConfig(workspace string, cfg *SyncConfig) error {
	return writeConfig(workspace, cfg, false)
}

// writeConfig writes cfg to the workspace's sync.yaml atomically. When
// ensureDefaults is true and cfg carries no defaults, an empty `defaults:`
// mapping is emitted ahead of `profiles:` so a freshly created sync.yaml has
// the structure §7.5.6 / §7.5.7 describe.
func writeConfig(workspace string, cfg *SyncConfig, ensureDefaults bool) error {
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := marshalConfig(cfg, ensureDefaults)
	if err != nil {
		return err
	}
	final := ConfigPath(workspace)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// marshalConfig renders cfg to YAML. When ensureDefaults is true and the
// rendered config has no `defaults:` key (a zero-value Defaults is dropped by
// omitempty), an empty `defaults:` mapping is prepended so it precedes
// `profiles:`, matching the fresh-file layout `podium profile edit` produces.
//
// spec: §7.5.6 — a fresh sync.yaml is created "with the new profile and an
// empty defaults: block".
func marshalConfig(cfg *SyncConfig, ensureDefaults bool) ([]byte, error) {
	var node yaml.Node
	if err := node.Encode(cfg); err != nil {
		return nil, err
	}
	if ensureDefaults && findConfigKey(&node, "defaults") == nil {
		node.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "defaults"},
			{Kind: yaml.MappingNode, Tag: "!!map"},
		}, node.Content...)
	}
	return yaml.Marshal(&node)
}

// findConfigKey returns the value node for key in a mapping node, or nil.
func findConfigKey(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// requiredServerVersion returns the highest min_server_version pinned by the
// `defaults:` block or by any of the named profiles, and whether any pin was
// found (§6.7 "Versioning"). An unparsable pin is treated as higher than a
// parsable one so checkServerVersion surfaces it rather than dropping it.
func requiredServerVersion(defaults Defaults, profiles map[string]Profile, names []string) (string, bool) {
	pins := []string{defaults.MinServerVersion}
	for _, name := range names {
		if p, ok := profiles[name]; ok {
			pins = append(pins, p.MinServerVersion)
		}
	}
	highest := ""
	for _, p := range pins {
		if p == "" {
			continue
		}
		if highest == "" {
			highest = p
			continue
		}
		if c, err := version.Compare(p, highest); err != nil || c > 0 {
			highest = p
		}
	}
	return highest, highest != ""
}

// checkServerVersion verifies that binaryVersion satisfies the highest pin
// across defaults and the named profiles. It returns a
// config.server_version_too_old error when binaryVersion is below the pin, a
// config.invalid_min_version error when a pin (or the binary version) is
// unparsable, or nil when no pin applies or the binary satisfies it.
func checkServerVersion(binaryVersion string, defaults Defaults, profiles map[string]Profile, names []string) error {
	min, ok := requiredServerVersion(defaults, profiles, names)
	if !ok {
		return nil
	}
	atLeast, err := version.AtLeast(binaryVersion, min)
	if err != nil {
		return fmt.Errorf("config.invalid_min_version: cannot compare binary version %q against min_server_version %q: %w",
			binaryVersion, min, err)
	}
	if !atLeast {
		return fmt.Errorf("config.server_version_too_old: this binary is version %s but the configuration pins min_server_version %s; upgrade Podium to run this profile",
			binaryVersion, min)
	}
	return nil
}

// CheckServerVersion enforces the §6.7 "Versioning" pin against a raw
// SyncConfig: binaryVersion must satisfy the highest min_server_version pinned
// by defaults or by any of the named profiles. Pass every profile name for a
// server that can serve any profile (the MCP bridge). See checkServerVersion
// for the error codes.
func (cfg *SyncConfig) CheckServerVersion(binaryVersion string, profiles ...string) error {
	return checkServerVersion(binaryVersion, cfg.Defaults, cfg.Profiles, profiles)
}

// CheckServerVersion enforces the §6.7 "Versioning" pin against a merged
// (multi-scope) config: binaryVersion must satisfy the highest
// min_server_version pinned by the merged defaults or by the active profile.
// Pass the single resolved profile name (a `podium sync` run resolves exactly
// one profile). See checkServerVersion for the error codes.
func (cfg *MergedConfig) CheckServerVersion(binaryVersion string, profiles ...string) error {
	return checkServerVersion(binaryVersion, cfg.Defaults, cfg.Profiles, profiles)
}
