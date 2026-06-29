// Package publish parses and resolves the operator-authored publish.yaml that
// drives `podium publish` (§7.8). It loads the same three config scopes
// sync.yaml uses (§7.5.2), resolves each marketplace output against the shared
// defaults, and validates the result against the publish-target roster and the
// §7.5.1 glob syntax. The marketplace component types and the render pipeline
// live in pkg/sync; this package carries only the publish.yaml loader.
package publish

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/sync"
	"gopkg.in/yaml.v3"
)

// ErrConfigInvalid is the config.invalid error class for a publish.yaml that
// fails validation. It aliases sync.ErrConfigInvalid so a caller asserting
// against either var with errors.Is matches a validation failure from the loader
// or from the pkg/sync marketplace runner.
var ErrConfigInvalid = sync.ErrConfigInvalid

// The publish.yaml component types reuse the pkg/sync marketplace types so the
// loader, the render pipeline, and the runner share one schema definition.
type (
	// GitRemote is the `git:` block of a marketplace output (§7.8).
	GitRemote = sync.GitRemote
	// PluginFilter is one `plugins:` entry: a named scope filter (§7.8).
	PluginFilter = sync.PluginFilter
	// Workflow groups the prepare and publish command lists (§7.8).
	Workflow = sync.Workflow
	// Command is one step of a workflow phase (§7.8).
	Command = sync.Command
	// Duration is a per-command timeout parsed from a Go duration string.
	Duration = sync.Duration
	// ResolvedOutput is one marketplace output with its defaults applied.
	ResolvedOutput = sync.ResolvedOutput
)

// PublishConfig is the in-memory representation of `.podium/publish.yaml`
// (§7.8). Its top-level keys are `defaults` and `marketplaces`. The same schema
// is read from up to three file scopes and merged by per-key precedence,
// mirroring sync.yaml (§7.5.2).
type PublishConfig struct {
	Defaults     Defaults            `yaml:"defaults,omitempty"`
	Marketplaces []MarketplaceOutput `yaml:"marketplaces,omitempty"`
}

// Defaults is the `defaults:` block. It holds the registry, the publishing
// identity (the §4.6 effective-view principal the render runs as), and a
// default workflow each marketplace inherits unless it declares its own.
type Defaults struct {
	Registry string   `yaml:"registry,omitempty"`
	Identity string   `yaml:"identity,omitempty"`
	Workflow Workflow `yaml:"workflow,omitempty"`
}

// MarketplaceOutput is one entry under `marketplaces:`: a named publishing
// destination with a git repository, a harness set, a plugin list, and a
// workflow. Each marketplace inherits the defaults and may override them (§7.8):
// Registry and Identity, when set, replace the corresponding default for that
// output, and an output that declares `workflow` replaces the default workflow
// for that output in full.
type MarketplaceOutput struct {
	ID            string         `yaml:"id"`
	Registry      string         `yaml:"registry,omitempty"`
	Identity      string         `yaml:"identity,omitempty"`
	Git           GitRemote      `yaml:"git,omitempty"`
	Harnesses     []string       `yaml:"harnesses,omitempty"`
	CommitMessage string         `yaml:"commit_message,omitempty"`
	Plugins       []PluginFilter `yaml:"plugins,omitempty"`
	Workflow      Workflow       `yaml:"workflow,omitempty"`
}

// configFileName is the publish config file name in each scope, matching the
// sync.yaml convention (§7.5.2). The project-local scope uses publish.local.yaml.
const (
	configFileName      = "publish.yaml"
	localConfigFileName = "publish.local.yaml"
)

// ConfigPath returns the canonical path to a workspace's publish.yaml.
func ConfigPath(workspace string) string {
	return filepath.Join(workspace, ".podium", configFileName)
}

