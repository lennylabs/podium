package e2e

// End-to-end tests for the §4.6 multi-layer composition and overlay journeys
// (TEST-GAPS §MULTILAYER: G-MULTILAYER-1 .. G-MULTILAYER-4).
//
// These exercise layer composition over a running server with two or more
// layers: a winner change driving re-sync cleanup, a hidden-parent extends
// merge under per-identity visibility, per-caller layer shadowing with pinned
// parents, and conflicting cross-layer DOMAIN.md featured/deprioritize lists
// fused with a workspace overlay.

import (
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- G-MULTILAYER-1 ---------------------------------------------------------

// G-MULTILAYER-1 — a winning-artifact change across two layers drives a re-sync
// that cleans the prior winner's outputs.
//
// Two local-source layers carry a shared context artifact ctx/notes: layer-base
// holds version 1.0.0 and layer-overlay holds version 2.0.0 declaring
// extends: ctx/notes (the §4.6 overlay that sanctions the cross-layer collision;
// without extends the second contribution is rejected at ingest). layer-overlay
// additionally contributes two artifacts the base does not: a standalone context
// ctx/extra and an mcp-server servers/warehouse that config-merges into .mcp.json.
//
// A first sync materializes the overlay's winner (ctx/notes v2 body, ctx/extra,
// and the servers/warehouse .mcp.json entry). Removing the overlay layer flips
// the effective view back to the base: ctx/notes resolves to 1.0.0, and
// ctx/extra and servers/warehouse leave the caller's view entirely. The re-sync
// must rewrite ctx/notes to the v1 body, delete the now-absent ctx/extra
// standalone file, strip the Podium servers/warehouse entry from .mcp.json, and
// preserve an operator-owned .mcp.json entry.
//
// Reconciliation note: the gap's literal trigger is `podium layer reorder`, but
// §4.7.6 latest-version resolution selects the winning version by recency across
// the caller's whole effective view, independent of layer precedence, so a
// reorder alone does not flip a shared-id winner. §4.6 names the spec-sanctioned
// way to replace rather than extend an artifact: "the lower-precedence layer
// must remove it first." This test drives that removal and additionally asserts
// `podium layer reorder` re-sequences the Order field (its §7.3.1 / §11
// contract) while the resolved version stays governed by §4.7.6.
//
// spec: §4.6 (cross-layer collision + extends overlay, replace-by-removal),
// §4.7.6 (latest resolution), §7.5 (server-source sync + stale cleanup), §6.7
// (config-merge reconcile), §7.3.1 / §11 (layer reorder/unregister).
func TestMultiLayer_WinnerChangeReSyncCleansPriorOutputs(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")

	base := newRepublishLayer(t, srv, "layer-base")
	overlay := newRepublishLayer(t, srv, "layer-overlay")

	// Publish the base first so the overlay's extends parent already exists when
	// the overlay ingests (the cross-layer overlay is sanctioned by extends).
	base.publishVersion(t, versionSpec{
		ID: "ctx/notes", Version: "1.0.0", Type: "context",
		Description: "BASE notes body marker",
	})
	overlay.publishVersion(t, versionSpec{
		ID: "ctx/notes", Version: "2.0.0", Type: "context",
		Description: "OVERLAY notes body marker", Extends: "ctx/notes@1.x",
	})
	// Two overlay-only artifacts: a standalone context and an mcp-server.
	overlay.writeArtifact(t, versionSpec{
		ID: "ctx/extra", Version: "1.0.0", Type: "context",
		Description: "overlay-only extra context",
	})
	mlWriteMCPServer(t, overlay.dir, "servers/warehouse", "warehouse-mcp", "npx:@acme/warehouse-mcp")
	mlReingest(t, srv, "layer-overlay")

	// The effective view resolves ctx/notes to the overlay's v2 (latest), and the
	// overlay-only artifacts are present.
	if st, got := overlay.loadVersion(t, "ctx/notes", ""); st != 200 || got.Version != "2.0.0" {
		t.Fatalf("pre-removal ctx/notes = HTTP %d version=%q, want 200 / 2.0.0", st, got.Version)
	}

	// `podium layer reorder` re-sequences Order (its documented contract); the
	// resolved latest version is unaffected (§4.7.6 governs version selection).
	cliWantExit(t, runPodium(t, "", brEnv(srv.BaseURL), "layer", "reorder", "layer-overlay", "layer-base"), 0, "reorder")
	ll := runPodium(t, "", brEnv(srv.BaseURL), "layer", "list")
	if cliLayerOrder(t, ll.Stdout, "layer-base") <= cliLayerOrder(t, ll.Stdout, "layer-overlay") {
		t.Errorf("after reorder overlay base: Order(layer-base) should exceed Order(layer-overlay):\n%s", ll.Stdout)
	}
	if st, got := overlay.loadVersion(t, "ctx/notes", ""); st != 200 || got.Version != "2.0.0" {
		t.Errorf("post-reorder ctx/notes = HTTP %d version=%q; reorder must not change §4.7.6 latest resolution", st, got.Version)
	}

	// First sync materializes the overlay's winner.
	target := t.TempDir()
	mlSyncServer(t, srv, target, "claude-code")

	notesPath := filepath.Join(target, ".podium", "context", "ctx", "notes", "ARTIFACT.md")
	extraPath := filepath.Join(target, ".podium", "context", "ctx", "extra", "ARTIFACT.md")
	mcpPath := filepath.Join(target, ".mcp.json")

	if body := readFile(t, notesPath); !strings.Contains(body, "OVERLAY notes body marker") {
		t.Fatalf("ctx/notes did not materialize the overlay (v2) body:\n%s", body)
	}
	mustExist(t, extraPath)
	servers := mlMCPServers(t, mcpPath)
	if _, ok := servers["warehouse-mcp"]; !ok {
		t.Fatalf(".mcp.json missing the Podium warehouse-mcp entry:\n%s", readFile(t, mcpPath))
	}

	// Operator adds their own entry to .mcp.json between syncs.
	mcpObj := mzReadJSON(t, mcpPath)
	mcpObj["mcpServers"].(map[string]any)["operator-db"] = map[string]any{"command": "operator-mcp"}
	mzWriteJSON(t, mcpPath, mcpObj)

	// Remove the overlay layer; the base becomes the effective winner for
	// ctx/notes and the overlay-only artifacts disappear from the view (§4.6
	// replace-by-removal, §11 unregister removes the layer's artifacts).
	cliWantExit(t, runPodium(t, "", brEnv(srv.BaseURL), "layer", "unregister", "layer-overlay"), 0, "unregister overlay")
	if st, got := base.loadVersion(t, "ctx/notes", ""); st != 200 || got.Version != "1.0.0" {
		t.Fatalf("post-removal ctx/notes = HTTP %d version=%q, want 200 / 1.0.0 (base wins)", st, got.Version)
	}
	if st, _ := base.loadVersion(t, "ctx/extra", ""); st != http.StatusNotFound {
		t.Fatalf("post-removal ctx/extra = HTTP %d, want 404 (overlay-only artifact gone)", st)
	}

	// Re-sync against the now-base-only view.
	mlSyncServer(t, srv, target, "claude-code")

	// The version flips: ctx/notes now carries the base (v1) body.
	notesAfter := readFile(t, notesPath)
	if !strings.Contains(notesAfter, "BASE notes body marker") {
		t.Errorf("ctx/notes did not flip to the base (v1) body after re-sync:\n%s", notesAfter)
	}
	if strings.Contains(notesAfter, "OVERLAY notes body marker") {
		t.Errorf("ctx/notes still carries the stale overlay (v2) body:\n%s", notesAfter)
	}
	// The prior winner's standalone file is deleted.
	mustNotExist(t, extraPath)
	// The prior Podium config-merge entry is stripped; the operator entry survives.
	after := mlMCPServers(t, mcpPath)
	if _, ok := after["warehouse-mcp"]; ok {
		t.Errorf(".mcp.json still carries the Podium warehouse-mcp entry after the overlay is gone:\n%s", readFile(t, mcpPath))
	}
	if _, ok := after["operator-db"]; !ok {
		t.Errorf(".mcp.json lost the operator operator-db entry across reconcile:\n%s", readFile(t, mcpPath))
	}
}

// mlWriteMCPServer writes an mcp-server ARTIFACT.md under dir at the slash id.
func mlWriteMCPServer(t *testing.T, dir, id, name, serverIdentifier string) {
	t.Helper()
	artDir := filepath.Join(dir, filepath.FromSlash(id))
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", artDir, err)
	}
	body := "---\ntype: mcp-server\nname: " + name + "\nversion: 1.0.0\ndescription: " + name +
		".\nserver_identifier: " + serverIdentifier + "\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(artDir, "ARTIFACT.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write mcp-server %s: %v", id, err)
	}
}

