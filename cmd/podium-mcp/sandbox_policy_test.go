package main

import (
	"strings"
	"testing"
)

func sandboxResp(profile string) loadArtifactResponse {
	fm := "---\ntype: skill\nversion: 1.0.0\nname: x\ndescription: x\n"
	if profile != "" {
		fm += "sandbox_profile: " + profile + "\n"
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

// Spec: §4.4.1 — `unrestricted` is always accepted; the host
// doesn't need to declare it.
func TestSandboxPolicy_UnrestrictedAlwaysAccepted(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostSandboxes: nil}}
	if err := srv.enforceSandboxPolicy(sandboxResp("unrestricted")); err != nil {
		t.Errorf("unrestricted refused: %v", err)
	}
}

// Spec: §4.4.1 — when sandbox_profile is omitted, the artifact
// counts as unrestricted (default).
func TestSandboxPolicy_OmittedDefaultsToUnrestricted(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostSandboxes: nil}}
	if err := srv.enforceSandboxPolicy(sandboxResp("")); err != nil {
		t.Errorf("missing profile refused: %v", err)
	}
}

// Spec: §4.4.1 — hosts without the declared sandbox profile MUST
// refuse to materialize an artifact whose profile is non-
// unrestricted, unless the operator explicitly ignores the
// constraint.
func TestSandboxPolicy_RefusesUnsupportedProfile(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostSandboxes: []string{"unrestricted"}}}
	err := srv.enforceSandboxPolicy(sandboxResp("seccomp-strict"))
	if err == nil {
		t.Errorf("err = nil, want refusal")
	}
	if err != nil && !strings.Contains(err.Error(), "seccomp-strict") {
		t.Errorf("err = %v, want it to name the requested profile", err)
	}
}

// Spec: §4.4.1 — hosts that do support the profile materialize
// it without warning.
func TestSandboxPolicy_AllowsSupportedProfile(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{hostSandboxes: []string{"unrestricted", "read-only-fs"}}}
	if err := srv.enforceSandboxPolicy(sandboxResp("read-only-fs")); err != nil {
		t.Errorf("supported profile refused: %v", err)
	}
}

// Spec: §4.4.1 — when ignoreSandbox is true, the policy allows
// non-unrestricted profiles even on a host that doesn't list them.
// Operators see the override loudly via the audit-sink + log path
// (covered in cmd/podium-mcp main wiring).
func TestSandboxPolicy_IgnoreSandboxOverridesRefusal(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{
		hostSandboxes: []string{"unrestricted"},
		ignoreSandbox: true,
	}}
	if err := srv.enforceSandboxPolicy(sandboxResp("seccomp-strict")); err != nil {
		t.Errorf("ignoreSandbox should override: %v", err)
	}
}
