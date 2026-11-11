package e2e

// End-to-end tests for the §4.6 multi-layer composition and overlay journeys.
//
// These exercise layer composition over a running server with two or more
// layers: a layer removal driving re-sync cleanup, and conflicting cross-layer
// DOMAIN.md featured/deprioritize lists fused with a workspace overlay.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ------------------------------------------------------------------------

// removing a layer drives a re-sync that cleans the removed layer's outputs.
//
// Two local-source layers contribute distinct artifacts: layer-base holds the
// context ctx/notes, and layer-overlay holds a standalone context ctx/extra and
// an mcp-server servers/warehouse that config-merges into .mcp.json. Two
// layers cannot contribute the same ID, because §4.6 rejects every same-ID
// collision between registry-side layers.
//
// A first sync materializes all three. Removing the overlay layer takes
// ctx/extra and servers/warehouse out of the caller's view. The re-sync must
// keep ctx/notes, delete the now-absent ctx/extra standalone file, strip the
// Podium servers/warehouse entry from .mcp.json, and preserve an
// operator-owned .mcp.json entry. The test also asserts that `podium layer
// reorder` re-sequences the Order field (its §7.3.1 / §11 contract).
//
// spec: §4.6 (layer composition, replace-by-removal), §7.5 (server-source sync
// + stale cleanup), §6.7 (config-merge reconcile), §7.3.1 / §11 (layer
// reorder/unregister).
func TestMultiLayer_LayerRemovalReSyncCleansPriorOutputs(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")

	base := newRepublishLayer(t, srv, "layer-base")
	overlay := newRepublishLayer(t, srv, "layer-overlay")

	base.publishVersion(t, versionSpec{
		ID: "ctx/notes", Version: "1.0.0", Type: "context",
		Description: "BASE notes body marker",
	})
	// Two overlay artifacts: a standalone context and an mcp-server.
	overlay.writeArtifact(t, versionSpec{
		ID: "ctx/extra", Version: "1.0.0", Type: "context",
		Description: "overlay-only extra context",
	})
	mlWriteMCPServer(t, overlay.dir, "servers/warehouse", "warehouse-mcp", "npx:@acme/warehouse-mcp")
	mlReingest(t, srv, "layer-overlay")

	// The effective view holds every artifact of both layers.
	if st, got := overlay.loadVersion(t, "ctx/extra", ""); st != 200 || got.Version != "1.0.0" {
		t.Fatalf("pre-removal ctx/extra = HTTP %d version=%q, want 200 / 1.0.0", st, got.Version)
	}

	// `podium layer reorder` re-sequences Order (its documented contract).
	cliWantExit(t, runPodium(t, "", brEnv(srv.BaseURL), "layer", "reorder", "layer-overlay", "layer-base"), 0, "reorder")
	ll := runPodium(t, "", brEnv(srv.BaseURL), "layer", "list")
	if cliLayerOrder(t, ll.Stdout, "layer-base") <= cliLayerOrder(t, ll.Stdout, "layer-overlay") {
		t.Errorf("after reorder overlay base: Order(layer-base) should exceed Order(layer-overlay):\n%s", ll.Stdout)
	}

	// First sync materializes both layers.
	target := t.TempDir()
	mlSyncServer(t, srv, target, "claude-code")

	notesPath := filepath.Join(target, ".podium", "context", "ctx", "notes", "ARTIFACT.md")
	extraPath := filepath.Join(target, ".podium", "context", "ctx", "extra", "ARTIFACT.md")
	mcpPath := filepath.Join(target, ".mcp.json")

	if body := readFile(t, notesPath); !strings.Contains(body, "BASE notes body marker") {
		t.Fatalf("ctx/notes did not materialize the base body:\n%s", body)
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

	// Remove the overlay layer; its artifacts disappear from the view (§4.6
	// replace-by-removal, §11 unregister removes the layer's artifacts).
	cliWantExit(t, runPodium(t, "", brEnv(srv.BaseURL), "layer", "unregister", "layer-overlay"), 0, "unregister overlay")
	if st, got := base.loadVersion(t, "ctx/notes", ""); st != 200 || got.Version != "1.0.0" {
		t.Fatalf("post-removal ctx/notes = HTTP %d version=%q, want 200 / 1.0.0", st, got.Version)
	}
	if st, _ := base.loadVersion(t, "ctx/extra", ""); st != http.StatusNotFound {
		t.Fatalf("post-removal ctx/extra = HTTP %d, want 404 (overlay-only artifact gone)", st)
	}

	// Re-sync against the now-base-only view.
	mlSyncServer(t, srv, target, "claude-code")

	// ctx/notes keeps the base body.
	if notesAfter := readFile(t, notesPath); !strings.Contains(notesAfter, "BASE notes body marker") {
		t.Errorf("ctx/notes lost the base body after re-sync:\n%s", notesAfter)
	}
	// The removed layer's standalone file is deleted.
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
	res := runPodium(t, "", append(srvSyncEnv(srv), "HOME="+t.TempDir()),
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

// ------------------------------------------------------------------------

// two layers contribute conflicting DOMAIN.md featured and
// deprioritize lists for one domain, and a workspace overlay adds an artifact
// surfaced by a distinctive keyword.
//
// A multi_layer filesystem registry stages layer-admin (lower precedence) and
// layer-user (higher) each with a finance/ap/DOMAIN.md. The admin layer features
// finance/ap/alpha and deprioritizes finance/ap/beta with notable_count 10; the
// user layer features finance/ap/gamma and deprioritizes finance/ap/delta with
// notable_count 4. The §4.5.4 cross-layer merge unions the lists append-unique
// (featured = [alpha, gamma], deprioritize = [beta, delta]) and takes the
// most-restrictive notable_count (4). load_domain therefore lists exactly four
// notable artifacts: both featured first (each tagged source: featured), then
// both deprioritized last. The most-restrictive folding is observable because
// the list is capped at the lower count (4), not the admin layer's 10.
//
// A workspace overlay adds finance/overlay-helper (a sibling domain so it does
// not perturb the finance/ap notable list) carrying a distinctive keyword in its
// description. Driven through the podium-mcp bridge, search_artifacts fuses the
// registry and overlay streams via RRF, so a query for the overlay keyword
// surfaces finance/overlay-helper flagged overlay: true.
//
// spec: §4.5.4 (cross-layer DOMAIN.md merge: featured/deprioritize append-unique,
// notable_count most-restrictive), §4.5.5 (notable selection ordering), §6.4.1
// (overlay search fusion via RRF).
func TestMultiLayer_ConflictingDomainListsMergeAndOverlaySearch(t *testing.T) {
	t.Parallel()
	reg := dmMultiLayer(t, map[string]string{
		// Four artifacts under finance/ap, two featured and two deprioritized
		// across the conflicting layers.
		"layer-admin/finance/ap/alpha/ARTIFACT.md": contextArtifact("alpha"),
		"layer-admin/finance/ap/beta/ARTIFACT.md":  contextArtifact("beta"),
		"layer-user/finance/ap/gamma/ARTIFACT.md":  contextArtifact("gamma"),
		"layer-user/finance/ap/delta/ARTIFACT.md":  contextArtifact("delta"),
		// Conflicting DOMAIN.md featured/deprioritize plus differing notable_count.
		"layer-admin/finance/ap/DOMAIN.md": "---\ndescription: AP admin\ndiscovery:\n" +
			"  notable_count: 10\n  featured:\n    - finance/ap/alpha\n  deprioritize:\n    - finance/ap/beta\n---\n\n# AP\n",
		"layer-user/finance/ap/DOMAIN.md": "---\ndescription: AP user\ndiscovery:\n" +
			"  notable_count: 4\n  featured:\n    - finance/ap/gamma\n  deprioritize:\n    - finance/ap/delta\n---\n\n# AP\n",
	})
	srv := startServer(t, reg)

	// load_domain merges the two layers' DOMAIN.md lists.
	var dom map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &dom)
	ids := dmNotableIDs(dom)

	// Most-restrictive folding: notable_count is the lower value (4), not 10.
	if len(ids) != 4 {
		t.Fatalf("notable has %d entries, want 4 (most-restrictive notable_count from the user layer): %v", len(ids), ids)
	}
	// Append-unique featured union: both featured artifacts appear, both tagged
	// source: featured, and both occupy the first two notable slots ahead of the
	// deprioritized tier.
	featuredSet := map[string]bool{"finance/ap/alpha": true, "finance/ap/gamma": true}
	if !featuredSet[ids[0]] || !featuredSet[ids[1]] || ids[0] == ids[1] {
		t.Errorf("first two notable = %v, want the union of both layers' featured {alpha, gamma}", ids[:2])
	}
	for _, id := range []string{"finance/ap/alpha", "finance/ap/gamma"} {
		entry := dmNotableEntry(dom, id)
		if entry == nil {
			t.Errorf("featured %s missing from notable: %v", id, ids)
			continue
		}
		if entry["source"] != "featured" {
			t.Errorf("notable %s source = %v, want featured", id, entry["source"])
		}
	}
	// Append-unique deprioritize union: both deprioritized artifacts are ranked
	// in the last two slots (after both featured).
	depriSet := map[string]bool{"finance/ap/beta": true, "finance/ap/delta": true}
	if !depriSet[ids[2]] || !depriSet[ids[3]] || ids[2] == ids[3] {
		t.Errorf("last two notable = %v, want the union of both layers' deprioritize {beta, delta} ranked last", ids[2:])
	}

	// A workspace overlay adds a sibling-domain artifact with a distinctive
	// keyword. Driven through the bridge, search_artifacts fuses it in.
	overlay := t.TempDir()
	writeOverlayFile(t, overlay, "finance/overlay-helper/ARTIFACT.md",
		"---\ntype: context\nversion: 0.1.0\ndescription: zircon reconciliation overlay helper\nsensitivity: low\n---\n\noverlay body\n")

	res := mcpExec(t, chMCPEnv(t, reg, "PODIUM_HARNESS=none", "PODIUM_OVERLAY_PATH="+overlay),
		toolCall(1, "search_artifacts", map[string]any{"query": "zircon"}))
	search := rpcResult(t, res.Stdout, 1)
	results, _ := search["results"].([]any)
	var found map[string]any
	for _, r := range results {
		if m, ok := r.(map[string]any); ok && m["id"] == "finance/overlay-helper" {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatalf("overlay artifact finance/overlay-helper did not surface in fused search for its keyword:\n%s", res.Stdout)
	}
	if found["overlay"] != true {
		t.Errorf("fused overlay hit missing overlay: true flag: %v", found)
	}
}
