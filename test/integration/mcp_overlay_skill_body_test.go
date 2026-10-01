package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// Spec: §4.3.4 / §4.4 / §6.4 — a skill's manifest body is its SKILL.md prose,
// and the workspace overlay uses the registry's format, so load_artifact on an
// overlay skill returns the same manifest_body the registry returns for the
// same package. Before the fix, the overlay path returned the ARTIFACT.md
// pointer comment in place of the SKILL.md body.
func TestPodiumMCP_OverlaySkillLoadReturnsSkillBody(t *testing.T) {
	t.Parallel()
	const (
		artifactMD = "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
		skillBody  = "Run the variance analysis.\n"
	)
	skillMD := func(name string) string {
		return "---\nname: " + name + "\ndescription: Variance analysis skill\n---\n\n" + skillBody
	}
	h := registryharness.New(t,
		testharness.WriteTreeOption{Path: "team/reg-skill/ARTIFACT.md", Content: artifactMD},
		testharness.WriteTreeOption{Path: "team/reg-skill/SKILL.md", Content: skillMD("reg-skill")},
	)
	overlayDir := t.TempDir()
	testharness.WriteTree(t, overlayDir,
		testharness.WriteTreeOption{Path: "drafts/local-skill/ARTIFACT.md", Content: artifactMD},
		testharness.WriteTreeOption{Path: "drafts/local-skill/SKILL.md", Content: skillMD("local-skill")},
	)

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"PODIUM_VERIFY_SIGNATURES=never",
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_HARNESS=none",
		"PODIUM_CACHE_DIR="+t.TempDir(),
		"PODIUM_OVERLAY_PATH="+overlayDir,
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "initialize", ID: 1, Params: map[string]any{"protocolVersion": "2024-11-05"}},
		{Method: "tools/call", ID: 2, Params: map[string]any{
			"name": "load_artifact", "arguments": map[string]any{"id": "team/reg-skill"}}},
		{Method: "tools/call", ID: 3, Params: map[string]any{
			"name": "load_artifact", "arguments": map[string]any{"id": "drafts/local-skill"}}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}

	registry := decodeLoadArtifactResult(t, stdout.String(), 2)
	overlay := decodeLoadArtifactResult(t, stdout.String(), 3)
	if registry.Error != "" || overlay.Error != "" {
		t.Fatalf("load failed: registry=%q overlay=%q", registry.Error, overlay.Error)
	}
	if overlay.Layer != "overlay" {
		t.Fatalf("drafts/local-skill layer = %q, want overlay", overlay.Layer)
	}
	if strings.TrimSpace(registry.ManifestBody) != strings.TrimSpace(skillBody) {
		t.Errorf("registry manifest_body = %q, want the SKILL.md body %q", registry.ManifestBody, skillBody)
	}
	if overlay.ManifestBody != registry.ManifestBody {
		t.Errorf("overlay manifest_body = %q, want the registry's SKILL.md body %q", overlay.ManifestBody, registry.ManifestBody)
	}
}

// loadArtifactResult is the load_artifact domain object carried in the
// §6.1.1 CallToolResult's structuredContent.
type loadArtifactResult struct {
	Error        string `json:"error"`
	Layer        string `json:"layer"`
	ManifestBody string `json:"manifest_body"`
}

// decodeLoadArtifactResult finds the JSON-RPC response with the given id in
// the newline-delimited bridge stdout and returns its load_artifact result.
func decodeLoadArtifactResult(t *testing.T, stdout string, id int) loadArtifactResult {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var env struct {
			ID     int `json:"id"`
			Result struct {
				StructuredContent loadArtifactResult `json:"structuredContent"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &env) != nil || env.ID != id {
			continue
		}
		return env.Result.StructuredContent
	}
	t.Fatalf("no load_artifact response with id=%d in:\n%s", id, stdout)
	return loadArtifactResult{}
}
