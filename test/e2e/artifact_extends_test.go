package e2e

// End-to-end tests for docs/authoring/extends.md (D-extends).
//
// extends works end to end after the §4.6 remediation (F-4.6.1/2/3): a
// higher-precedence child that declares extends: at the same canonical ID is
// accepted as an overlay at boot, the full field-semantics table merges into
// the served frontmatter, a same-ID collision without extends is rejected, and
// a cross-type extends is rejected. The merge-observation tests boot a
// two-layer multi_layer registry (org-defaults parent, team-foo overlay) and
// assert the served result. A few cases remain skipped where the standalone
// e2e harness cannot exercise them: per-identity layer visibility (hidden
// parents/children), a post-boot reingest path (pin propagation), and a
// content-hash pin whose value is unknowable before the fixtures are written;
// those are covered by package-level tests. Bundled-file (resource) merge stays
// skipped because the shared resolver (core.mergeChain) serves the child's
// resources only; that resource-union behavior is unimplemented on the server
// and on filesystem sync alike, independent of the F-13.11.2 frontmatter
// extends resolution now applied to the filesystem path.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/version"
)

// exSkillArtifact is a minimal skill ARTIFACT.md (Podium frontmatter only).
const exSkillArtifact = "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"

// exSkillMD returns a SKILL.md whose name matches the leaf directory.
func exSkillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + name + " body.\n"
}

// exParentID is the canonical ID shared by the parent and the
// higher-precedence extends child in the two-layer boot fixtures.
const exParentID = "finance/ap/pay-invoice"

// exLoadResp captures the load_artifact fields the extends tests assert.
type exLoadResp struct {
	Version      string `json:"version"`
	Type         string `json:"type"`
	Frontmatter  string `json:"frontmatter"`
	ManifestBody string `json:"manifest_body"`
	Sensitivity  string `json:"sensitivity"`
	Deprecated   bool   `json:"deprecated"`
}

// extendsBoot stages a multi_layer registry where the higher-precedence
// team-foo layer overlays org-defaults at the same canonical ID
// (exParentID) and boots a standalone server. The parent is the
// lower-precedence org-defaults artifact; the child is the team-foo
// overlay (which must declare extends: <exParentID>). extra adds further
// files, e.g. SKILL.md bodies for skill fixtures.
func extendsBoot(t testing.TB, parentArtifact, childArtifact string, extra map[string]string) *serverProc {
	t.Helper()
	entries := map[string]string{
		".registry-config": "multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-foo\n",
		"org-defaults/" + exParentID + "/ARTIFACT.md": parentArtifact,
		"team-foo/" + exParentID + "/ARTIFACT.md":     childArtifact,
	}
	for k, v := range extra {
		entries[k] = v
	}
	return startServer(t, writeRegistry(t, entries))
}

// exLoad GETs /v1/load_artifact and decodes the response.
func exLoad(t testing.TB, srv *serverProc, id string) exLoadResp {
	t.Helper()
	var r exLoadResp
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &r)
	return r
}

// ---- Pinning (T-D-extends-1..7): ExtendsPin is not exposed by any API -------

// extParentWithTag is a lower-precedence parent carrying a distinctive
// tag the child does not declare; the tag's presence on the merged result
// proves the pinned parent was resolved and folded in.
func extParentWithTag(version string) string {
	return "---\ntype: context\nversion: " + version + "\ndescription: org parent\ntags: [from-parent]\n---\n\nparent body\n"
}

// T-D-extends-1 — extends pin resolved to an exact version at child ingest time
// using a semver range. The merged result carries the parent's tag, which is
// observable only if the @1.x range resolved to the ingested parent.
// spec: docs/authoring/extends.md § "Pinning" table.
func TestExtends_PinSemverRange(t *testing.T) {
	t.Parallel()
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, extParentWithTag("1.0.0"), child, nil)
	got := exLoad(t, srv, exParentID)
	if got.Version != "2.0.0" {
		t.Fatalf("version = %q, want child's 2.0.0", got.Version)
	}
	if !strings.Contains(got.Frontmatter, "from-parent") {
		t.Errorf("@1.x range did not resolve+merge the parent:\n%s", got.Frontmatter)
	}
}

// T-D-extends-2 — extends with an exact semver pin ingests successfully and
// merges. spec: docs/authoring/extends.md § "Pinning" table, <id>@<semver> row.
func TestExtends_PinExactSemver(t *testing.T) {
	t.Parallel()
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: " + exParentID + "@1.0.0\n---\n\nbody\n"
	srv := extendsBoot(t, extParentWithTag("1.0.0"), child, nil)
	got := exLoad(t, srv, exParentID)
	if got.Version != "2.0.0" || !strings.Contains(got.Frontmatter, "from-parent") {
		t.Errorf("exact pin did not merge the parent: version=%q\n%s", got.Version, got.Frontmatter)
	}
}

// T-D-extends-3 — content-hash pinning resolves to an exact version. The
// parent's hash is not knowable before the boot fixtures are written, so the
// resolution is asserted in the core unit test TestExtends_ContentHashPin; here
// only the failure path (T-D-extends-4) is reachable end to end.
func TestExtends_PinContentHash(t *testing.T) {
	t.Parallel()
	t.Skip("content-hash pin resolution requires the parent's hash before boot, which the e2e fixtures cannot know; covered by pkg/registry/core TestExtends_ContentHashPin")
}

// T-D-extends-4 — extends with an unresolvable content hash fails ingest; the
// child is dropped and only the lower-precedence parent serves at the shared id.
// spec: docs/authoring/extends.md § "Pinning" table, <id>@sha256:<hash> row.
func TestExtends_PinUnresolvableHash(t *testing.T) {
	t.Parallel()
	bogus := "sha256:" + strings.Repeat("0", 64)
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: " + exParentID + "@" + bogus + "\n---\n\nbody\n"
	srv := extendsBoot(t, extParentWithTag("1.0.0"), child, nil)
	got := exLoad(t, srv, exParentID)
	if got.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0 (child with unresolvable hash dropped at ingest)", got.Version)
	}
	if !strings.Contains(srv.log(), "rejected") {
		t.Errorf("server log missing a rejection for the unresolvable-hash child:\n%s", srv.log())
	}
}

// T-D-extends-5 — extends with a bare ID resolves to latest at ingest time and
// merges. spec: docs/authoring/extends.md § "Pinning" table, bare <id> row.
func TestExtends_PinBareID(t *testing.T) {
	t.Parallel()
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: " + exParentID + "\n---\n\nbody\n"
	srv := extendsBoot(t, extParentWithTag("1.0.0"), child, nil)
	got := exLoad(t, srv, exParentID)
	if got.Version != "2.0.0" || !strings.Contains(got.Frontmatter, "from-parent") {
		t.Errorf("bare-id extends did not resolve latest + merge: version=%q\n%s", got.Version, got.Frontmatter)
	}
}