// mlReingest triggers a reingest of layerID against srv, failing on a non-zero
// exit. Flags precede the positional id (Go's flag parser stops at the first
// non-flag argument).
func mlReingest(t *testing.T, srv *serverProc, layerID string) {
	t.Helper()
	res := runPodium(t, "", nil, "layer", "reingest", "--registry", srv.BaseURL, layerID)
	if res.Exit != 0 {
		t.Fatalf("layer reingest %q exit=%d stderr=%s stdout=%s", layerID, res.Exit, res.Stderr, res.Stdout)
	}
}

// mlSyncServer runs a one-shot server-source sync, failing on a non-zero exit.
func mlSyncServer(t *testing.T, srv *serverProc, target, harness string) {
	t.Helper()
	res := runPodium(t, "", []string{"HOME=" + t.TempDir()},
		"sync", "--registry", srv.BaseURL, "--target", target, "--harness", harness)
	if res.Exit != 0 {
		t.Fatalf("server-source sync(%s) exit=%d stderr=%s", harness, res.Exit, res.Stderr)
	}
}

// mlMCPServers reads the mcpServers object from a .mcp.json file.
func mlMCPServers(t *testing.T, path string) map[string]any {
	t.Helper()
	obj := mzReadJSON(t, path)
	servers, ok := obj["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf(".mcp.json missing mcpServers object:\n%s", readFile(t, path))
	}
	return servers
}

