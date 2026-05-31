package serverboot

import (
	"testing"
)

// Spec: §13.2.1 (F-13.2.3) — the automatic read-only fallback runs out of the
// box: the failure threshold defaults to 3 and the probe interval to 5 s when
// neither the env nor registry.yaml set them.
func TestLoadConfig_ReadOnlyProbeDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Ensure the probe env vars are unset for this process.
	t.Setenv("PODIUM_READONLY_PROBE_FAILURES", "")
	t.Setenv("PODIUM_READONLY_PROBE_INTERVAL", "")
	c := LoadConfig()
	if c.readOnlyProbeFailures != 3 {
		t.Errorf("readOnlyProbeFailures = %d, want 3 (spec default)", c.readOnlyProbeFailures)
	}
	if c.readOnlyProbeInterval != 5 {
		t.Errorf("readOnlyProbeInterval = %d, want 5 (spec default)", c.readOnlyProbeInterval)
	}
}

// Spec: §13.2.1 — an explicit failure threshold of 0 disables the probe; the
// spec default must not silently re-enable it. F-13.2.3.
func TestLoadConfig_ReadOnlyProbeExplicitDisable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_READONLY_PROBE_FAILURES", "0")
	c := LoadConfig()
	if c.readOnlyProbeFailures != 0 {
		t.Errorf("readOnlyProbeFailures = %d, want 0 (explicit disable preserved)", c.readOnlyProbeFailures)
	}
}

// Spec: §13.2.1 — env values override the spec default. F-13.2.3.
func TestLoadConfig_ReadOnlyProbeEnvOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_READONLY_PROBE_FAILURES", "7")
	t.Setenv("PODIUM_READONLY_PROBE_INTERVAL", "12")
	c := LoadConfig()
	if c.readOnlyProbeFailures != 7 {
		t.Errorf("readOnlyProbeFailures = %d, want 7", c.readOnlyProbeFailures)
	}
	if c.readOnlyProbeInterval != 12 {
		t.Errorf("readOnlyProbeInterval = %d, want 12", c.readOnlyProbeInterval)
	}
}

// Spec: §13.10 (F-13.2.1) — the --allow-public-bind escape hatch is sourced
// from PODIUM_ALLOW_PUBLIC_BIND and defaults off.
func TestLoadConfig_AllowPublicBind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_ALLOW_PUBLIC_BIND", "")
	if c := LoadConfig(); c.allowPublicBind {
		t.Errorf("allowPublicBind = true, want false by default")
	}
	t.Setenv("PODIUM_ALLOW_PUBLIC_BIND", "true")
	if c := LoadConfig(); !c.allowPublicBind {
		t.Errorf("allowPublicBind = false, want true when PODIUM_ALLOW_PUBLIC_BIND=true")
	}
}