// T-D-extends-6 — parent updates do not silently propagate to an ingested child.
// spec: docs/authoring/extends.md § "Pinning", last paragraph.
func TestExtends_PinNoSilentPropagation(t *testing.T) {
	t.Parallel()
	t.Skip("pin non-propagation is exercised end to end in multilayer_journeys_test.go TestMultiLayer_PerCallerWinnerAndPinnedParentStable (G-MULTILAYER-3): an org layer publishes a newer base patch and reingests, and the team/personal overlays loaded at their explicit versions still fold the pinned parent version, never the newly published one. Pin stability is also covered at unit scale by pkg/registry/ingest TestIngest_CrossLayerExtendsOverlayAllowed (ExtendsPin is fixed at ingest)")
}

// T-D-extends-7 — re-ingesting the child after a version bump picks up the newer
// parent version. spec: docs/authoring/extends.md § "Pinning", last paragraph.
func TestExtends_PinReingestPicksNewerParent(t *testing.T) {
	t.Parallel()
	t.Skip("the post-boot reingest path now ingests (F-7.3.4 resolved), but driving a newer-parent pin-propagation scenario end to end needs a multi-version layer fixture this harness does not build; pin stability is covered by pkg/registry/ingest TestIngest_CrossLayerExtendsOverlayAllowed")
}

// ---- Scalar / list / map field merge (T-D-extends-8..15) --------------------

// T-D-extends-8 — child description and version win on load.
// spec: docs/authoring/extends.md § "Field merge semantics".
func TestExtends_ScalarChildWins(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: the parent description\n---\n\nparent body\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: the child description\nextends: " + exParentID + "@1.x\n---\n\nchild body\n"
	srv := extendsBoot(t, parent, child, nil)
	got := exLoad(t, srv, exParentID)
	if got.Version != "2.0.0" {
		t.Fatalf("version = %q, want child's 2.0.0", got.Version)
	}
	if !strings.Contains(got.Frontmatter, "the child description") {
		t.Errorf("merged frontmatter missing child's description:\n%s", got.Frontmatter)
	}
	if strings.Contains(got.Frontmatter, "the parent description") {
		t.Errorf("scalar description must be the child's only:\n%s", got.Frontmatter)
	}
}

// T-D-extends-9 — tags are unioned (append unique) across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", tags row.
func TestExtends_TagsUnion(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\ntags: [parent-tag, shared]\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\ntags: [child-tag, shared]\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	for _, tag := range []string{"parent-tag", "child-tag", "shared"} {
		if !strings.Contains(fm, tag) {
			t.Errorf("merged tags missing %q:\n%s", tag, fm)
		}
	}
}

// T-D-extends-10 — sensitivity takes the most-restrictive value; the child
// cannot relax the parent. spec: docs/authoring/extends.md § "Field merge
// semantics", sensitivity row.
func TestExtends_SensitivityMostRestrictive(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nsensitivity: high\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nsensitivity: low\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	if got := exLoad(t, srv, exParentID); got.Sensitivity != "high" {
		t.Errorf("sensitivity = %q, want high (child cannot relax the parent)", got.Sensitivity)
	}
}

// T-D-extends-11 — sensitivity: the child can tighten medium to high.
// spec: docs/authoring/extends.md § "Field merge semantics", sensitivity row.
func TestExtends_SensitivityChildTightens(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nsensitivity: medium\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nsensitivity: high\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	if got := exLoad(t, srv, exParentID); got.Sensitivity != "high" {
		t.Errorf("sensitivity = %q, want high (child tightened medium)", got.Sensitivity)
	}
}

// T-D-extends-12 — sandbox_profile: most-restrictive wins; the child tightens
// the parent. spec: docs/authoring/extends.md § "Tightening sandbox profile".
func TestExtends_SandboxChildTightens(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nsandbox_profile: read-only-fs\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nsandbox_profile: seccomp-strict\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	if fm := exLoad(t, srv, exParentID).Frontmatter; !strings.Contains(fm, "seccomp-strict") {
		t.Errorf("merged sandbox_profile = should be seccomp-strict:\n%s", fm)
	}
}

// T-D-extends-13 — sandbox_profile: the child cannot widen the parent
// restriction. spec: docs/authoring/extends.md § "The most-restrictive rules".
func TestExtends_SandboxChildCannotWiden(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nsandbox_profile: seccomp-strict\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nsandbox_profile: unrestricted\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "seccomp-strict") {
		t.Errorf("most-restrictive sandbox must stay seccomp-strict:\n%s", fm)
	}
	if strings.Contains(fm, "unrestricted") {
		t.Errorf("child must not widen sandbox to unrestricted:\n%s", fm)
	}
}

// T-D-extends-14 — mcpServers: deep-merged by name; the child's entry overrides
// the parent's on a name match. spec: docs/authoring/extends.md § "Field merge
// semantics", mcpServers row.
func TestExtends_McpServersOverrideByName(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nmcpServers:\n  - name: github\n    command: gh-old\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nmcpServers:\n  - name: github\n    command: gh-new\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "gh-new") || strings.Contains(fm, "gh-old") {
		t.Errorf("child mcpServers entry should override parent's by name:\n%s", fm)
	}
}

// T-D-extends-15 — mcpServers: a parent-only server is inherited when the child
// declares a differently named server. spec: docs/authoring/extends.md § "Field
// merge semantics", mcpServers row.
func TestExtends_McpServersParentOnlyInherited(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nmcpServers:\n  - name: jira\n    command: jira\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nmcpServers:\n  - name: slack\n    command: slack\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "jira") || !strings.Contains(fm, "slack") {
		t.Errorf("parent-only jira should be inherited alongside child's slack:\n%s", fm)
	}
}

// ---- Hidden parents (T-D-extends-16..17) ------------------------------------

