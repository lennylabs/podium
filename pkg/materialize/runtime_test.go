package materialize

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.4.1 / §6.10 — when the host cannot satisfy a manifest's
// runtime_requirements, materialize fails with
// materialize.runtime_unavailable.
// Phase: 0
// Matrix: §6.10 (materialize.runtime_unavailable)
func TestCheckRuntimeRequirements_PythonMinVersion(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	req := map[string]any{"python": ">=3.10"}

	// Host has 3.11 → satisfies.
	if err := CheckRuntimeRequirements(req, HostCapabilities{Python: "3.11.4"}); err != nil {
		t.Errorf("3.11 should satisfy >=3.10: %v", err)
	}
	// Host has 3.9 → does not satisfy.
	err := CheckRuntimeRequirements(req, HostCapabilities{Python: "3.9.0"})
	if !errors.Is(err, ErrRuntimeUnavailable) {
		t.Errorf("3.9 should not satisfy >=3.10: %v", err)
	}
	// Host has none → does not satisfy.
	err = CheckRuntimeRequirements(req, HostCapabilities{})
	if !errors.Is(err, ErrRuntimeUnavailable) {
		t.Errorf("missing python should not satisfy: %v", err)
	}
}

// Spec: §4.4.1 — system_packages requirements check the host's
// advertised packages.
// Phase: 0
func TestCheckRuntimeRequirements_SystemPackages(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	req := map[string]any{"system_packages": []string{"jq", "curl"}}
	host := HostCapabilities{SystemPackages: []string{"jq", "curl", "ripgrep"}}
	if err := CheckRuntimeRequirements(req, host); err != nil {
		t.Errorf("expected satisfaction: %v", err)
	}
	host = HostCapabilities{SystemPackages: []string{"jq"}}
	err := CheckRuntimeRequirements(req, host)
	if !errors.Is(err, ErrRuntimeUnavailable) {
		t.Errorf("missing curl should fail: %v", err)
	}
}

// Spec: §4.4.1 — empty requirements are always satisfied.
// Phase: 0
func TestCheckRuntimeRequirements_EmptyAlwaysSatisfied(t *testing.T) {
	testharness.RequirePhase(t, 0)
	t.Parallel()
	if err := CheckRuntimeRequirements(nil, HostCapabilities{}); err != nil {
		t.Errorf("empty req: %v", err)
	}
	if err := CheckRuntimeRequirements(map[string]any{}, HostCapabilities{}); err != nil {
		t.Errorf("empty map req: %v", err)
	}
}