// ---- shared injected-session-token visibility harness ----------------------

// mlVisLayer is one declarative layer in the visibility-capable server config:
// a local-source layer (its on-disk artifact tree) plus a §4.6 visibility block.
type mlVisLayer struct {
	id   string
	path string
	vis  string // a YAML `visibility:` block body, indented under the entry
}

// mlVisServer boots a standalone server in injected-session-token mode over a
// registry.yaml whose `layers:` list carries per-layer visibility blocks, then
// registers the runtime signing key so per-caller JWTs verify. Layers ingest at
// boot in list order (lowest precedence first). This is the visibility-capable
// harness the per-identity multi-layer journeys need: an anonymous caller is
// rejected before visibility, and each caller presents a signed token whose sub
// the §4.6 evaluator matches against users:/organization:/groups: filters.
func mlVisServer(t *testing.T, home string, layers []mlVisLayer, pemPath string) *serverProc {
	t.Helper()
	var b strings.Builder
	b.WriteString("registry:\n  layers:\n")
	for _, l := range layers {
		b.WriteString("    - id: " + l.id + "\n")
		b.WriteString("      source:\n        local:\n          path: " + l.path + "\n")
		b.WriteString("      visibility:\n" + l.vis)
	}
	cfgPath := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
	}, "serve", "--standalone")
	injRegisterRuntime(t, srv, pemPath)
	return srv
}

// mlToken mints a verified caller token for sub with optional group claims.
func mlToken(t *testing.T, priv *rsa.PrivateKey, sub string, groups ...string) string {
	t.Helper()
	claims := injClaims(sub)
	if len(groups) > 0 {
		gs := make([]any, len(groups))
		for i, g := range groups {
			gs[i] = g
		}
		claims["groups"] = gs
	}
	return injSignJWT(t, priv, claims)
}

// mlGetJSON issues an authenticated GET and decodes a JSON body, returning the
// status code. A non-2xx leaves dst zero-valued.
func mlGetJSON(t *testing.T, url, token string, dst any) int {
	t.Helper()
	st, body := injGet(t, url, token)
	if st == http.StatusOK && dst != nil {
		if err := json.Unmarshal(body, dst); err != nil {
			t.Fatalf("decode %s: %v\nbody: %s", url, err, body)
		}
	}
	return st
}
