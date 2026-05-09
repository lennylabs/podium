package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	Defaults Defaults            `yaml:"defaults,omitempty"`
	Profiles map[string]Profile  `yaml:"profiles,omitempty"`
	Targets  []TargetEntry       `yaml:"targets,omitempty"`
}

// Defaults is the `defaults:` block.
type Defaults struct {
	Registry string `yaml:"registry,omitempty"`
	Harness  string `yaml:"harness,omitempty"`
	Target   string `yaml:"target,omitempty"`
	Profile  string `yaml:"profile,omitempty"`
}

// Profile is one entry under `profiles:`. Names without explicit
// values are normalized to empty slices on read.
type Profile struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	Type    []string `yaml:"type,omitempty"`
	Target  string   `yaml:"target,omitempty"`
	Harness string   `yaml:"harness,omitempty"`
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
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
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

// EnsureConfig loads sync.yaml or returns a fresh SyncConfig when the
// file does not exist. Used by save-as / profile edit which create
// the file as needed.
func EnsureConfig(workspace string) (*SyncConfig, error) {
	cfg, err := ReadConfig(workspace)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &SyncConfig{Profiles: map[string]Profile{}}, nil
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	return cfg, nil
}
