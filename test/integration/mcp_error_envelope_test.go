package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
)

// Spec: §6.10 — when the registry answers a meta-tool call
// with a structured error envelope, the MCP bridge binary surfaces the
// discrete code, details, retryable, and suggested_action fields in the
// tool-call result rather than collapsing them into an opaque string.
// Matrix: §6.10 (auth.untrusted_runtime)
func TestPodiumMCP_StructuredErrorEnvelopeReachesClient(t *testing.T) {
	t.Parallel()
	const envBody = `{"code":"auth.untrusted_runtime",` +
		`"message":"Runtime 'managed-runtime-x' is not registered with the registry.",` +
		`"details":{"runtime_iss":"managed-runtime-x"},` +
		`"retryable":false,` +
		`"suggested_action":"Register the runtime's signing key via 'podium admin runtime register'."}`
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(envBody))
	}))
	t.Cleanup(reg.Close)

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+reg.URL,
		"PODIUM_CACHE_DIR="+t.TempDir(),
	)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name":      "load_domain",
			"arguments": map[string]any{"path": "finance"},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}

	// The §6.10 error envelope is carried under structuredContent in the
	// §6.1.1 CallToolResult, with isError set on the envelope.
	var resp struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode: %v\nstdout: %s", err, stdout.String())
	}
	if !resp.Result.IsError {
		t.Errorf("error envelope must set isError on the CallToolResult: %+v", resp.Result)
	}
	r := resp.Result.StructuredContent
	if r == nil {
		t.Fatalf("nil result: %s", stdout.String())
	}
	if r["code"] != "auth.untrusted_runtime" {
		t.Errorf("code = %v, want auth.untrusted_runtime (result=%v)", r["code"], r)
	}
	details, ok := r["details"].(map[string]any)
	if !ok || details["runtime_iss"] != "managed-runtime-x" {
		t.Errorf("details = %v, want runtime_iss=managed-runtime-x", r["details"])
	}
	if _, ok := r["retryable"].(bool); !ok {
		t.Errorf("retryable missing or not a bool: %v", r["retryable"])
	}
	if s, _ := r["suggested_action"].(string); s == "" {
		t.Errorf("suggested_action empty, want the registry's remediation hint")
	}
}