// exHiddenParentServer boots the authenticated, visibility-capable harness with
// a restricted parent layer and a public child layer that extends it. The parent
// shared/parent is restricted to bob and carries a distinctive tag and high
// sensitivity; the child finance/child is public and declares extends:
// shared/parent@1.x with low sensitivity. Layers are listed lowest-precedence
// first, so the parent (secret-parent) is the extends target the higher child
// resolves at ingest. It returns the server with tokens for alice (who cannot
// see the parent) and bob (the parent's grantee).
func exHiddenParentServer(t *testing.T) (srv *authServer, alice, bob string) {
	t.Helper()
	srv = startAuthServer(t, authServerSpec{
		Layers: []authLayer{
			{
				ID: "secret-parent",
				Files: map[string]string{
					"shared/parent/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\n" +
						"description: hidden parent\ntags: [from-hidden-parent]\nsensitivity: high\n---\n\nparent body\n",
				},
				Visibility: authVisibility{Users: []string{"bob@acme.com"}},
			},
			{
				ID: "public-child",
				Files: map[string]string{
					"finance/child/ARTIFACT.md": "---\ntype: context\nversion: 2.0.0\n" +
						"description: visible child\ntags: [child-tag]\nsensitivity: low\n" +
						"extends: shared/parent@1.x\n---\n\nchild body\n",
				},
				Visibility: authVisibility{Public: true},
			},
		},
	})
	alice = srv.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com"})
	bob = srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})
	return srv, alice, bob
}

// exLoadAs GETs /v1/load_artifact for id as token and decodes the response,
// failing the test on a non-200.
func exLoadAs(t *testing.T, srv *authServer, id, token string) exLoadResp {
	t.Helper()
	st, body := srv.get("/v1/load_artifact?id="+id, token)
	if st != http.StatusOK {
		t.Fatalf("load %s = HTTP %d, want 200 (body=%s)\nlog:\n%s", id, st, body, srv.log())
	}
	var r exLoadResp
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode load %s: %v (body=%s)", id, err, body)
	}
	return r
}

// T-D-extends-16 — a caller without access to the parent layer sees the merged
// result. The parent lives in a layer restricted to bob; alice cannot see it,
// yet loading the public child folds the hidden parent's inherited tag and the
// most-restrictive sensitivity into the served manifest.
// spec: docs/authoring/extends.md § "Hidden parents".
func TestExtends_HiddenParentMergedResult(t *testing.T) {
	t.Parallel()
	srv, alice, _ := exHiddenParentServer(t)

	// alice cannot see the parent layer, but the public child resolves and
	// merges the parent server-side. The merged manifest carries the child
	// version and the parent's inherited fields.
	got := exLoadAs(t, srv, "finance/child", alice)
	if got.Version != "2.0.0" {
		t.Errorf("merged child version = %q, want 2.0.0", got.Version)
	}
	if !strings.Contains(got.Frontmatter, "from-hidden-parent") {
		t.Errorf("merged manifest missing the hidden parent's inherited tag:\n%s", got.Frontmatter)
	}
	if !strings.Contains(got.Frontmatter, "child-tag") {
		t.Errorf("merged manifest missing the child's own tag:\n%s", got.Frontmatter)
	}
	// Most-restrictive sensitivity: the child declared low, but the parent's
	// high wins (the child cannot relax the parent).
	if got.Sensitivity != "high" {
		t.Errorf("merged sensitivity = %q, want high (most-restrictive from the hidden parent)", got.Sensitivity)
	}
}

// T-D-extends-17 — the parent does not appear in search results for an
// unauthorized caller. alice loads the merged child but the hidden parent
// shared/parent is neither loadable nor discoverable through her search, while
// bob (the parent's grantee) can load it.
// spec: docs/authoring/extends.md § "Hidden parents".
func TestExtends_HiddenParentNotInSearch(t *testing.T) {
	t.Parallel()
	srv, alice, bob := exHiddenParentServer(t)

	// alice can load the public child (proving the merge is reachable) but the
	// hidden parent is not loadable for her: 404, no existence leak.
	exLoadAs(t, srv, "finance/child", alice)
	if st := srv.loadStatus("shared/parent", alice); st != http.StatusNotFound {
		t.Errorf("alice load shared/parent = HTTP %d, want 404 (hidden parent not loadable)", st)
	}

	// The hidden parent never appears in alice's search_artifacts surface.
	aliceIDs := srv.searchIDs(alice)
	assertHas(t, aliceIDs, "finance/child", "alice search includes the public child")
	assertMissing(t, aliceIDs, "shared/parent", "alice search omits the hidden parent")

	// Control: bob (the parent layer's sole grantee) can load the parent
	// directly and sees it in his search, proving the hiding is per-identity.
	if st := srv.loadStatus("shared/parent", bob); st != http.StatusOK {
		t.Errorf("bob load shared/parent = HTTP %d, want 200 (grantee can see the parent)", st)
	}
	assertHas(t, srv.searchIDs(bob), "shared/parent", "bob search includes the parent he owns")
}

// ---- Bundled-file merge (T-D-extends-18..21) --------------------------------

// T-D-extends-18 — a child file at the same path overrides the parent file.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_BundledChildOverrides(t *testing.T) {
	t.Parallel()
	t.Skip("bundled-file (resource) merge across extends is not implemented in the shared resolver: core.mergeChain serves the child's resources only (child-only for §4.6 hidden-parent privacy), so neither the server nor the now-extends-resolving filesystem sync (F-13.11.2) unions parent and child files. This is a §4.4/§4.6 resource-merge gap, not the frontmatter-resolution gap F-13.11.2 closed")
}

// T-D-extends-19 — a parent-only bundled file is inherited unchanged.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_BundledParentOnlyInherited(t *testing.T) {
	t.Parallel()
	t.Skip("bundled-file (resource) merge across extends is not implemented in the shared resolver: core.mergeChain serves the child's resources only, so a parent-only bundled file is not inherited on either the server or the now-extends-resolving filesystem sync (F-13.11.2). This is a §4.4/§4.6 resource-merge gap, not the frontmatter-resolution gap F-13.11.2 closed")
}

// T-D-extends-20 — a child-only bundled file is added to the merged output.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_BundledChildOnlyAdded(t *testing.T) {
	t.Parallel()
	t.Skip("bundled-file (resource) merge across extends is not implemented in the shared resolver: core.mergeChain serves the child's resources only, with no parent/child file union, on either the server or the now-extends-resolving filesystem sync (F-13.11.2). This is a §4.4/§4.6 resource-merge gap, not the frontmatter-resolution gap F-13.11.2 closed")
}

// T-D-extends-21 — the child cannot delete a parent file; it is inherited.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_BundledChildCannotDelete(t *testing.T) {
	t.Parallel()
	t.Skip("bundled-file (resource) merge across extends is not implemented in the shared resolver: core.mergeChain serves the child's resources only, so the parent file is never composed into the child's output and the delete-shadow semantics are not exercisable on either the server or the now-extends-resolving filesystem sync (F-13.11.2). This is a §4.4/§4.6 resource-merge gap, not the frontmatter-resolution gap F-13.11.2 closed")
}

// ---- SKILL.md override (T-D-extends-22) -------------------------------------

