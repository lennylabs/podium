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

// Spec: §4.4.1 — PODIUM_IGNORE_RUNTIME_REQUIREMENTS takes precedence over
// PODIUM_ENFORCE_RUNTIME_REQUIREMENTS: with both set, an unsatisfied
// requirement is admitted.
func TestRuntimePolicy_IgnoreWinsOverEnforce(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{enforceRuntime: true, ignoreRuntime: true}}
	if err := srv.enforceRuntimePolicy(runtimeResp("  python: \">=3.10\"\n")); err != nil {
		t.Errorf("ignore should win over enforce: %v", err)
	}
}

// Spec: §4.4.1 — frontmatter enforceRuntimePolicy cannot parse is refused
// with ErrRuntimeUnavailable even when the ignore flag is set, because the
// parse runs before the ignore branch.
//
// The test pins enforceRuntimePolicy's own branch only. deliverLoadArtifact
// runs the sandbox gate first, and that gate refuses unparseable frontmatter
// with materialize.sandbox_unsupported, so a client never receives
// materialize.runtime_unavailable for this input.
func TestRuntimePolicy_MalformedFrontmatterRefusedEvenWithIgnore(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{enforceRuntime: true, ignoreRuntime: true}}
	resp := runtimeResp("")
	resp.Frontmatter = "---\ntype: [skill\nversion: 1.0.0\n---\n"
	if err := srv.enforceRuntimePolicy(resp); !errors.Is(err, materialize.ErrRuntimeUnavailable) {
		t.Errorf("err = %v, want ErrRuntimeUnavailable for malformed frontmatter", err)
	}
}

// Spec: §4.4.1 / §6.2 — PODIUM_IGNORE_RUNTIME_REQUIREMENTS has no effect
// while the gate is inactive: the artifact is admitted because the gate does
// not run, and no bypass warning is written.
//
// The test does not call t.Parallel because captureStderr swaps the
// process-wide os.Stderr.
func TestRuntimePolicy_IgnoreNoEffectWhenUnconfigured(t *testing.T) {
	srv := &mcpServer{cfg: &config{ignoreRuntime: true}}
	var err error
	out := captureStderr(t, func() {
		err = srv.enforceRuntimePolicy(runtimeResp("  python: \">=3.11\"\n"))
	})
	if err != nil {
		t.Errorf("inactive gate should admit: %v", err)
	}
	if strings.Contains(out, "WARN:") {
		t.Errorf("inactive gate wrote a bypass warning: %q", out)
	}
}

// Spec: §6.2 — a whitespace-only PODIUM_HOST_PYTHON activates the gate and
// then fails every python requirement, because loadConfig copies the value
// verbatim and the version check trims it to empty.
func TestRuntimePolicy_WhitespaceHostPythonFailsRequirement(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostPython: "  "}}
	if err := srv.enforceRuntimePolicy(runtimeResp("  python: \">=3.10\"\n")); !errors.Is(err, materialize.ErrRuntimeUnavailable) {
		t.Errorf("err = %v, want ErrRuntimeUnavailable for whitespace host python", err)
	}
}
