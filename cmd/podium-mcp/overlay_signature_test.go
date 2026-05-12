package main

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
)

// overlayTestServer constructs an mcpServer with a configured
// verification policy and a fresh content cache.
func overlayTestServer(t *testing.T, policy sign.VerificationPolicy) *mcpServer {
	t.Helper()
	cache, _ := newContentCache(t.TempDir())
	return &mcpServer{
		cfg: &config{
			harness:           "none",
			verifyPolicy:      policy,
			signatureProvider: "noop",
		},
		cache:    cache,
		adapters: adapter.DefaultRegistry(),
	}
}

// Spec: §6.4 / §6.6 — the workspace overlay is the developer's
// own local files, not registry-issued bytes. §6.6 scopes the
// five-step materialization pipeline (including step 2 signature
// verification per PODIUM_VERIFY_SIGNATURES) to artifacts the
// registry returns. The overlay is a deliberate trust boundary:
// it bypasses signature verification because the developer wrote
// the bytes themselves and a registry chain-of-custody signature
// would be tautological.
//
// This test pins that boundary: an overlay load succeeds even
// under PolicyAlways. Any change to enforce signatures on overlay
// loads is a spec change, not a bug fix, and would break the
// "in-progress drafts" workflow §6.4 describes.
func TestLoadArtifactFromOverlay_PolicyAlwaysStillAllowsOverlay(t *testing.T) {
	t.Parallel()
	s := overlayTestServer(t, sign.PolicyAlways)
	rec := &filesystem.ArtifactRecord{
		ID: "personal/draft",
		ArtifactBytes: []byte(
			"---\ntype: context\nversion: 1.0.0\nsensitivity: low\n---\n"),
		Artifact: &manifest.Artifact{
			Type:        manifest.TypeContext,
			Version:     "1.0.0",
			Sensitivity: manifest.SensitivityLow,
		},
	}
	got := s.loadArtifactFromOverlay(rec, nil)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T (%v)", got, got)
	}
	if _, hasErr := m["error"]; hasErr {
		t.Errorf("overlay load returned %v under PolicyAlways; spec §6.6 scopes "+
			"signature verification to registry-returned bytes (§6.4 overlay is "+
			"the developer's own files, exempt by design)", m["error"])
	}
	if m["id"] != "personal/draft" {
		t.Errorf("id = %v", m["id"])
	}
}

// Spec: §4.7.9 — the registry-fetched path DOES enforce signature
// policy. A response with no signature under PolicyAlways must
// return an error result. This contrasts with the overlay path
// above and pins the asymmetry.
func TestDeliverLoadArtifact_PolicyAlwaysRejectsUnsigned(t *testing.T) {
	t.Parallel()
	s := overlayTestServer(t, sign.PolicyAlways)
	resp := loadArtifactResponse{
		ID:          "team/x",
		Type:        "context",
		Version:     "1.0.0",
		ContentHash: "sha256:" + strings.Repeat("a", 64),
		Frontmatter: "---\ntype: context\nversion: 1.0.0\nsensitivity: low\n---\n",
		Sensitivity: "low",
		Signature:   "", // missing
	}
	got := s.deliverLoadArtifact(resp)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T (%v)", got, got)
	}
	errStr, _ := m["error"].(string)
	if !strings.Contains(errStr, "materialize.signature_invalid") &&
		!strings.Contains(errStr, "signature_missing") {
		t.Errorf("error = %q, want materialize.signature_invalid or signature_missing", errStr)
	}
}

// Spec: §4.7.9 — PolicyMediumAndAbove (the default) requires a
// signature only for medium/high sensitivity. A low-sensitivity
// overlay load under the default policy succeeds — that's the
// "personal drafts work without signing keys" case.
func TestLoadArtifactFromOverlay_PolicyMediumAndAboveAllowsLowSensitivity(t *testing.T) {
	t.Parallel()
	s := overlayTestServer(t, sign.PolicyMediumAndAbove)
	rec := &filesystem.ArtifactRecord{
		ID: "personal/draft",
		ArtifactBytes: []byte(
			"---\ntype: context\nversion: 1.0.0\nsensitivity: low\n---\n"),
		Artifact: &manifest.Artifact{
			Type:        manifest.TypeContext,
			Version:     "1.0.0",
			Sensitivity: manifest.SensitivityLow,
		},
	}
	got := s.loadArtifactFromOverlay(rec, nil)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if _, has := m["error"]; has {
		t.Errorf("low-sensitivity overlay should not require a signature, got %v", m)
	}
}

// Spec: §6.4 / §6.6 — a high-sensitivity overlay artifact loads
// without signature verification even under PolicyMediumAndAbove.
// This is the same trust boundary as above: overlay bytes are the
// developer's own local files, exempt from the registry-issued
// signature regime. Sensitivity affects how the host treats the
// content (audit redaction, sandbox enforcement) but does not
// promote the overlay into the registry's chain of custody.
func TestLoadArtifactFromOverlay_HighSensitivityAllowedOnLocalAuthor(t *testing.T) {
	t.Parallel()
	s := overlayTestServer(t, sign.PolicyMediumAndAbove)
	rec := &filesystem.ArtifactRecord{
		ID: "personal/high",
		ArtifactBytes: []byte(
			"---\ntype: context\nversion: 1.0.0\nsensitivity: high\n---\n"),
		Artifact: &manifest.Artifact{
			Type:        manifest.TypeContext,
			Version:     "1.0.0",
			Sensitivity: manifest.SensitivityHigh,
		},
	}
	got := s.loadArtifactFromOverlay(rec, nil)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if _, has := m["error"]; has {
		t.Errorf("high-sensitivity overlay rejected under PolicyMediumAndAbove; "+
			"spec §6.6 scopes signature verification to registry-returned bytes. "+
			"Got: %v", m["error"])
	}
	if m["id"] != "personal/high" {
		t.Errorf("id = %v", m["id"])
	}
}

// Spec: §4.7.9 — PolicyNever exempts everything. Overlay loads
// under PolicyNever pass regardless of sensitivity.
func TestLoadArtifactFromOverlay_PolicyNeverAlwaysAllows(t *testing.T) {
	t.Parallel()
	s := overlayTestServer(t, sign.PolicyNever)
	rec := &filesystem.ArtifactRecord{
		ID:            "personal/x",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nsensitivity: high\n---\n"),
		Artifact: &manifest.Artifact{
			Type:        manifest.TypeSkill,
			Version:     "1.0.0",
			Sensitivity: manifest.SensitivityHigh,
		},
	}
	got := s.loadArtifactFromOverlay(rec, nil)
	m, ok := got.(map[string]any)
	if !ok || m["id"] != "personal/x" {
		t.Errorf("got %+v", got)
	}
}