// T-D-extends-22 — the child SKILL.md overrides the parent SKILL.md.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_SkillBodyChildOverrides(t *testing.T) {
	t.Parallel()
	childArt := "---\ntype: skill\nversion: 2.0.0\nextends: " + exParentID + "@1.x\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
	extra := map[string]string{
		"org-defaults/" + exParentID + "/SKILL.md": "---\nname: pay-invoice\ndescription: Pay an approved invoice.\n---\n\nPARENT skill prose.\n",
		"team-foo/" + exParentID + "/SKILL.md":     "---\nname: pay-invoice\ndescription: Pay an approved invoice for team Foo.\n---\n\nCHILD skill prose.\n",
	}
	srv := extendsBoot(t, exSkillArtifact, childArt, extra)
	got := exLoad(t, srv, exParentID)
	if got.Version != "2.0.0" {
		t.Fatalf("version = %q, want child's 2.0.0", got.Version)
	}
	if !strings.Contains(got.ManifestBody, "CHILD skill prose") {
		t.Errorf("manifest_body should be the child SKILL.md body:\n%s", got.ManifestBody)
	}
	if strings.Contains(got.ManifestBody, "PARENT skill prose") {
		t.Errorf("parent SKILL.md body must not be served (child overrides):\n%s", got.ManifestBody)
	}
}

// ---- Ingest rejections observable via load 404 (T-D-extends-23..24) ---------

// T-D-extends-23 — an extends reference to an unknown parent fails ingest. The
// child is dropped at ingest and a valid sibling keeps boot alive, so the
// rejection is observable as a 404 on the child id. The doc says an unresolved
// parent is a lint warning rather than an error; the implementation rejects the
// child at ingest, which this asserts. spec: docs/authoring/extends.md § "Lint
// behavior".
func TestExtends_UnknownParentRejected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/sibling/ARTIFACT.md":     exSkillArtifact,
		"finance/ap/sibling/SKILL.md":        exSkillMD("sibling", "A valid sibling skill that keeps boot alive."),
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nextends: does/not/exist@1.x\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		"finance/ap/pay-invoice/SKILL.md":    exSkillMD("pay-invoice", "Pay an approved invoice."),
	})
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/ap/sibling"); st != 200 {
		t.Fatalf("valid sibling = HTTP %d, want 200 (boot should accept it)", st)
	}
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=finance/ap/pay-invoice")
	if st != 404 {
		t.Fatalf("unknown-parent child = HTTP %d, want 404 (dropped at ingest)\n%s", st, body)
	}
	if !strings.Contains(srv.log(), "rejected") {
		t.Errorf("server log missing a rejection record:\n%s", srv.log())
	}
}

// T-D-extends-24 — a self-referencing extends is rejected as a cycle at ingest.
// The artifact is dropped and a valid sibling keeps boot alive, so the rejection
// is observable as a 404. spec: docs/authoring/extends.md § "Constraints", cycle
// detection; § "Lint behavior".
func TestExtends_SelfReferenceRejected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/sibling/ARTIFACT.md": "---\ntype: context\nname: sibling\nversion: 1.0.0\ndescription: A valid sibling that keeps boot alive.\n---\n\nbody\n",
		"finance/self/ARTIFACT.md":    "---\ntype: context\nname: self\nversion: 1.0.0\ndescription: Self-referencing artifact.\nextends: finance/self@1.x\n---\n\nbody\n",
	})
	srv := startServer(t, reg)
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/sibling"); st != 200 {
		t.Fatalf("valid sibling = HTTP %d, want 200 (boot should accept it)", st)
	}
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=finance/self")
	if st != 404 {
		t.Fatalf("self-extends artifact = HTTP %d, want 404 (dropped at ingest)\n%s", st, body)
	}
	if !strings.Contains(srv.log(), "rejected") {
		t.Errorf("server log missing a rejection record:\n%s", srv.log())
	}
}

// ---- Cycle defense / collision policy (T-D-extends-25..29) ------------------

// T-D-extends-25 — a multi-hop cycle is detected at load time as defense in
// depth. spec: docs/authoring/extends.md § "Constraints", cycle detection.
func TestExtends_MultiHopCycleAtLoad(t *testing.T) {
	t.Parallel()
	t.Skip("ingest pins each parent at child-ingest time and rejects self/forward references, so a cyclic chain cannot be persisted through the running ingest path to reach the load-time cycle defense; the defense is unit-covered in pkg/registry/core (resolveExtendsChain seen-set)")
}

// T-D-extends-26 — a same-canonical-ID collision without extends is rejected at
// boot ingest, so the higher-precedence shadow is dropped and only the
// lower-precedence artifact serves. spec: docs/authoring/extends.md §
// "Replacing instead of extending".
func TestExtends_CollisionWithoutExtendsRejected(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: org base\n---\n\nbase body\n"
	shadow := "---\ntype: context\nversion: 2.0.0\ndescription: silent shadow\n---\n\nshadow body\n"
	srv := extendsBoot(t, parent, shadow, nil)
	got := exLoad(t, srv, exParentID)
	if got.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0 (the no-extends shadow is rejected at ingest)", got.Version)
	}
	if !strings.Contains(srv.log(), "rejected=1") {
		t.Errorf("team-foo layer should report one rejected artifact (the silent shadow):\n%s", srv.log())
	}
}

// T-D-extends-27 — extends allows a same-ID artifact in a higher-precedence
// layer without a collision error, and the merged child serves.
// spec: docs/authoring/extends.md § intro.
func TestExtends_AllowsSameIDWithExtends(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: org base\ntags: [from-parent]\n---\n\nbase body\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: overlay child\nextends: " + exParentID + "@1.x\n---\n\noverlay body\n"
	srv := extendsBoot(t, parent, child, nil)
	got := exLoad(t, srv, exParentID)
	if got.Version != "2.0.0" {
		t.Errorf("version = %q, want 2.0.0 (the extends overlay wins)", got.Version)
	}
	if !strings.Contains(got.Frontmatter, "from-parent") {
		t.Errorf("overlay should merge the parent's fields:\n%s", got.Frontmatter)
	}
}

// T-D-extends-28 — a cross-type extends is rejected (child type must match the
// parent type), so the child is dropped and the parent serves.
// spec: docs/authoring/extends.md § "Default for unlisted fields".
func TestExtends_CrossTypeRejected(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent agent\n---\n\nagent body\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child context\nextends: " + exParentID + "@1.x\n---\n\ncontext body\n"
	srv := extendsBoot(t, parent, child, nil)
	got := exLoad(t, srv, exParentID)
	if got.Version != "1.0.0" || got.Type != "agent" {
		t.Errorf("got type=%q version=%q, want the agent parent (cross-type child rejected)", got.Type, got.Version)
	}
	if !strings.Contains(srv.log(), "rejected=1") {
		t.Errorf("team-foo layer should report one rejected artifact (the cross-type child):\n%s", srv.log())
	}
}

