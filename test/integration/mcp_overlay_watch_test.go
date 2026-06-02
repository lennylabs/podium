package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// Spec: §6.4 / §6.4.1 / §14.7 — the MCP bridge watches the resolved overlay
// path via fsnotify and re-indexes on change, so an artifact added to the
// workspace overlay while the bridge is running becomes searchable without
// restarting the subprocess. The pre-fix bridge loaded the overlay once at
// startup and never refreshed it, so a draft created after launch stayed
// invisible. Findings F-6.4.3 / F-14.7.1 (watcher mechanism).
//
// The test owns the subprocess lifecycle: it drives stdin over a pipe with
// timed writes, bounds the run with a context deadline, and always closes
// stdin (EOF) so the serve loop exits.
func TestPodiumMCP_OverlayWatchReindexesOnChange(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t) // empty registry: matches come only from the overlay
	ws := t.TempDir()
	overlayDir := ws + "/overlay"
	// Seed one draft so the overlay exists at startup.
	testharness.WriteTree(t, overlayDir, testharness.WriteTreeOption{
		Path:    "alpha/ARTIFACT.md",
		Content: "---\ntype: context\nversion: 0.1.0\ndescription: alpha placeholder\n---\n\nalpha body\n",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bin := buildMCP(t)
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(),
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_CACHE_DIR="+t.TempDir(),
		"PODIUM_OVERLAY_PATH="+overlayDir,
	)
	stdinR, stdinW := io.Pipe()
	cmd.Stdin = stdinR
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = cmd.Wait()
	})

	write := func(v any) {
		b, _ := json.Marshal(v)
		if _, err := stdinW.Write(append(b, '\n')); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
	}

	// initialize starts the serve loop (and the overlay watcher).
	write(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05"}})
	// Let the serve loop start and the watcher capture its baseline.
	time.Sleep(300 * time.Millisecond)

	// Add a new draft after launch. It must be picked up by the watcher.
	testharness.WriteTree(t, overlayDir, testharness.WriteTreeOption{
		Path:    "beta/ARTIFACT.md",
		Content: "---\ntype: context\nversion: 0.1.0\ndescription: variance analysis helper\n---\n\nbeta body\n",
	})
	// Wait for the fsnotify event plus the re-index debounce.
	time.Sleep(2 * time.Second)

	write(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "search_artifacts",
			"arguments": map[string]any{"query": "variance", "top_k": float64(10)}}})
	time.Sleep(500 * time.Millisecond)
	_ = stdinW.Close()
	if err := cmd.Wait(); err != nil && ctx.Err() != nil {
		t.Fatalf("bridge timed out: %v", err)
	}

	if !strings.Contains(stdout.String(), "beta") {
		t.Errorf("overlay draft added at runtime not surfaced by search (watcher did not re-index):\n%s", stdout.String())
	}
}
