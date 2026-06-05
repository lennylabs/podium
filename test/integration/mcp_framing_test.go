package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// runMCPSession runs the real podium-mcp binary against a harness registry,
// feeds it the given stdin bytes, and returns stdout. The TEST owns the
// subprocess lifecycle: stdin is a fixed buffer (EOF ends the serve loop),
// and a context deadline guarantees teardown.
func runMCPSession(t *testing.T, registryURL string, stdin []byte) string {
	t.Helper()
	bin := buildMCP(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(cmd.Env, "PODIUM_REGISTRY="+registryURL)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}
	return stdout.String()
}

// Spec: §6.8 — the host drives the lifecycle handshake by sending a
// notifications/initialized notification after initialize, plus other
// id-less notifications. A notification must draw no response, in particular
// not a -32601 error frame that strict hosts treat as a protocol error.
// End-to-end through the real binary.
func TestPodiumMCP_NotificationDrawsNoResponse(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`
	notif := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	cancelled := `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`
	toolsReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	stdin := []byte(strings.Join([]string{initReq, notif, cancelled, toolsReq}, "\n") + "\n")

	out := runMCPSession(t, h.URL, stdin)

	if strings.Contains(out, "-32601") || strings.Contains(out, "method not found") {
		t.Errorf("a notification drew an error frame:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d response lines, want 2 (initialize + tools/list, notifications silent):\n%s", len(lines), out)
	}
	// Confirm the responses carry the request ids 1 and 2, not a notification.
	for i, want := range []float64{1, 2} {
		var resp struct {
			ID json.Number `json:"id"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &resp); err != nil {
			t.Fatalf("line %d not JSON: %v", i, err)
		}
		got, _ := resp.ID.Float64()
		if got != want {
			t.Errorf("line %d id = %v, want %v", i, got, want)
		}
	}
}

// Spec: §6.8 — the long-lived stdio subprocess must not be torn down by a
// single oversized inbound frame; that request fails with a structured error
// and the session keeps serving subsequent frames. End-to-end.
func TestPodiumMCP_OversizedFrameKeepsServing(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t)
	// A frame larger than the 16 MiB cap, followed by a valid tools/list.
	oversized := append(bytes.Repeat([]byte("x"), 16*1024*1024+64), '\n')
	toolsReq := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	stdin := append(oversized, toolsReq...)

	out := runMCPSession(t, h.URL, stdin)

	if !strings.Contains(out, "-32600") {
		t.Errorf("oversized frame must yield a -32600 error, got:\n%s", firstN(out, 400))
	}
	if !strings.Contains(out, "load_artifact") {
		t.Errorf("tools/list after the oversized frame was not served:\n%s", firstN(out, 400))
	}
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
