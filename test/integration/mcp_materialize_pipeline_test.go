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

// mcpResult decodes one tools/call response's result envelope.
type mcpResult struct {
	Result struct {
		ID             string   `json:"id"`
		Error          string   `json:"error"`
		Code           string   `json:"code"`
		MaterializedAt []string `json:"materialized_at"`
	} `json:"result"`
}

// Spec: §6.6 step 2 / §4.7.6 (F-6.6.2) — end to end through the real bridge:
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
	if !strings.Contains(resp.Result.Error, "materialize.content_hash_mismatch") {
		t.Errorf("error = %q, want materialize.content_hash_mismatch", resp.Result.Error)
	}
	if files := testharness.ReadTree(t, target); len(files) != 0 {
		t.Errorf("tampered artifact was materialized: %v", keysOf(files))
	}
}

// Spec: §6.6 step 1 / §13.11 (F-6.6.3) — end to end: the bridge fetches a
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
	if resp.Result.Error != "" {
		t.Fatalf("load failed: %s", resp.Result.Error)
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