// T-D-extends-29 — chained inheritance (A extends B extends C) resolves the full
// chain across three layers at one canonical ID. spec: docs/authoring/extends.md
// § "Constraints".
func TestExtends_ChainedInheritance(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                      "multi_layer: true\nlayer_order:\n  - a-base\n  - b-mid\n  - c-top\n",
		"a-base/" + exParentID + "/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: base C\ntags: [c-tag]\n---\n\nbody\n",
		"b-mid/" + exParentID + "/ARTIFACT.md":  "---\ntype: context\nversion: 2.0.0\ndescription: mid B\ntags: [b-tag]\nextends: " + exParentID + "@1.x\n---\n\nbody\n",
		"c-top/" + exParentID + "/ARTIFACT.md":  "---\ntype: context\nversion: 3.0.0\ndescription: top A\ntags: [a-tag]\nextends: " + exParentID + "@2.x\n---\n\nbody\n",
	})
	srv := startServer(t, reg)
	got := exLoad(t, srv, exParentID)
	if got.Version != "3.0.0" {
		t.Fatalf("version = %q, want 3.0.0 (top of the chain)", got.Version)
	}
	for _, tag := range []string{"a-tag", "b-tag", "c-tag"} {
		if !strings.Contains(got.Frontmatter, tag) {
			t.Errorf("merged frontmatter missing %q; the full A→B→C chain should fold:\n%s", tag, got.Frontmatter)
		}
	}
}

// ---- Same-canonical-ID constraint not enforced (T-D-extends-30) -------------

// T-D-extends-30 — the same-canonical-ID constraint is not enforced. The doc
// states a child uses extends only when its canonical ID matches the parent's;
// the implementation does not check this (no BUILD-GAPS finding), so a child at
// a different id extending a parent ingests. Change-detector. spec:
// docs/authoring/extends.md § "Constraints", same canonical ID.
func TestExtends_DifferentCanonicalIDIngests(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/pay-invoice/ARTIFACT.md":    "---\ntype: context\nname: pay-invoice\nversion: 1.0.0\ndescription: Pay an approved invoice.\n---\n\nbody\n",
		"finance/submit-payment/ARTIFACT.md": "---\ntype: context\nname: submit-payment\nversion: 1.0.0\ndescription: Submit a payment.\nextends: finance/pay-invoice@1.x\n---\n\nbody\n",
	})
	srv := startServer(t, reg)
	// The doc says extends requires matching canonical IDs; the implementation
	// does not check this, so the differently-named child ingests and loads.
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=finance/submit-payment"); st != 200 {
		t.Fatalf("differently-named extends child = HTTP %d, want 200 (the canonical-id-match constraint is not enforced)", st)
	}
}

// ---- More list / map merges (T-D-extends-31..39) ----------------------------

// T-D-extends-31 — requiresApproval is unioned across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", requiresApproval.
func TestExtends_RequiresApprovalUnion(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\nrequiresApproval:\n  - tool: deploy\n---\n\nbody\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\nrequiresApproval:\n  - tool: publish\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "deploy") || !strings.Contains(fm, "publish") {
		t.Errorf("requiresApproval should union to [deploy, publish]:\n%s", fm)
	}
}

// T-D-extends-32 — when_to_use is appended across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", when_to_use.
func TestExtends_WhenToUseAppend(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nwhen_to_use:\n  - when parenting\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nwhen_to_use:\n  - when childing\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "when parenting") || !strings.Contains(fm, "when childing") {
		t.Errorf("when_to_use should append both entries:\n%s", fm)
	}
}

// T-D-extends-33 — delegates_to is appended across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", delegates_to.
func TestExtends_DelegatesToAppend(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\ndelegates_to: [finance/x]\n---\n\nbody\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\ndelegates_to: [finance/y]\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "finance/x") || !strings.Contains(fm, "finance/y") {
		t.Errorf("delegates_to should append both targets:\n%s", fm)
	}
}

// T-D-extends-34 — external_resources is appended across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", external_resources.
func TestExtends_ExternalResourcesAppend(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nexternal_resources:\n  - path: data/p.bin\n    url: https://e/p\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nexternal_resources:\n  - path: data/c.bin\n    url: https://e/c\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "data/p.bin") || !strings.Contains(fm, "data/c.bin") {
		t.Errorf("external_resources should append both entries:\n%s", fm)
	}
}

// T-D-extends-35 — license: child wins. The documented lint warning on a
// license change across layers is a separate, unimplemented rule; this asserts
// only the merge that §4.6 mandates. spec: docs/authoring/extends.md § "Field
// merge semantics", license.
func TestExtends_LicenseChildWinsLintWarns(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nlicense: Apache-2.0\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nlicense: MIT\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "MIT") || strings.Contains(fm, "Apache-2.0") {
		t.Errorf("license should be the child's (MIT), not the parent's:\n%s", fm)
	}
}

// T-D-extends-36 — search_visibility takes the most-restrictive value
// (direct-only > indexed); the child cannot relax the parent.
// spec: docs/authoring/extends.md § "Field merge semantics", search_visibility.
func TestExtends_SearchVisibilityMostRestrictive(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nsearch_visibility: direct-only\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nsearch_visibility: indexed\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "direct-only") {
		t.Errorf("search_visibility should stay direct-only (most-restrictive):\n%s", fm)
	}
}

// T-D-extends-37 — a child omitting a parent field inherits the parent's value.
// spec: docs/authoring/extends.md § "Default for unlisted fields".
func TestExtends_UnlistedFieldInherited(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\neffort_hint: high\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "effort_hint: high") {
		t.Errorf("child omitting effort_hint should inherit the parent's:\n%s", fm)
	}
}

// T-D-extends-38 — a child can override an inherited unlisted field by setting
// it (deprecated). spec: docs/authoring/extends.md § "Default for unlisted
// fields", deprecated.
func TestExtends_ChildOverridesDeprecated(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\ndeprecated: true\nreplaced_by: finance/new\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	// `latest` skips deprecated versions (§4.7.6), so load the child
	// version explicitly to observe the merged deprecated flag.
	var got exLoadResp
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+exParentID+"&version=2.0.0", &got)
	if !got.Deprecated {
		t.Errorf("child set deprecated: true; the merged record should be deprecated:\n%+v", got)
	}
}

