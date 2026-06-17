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

// Spec: §4.5.2 / §4.5.4 / §6.4 — the MCP bridge applies the
// workspace-overlay DOMAIN.md merge to load_domain client-side. A
// workspace-local DOMAIN.md include: resolves over the merged view (registry
// catalog ∪ overlay), so this exercises the new GET /v1/catalog endpoint
// through the real in-process registry server and the real bridge binary: an
// overlay `drafts` domain that imports `finance/ap/*` surfaces both the
// registry artifact (via the catalog) and the overlay artifact in notable.
func TestPodiumMCP_LoadDomainOverlayIncludeMergedView(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		// Registry-side artifact the local include: must pull in via the catalog.
		testharness.WriteTreeOption{
			Path:    "finance/ap/registry-pay/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: registry pay\n---\n\nbody\n",
		},
	)
	ws := t.TempDir()
	// A workspace-local DOMAIN.md at drafts/ imports finance/ap/*; the registry
	// never sees it, so the bridge resolves it over the merged view.
	testharness.WriteTree(t, ws,
		testharness.WriteTreeOption{
			Path:    ".podium/overlay/drafts/DOMAIN.md",
			Content: "---\ndescription: Local drafts\ninclude:\n  - finance/ap/*\n---\n",
		},
		testharness.WriteTreeOption{
			Path:    ".podium/overlay/finance/ap/overlay-pay/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 0.1.0\ndescription: overlay pay\n---\n\nbody\n",
		},
	)

	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"PODIUM_REGISTRY="+h.URL,
		"PODIUM_CACHE_DIR="+t.TempDir(),
		"PODIUM_OVERLAY_PATH="+ws+"/.podium/overlay",
	)
	initLine, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}},
	})
	callLine, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "load_domain", "arguments": map[string]any{"path": "drafts"}},
	})
	cmd.Stdin = bytes.NewReader(bytes.Join([][]byte{initLine, callLine}, []byte("\n")))

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstdout: %s", err, stdout.String())
	}
	out := decodeLoadDomainNotableIDs(t, stdout.String(), 2)
	if !out["finance/ap/registry-pay"] {
		t.Errorf("merged-view include missed the registry artifact (catalog round-trip): %v", out)
	}
	if !out["finance/ap/overlay-pay"] {
		t.Errorf("merged-view include missed the overlay artifact: %v", out)
	}
}

// decodeLoadDomainNotableIDs finds the JSON-RPC response with the given id in
// the newline-delimited bridge stdout and returns the set of notable ids.
func decodeLoadDomainNotableIDs(t *testing.T, stdout string, id int) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// load_domain's domain object lives under structuredContent in the
		// §6.1.1 CallToolResult envelope.
		var env struct {
			ID     int `json:"id"`
			Result struct {
				StructuredContent struct {
					Notable []struct {
						ID string `json:"id"`
					} `json:"notable"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &env) != nil || env.ID != id {
			continue
		}
		for _, n := range env.Result.StructuredContent.Notable {
			ids[n.ID] = true
		}
		return ids
	}
	t.Fatalf("no load_domain response with id=%d in:\n%s", id, stdout)
	return ids
}