// ReadConfigFile reads a PublishConfig from an explicit path. A missing file
// returns (nil, nil) so callers can treat an absent scope as empty, matching
// sync.ReadConfigFile. `podium publish --config <path>` reads one file directly
// through this, and LoadMergedConfig reads each scope file through it.
func ReadConfigFile(path string) (*PublishConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cfg := &PublishConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// LoadMergedConfig discovers the workspace by walking up from startDir, loads
// the three §7.5.2 file scopes (`<homeDir>/.podium/publish.yaml`,
// `<workspace>/.podium/publish.yaml`, `<workspace>/.podium/publish.local.yaml`),
// and merges them by per-key precedence (project-local > project-shared >
// user-global). homeDir locates the user-global file; tests pass an explicit
// directory. An absent scope file contributes nothing. The returned workspace
// is "" when no `.podium/` is found; only the user-global scope then applies.
//
// spec: §7.8 — publish.yaml "with the same three-scope resolution and the same
// precedence rules as sync.yaml (§7.5.2)".
func LoadMergedConfig(startDir, homeDir string) (*PublishConfig, string, error) {
	workspace, _ := sync.DiscoverWorkspace(startDir)

	var scopes []*PublishConfig
	if homeDir != "" {
		cfg, err := ReadConfigFile(filepath.Join(homeDir, ".podium", configFileName))
		if err != nil {
			return nil, workspace, err
		}
		scopes = append(scopes, cfg)
	}
	if workspace != "" {
		shared, err := ReadConfigFile(filepath.Join(workspace, ".podium", configFileName))
		if err != nil {
			return nil, workspace, err
		}
		local, err := ReadConfigFile(filepath.Join(workspace, ".podium", localConfigFileName))
		if err != nil {
			return nil, workspace, err
		}
		scopes = append(scopes, shared, local)
	}

	return mergeScopes(scopes), workspace, nil
}

// mergeScopes folds the scopes (ordered low to high precedence) into one
// PublishConfig. Defaults merge per key: a higher-precedence non-empty value
// wins. The marketplaces list is the structural analog of the sync.yaml
// `targets:` list, which §7.5.2 resolves by whole-list replacement: the
// highest-precedence scope that declares a non-empty `marketplaces:` replaces
// the entire list, mirroring sync's `if len(f.cfg.Targets) > 0` rule
// (pkg/sync/resolve.go). A lower-precedence list does not survive into the
// merged config once a higher scope declares its own.
func mergeScopes(scopes []*PublishConfig) *PublishConfig {
	merged := &PublishConfig{}
	for _, cfg := range scopes {
		if cfg == nil {
			continue
		}
		mergeDefaults(&merged.Defaults, cfg.Defaults)
		if len(cfg.Marketplaces) > 0 {
			merged.Marketplaces = cfg.Marketplaces
		}
	}
	return merged
}

// mergeDefaults overlays src onto dst, keeping a non-empty src field. A non-zero
// src workflow replaces dst's workflow in full, because a workflow is overridden
// as a unit (§7.8) rather than field by field.
func mergeDefaults(dst *Defaults, src Defaults) {
	if src.Registry != "" {
		dst.Registry = src.Registry
	}
	if src.Identity != "" {
		dst.Identity = src.Identity
	}
	if !src.Workflow.IsZero() {
		dst.Workflow = src.Workflow
	}
}

// Resolve applies the merged defaults to each marketplace output and validates
// the result. Each marketplace inherits the defaults and may override them
// (§7.8). The registry resolves per key by the §7.5.2 precedence ladder: the
// PODIUM_REGISTRY env var wins over the output's own registry, which in turn
// wins over the merged defaults.registry, mirroring sync's Resolve
// (pkg/sync/resolve.go). env is os.Getenv or a test stub; a nil env reads no
// environment. The §7.8 Pattern A CI run depends on the env level: it sets
// PODIUM_REGISTRY as an env var with no defaults.registry in publish.yaml.
// Identity has no recognized PODIUM_* env var, so the output's own identity wins
// over the default identity. Each output's effective workflow is its own
// workflow when it declares one, else the default workflow in full. Validation
// rejects an output whose harness set names a non-publish-target harness and an
// output with a malformed plugin glob, both with config.invalid (§6.10), through
// the shared sync.ValidateOutput.
//
// spec: §7.5.2 (precedence ladder, env vars above the file scopes), §7.8.
func (cfg *PublishConfig) Resolve(env func(string) string) ([]ResolvedOutput, error) {
	if cfg == nil {
		return nil, nil
	}
	if env == nil {
		env = func(string) string { return "" }
	}
	out := make([]ResolvedOutput, 0, len(cfg.Marketplaces))
	for _, m := range cfg.Marketplaces {
		workflow := m.Workflow
		if workflow.IsZero() {
			workflow = cfg.Defaults.Workflow
		}
		resolved := ResolvedOutput{
			ID:            m.ID,
			Registry:      firstNonEmpty(env("PODIUM_REGISTRY"), m.Registry, cfg.Defaults.Registry),
			Identity:      firstNonEmpty(m.Identity, cfg.Defaults.Identity),
			Git:           m.Git,
			Harnesses:     m.Harnesses,
			CommitMessage: m.CommitMessage,
			Plugins:       m.Plugins,
			Workflow:      workflow,
		}
		if err := sync.ValidateOutput(resolved); err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// firstNonEmpty returns the first non-empty argument, or "". It mirrors the
// precedence picker sync uses (pkg/sync/resolve.go) so publish and sync resolve
// the registry by the same §7.5.2 rule.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
