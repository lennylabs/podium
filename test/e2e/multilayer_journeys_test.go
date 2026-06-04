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

// ---- G-MULTILAYER-2 ---------------------------------------------------------

// G-MULTILAYER-2 — a child extends a parent in a layer the caller cannot see:
// the registry resolves and merges the parent server-side and serves the merged
// manifest, but the parent's ID stays unloadable and unenumerable for that
// caller (§4.6 hidden parents).
//
// Layer secret-parent (lowest precedence) holds shared/parent restricted to
// users: [alice@acme.com] with a distinctive tag and high sensitivity. Layer
// public-child (higher precedence) holds finance/child, public, declaring
// extends: shared/parent@1.x with low sensitivity. bob is a verified caller who
// is not alice, so the parent's layer is invisible to him. Loading finance/child
// as bob returns the merged manifest: the parent's inherited tag is folded in
// and the sensitivity is the most-restrictive (high, the parent's). The parent
// shared/parent is not loadable as bob (404) and never appears in his
// search_artifacts or load_domain enumeration.
//
// Reconciliation note: the gap names an "organization-visibility" parent and an
// "unauthenticated public-mode" caller, but §4.6 makes an organization layer
// visible to every authenticated caller and §13.10 public mode bypasses
// visibility entirely, so neither hides the parent from a caller who can reach
// the public child. The standalone harness expresses "a caller who cannot
// discover the parent" with a users:-restricted parent and a verified non-owner
// caller (the same construction as the registry-core TestExtends_HiddenParent),
// which is the realizable per-identity hiding the gap intends.
//
// spec: §4.6 (hidden parents, extends merge, most-restrictive sensitivity),
// §6.3.2 (injected-session-token verification), §5 (search_artifacts visibility),
// §4.5 (load_domain visibility).
func TestMultiLayer_HiddenParentMergedButUndiscoverable(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	parentRoot := writeRegistry(t, map[string]string{
		"shared/parent/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\n" +
			"description: hidden parent\ntags: [from-hidden-parent]\nsensitivity: high\n---\n\nparent body\n",
	})
	childRoot := writeRegistry(t, map[string]string{
		"finance/child/ARTIFACT.md": "---\ntype: context\nversion: 2.0.0\n" +
			"description: visible child\ntags: [child-tag]\nsensitivity: low\n" +
			"extends: shared/parent@1.x\n---\n\nchild body\n",
	})

	srv := mlVisServer(t, home, []mlVisLayer{
		{id: "secret-parent", path: parentRoot, vis: "        users: [alice@acme.com]\n"},
		{id: "public-child", path: childRoot, vis: "        public: true\n"},
	}, pemPath)

	bob := mlToken(t, priv, "bob@acme.com")

	// bob loads the public child: the hidden parent is merged server-side.
	var child exLoadResp
	if st := mlGetJSON(t, srv.BaseURL+"/v1/load_artifact?id=finance/child", bob, &child); st != http.StatusOK {
		t.Fatalf("bob load finance/child = HTTP %d, want 200\nlog:\n%s", st, srv.log())
	}
	if child.Version != "2.0.0" {
		t.Errorf("merged child version = %q, want 2.0.0", child.Version)
	}
	if !strings.Contains(child.Frontmatter, "from-hidden-parent") {
		t.Errorf("merged manifest missing the hidden parent's inherited tag:\n%s", child.Frontmatter)
	}
	if !strings.Contains(child.Frontmatter, "child-tag") {
		t.Errorf("merged manifest missing the child's own tag:\n%s", child.Frontmatter)
	}
	// Most-restrictive sensitivity: the child declared low, but the parent's high
	// wins (the child cannot relax the parent).
	if child.Sensitivity != "high" {
		t.Errorf("merged sensitivity = %q, want high (most-restrictive from the hidden parent)", child.Sensitivity)
	}

	// The parent itself is not loadable as bob.
	if st, _ := injGet(t, srv.BaseURL+"/v1/load_artifact?id=shared/parent", bob); st != http.StatusNotFound {
		t.Errorf("bob load shared/parent = HTTP %d, want 404 (hidden parent not loadable)", st)
	}

	// The parent never appears in bob's search_artifacts.
	var search struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if st := mlGetJSON(t, srv.BaseURL+"/v1/search_artifacts?query=parent", bob, &search); st != http.StatusOK {
		t.Fatalf("bob search_artifacts = HTTP %d, want 200", st)
	}
	for _, r := range search.Results {
		if r.ID == "shared/parent" {
			t.Errorf("hidden parent shared/parent leaked through bob's search_artifacts: %+v", search.Results)
		}
	}

	// The parent never appears in bob's load_domain enumeration of `shared`.
	var dom struct {
		Notable []struct {
			ID string `json:"id"`
		} `json:"notable"`
	}
	// load_domain for the parent's domain is itself invisible to bob (the only
	// artifact under `shared` is the hidden parent), so it is either 404 or an
	// empty notable set; in neither case may shared/parent appear.
	st := mlGetJSON(t, srv.BaseURL+"/v1/load_domain?path=shared", bob, &dom)
	if st == http.StatusOK {
		for _, n := range dom.Notable {
			if n.ID == "shared/parent" {
				t.Errorf("hidden parent shared/parent leaked through bob's load_domain: %+v", dom.Notable)
			}
		}
	}

	// Control: alice (the parent layer's sole grantee) can load the parent
	// directly, proving the parent exists and the hiding is per-identity.
	alice := mlToken(t, priv, "alice@acme.com")
	if st, _ := injGet(t, srv.BaseURL+"/v1/load_artifact?id=shared/parent", alice); st != http.StatusOK {
		t.Errorf("alice load shared/parent = HTTP %d, want 200 (grantee can see the parent)", st)
	}
}

