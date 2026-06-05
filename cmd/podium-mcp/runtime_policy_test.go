package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/materialize"
)

// runtimeResp builds a load_artifact response whose frontmatter carries
// the given runtime_requirements YAML lines (already indented under the
// runtime_requirements: key), or none when empty.
func runtimeResp(reqLines string) loadArtifactResponse {
	fm := "---\ntype: skill\nversion: 1.0.0\nname: x\ndescription: x\n"
	if reqLines != "" {
		fm += "runtime_requirements:\n" + reqLines
	}
	fm += "---\n"
	return loadArtifactResponse{
		ID:           "team/x",
		Type:         "skill",
		Version:      "1.0.0",
		Frontmatter:  fm,
		ManifestBody: "body",
	}
}

// Spec: §4.4.1 — a host that advertises no capabilities does not gate; it
// surfaces runtime_requirements without refusing.
func TestRuntimePolicy_InactiveWhenUnconfigured(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}}
	if err := srv.enforceRuntimePolicy(runtimeResp("  python: \">=3.11\"\n")); err != nil {
		t.Errorf("unconfigured host should not gate: %v", err)
	}
}

// Spec: §4.4.1 — once the host advertises a capability, an artifact it
// cannot satisfy is refused with materialize.runtime_unavailable.
func TestRuntimePolicy_RefusesUnsatisfiedPython(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostPython: "3.9.0"}}
	err := srv.enforceRuntimePolicy(runtimeResp("  python: \">=3.11\"\n"))
	if !errors.Is(err, materialize.ErrRuntimeUnavailable) {
		t.Errorf("err = %v, want ErrRuntimeUnavailable", err)
	}
	if err == nil || !strings.Contains(err.Error(), "python") {
		t.Errorf("err should name python: %v", err)
	}
}

// Spec: §4.4.1 — a satisfying host materializes.
func TestRuntimePolicy_AllowsSatisfiedPython(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostPython: "3.11.4"}}
	if err := srv.enforceRuntimePolicy(runtimeResp("  python: \">=3.11\"\n")); err != nil {
		t.Errorf("satisfying host refused: %v", err)
	}
}

// Spec: §4.4.1 — a system_packages requirement is checked against
// advertised packages once the host opts in.
func TestRuntimePolicy_RefusesMissingPackage(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostPackages: []string{"jq"}}}
	err := srv.enforceRuntimePolicy(runtimeResp("  system_packages: [jq, curl]\n"))
	if !errors.Is(err, materialize.ErrRuntimeUnavailable) || err == nil || !strings.Contains(err.Error(), "curl") {
		t.Errorf("err = %v, want refusal naming curl", err)
	}
}

// Spec: §4.4.1 — enforceRuntime forces the gate active even with no
// advertised capability, refusing any artifact that declares a requirement.
func TestRuntimePolicy_EnforceFlagFailsClosed(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{enforceRuntime: true}}
	if err := srv.enforceRuntimePolicy(runtimeResp("  python: \">=3.11\"\n")); !errors.Is(err, materialize.ErrRuntimeUnavailable) {
		t.Errorf("enforce flag should fail closed: %v", err)
	}
}

// Spec: §4.4.1 — ignoreRuntime bypasses the gate with a loud warning even
// when the host cannot satisfy the requirement.
func TestRuntimePolicy_IgnoreOverridesRefusal(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostPython: "3.9.0", ignoreRuntime: true}}
	if err := srv.enforceRuntimePolicy(runtimeResp("  python: \">=3.11\"\n")); err != nil {
		t.Errorf("ignoreRuntime should override: %v", err)
	}
}

// An artifact with no runtime_requirements is never gated, even on a host
// that advertises capabilities.
func TestRuntimePolicy_NoRequirementsAlwaysAllowed(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostPython: "3.9.0"}}
	if err := srv.enforceRuntimePolicy(runtimeResp("")); err != nil {
		t.Errorf("no requirements should not gate: %v", err)
	}
}

// sandboxProfileOf reports the declared profile and defaults to
// unrestricted; it backs the §4.4.1 seccomp baseline delivery decision.
func TestSandboxProfileOf(t *testing.T) {
	t.Parallel()
	if got := sandboxProfileOf("---\ntype: context\nversion: 1.0.0\nsandbox_profile: seccomp-strict\n---\n"); got != "seccomp-strict" {
		t.Errorf("got %q, want seccomp-strict", got)
	}
	if got := sandboxProfileOf("---\ntype: context\nversion: 1.0.0\n---\n"); got != "unrestricted" {
		t.Errorf("absent profile = %q, want unrestricted", got)
	}
}
