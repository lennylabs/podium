package integration

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// Spec: §6.6 Materialization — load_artifact through the MCP bridge
// runs the configured HarnessAdapter and writes adapter output
// atomically to PODIUM_MATERIALIZE_ROOT.
// Phase: 4
func TestPodiumMCP_LoadArtifactMaterializes(t *testing.T) {
	testharness.RequirePhase(t, 4)
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path: "company-glossary/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\n" +
				"description: Company glossary\n---\n\n" +
				"# Glossary\n\nbody\n",
		},
	)

	target := t.TempDir()
	cache := t.TempDir()

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT="+target,
		"PODIUM_CACHE_DIR="+cache,
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name": "load_artifact",
			"arguments": map[string]any{
				"id": "company-glossary",
			},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run mcp: %v\nstdout:\n%s", err, stdout.String())
	}

	// The response contains materialized_at paths.
	var resp struct {
		Result struct {
			ID             string   `json:"id"`
			MaterializedAt []string `json:"materialized_at"`
		} `json:"result"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstdout: %s", err, stdout.String())
	}
	if resp.Result.ID != "company-glossary" {
		t.Errorf("ID = %q, want company-glossary", resp.Result.ID)
	}
	if len(resp.Result.MaterializedAt) == 0 {
		t.Errorf("expected at least one materialized_at path")
	}

	// The artifact appears under the materialization target via the
	// none adapter's canonical pass-through layout.
	files := testharness.ReadTree(t, target)
	if _, ok := files["company-glossary/ARTIFACT.md"]; !ok {
		t.Errorf("expected company-glossary/ARTIFACT.md under target, got: %v", keysOf(files))
	}
}

// Spec: §6.6 — selecting the claude-code adapter via PODIUM_HARNESS
// drives the corresponding harness-native layout.
// Phase: 4
func TestPodiumMCP_LoadArtifactWithClaudeCodeAdapter(t *testing.T) {
	testharness.RequirePhase(t, 4)
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path: "ts-style/ARTIFACT.md",
			Content: "---\ntype: rule\nversion: 1.0.0\n" +
				"description: TypeScript style rules\n" +
				"rule_mode: always\n---\n\nrules\n",
		},
	)
	target := t.TempDir()

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_HARNESS=claude-code",
		"PODIUM_MATERIALIZE_ROOT="+target,
		"PODIUM_CACHE_DIR="+t.TempDir(),
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name":      "load_artifact",
			"arguments": map[string]any{"id": "ts-style"},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}
	files := testharness.ReadTree(t, target)
	if _, ok := files[".claude/rules/ts-style.md"]; !ok {
		t.Errorf("expected .claude/rules/ts-style.md, got: %v", keysOf(files))
	}
}

// Spec: §6.6 / §6.7 — per-call harness override on load_artifact
// switches the adapter without restarting the bridge.
// Phase: 4
func TestPodiumMCP_PerCallHarnessOverride(t *testing.T) {
	testharness.RequirePhase(t, 4)
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path: "ts-style/ARTIFACT.md",
			Content: "---\ntype: rule\nversion: 1.0.0\n" +
				"description: rules\n" +
				"rule_mode: always\n---\n\nrules\n",
		},
	)
	target := t.TempDir()

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT="+target,
		"PODIUM_CACHE_DIR="+t.TempDir(),
	)
	// Override harness=cursor on the per-call argument.
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name": "load_artifact",
			"arguments": map[string]any{
				"id":      "ts-style",
				"harness": "cursor",
			},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}
	files := testharness.ReadTree(t, target)
	if _, ok := files[".cursor/rules/ts-style.mdc"]; !ok {
		t.Errorf("expected .cursor/rules/ts-style.mdc (cursor override), got: %v", keysOf(files))
	}
}

// Spec: §6.5 — content_hash bytes land in the §6.5 content cache
// after load_artifact.
// Phase: 4
func TestPodiumMCP_LoadArtifactPopulatesContentCache(t *testing.T) {
	testharness.RequirePhase(t, 4)
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\n" +
				"description: x\n---\n\nbody\n",
		},
	)
	cache := t.TempDir()

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT="+t.TempDir(),
		"PODIUM_CACHE_DIR="+cache,
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name":      "load_artifact",
			"arguments": map[string]any{"id": "x"},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}

	// At least one bucket was created under the cache root.
	got := testharness.ReadTree(t, cache)
	hasFrontmatter := false
	for path := range got {
		if filepath.Base(path) == "frontmatter" {
			hasFrontmatter = true
			break
		}
	}
	if !hasFrontmatter {
		t.Errorf("expected a frontmatter entry in cache, got: %v", keysOf(got))
	}
}

// Spec: §6.6 — when PODIUM_MATERIALIZE_ROOT is unset, load_artifact
// returns the manifest body without materializing (read-only flow).
// Phase: 4
func TestPodiumMCP_NoMaterializeRootReturnsManifestOnly(t *testing.T) {
	testharness.RequirePhase(t, 4)
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path:    "x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\n---\n\nbody\n",
		},
	)
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_CACHE_DIR="+t.TempDir(),
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name":      "load_artifact",
			"arguments": map[string]any{"id": "x"},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"id":"x"`) {
		t.Errorf("expected response to carry id=x, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"materialized_at":[]`) {
		t.Errorf("expected materialized_at: [], got: %s", stdout.String())
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