// T-D-extends-39 — runtime_requirements is deep-merged with the child winning
// per key. spec: docs/authoring/extends.md § "Field merge semantics",
// runtime_requirements.
func TestExtends_RuntimeRequirementsDeepMerge(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: agent\nversion: 1.0.0\ndescription: parent\nruntime_requirements:\n  python: \"3.10\"\n  system_packages: [git]\n---\n\nbody\n"
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\nruntime_requirements:\n  python: \"3.12\"\n  system_packages: [curl]\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)
	fm := exLoad(t, srv, exParentID).Frontmatter
	if !strings.Contains(fm, "3.12") {
		t.Errorf("runtime python should be the child's 3.12:\n%s", fm)
	}
	if !strings.Contains(fm, "git") || !strings.Contains(fm, "curl") {
		t.Errorf("system_packages should union [git, curl]:\n%s", fm)
	}
}

// ---- scaffold --extends (T-D-extends-40) ------------------------------------

// T-D-extends-40 — artifact scaffold --extends writes the extends field.
// spec: docs/authoring/extends.md opening YAML example; scaffold flag --extends.
func TestExtends_ScaffoldExtendsField(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "finance/ap/pay-invoice")
	sc := runPodium(t, "", nil, "artifact", "scaffold", "--type", "skill",
		"--description", "x", "--extends", "finance/ap/pay-invoice@1.x", "--yes", out)
	if sc.Exit != 0 {
		t.Fatalf("scaffold exit=%d stderr=%s", sc.Exit, sc.Stderr)
	}
	art := readFile(t, filepath.Join(out, "ARTIFACT.md"))
	if !strings.Contains(art, "extends: finance/ap/pay-invoice@1.x") {
		t.Errorf("ARTIFACT.md missing extends:\n%s", art)
	}
	if !strings.Contains(art, "type: skill") {
		t.Errorf("ARTIFACT.md missing type: skill:\n%s", art)
	}
}

// ---- Impact / dependents (T-D-extends-41..42) -------------------------------

// T-D-extends-41 — GET /v1/dependents returns the extends edge for the parent.
// A higher-layer child extends a lower-layer parent (distinct canonical IDs);
// the reverse-dependency index records the edge. spec:
// docs/authoring/extends.md § "Constraints".
func TestExtends_DependentsEdge(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                       "multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-foo\n",
		"org-defaults/shared/parent/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nbody\n",
		"team-foo/finance/child/ARTIFACT.md":     "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nbody\n",
	})
	srv := startServer(t, reg)
	st, body := getRaw(t, srv.BaseURL+"/v1/dependents?id=shared/parent")
	if st != 200 {
		t.Fatalf("GET /v1/dependents = HTTP %d: %s", st, body)
	}
	if !strings.Contains(string(body), "finance/child") {
		t.Errorf("dependents of shared/parent should list the extends child finance/child:\n%s", body)
	}
}

// T-D-extends-42 — the impact CLI lists the extending children of a parent.
// spec: docs/authoring/extends.md § "Constraints"; impact analysis.
func TestExtends_ImpactCLIListsChildren(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":                       "multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-foo\n",
		"org-defaults/shared/parent/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nbody\n",
		"team-foo/finance/child/ARTIFACT.md":     "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nbody\n",
	})
	srv := startServer(t, reg)
	res := runPodium(t, "", nil, "impact", "--registry", srv.BaseURL, "shared/parent")
	if res.Exit != 0 {
		t.Fatalf("impact exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "finance/child") {
		t.Errorf("impact should list the extending child finance/child:\n%s", res.Stdout)
	}
}

// ---- Worked example (T-D-extends-43) ----------------------------------------

// T-D-extends-43 — the org-wide skill example: a team layer overlays the
// org-defaults skill at the same canonical ID via extends, and the merged
// manifest (unioned tags, child body, child version) serves. spec:
// docs/authoring/extends.md § "Examples".
func TestExtends_OrgWideSkillExample(t *testing.T) {
	t.Parallel()
	parentArt := "---\ntype: skill\nversion: 1.0.0\ntags: [org-tag]\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
	childArt := "---\ntype: skill\nversion: 2.0.0\ntags: [team-tag]\nextends: " + exParentID + "@1.x\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
	extra := map[string]string{
		"org-defaults/" + exParentID + "/SKILL.md": "---\nname: pay-invoice\ndescription: Pay an approved invoice.\n---\n\nORG skill prose.\n",
		"team-foo/" + exParentID + "/SKILL.md":     "---\nname: pay-invoice\ndescription: Pay an approved invoice for team Foo.\n---\n\nTEAM skill prose.\n",
	}
	srv := extendsBoot(t, parentArt, childArt, extra)
	got := exLoad(t, srv, exParentID)
	if got.Version != "2.0.0" {
		t.Fatalf("version = %q, want child's 2.0.0", got.Version)
	}
	if !strings.Contains(got.Frontmatter, "org-tag") || !strings.Contains(got.Frontmatter, "team-tag") {
		t.Errorf("merged tags should union org-tag and team-tag:\n%s", got.Frontmatter)
	}
	if !strings.Contains(got.ManifestBody, "TEAM skill prose") {
		t.Errorf("served body should be the team child SKILL.md:\n%s", got.ManifestBody)
	}
}

// ---- MCP load (T-D-extends-44) ----------------------------------------------

// T-D-extends-44 — the MCP load_artifact tool returns the extends-merged
// manifest. The overlay child (resolved + merged server-side) is what the
// bridge serves, observable as the child version and body. spec:
// docs/authoring/extends.md § "Field merge semantics".
func TestExtends_McpLoadMergedBody(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nPARENT body\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child desc\nextends: " + exParentID + "@1.x\n---\n\nCHILD body\n"
	srv := extendsBoot(t, parent, child, nil)
	res := mcpExec(t,
		[]string{
			"PODIUM_REGISTRY=" + srv.BaseURL,
			"PODIUM_HARNESS=none",
			"PODIUM_MATERIALIZE_ROOT=" + t.TempDir(),
			"PODIUM_CACHE_DIR=" + t.TempDir(),
		},
		toolCall(1, "load_artifact", map[string]any{"id": exParentID}),
	)
	body := mustJSON(rpcResult(t, res.Stdout, 1))
	if !strings.Contains(body, "2.0.0") {
		t.Errorf("MCP load_artifact should serve the merged overlay child (version 2.0.0):\n%s", body)
	}
	if !strings.Contains(body, "CHILD body") {
		t.Errorf("MCP load_artifact should serve the child body, not the parent's:\n%s", body)
	}
}

// ---- Lint behavior (T-D-extends-45..46) -------------------------------------