// ---- G-MULTILAYER-3 ---------------------------------------------------------

// mlWriteArtifact writes a single-file context ARTIFACT.md under dir at the
// slash id with the given version, a distinctive tag, and an optional extends
// pin. The journey under test (per-caller visibility-filtered latest selection
// plus pinned-parent stability) is type-agnostic; context keeps the fixture to
// one file per version with no SKILL.md plumbing while still carrying the tag
// the merge folds.
func mlWriteArtifact(t *testing.T, dir, id, version, tag, extends string) {
	t.Helper()
	artDir := filepath.Join(dir, filepath.FromSlash(id))
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", artDir, err)
	}
	var b strings.Builder
	b.WriteString("---\ntype: context\nversion: " + version + "\ndescription: runbook " + version + "\n")
	b.WriteString("tags: [" + tag + "]\n")
	if extends != "" {
		b.WriteString("extends: " + extends + "\n")
	}
	b.WriteString("---\n\nrunbook body " + version + "\n")
	if err := os.WriteFile(filepath.Join(artDir, "ARTIFACT.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write artifact %s@%s: %v", id, version, err)
	}
}

// G-MULTILAYER-3 — org, team, and personal layers carry one artifact with
// extends up the chain; the per-caller winner differs by which layers each
// caller can see, and a pinned parent stays fixed when the org publishes a newer
// patch.
//
// Three layers carry eng/runbook. The org layer (organization-visible) holds
// v1.0.0; the team layer (group engineering) holds v2.0.0 extending
// eng/runbook@1.x; alice's personal layer (users:[alice]) holds v3.0.0 extending
// eng/runbook@2.x. §4.7.6 resolves latest as the most-recently-ingested visible
// version: alice (who sees all three) resolves the personal v3.0.0; bob (no
// personal layer) resolves the team v2.0.0. The per-caller winners differ.
//
// The team's v2 pins its parent to the org's exact 1.0.0 at ingest (the stored
// pin is eng/runbook@1.0.0, not the @1.x range). When the org publishes a new
// patch v1.0.1 with a different marker tag and the org layer reingests, the
// team overlay loaded by its explicit version still folds the org's v1.0.0
// marker, never the v1.0.1 marker: the pinned parent does not silently
// propagate. The personal overlay's pinned chain is stable the same way.
//
// Reconciliation note: pin stability is asserted by loading the team and
// personal overlays at their explicit versions, because §4.7.6 latest is the
// most-recently-ingested visible version, so the org's later v1.0.1 publish
// legitimately becomes bob's new latest winner; that recency shift is separate
// from the extends pin, which stays fixed. The journey uses context artifacts
// rather than a skill (the gap's illustrative type); the visibility-filtered
// latest selection and the §4.6 extends pin are type-agnostic, and context
// keeps each version to a single file.
//
// spec: §4.6 (layer composition, extends pin fixed at ingest, no silent
// propagation), §4.7.6 (latest = most-recently-ingested across the caller's
// effective view), §6.3.1 (group/users visibility), §6.3.2
// (injected-session-token).
func TestMultiLayer_PerCallerWinnerAndPinnedParentStable(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pemPath := injKeyPair(t)

	orgRoot := writeRegistry(t, nil)
	teamRoot := writeRegistry(t, nil)
	personalRoot := writeRegistry(t, nil)

	// Lowest precedence first. Each higher layer extends the one below.
	mlWriteArtifact(t, orgRoot, "eng/runbook", "1.0.0", "org-base-v1", "")
	mlWriteArtifact(t, teamRoot, "eng/runbook", "2.0.0", "team-v2", "eng/runbook@1.x")
	mlWriteArtifact(t, personalRoot, "eng/runbook", "3.0.0", "alice-v3", "eng/runbook@2.x")

	srv := mlVisServer(t, home, []mlVisLayer{
		{id: "org", path: orgRoot, vis: "        organization: true\n"},
		{id: "team", path: teamRoot, vis: "        groups: [engineering]\n"},
		{id: "personal", path: personalRoot, vis: "        users: [alice@acme.com]\n"},
	}, pemPath)

	alice := mlToken(t, priv, "alice@acme.com", "engineering")
	bob := mlToken(t, priv, "bob@acme.com", "engineering")

	// alice sees all three layers; her winner is the personal v3.0.0.
	var aliceView exLoadResp
	if st := mlGetJSON(t, srv.BaseURL+"/v1/load_artifact?id=eng/runbook", alice, &aliceView); st != http.StatusOK {
		t.Fatalf("alice load eng/runbook = HTTP %d, want 200\nlog:\n%s", st, srv.log())
	}
	if aliceView.Version != "3.0.0" {
		t.Errorf("alice winner = %q, want 3.0.0 (her personal layer)", aliceView.Version)
	}
	// The full chain folds: alice-v3 + team-v2 + org-base-v1 tags.
	for _, tag := range []string{"alice-v3", "team-v2", "org-base-v1"} {
		if !strings.Contains(aliceView.Frontmatter, tag) {
			t.Errorf("alice merged frontmatter missing %q (full chain should fold):\n%s", tag, aliceView.Frontmatter)
		}
	}

	// bob has no personal layer; his winner is the team v2.0.0.
	var bobView exLoadResp
	if st := mlGetJSON(t, srv.BaseURL+"/v1/load_artifact?id=eng/runbook", bob, &bobView); st != http.StatusOK {
		t.Fatalf("bob load eng/runbook = HTTP %d, want 200", st)
	}
	if bobView.Version != "2.0.0" {
		t.Errorf("bob winner = %q, want 2.0.0 (team layer; personal hidden)", bobView.Version)
	}
	if strings.Contains(bobView.Frontmatter, "alice-v3") {
		t.Errorf("bob merged frontmatter leaked alice's personal v3 content:\n%s", bobView.Frontmatter)
	}
	// bob's merged result folds the org base at its pinned version 1.0.0.
	if !strings.Contains(bobView.Frontmatter, "org-base-v1") {
		t.Errorf("bob merged frontmatter missing the pinned org base tag org-base-v1:\n%s", bobView.Frontmatter)
	}

	// Capture the team overlay (v2.0.0) and personal overlay (v3.0.0) at their
	// explicit versions before the org publish. Their merged frontmatter folds
	// the org base at the pinned 1.0.0.
	teamBefore := mlLoadVersion(t, srv, alice, "eng/runbook", "2.0.0").Frontmatter
	personalBefore := mlLoadVersion(t, srv, alice, "eng/runbook", "3.0.0").Frontmatter
	for _, fm := range []string{teamBefore, personalBefore} {
		if !strings.Contains(fm, "org-base-v1") {
			t.Fatalf("pinned overlay did not fold the org base v1.0.0 before publish:\n%s", fm)
		}
	}

	// The org publishes a new patch v1.0.1 with a different marker and reingests
	// the org layer. The reingest needs a verified token (injected-session-token
	// mode verifies every caller-identity route); reingest is not admin-gated.
	mlWriteArtifact(t, orgRoot, "eng/runbook", "1.0.1", "org-base-v101", "")
	ri := runPodium(t, "", []string{"PODIUM_SESSION_TOKEN=" + alice},
		"layer", "reingest", "--registry", srv.BaseURL, "org")
	if ri.Exit != 0 {
		t.Fatalf("org layer reingest exit=%d stderr=%s stdout=%s", ri.Exit, ri.Stderr, ri.Stdout)
	}
	// The new org version is resolvable (confirms the reingest landed).
	if st, _ := injGet(t, srv.BaseURL+"/v1/load_artifact?id=eng/runbook&version=1.0.1", alice); st != http.StatusOK {
		t.Fatalf("org v1.0.1 not resolvable after reingest")
	}

	// The team and personal pinned parents do not change: loading each overlay
	// at its explicit version still folds the org's pinned v1.0.0 marker and
	// never the new v1.0.1 marker. The extends pin is fixed at the child's
	// ingest, so the org publish does not silently propagate.
	teamAfter := mlLoadVersion(t, srv, alice, "eng/runbook", "2.0.0").Frontmatter
	personalAfter := mlLoadVersion(t, srv, alice, "eng/runbook", "3.0.0").Frontmatter
	for label, pair := range map[string][2]string{
		"team":     {teamBefore, teamAfter},
		"personal": {personalBefore, personalAfter},
	} {
		after := pair[1]
		if !strings.Contains(after, "org-base-v1") {
			t.Errorf("%s pinned parent changed after the org publish: dropped org-base-v1:\n%s", label, after)
		}
		if strings.Contains(after, "org-base-v101") {
			t.Errorf("%s pinned parent silently propagated the org publish (org-base-v101 leaked):\n%s", label, after)
		}
		if after != pair[0] {
			t.Errorf("%s overlay merged frontmatter changed across the org publish:\nbefore:\n%s\nafter:\n%s", label, pair[0], after)
		}
	}
}

// mlLoadVersion loads an artifact at an explicit version as a verified caller
// and returns the decoded envelope, failing on a non-200.
func mlLoadVersion(t *testing.T, srv *serverProc, token, id, version string) exLoadResp {
	t.Helper()
	var r exLoadResp
	st := mlGetJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id+"&version="+version, token, &r)
	if st != http.StatusOK {
		t.Fatalf("load %s@%s = HTTP %d, want 200", id, version, st)
	}
	return r
}

// ---- G-MULTILAYER-4 ---------------------------------------------------------

// G-MULTILAYER-4 — two layers contribute conflicting DOMAIN.md featured and
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
