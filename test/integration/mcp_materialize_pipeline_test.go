package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/version"
)

// mcpResult decodes one tools/call response: the §6.1.1 CallToolResult
// envelope plus the meta-tool domain object it carries in structuredContent.
type mcpResult struct {
	Result struct {
		IsError           bool      `json:"isError"`
		StructuredContent mcpDomain `json:"structuredContent"`
	} `json:"result"`
}

// mcpDomain is the meta-tool domain object (the load_artifact result fields)
// carried in the CallToolResult's structuredContent.
type mcpDomain struct {
	ID             string   `json:"id"`
	Error          string   `json:"error"`
	Code           string   `json:"code"`
	MaterializedAt []string `json:"materialized_at"`
}

// Spec: §6.6 step 2 / §4.7.6 — end to end through the real bridge:
// a registry response whose frontmatter does not reproduce the served
// content_hash (a tamper or non-TLS MITM) is rejected with
// materialize.content_hash_mismatch and nothing is written to disk.
func TestPodiumMCP_ContentHashMismatchRejected(t *testing.T) {
	t.Parallel()
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/load_artifact" {
			// content_hash is inconsistent with the served frontmatter.
			_, _ = w.Write([]byte(`{"id":"finance/a","version":"1.0.0","type":"context",` +
				`"content_hash":"sha256:` + strings.Repeat("0", 64) + `",` +
				`"frontmatter":"---\ntype: context\n---\n"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(reg.Close)

	target := t.TempDir()
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+reg.URL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT="+target,
		"PODIUM_CACHE_DIR="+t.TempDir(),
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name": "load_artifact", "arguments": map[string]any{"id": "finance/a"}}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run mcp: %v\nstdout:\n%s", err, stdout.String())
	}
	var resp mcpResult
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstdout: %s", err, stdout.String())
	}
	if !resp.Result.IsError {
		t.Errorf("rejected load must set isError on the CallToolResult: %+v", resp.Result)
	}
	if !strings.Contains(resp.Result.StructuredContent.Error, "materialize.content_hash_mismatch") {
		t.Errorf("error = %q, want materialize.content_hash_mismatch", resp.Result.StructuredContent.Error)
	}
	if files := testharness.ReadTree(t, target); len(files) != 0 {
		t.Errorf("tampered artifact was materialized: %v", keysOf(files))
	}
}

// Spec: §6.6 step 2 / §4.7.6 — end to end: a skill's content_hash covers the
// verbatim SKILL.md the registry ships in skill_raw, so the bridge reproduces
// the hash over (ARTIFACT.md, SKILL.md) and a consistent skill materializes its
// SKILL.md verbatim instead of skipping the check.
func TestPodiumMCP_SkillContentHashVerifies(t *testing.T) {
	t.Parallel()
	fm := "---\ntype: skill\n---\n"
	skillRaw := "---\nname: demo\ndescription: a demo skill\n---\nskill prose\n"
	hash := "sha256:" + version.ContentHash([]byte(fm), []byte(skillRaw))
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/load_artifact" {
			b, _ := json.Marshal(map[string]any{
				"id": "team/demo", "version": "1.0.0", "type": "skill",
				"content_hash": hash, "frontmatter": fm,
				"skill_raw": skillRaw, "manifest_body": "skill prose\n",
			})
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(reg.Close)

	target := t.TempDir()
	resp := runMCPLoad(t, reg.URL, target, "team/demo")
	if resp.Result.StructuredContent.Error != "" {
		t.Fatalf("valid skill load failed: %s", resp.Result.StructuredContent.Error)
	}
	if files := testharness.ReadTree(t, target); files["team/demo/SKILL.md"] != skillRaw {
		t.Errorf("SKILL.md not materialized verbatim; tree: %v", keysOf(files))
	}
}

// Spec: §6.6 step 2 — end to end: a skill whose served SKILL.md was altered while
// content_hash was kept is rejected before any write, closing the path where a
// skill previously skipped the content-hash check entirely.
func TestPodiumMCP_SkillTamperedSkillRawRejected(t *testing.T) {
	t.Parallel()
	fm := "---\ntype: skill\n---\n"
	skillRaw := "---\nname: demo\ndescription: a demo skill\n---\nskill prose\n"
	hash := "sha256:" + version.ContentHash([]byte(fm), []byte(skillRaw))
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/load_artifact" {
			b, _ := json.Marshal(map[string]any{
				"id": "team/demo", "version": "1.0.0", "type": "skill",
				"content_hash": hash, "frontmatter": fm,
				// skill_raw altered after the hash was fixed.
				"skill_raw": skillRaw + "\ninjected", "manifest_body": "skill prose\n",
			})
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(reg.Close)

	target := t.TempDir()
	resp := runMCPLoad(t, reg.URL, target, "team/demo")
	if !strings.Contains(resp.Result.StructuredContent.Error, "materialize.content_hash_mismatch") {
		t.Errorf("error = %q, want materialize.content_hash_mismatch", resp.Result.StructuredContent.Error)
	}
	if files := testharness.ReadTree(t, target); len(files) != 0 {
		t.Errorf("tampered skill was materialized: %v", keysOf(files))
	}
}

// Spec: §6.6 step 2 / §4.6 — end to end: a merged manifest's served frontmatter
// is a re-serialization with the hidden parent stripped, so the bridge
// reproduces the hash from the leaf child's raw_frontmatter; a consistent merged
// manifest materializes its (merged) ARTIFACT.md.
func TestPodiumMCP_MergedManifestContentHashVerifies(t *testing.T) {
	t.Parallel()
	raw := "---\ntype: context\nextends: shared/parent@1.x\n---\nbody"
	served := "---\ntype: context\n---\nbody" // re-serialized, parent stripped
	hash := "sha256:" + version.ContentHash([]byte(raw))
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/load_artifact" {
			b, _ := json.Marshal(map[string]any{
				"id": "team/x", "version": "1.0.0", "type": "context",
				"content_hash": hash, "frontmatter": served,
				"manifest_merged": true, "raw_frontmatter": raw,
			})
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(reg.Close)

	target := t.TempDir()
	resp := runMCPLoad(t, reg.URL, target, "team/x")
	if resp.Result.StructuredContent.Error != "" {
		t.Fatalf("valid merged manifest load failed: %s", resp.Result.StructuredContent.Error)
	}
	if files := testharness.ReadTree(t, target); files["team/x/ARTIFACT.md"] != served {
		t.Errorf("merged ARTIFACT.md not materialized; tree: %v", keysOf(files))
	}
}

// runMCPLoad drives the real bridge subprocess through one load_artifact call
// against reg, materializing into target, and returns the decoded result. The
// test owns the subprocess lifecycle (bounded stdin, cmd.Run to completion).
func runMCPLoad(t *testing.T, regURL, target, id string) mcpResult {
	t.Helper()
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+regURL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT="+target,
		"PODIUM_CACHE_DIR="+t.TempDir(),
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name": "load_artifact", "arguments": map[string]any{"id": id}}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run mcp: %v\nstdout:\n%s", err, stdout.String())
	}
	var resp mcpResult
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstdout: %s", err, stdout.String())
	}
	return resp
}

// Spec: §6.6 step 1 / §13.11 — end to end: the bridge fetches a
// large_resource with the same session token it used for load_artifact, so an
// authenticated object route serves it; the decoded bytes materialize and the
// artifact-level content hash (which covers the fetched resource) verifies.
func TestPodiumMCP_LargeResourceFetchSendsToken(t *testing.T) {
	t.Parallel()
	blob := []byte(strings.Repeat("X", 4096))
	sum := sha256.Sum256(blob)
	resourceHash := "sha256:" + hex.EncodeToString(sum[:])

	var mu sync.Mutex
	var gotAuth string
	obj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(blob)
	}))
	t.Cleanup(obj.Close)

	fm := "---\ntype: context\n---\n"
	// Artifact-level hash over frontmatter + the (sorted) bundled resource,
	// matching the registry's canonicalization.
	artHash := "sha256:" + version.ContentHash([]byte(fm), nil, []byte("data/big.bin"), blob)
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/load_artifact" {
			b, _ := json.Marshal(map[string]any{
				"id": "finance/a", "version": "1.0.0", "type": "context",
				"content_hash": artHash, "frontmatter": fm,
				"large_resources": map[string]any{
					"data/big.bin": map[string]any{
						"presigned_url": obj.URL, "content_hash": resourceHash, "size": len(blob),
					},
				},
			})
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(reg.Close)

	tokFile := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tokFile, []byte("sektok"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	target := t.TempDir()
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+reg.URL,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT="+target,
		"PODIUM_CACHE_DIR="+t.TempDir(),
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_SESSION_TOKEN_FILE="+tokFile,
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name": "load_artifact", "arguments": map[string]any{"id": "finance/a"}}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run mcp: %v\nstdout:\n%s", err, stdout.String())
	}
	var resp mcpResult
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstdout: %s", err, stdout.String())
	}
	if resp.Result.StructuredContent.Error != "" {
		t.Fatalf("load failed: %s", resp.Result.StructuredContent.Error)
	}
	mu.Lock()
	auth := gotAuth
	mu.Unlock()
	if auth != "Bearer sektok" {
		t.Errorf("object route saw Authorization = %q, want Bearer sektok", auth)
	}
	files := testharness.ReadTree(t, target)
	if files["finance/a/data/big.bin"] != string(blob) {
		t.Errorf("large resource not materialized with decoded bytes; got tree: %v", keysOf(files))
	}
}
