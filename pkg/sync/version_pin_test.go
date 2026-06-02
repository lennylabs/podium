package sync_test

import (
	"strings"
	"testing"

	sync "github.com/lennylabs/podium/pkg/sync"
)

// Spec: §6.7 "Versioning" — a binary at or above the pinned
// min_server_version satisfies the check; defaults with no pin never fails.
func TestCheckServerVersion_DefaultsPin(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{Defaults: sync.Defaults{MinServerVersion: "1.2.0"}}

	if err := cfg.CheckServerVersion("1.2.0"); err != nil {
		t.Errorf("binary == pin should pass, got %v", err)
	}
	if err := cfg.CheckServerVersion("1.3.0"); err != nil {
		t.Errorf("binary above pin should pass, got %v", err)
	}
	err := cfg.CheckServerVersion("1.1.9")
	if err == nil || !strings.Contains(err.Error(), "config.server_version_too_old") {
		t.Errorf("binary below pin should refuse with config.server_version_too_old, got %v", err)
	}

	// No pin anywhere: always passes.
	empty := &sync.SyncConfig{}
	if err := empty.CheckServerVersion("0.0.1"); err != nil {
		t.Errorf("no pin should pass, got %v", err)
	}
}

// Spec: §6.7 "Versioning" — the bridge serves any profile, so it must satisfy
// the highest pin across defaults and every profile passed.
func TestCheckServerVersion_ProfilePinIsHighest(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{MinServerVersion: "1.0.0"},
		Profiles: map[string]sync.Profile{
			"prod": {MinServerVersion: "2.1.0"},
			"dev":  {MinServerVersion: "1.5.0"},
		},
	}
	// Against all profiles, the binary must meet the highest pin (2.1.0).
	if err := cfg.CheckServerVersion("2.0.0", "prod", "dev"); err == nil {
		t.Errorf("binary 2.0.0 below prod pin 2.1.0 should refuse")
	}
	if err := cfg.CheckServerVersion("2.1.0", "prod", "dev"); err != nil {
		t.Errorf("binary 2.1.0 meets the highest pin, got %v", err)
	}
	// Scoped to only the dev profile, the binary need only meet 1.5.0.
	if err := cfg.CheckServerVersion("1.5.0", "dev"); err != nil {
		t.Errorf("binary 1.5.0 meets dev pin, got %v", err)
	}
}

// Spec: §6.7 "Versioning" — an unparsable pin surfaces config.invalid_min_version
// rather than silently passing.
func TestCheckServerVersion_UnparsablePin(t *testing.T) {
	t.Parallel()
	cfg := &sync.SyncConfig{Defaults: sync.Defaults{MinServerVersion: "not-a-version"}}
	err := cfg.CheckServerVersion("1.0.0")
	if err == nil || !strings.Contains(err.Error(), "config.invalid_min_version") {
		t.Errorf("unparsable pin should refuse with config.invalid_min_version, got %v", err)
	}
}

// Spec: §6.7 "Versioning" — the merged (multi-scope) config enforces the same
// pin against the single resolved profile.
func TestCheckServerVersion_MergedConfig(t *testing.T) {
	t.Parallel()
	merged := &sync.MergedConfig{
		Defaults: sync.Defaults{MinServerVersion: "1.0.0"},
		Profiles: map[string]sync.Profile{"prod": {MinServerVersion: "2.0.0"}},
	}
	// The active profile prod pins 2.0.0.
	if err := merged.CheckServerVersion("1.9.0", "prod"); err == nil {
		t.Errorf("binary 1.9.0 below prod pin 2.0.0 should refuse")
	}
	if err := merged.CheckServerVersion("2.0.0", "prod"); err != nil {
		t.Errorf("binary 2.0.0 meets prod pin, got %v", err)
	}
	// No active profile (empty name): only the defaults pin (1.0.0) applies.
	if err := merged.CheckServerVersion("1.0.0", ""); err != nil {
		t.Errorf("binary meets defaults pin with no active profile, got %v", err)
	}
	if err := merged.CheckServerVersion("0.9.0", ""); err == nil {
		t.Errorf("binary 0.9.0 below defaults pin 1.0.0 should refuse")
	}
}

// Spec: §6.7 "Versioning" — min_server_version round-trips through sync.yaml
// read/write on both defaults and a profile.
func TestMinServerVersion_RoundTrips(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	cfg := &sync.SyncConfig{
		Defaults: sync.Defaults{Registry: "https://r.example", MinServerVersion: "1.2.0"},
		Profiles: map[string]sync.Profile{"prod": {MinServerVersion: "2.0.0"}},
	}
	if err := sync.WriteConfig(ws, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	got, err := sync.ReadConfig(ws)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if got.Defaults.MinServerVersion != "1.2.0" {
		t.Errorf("defaults.min_server_version = %q, want 1.2.0", got.Defaults.MinServerVersion)
	}
	if got.Profiles["prod"].MinServerVersion != "2.0.0" {
		t.Errorf("profiles.prod.min_server_version = %q, want 2.0.0", got.Profiles["prod"].MinServerVersion)
	}
}
