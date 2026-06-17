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
func TestPodiumMCP_LoadArtifactMaterializes(t *testing.T) {
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

	// The response contains materialized_at paths. The domain object lives
	// under structuredContent in the §6.1.1 CallToolResult envelope.
	var resp struct {
		Result struct {
			StructuredContent struct {
				ID             string   `json:"id"`
				MaterializedAt []string `json:"materialized_at"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstdout: %s", err, stdout.String())
	}
	sc := resp.Result.StructuredContent
	if sc.ID != "company-glossary" {
		t.Errorf("ID = %q, want company-glossary", sc.ID)
	}
	if len(sc.MaterializedAt) == 0 {
		t.Errorf("expected at least one materialized_at path")
	}

	// The artifact appears under the materialization target via the
	// none adapter's canonical pass-through layout.
	files := testharness.ReadTree(t, target)
	if _, ok := files["company-glossary/ARTIFACT.md"]; !ok {
		t.Errorf("expected company-glossary/ARTIFACT.md under target, got: %v", keysOf(files))
	}
}

// Spec: §4.3 target_harnesses — load_artifact through the MCP
// bridge does not write harness-native files when the artifact opts out
// of the active harness. The manifest content still returns; only the
// on-disk materialization is suppressed (materialized_at is empty and the
// target stays bare).
func TestPodiumMCP_TargetHarnessesSuppressesMaterialize(t *testing.T) {
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path: "tools/cc-only/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\n" +
				"description: claude-code only\n" +
				"target_harnesses: [claude-code]\n---\n\nbody\n",
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
			"name":      "load_artifact",
			"arguments": map[string]any{"id": "tools/cc-only"},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run mcp: %v\nstdout:\n%s", err, stdout.String())
	}

	var resp struct {
		Result struct {
			StructuredContent struct {
				ID             string   `json:"id"`
				MaterializedAt []string `json:"materialized_at"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstdout: %s", err, stdout.String())
	}
	sc := resp.Result.StructuredContent
	if sc.ID != "tools/cc-only" {
		t.Errorf("ID = %q, want tools/cc-only (load still succeeds)", sc.ID)
	}
	if len(sc.MaterializedAt) != 0 {
		t.Errorf("materialized_at = %v, want empty (artifact opted out of the none harness)", sc.MaterializedAt)
	}
	if files := testharness.ReadTree(t, target); len(files) != 0 {
		t.Errorf("target should be empty for an opted-out artifact, got: %v", keysOf(files))
	}
}

// Spec: §6.9 "Adapter cannot translate an artifact" —
// load_artifact under a harness whose §6.7.1 cell is ✗ for a field the
// artifact uses fails with a structured error naming the field and
// suggesting harness: none, and writes nothing to disk. Here a
// rule_mode: glob rule materializes under claude-desktop (✗ for glob).
func TestPodiumMCP_UntranslatableFieldFailsMaterialize(t *testing.T) {
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path: "style/glob-rule/ARTIFACT.md",
			Content: "---\ntype: rule\nversion: 1.0.0\n" +
				"description: glob rule\nrule_mode: glob\n" +
				"rule_globs: \"src/**/*.ts\"\n---\n\nrules\n",
		},
	)

	target := t.TempDir()

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_HARNESS=claude-desktop",
		"PODIUM_MATERIALIZE_ROOT="+target,
		"PODIUM_CACHE_DIR="+t.TempDir(),
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name":      "load_artifact",
			"arguments": map[string]any{"id": "style/glob-rule"},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run mcp: %v\nstdout:\n%s", err, stdout.String())
	}

	// The error envelope lives under structuredContent, and the CallToolResult
	// is flagged isError (§6.1.1) so the host marks the failure.
	var resp struct {
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Error string `json:"error"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstdout: %s", err, stdout.String())
	}
	if !resp.Result.IsError {
		t.Errorf("untranslatable load must set isError on the CallToolResult: %v", resp.Result)
	}
	if !strings.Contains(resp.Result.StructuredContent.Error, "materialize.untranslatable") ||
		!strings.Contains(resp.Result.StructuredContent.Error, "rule_mode: glob") {
		t.Errorf("error = %q, want a materialize.untranslatable error naming rule_mode: glob", resp.Result.StructuredContent.Error)
	}
	if files := testharness.ReadTree(t, target); len(files) != 0 {
		t.Errorf("an untranslatable load must write nothing, got: %v", keysOf(files))
	}
}

// Spec: §6.6 — selecting the claude-code adapter via PODIUM_HARNESS
// drives the corresponding harness-native layout.
func TestPodiumMCP_LoadArtifactWithClaudeCodeAdapter(t *testing.T) {
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
func TestPodiumMCP_PerCallHarnessOverride(t *testing.T) {
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
func TestPodiumMCP_LoadArtifactPopulatesContentCache(t *testing.T) {
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
func TestPodiumMCP_NoMaterializeRootReturnsManifestOnly(t *testing.T) {
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