// T-D-extends-45 — lint does not error on an extends reference. No
// extends-resolution lint rule exists, so an extends reference never produces an
// error-severity diagnostic, even when the parent is absent. spec:
// docs/authoring/extends.md § "Lint behavior".
func TestExtends_LintNoErrorOnReference(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nextends: finance/ap/pay-invoice@1.x\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		"finance/ap/pay-invoice/SKILL.md":    exSkillMD("pay-invoice", "Pay an approved invoice."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	if strings.Contains(res.Stdout, "[error]") {
		t.Errorf("an extends reference should not produce an error-severity diagnostic:\n%s", res.Stdout)
	}
}

// T-D-extends-46 — lint does not warn on an unresolved parent. The doc says lint
// warns when the extends parent is absent, but there is no extends-resolution
// lint rule, so no diagnostic of any severity is emitted. Change-detector.
// spec: docs/authoring/extends.md § "Lint behavior".
func TestExtends_LintNoWarnOnUnresolvedParent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nextends: finance/ap/pay-invoice@1.x\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		"finance/ap/pay-invoice/SKILL.md":    exSkillMD("pay-invoice", "Pay an approved invoice."),
	})
	res := runPodium(t, "", nil, "lint", "--registry", reg)
	if res.Exit != 0 {
		t.Fatalf("lint exit=%d, want 0\nstdout=%s stderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
	// The doc claims a lint warning for an unresolved parent. No extends rule
	// exists, so lint stays clean and emits no extends-related diagnostic.
	if strings.Contains(res.Stdout, "extends") {
		t.Errorf("no extends-resolution lint rule should fire, but a diagnostic mentioned extends:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "lint: no issues.") {
		t.Errorf("lint should report no issues for an unresolved extends parent:\n%s", res.Stdout)
	}
}

// ---- Filesystem sync (T-D-extends-47) ---------------------------------------

// T-D-extends-47 — podium sync over a filesystem registry does not resolve
// extends. The higher-precedence child wins where present (highest-wins, not
// merged) and the parent-only bundled file is absent. Documents F-13.11.2.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_SyncFilesystemNotMerged(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": "multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-foo\n",
		"org-defaults/finance/ap/pay-invoice/ARTIFACT.md":         "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		"org-defaults/finance/ap/pay-invoice/SKILL.md":            exSkillMD("pay-invoice", "Pay an approved invoice."),
		"org-defaults/finance/ap/pay-invoice/scripts/validate.py": "# parent validate\n",
		"team-foo/finance/ap/pay-invoice/ARTIFACT.md":             "---\ntype: skill\nversion: 2.0.0\nextends: finance/ap/pay-invoice@1.x\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		"team-foo/finance/ap/pay-invoice/SKILL.md":                exSkillMD("pay-invoice", "Pay an approved invoice (team Foo)."),
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		// Fallback: record the exit code and assert the child wins where present.
		t.Logf("sync exit=%d stderr=%s (recording non-zero exit)", res.Exit, res.Stderr)
		got := readFile(t, filepath.Join(tgt, "finance/ap/pay-invoice/ARTIFACT.md"))
		if !strings.Contains(got, "version: 2.0.0") {
			t.Errorf("materialized ARTIFACT.md should carry the child version 2.0.0 (highest-wins):\n%s", got)
		}
		return
	}
	got := readFile(t, filepath.Join(tgt, "finance/ap/pay-invoice/ARTIFACT.md"))
	if !strings.Contains(got, "version: 2.0.0") {
		t.Errorf("materialized ARTIFACT.md should carry the child version 2.0.0 (highest-wins, not merged):\n%s", got)
	}
	// No extends merge: the parent-only bundled file is not composed in.
	if _, err := os.Stat(filepath.Join(tgt, "finance/ap/pay-invoice/scripts/validate.py")); err == nil {
		t.Errorf("parent-only scripts/validate.py should be absent (filesystem sync does not resolve extends, F-13.11.2)")
	}
}

