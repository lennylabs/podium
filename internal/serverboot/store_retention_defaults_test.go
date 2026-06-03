package serverboot

import (
	"testing"
)

// Spec: §8.4 (F-8.4.1) — the deprecated-version (90 days) and
// owner-unregistered-layer (30 days) windows are enforced defaults, so the
// store-retention scheduler runs out of the box. The interval defaults to
// one day and the day windows to 90 / 30 when the env vars are unset.
func TestLoadConfig_StoreRetentionDefaultsOn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_STORE_RETENTION_INTERVAL_SECONDS", "")
	t.Setenv("PODIUM_DEPRECATED_RETENTION_DAYS", "")
	t.Setenv("PODIUM_LAYER_RECOVERY_DAYS", "")
	c := LoadConfig()
	if c.storeRetentionInterval != 86400 {
		t.Errorf("storeRetentionInterval = %d, want 86400 (one day, default-on)", c.storeRetentionInterval)
	}
	if c.deprecatedRetentionDays != 90 {
		t.Errorf("deprecatedRetentionDays = %d, want 90 (spec default)", c.deprecatedRetentionDays)
	}
	if c.layerRecoveryDays != 30 {
		t.Errorf("layerRecoveryDays = %d, want 30 (spec default)", c.layerRecoveryDays)
	}
}

// Spec: §8.4 (F-8.4.1) — an explicit interval of 0 disables the scheduler;
// the new default-on behavior must not silently re-enable it.
func TestLoadConfig_StoreRetentionExplicitDisable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_STORE_RETENTION_INTERVAL_SECONDS", "0")
	c := LoadConfig()
	if c.storeRetentionInterval != 0 {
		t.Errorf("storeRetentionInterval = %d, want 0 (explicit disable preserved)", c.storeRetentionInterval)
	}
}

// Spec: §8.4 (F-8.4.1) — an explicit interval overrides the default.
func TestLoadConfig_StoreRetentionEnvOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_STORE_RETENTION_INTERVAL_SECONDS", "3600")
	c := LoadConfig()
	if c.storeRetentionInterval != 3600 {
		t.Errorf("storeRetentionInterval = %d, want 3600", c.storeRetentionInterval)
	}
}
