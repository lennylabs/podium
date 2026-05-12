package main

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// enforceSignaturePolicy bubbles up provider construction errors.
func TestEnforceSignaturePolicy_BadProviderConfig(t *testing.T) {
	t.Parallel()
	s := &mcpServer{cfg: &config{
		signatureProvider: "bogus",
		verifyPolicy:      sign.PolicyAlways,
	}}
	if err := s.enforceSignaturePolicy(loadArtifactResponse{}); err == nil {
		t.Errorf("expected provider error")
	}
}

// enforceSignaturePolicy passes when the policy is never.
func TestEnforceSignaturePolicy_PolicyNeverSucceeds(t *testing.T) {
	t.Parallel()
	s := &mcpServer{cfg: &config{
		signatureProvider: "noop",
		verifyPolicy:      sign.PolicyNever,
	}}
	if err := s.enforceSignaturePolicy(loadArtifactResponse{}); err != nil {
		t.Errorf("err = %v", err)
	}
}

// enforceSandboxPolicy with malformed frontmatter refuses (fail-closed).
func TestEnforceSandboxPolicy_MalformedFrontmatter(t *testing.T) {
	t.Parallel()
	s := &mcpServer{cfg: &config{enforceSandbox: true}}
	err := s.enforceSandboxPolicy(loadArtifactResponse{Frontmatter: "not yaml"})
	if err == nil {
		t.Errorf("expected error for malformed frontmatter")
	}
}

// enforceSandboxPolicy allows unrestricted profiles.
func TestEnforceSandboxPolicy_UnrestrictedAllowed(t *testing.T) {
	t.Parallel()
	s := &mcpServer{cfg: &config{}}
	fm := "---\ntype: skill\nversion: 1.0.0\nsandbox_profile: unrestricted\n---\n"
	if err := s.enforceSandboxPolicy(loadArtifactResponse{Frontmatter: fm}); err != nil {
		t.Errorf("err = %v", err)
	}
}

// enforceSandboxPolicy refuses when host doesn't support the named profile.
func TestEnforceSandboxPolicy_UnsupportedProfileRefused(t *testing.T) {
	t.Parallel()
	s := &mcpServer{cfg: &config{hostSandboxes: []string{"unrestricted"}}}
	fm := "---\ntype: skill\nversion: 1.0.0\nsandbox_profile: process\n---\n"
	err := s.enforceSandboxPolicy(loadArtifactResponse{Frontmatter: fm})
	if err == nil || !strings.Contains(err.Error(), "sandbox_profile") {
		t.Errorf("err = %v", err)
	}
}

// enforceSandboxPolicy with ignoreSandbox set bypasses with a warning.
func TestEnforceSandboxPolicy_IgnoreSandboxBypasses(t *testing.T) {
	t.Parallel()
	s := &mcpServer{cfg: &config{
		hostSandboxes: []string{"unrestricted"},
		ignoreSandbox: true,
	}}
	fm := "---\ntype: skill\nversion: 1.0.0\nsandbox_profile: process\n---\n"
	if err := s.enforceSandboxPolicy(loadArtifactResponse{Frontmatter: fm, ID: "x"}); err != nil {
		t.Errorf("ignoreSandbox should suppress; err = %v", err)
	}
}

// enforceSandboxPolicy allows when host supports the profile.
func TestEnforceSandboxPolicy_HostSupportsProfile(t *testing.T) {
	t.Parallel()
	s := &mcpServer{cfg: &config{
		hostSandboxes: []string{"unrestricted", "process"},
	}}
	fm := "---\ntype: skill\nversion: 1.0.0\nsandbox_profile: process\n---\n"
	if err := s.enforceSandboxPolicy(loadArtifactResponse{Frontmatter: fm}); err != nil {
		t.Errorf("err = %v", err)
	}
}

// sanitizeHash replaces unsafe filename characters.
func TestSanitizeHash(t *testing.T) {
	t.Parallel()
	if got := sanitizeHash("sha256:abc"); got != "sha256-abc" {
		t.Errorf("got %q", got)
	}
	// Empty input produces an opaque safe key, not empty.
	if got := sanitizeHash(""); got == "" {
		t.Errorf("got empty string for empty input; expected safe placeholder")
	}
}

// newContentCache returns a disabled cache on empty path.
func TestNewContentCache_EmptyPathDisabled(t *testing.T) {
	t.Parallel()
	c, err := newContentCache("")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if c == nil {
		t.Errorf("nil cache")
	}
	if c.has("sha256:x") {
		t.Errorf("disabled cache reports has=true")
	}
	// put on a disabled cache is a no-op.
	if err := c.put("sha256:x", "fm", "body", nil); err != nil {
		t.Errorf("put on disabled cache: %v", err)
	}
}

// jsonAny returns an errorResult for malformed JSON.
func TestJSONAny_MalformedReturnsError(t *testing.T) {
	t.Parallel()
	got := jsonAny([]byte("not json"))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if _, has := m["error"]; !has {
		t.Errorf("expected error key in %v", m)
	}
}