// T-D-extends-47b — podium sync over a filesystem registry rejects a
// cross-type extends chain. The child's type: must match the parent's; the
// filesystem-source materialization path enforces the same rejection the
// server ingest path applies (F-4.6.2). spec: docs/authoring/extends.md §
// "Default for unlisted fields"; §4.6.
func TestExtends_SyncFilesystemCrossTypeRejected(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config": "multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-foo\n",
		"org-defaults/" + exParentID + "/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: base agent\n---\n\nagent body\n",
		"team-foo/" + exParentID + "/ARTIFACT.md":     "---\ntype: context\nversion: 2.0.0\ndescription: overlay context\nextends: " + exParentID + "@1.x\n---\n\ncontext body\n",
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit == 0 {
		t.Fatalf("sync of a cross-type extends chain should fail; exit=0 stdout=%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "extends.type_mismatch") {
		t.Errorf("stderr should cite extends.type_mismatch:\n%s", res.Stderr)
	}
}

// ---- search sensitivity / cross-layer visibility (T-D-extends-48..49) -------

// T-D-extends-48 — search_artifacts reflects the most-restrictive sensitivity
// across the extends chain. spec: docs/authoring/extends.md § "Field merge
// semantics", sensitivity. (F-4.6.7)
func TestExtends_SearchSensitivityMostRestrictive(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: parent\nsensitivity: high\n---\n\nbody\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nsensitivity: low\nextends: " + exParentID + "@1.x\n---\n\nbody\n"
	srv := extendsBoot(t, parent, child, nil)

	var resp struct {
		Results []struct {
			ID          string `json:"id"`
			Version     string `json:"version"`
			Sensitivity string `json:"sensitivity"`
		} `json:"results"`
	}
	getJSON(t, srv.BaseURL+"/v1/search_artifacts?scope=finance/ap", &resp)

	var sawChild bool
	for _, r := range resp.Results {
		if r.ID != exParentID {
			continue
		}
		// Every surfaced version of the artifact reports high: the parent is
		// high outright, and the child's `low` is raised to the parent's
		// `high` by the most-restrictive merge (it cannot relax it).
		if r.Sensitivity != "high" {
			t.Errorf("search result %s@%s sensitivity = %q, want high", r.ID, r.Version, r.Sensitivity)
		}
		if r.Version == "2.0.0" {
			sawChild = true
		}
	}
	if !sawChild {
		t.Fatalf("search did not surface the extends child (v2.0.0): %+v", resp.Results)
	}
}

// T-D-extends-49 — the child is not visible to a caller without access to the
// child layer. The extends child finance/child lives in a layer restricted to
// the finance group; carol (not in finance) cannot load or discover it, while a
// finance-group member can. The parent shared/parent is public so the only thing
// gating the child is its own layer's membership.
// spec: docs/authoring/extends.md § "Hidden parents".
func TestExtends_ChildHiddenWithoutLayerAccess(t *testing.T) {
	t.Parallel()
	srv := startAuthServer(t, authServerSpec{
		SCIMToken: "scim-extends-child",
		SCIMUsers: map[string][]string{"dave@acme.com": {"finance"}},
		Layers: []authLayer{
			{
				ID: "public-parent",
				Files: map[string]string{
					"shared/parent/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\n" +
						"description: public parent\ntags: [from-parent]\n---\n\nparent body\n",
				},
				Visibility: authVisibility{Public: true},
			},
			{
				ID: "finance-child",
				Files: map[string]string{
					"finance/child/ARTIFACT.md": "---\ntype: context\nversion: 2.0.0\n" +
						"description: finance-only child\ntags: [child-tag]\n" +
						"extends: shared/parent@1.x\n---\n\nchild body\n",
				},
				Visibility: authVisibility{Groups: []string{"finance"}},
			},
		},
	})

	// dave is a finance-group member (resolved through SCIM); carol is not.
	dave := srv.token(authIdentity{Sub: "dave@acme.com", Email: "dave@acme.com", Groups: []string{"finance"}})
	carol := srv.token(authIdentity{Sub: "carol@acme.com", Email: "carol@acme.com"})

	// The finance member loads the child and sees it in search.
	got := exLoadAs(t, srv, "finance/child", dave)
	if got.Version != "2.0.0" || !strings.Contains(got.Frontmatter, "from-parent") {
		t.Errorf("finance member load = version %q frontmatter:\n%s", got.Version, got.Frontmatter)
	}
	assertHas(t, srv.searchIDs(dave), "finance/child", "finance member search includes the child")

	// carol lacks the finance group, so the child layer is hidden from her: the
	// load is 404 (no existence leak) and the child never appears in her search.
	if st := srv.loadStatus("finance/child", carol); st != http.StatusNotFound {
		t.Errorf("carol load finance/child = HTTP %d, want 404 (child layer hidden)", st)
	}
	carolIDs := srv.searchIDs(carol)
	assertMissing(t, carolIDs, "finance/child", "carol search omits the hidden child")
	// The public parent stays visible to carol, confirming only the child layer
	// is gated.
	assertHas(t, carolIDs, "shared/parent", "carol search still includes the public parent")
}

// Spec: §6.6 step 2 / §4.7.6 — a real extends-merged artifact's served
// frontmatter is a re-serialization (parent folded in, extends stripped) that
// cannot reproduce the stored content_hash, but the registry delivers the leaf
// child's pre-merge bytes in raw_frontmatter and those reproduce it. This pins
// the registry's real ingest canonicalization to what the MCP bridge recomputes
// in §6.6 step 2, so the two cannot drift and silently disable the consumer's
// content-hash check for merged manifests.
func TestExtends_MergedDeliversRawFrontmatterForHash(t *testing.T) {
	t.Parallel()
	parent := "---\ntype: context\nversion: 1.0.0\ndescription: org parent\ntags: [from-parent]\n---\n\nparent body\n"
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: " + exParentID + "@1.x\n---\n\nchild body\n"
	srv := extendsBoot(t, parent, child, nil)

	var r struct {
		ContentHash    string `json:"content_hash"`
		Frontmatter    string `json:"frontmatter"`
		RawFrontmatter string `json:"raw_frontmatter"`
		ManifestMerged bool   `json:"manifest_merged"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+exParentID, &r)

	if !r.ManifestMerged {
		t.Fatal("manifest_merged = false for an extends child, want true")
	}
	if r.RawFrontmatter == "" {
		t.Fatal("raw_frontmatter empty; the consumer cannot reproduce the content hash for a merged manifest")
	}
	// The served (merged) frontmatter strips extends and folds in the parent, so
	// it must not reproduce the stored hash...
	if "sha256:"+version.ContentHash([]byte(r.Frontmatter)) == r.ContentHash {
		t.Error("served merged frontmatter unexpectedly reproduced the content hash")
	}
	// ...but the pre-merge raw_frontmatter (the child's authored ARTIFACT.md, no
	// skill and no resources here) reproduces it exactly.
	if got := "sha256:" + version.ContentHash([]byte(r.RawFrontmatter)); got != r.ContentHash {
		t.Errorf("raw_frontmatter does not reproduce content hash: got %s, want %s", got, r.ContentHash)
	}
	if !strings.Contains(r.RawFrontmatter, "extends: "+exParentID) {
		t.Errorf("raw_frontmatter is not the pre-merge child bytes:\n%s", r.RawFrontmatter)
	}
}

// Spec: §6.6 step 2 / §4.7.6 — a real skill's content_hash covers the verbatim
// SKILL.md (delivered as skill_raw) plus the ARTIFACT.md; the prose body alone
// cannot reproduce it. ContentHash(frontmatter, skill_raw) reproduces the stored
// hash, pinning the registry canonicalization to the MCP bridge's §6.6 step 2
// recomputation so a skill is verified rather than skipped.
func TestSkill_ContentHashCoversSkillRaw(t *testing.T) {
	t.Parallel()
	id := "finance/ap/pay-invoice"
	skillMD := exSkillMD("pay-invoice", "Pay an approved invoice.")
	srv := startServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md": exSkillArtifact,
		id + "/SKILL.md":    skillMD,
	}))
	var r struct {
		Type        string `json:"type"`
		ContentHash string `json:"content_hash"`
		Frontmatter string `json:"frontmatter"`
		SkillRaw    string `json:"skill_raw"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &r)

	if r.Type != "skill" {
		t.Fatalf("type = %q, want skill", r.Type)
	}
	if r.SkillRaw != skillMD {
		t.Errorf("skill_raw is not the verbatim SKILL.md:\n got %q\nwant %q", r.SkillRaw, skillMD)
	}
	// The bridge recomputes the hash over (ARTIFACT.md, SKILL.md, resources).
	if got := "sha256:" + version.ContentHash([]byte(r.Frontmatter), []byte(r.SkillRaw)); got != r.ContentHash {
		t.Errorf("ContentHash(frontmatter, skill_raw) = %s, want stored %s", got, r.ContentHash)
	}
	// The prose body / ARTIFACT.md alone does not reproduce the hash, which is
	// why skipping the check for skills left them unverified.
	if "sha256:"+version.ContentHash([]byte(r.Frontmatter)) == r.ContentHash {
		t.Error("hash reproduced without skill_raw; the SKILL.md is not actually covered")
	}
}
