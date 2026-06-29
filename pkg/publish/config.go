// Package publish parses and resolves the operator-authored publish.yaml that
// drives `podium publish` (§7.8). It loads the same three config scopes
// sync.yaml uses (§7.5.2), resolves each marketplace output against the shared
// defaults, and validates the result against the publish-target roster and the
// §7.5.1 glob syntax.
package publish

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sync"
	"gopkg.in/yaml.v3"
)

// ErrConfigInvalid signals a publish.yaml that fails validation: a harness set
// naming a non-publish-target harness, or a malformed plugin glob. It maps to
// config.invalid in §6.10. Callers assert against it via errors.Is.
var ErrConfigInvalid = errors.New("config.invalid")

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

// GitRemote is the `git:` block of a marketplace output: the remote URL the
// workflow clones and pushes, and the branch it writes (§7.8).
type GitRemote struct {
	Remote string `yaml:"remote,omitempty"`
	Branch string `yaml:"branch,omitempty"`
}

// PluginFilter is one entry under `plugins:`: a named bundle of selected
// artifacts defined by a §7.5.1 scope filter (include, exclude, type). The
// publishing pipeline assigns each selected artifact to its plugin by
// evaluating the filters in declaration order (§7.8). Description is the
// optional human-readable plugin description the marketplace emitter carries
// into the per-plugin manifest (§6.7 "Plugin descriptor", open question 8); it
// is empty when the operator omits it.
type PluginFilter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Include     []string `yaml:"include,omitempty"`
	Exclude     []string `yaml:"exclude,omitempty"`
	Type        []string `yaml:"type,omitempty"`
}

// ScopeFilter returns the §7.5.1 selection this plugin filter expresses,
// reusing the sync scope-filter machinery so plugin selection and sync
// selection apply identical glob semantics (§7.8).
func (p PluginFilter) ScopeFilter() sync.ScopeFilter {
	return sync.ScopeFilter{Include: p.Include, Exclude: p.Exclude, Types: p.Type}
}

// Workflow groups the `prepare` and `publish` command lists `podium publish`
// runs around the render phase (§7.8). prepare places a checkout of the
// destination repository at the working directory, and publish takes the
// rendered tree to the remote. PrepareOnError and PublishOnError are optional
// per-phase cleanup lists: a failure in the prepare phase runs PrepareOnError,
// and a failure in the publish phase runs PublishOnError, before the failure
// propagates. The cleanup is scoped to the phase that failed, so a prepare
// failure does not run cleanup authored for a publish-phase checkout.
type Workflow struct {
	Prepare        []Command `yaml:"prepare,omitempty"`
	Publish        []Command `yaml:"publish,omitempty"`
	PrepareOnError []Command `yaml:"prepare_on_error,omitempty"`
	PublishOnError []Command `yaml:"publish_on_error,omitempty"`
}

// IsZero reports whether the workflow declares no commands. A marketplace whose
// workflow is zero inherits the default workflow; a non-zero workflow replaces
// it in full (§7.8).
func (w Workflow) IsZero() bool {
	return len(w.Prepare) == 0 && len(w.Publish) == 0 &&
		len(w.PrepareOnError) == 0 && len(w.PublishOnError) == 0
}

// commands returns every command the workflow declares across its phases and
// per-phase cleanup lists, so config validation (§7.8 --check) can check each
// for well-formedness before any side effect.
func (w Workflow) commands() []Command {
	all := make([]Command, 0, len(w.Prepare)+len(w.Publish)+len(w.PrepareOnError)+len(w.PublishOnError))
	all = append(all, w.Prepare...)
	all = append(all, w.Publish...)
	all = append(all, w.PrepareOnError...)
	all = append(all, w.PublishOnError...)
	return all
}

// Command is one step of a workflow phase (§7.8). It is an argv list under
// `run:` executed directly without a shell, or a string under `sh:` executed
// through `sh -c`. The per-command flags control failure handling:
// SkipIfNoChanges skips the command when the render produced no diff,
// ContinueOnError lets the pipeline proceed past a non-zero exit, and Timeout
// bounds the command's wall-clock duration.
type Command struct {
	Run             []string `yaml:"run,omitempty"`
	Sh              string   `yaml:"sh,omitempty"`
	SkipIfNoChanges bool     `yaml:"skip_if_no_changes,omitempty"`
	ContinueOnError bool     `yaml:"continue_on_error,omitempty"`
	Timeout         Duration `yaml:"timeout,omitempty"`
}

// Duration is a time.Duration that unmarshals from a Go duration string such as
// "30s" or "5m". A bare YAML integer is rejected so a unit is always explicit,
// because an ambiguous "timeout: 30" reads as nanoseconds under the default
// yaml decoding and surprises an operator who meant seconds.
type Duration time.Duration

// Duration returns the timeout as a time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// UnmarshalYAML parses a duration string into d.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("timeout: expected a duration string such as \"30s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("timeout: invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
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

// ResolvedOutput is one marketplace output with its defaults applied: the
// registry, the publishing identity, and the effective workflow (the output's
// own workflow when it declares one, else the default workflow). The git
// destination, harness set, plugins, and commit message come from the output.
type ResolvedOutput struct {
	ID            string
	Registry      string
	Identity      string
	Git           GitRemote
	Harnesses     []string
	CommitMessage string
	Plugins       []PluginFilter
	Workflow      Workflow
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
// output with a malformed plugin glob, both with config.invalid (§6.10).
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
		if err := validateOutput(resolved); err != nil {
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

// validateOutput rejects an output whose harness set names a non-publish-target
// harness (opencode, none, or an unknown id), an output with a malformed plugin
// glob, and an output with a malformed workflow command. All map to
// config.invalid (§6.10). The harness check reuses the §7.8 publish-target
// selector (adapter.EmitterForHarness), the glob check reuses the §7.5.1 sync
// glob validator (sync.ValidateGlob), and the command check reuses the per-step
// validation (Command.validate), so config validation and the render path agree
// on which harnesses publish, which globs are well-formed, and which commands
// are well-formed. Validating the workflow commands here makes --check
// fail-closed: a command that declares neither run: nor sh:, or both, is
// rejected before the prepare clone or the render runs (§7.8 "--check validates
// the config only").
func validateOutput(out ResolvedOutput) error {
	for _, h := range out.Harnesses {
		if _, err := adapter.EmitterForHarness(h); err != nil {
			return fmt.Errorf("%w: marketplace %q harness %q is not a publish target (opencode and none have no git-repo distribution): %w",
				ErrConfigInvalid, out.ID, h, err)
		}
	}
	for _, p := range out.Plugins {
		for _, g := range append(append([]string(nil), p.Include...), p.Exclude...) {
			if err := sync.ValidateGlob(g); err != nil {
				return fmt.Errorf("%w: marketplace %q plugin %q has a malformed glob %q: %w",
					ErrConfigInvalid, out.ID, p.Name, g, err)
			}
		}
	}
	for _, c := range out.Workflow.commands() {
		if err := c.validate(); err != nil {
			return fmt.Errorf("%w: marketplace %q command %q: %w",
				ErrConfigInvalid, out.ID, c.display(), err)
		}
	}
	return nil
}
