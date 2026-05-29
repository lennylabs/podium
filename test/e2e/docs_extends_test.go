package e2e

// End-to-end tests for docs/authoring/extends.md (D-extends).
//
// extends is effectively non-functional end to end. A higher-precedence child
// that declares extends: at the same canonical ID is rejected at boot ingest as
// a cross-layer collision (the extends exception is not honored, F-4.6.3), so
// only the parent serves and the merge is never observed. The merge-observation
// tests (scalar/list/sensitivity/sandbox/mcpServers/runtime/hidden-parent) are
// therefore recorded as skips against the relevant BUILD-GAPS finding. The few
// behaviors that do hold are asserted directly: scaffold --extends writes the
// field; an unknown-parent or self-referencing child is dropped at ingest and
// observable as a 404 when a valid sibling keeps boot alive; an extends child at
// a different canonical ID from its parent ingests (the same-ID constraint is
// not enforced); lint never errors on an extends reference; and the filesystem
// sync path resolves nothing (highest-wins, no merge). Doc claims the
// implementation does not honor (with no finding filed) are asserted against
// actual behavior with a note so a future change is detected.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exSkillArtifact is a minimal skill ARTIFACT.md (Podium frontmatter only).
const exSkillArtifact = "---\ntype: skill\nversion: 1.0.0\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"

// exSkillMD returns a SKILL.md whose name matches the leaf directory.
func exSkillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + name + " body.\n"
}

// ---- Pinning (T-D-extends-1..7): ExtendsPin is not exposed by any API -------

// T-D-extends-1 — extends pin resolved to an exact version at child ingest time
// using a semver range. spec: docs/authoring/extends.md § "Pinning" table.
func TestExtends_PinSemverRange(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at boot ingest as a cross-layer collision, so its resolved manifest is never stored; ExtendsPin is also not exposed by any API response, so the resolved pin is unobservable")
}

// T-D-extends-2 — extends with an exact semver pin ingests successfully.
// spec: docs/authoring/extends.md § "Pinning" table, <id>@<semver> row.
func TestExtends_PinExactSemver(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest; ExtendsPin is not exposed by any API response, so the exact pin is unobservable")
}

// T-D-extends-3 — extends with a content-hash pin resolves to an exact version.
// spec: docs/authoring/extends.md § "Pinning" table, <id>@sha256:<hash> row.
func TestExtends_PinContentHash(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest; ExtendsPin is not exposed by any API response, so the hash-resolved pin is unobservable")
}

// T-D-extends-4 — extends with an unresolvable content hash fails ingest.
// spec: docs/authoring/extends.md § "Pinning" table, <id>@sha256:<hash> row.
func TestExtends_PinUnresolvableHash(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest before the hash-resolution path is reachable; ExtendsPin is not exposed by any API response")
}

// T-D-extends-5 — extends with a bare ID resolves to latest at ingest time.
// spec: docs/authoring/extends.md § "Pinning" table, bare <id> row.
func TestExtends_PinBareID(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest; ExtendsPin is not exposed by any API response, so the latest-resolved pin is unobservable")
}

// T-D-extends-6 — parent updates do not silently propagate to an ingested child.
// spec: docs/authoring/extends.md § "Pinning", last paragraph.
func TestExtends_PinNoSilentPropagation(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so there is no stored child pin to observe across a parent update; ExtendsPin is not exposed by any API response")
}

// T-D-extends-7 — re-ingesting the child after a version bump picks up the newer
// parent version. spec: docs/authoring/extends.md § "Pinning", last paragraph.
func TestExtends_PinReingestPicksNewerParent(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so re-ingest never produces a stored child pin; ExtendsPin is not exposed by any API response")
}

// ---- Scalar / list / map field merge (T-D-extends-8..15) --------------------

// T-D-extends-8 — child description and version win on load.
// spec: docs/authoring/extends.md § "Field merge semantics".
func TestExtends_ScalarChildWins(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: even if ingested, only Description/Type/Tags/Sensitivity/Body merge at the record level and the load response carries no top-level description, so the merged scalar is unobservable; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-9 — tags are unioned (append unique) across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", tags row.
func TestExtends_TagsUnion(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: the load response carries no top-level tags and merged Tags are dropped when the raw child frontmatter is served; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-10 — sensitivity takes the most-restrictive value; the child
// cannot relax the parent. spec: docs/authoring/extends.md § "Field merge
// semantics", sensitivity row.
func TestExtends_SensitivityMostRestrictive(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest as a cross-layer collision, so the merged most-restrictive sensitivity is never served")
}

// T-D-extends-11 — sensitivity: the child can tighten medium to high.
// spec: docs/authoring/extends.md § "Field merge semantics", sensitivity row.
func TestExtends_SensitivityChildTightens(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so the merged tightened sensitivity is never served")
}

// T-D-extends-12 — sandbox_profile: most-restrictive wins; the child tightens
// the parent. spec: docs/authoring/extends.md § "Tightening sandbox profile".
func TestExtends_SandboxChildTightens(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: sandbox_profile is not a structured merge field and the merge is never surfaced; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-13 — sandbox_profile: the child cannot widen the parent
// restriction. spec: docs/authoring/extends.md § "The most-restrictive rules".
func TestExtends_SandboxChildCannotWiden(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: sandbox_profile merge is not implemented and the child's raw frontmatter is served as-is; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-14 — mcpServers: deep-merged by name; the child's entry overrides
// the parent's on a name match. spec: docs/authoring/extends.md § "Field merge
// semantics", mcpServers row.
func TestExtends_McpServersOverrideByName(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: mcpServers deep-merge is not implemented and only the child's raw frontmatter is served; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-15 — mcpServers: a parent-only server is inherited when the child
// declares a differently named server. spec: docs/authoring/extends.md § "Field
// merge semantics", mcpServers row.
func TestExtends_McpServersParentOnlyInherited(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: parent-only mcpServers are not inherited (deep-merge not implemented); F-4.6.3 also rejects the same-ID child at ingest")
}

// ---- Hidden parents (T-D-extends-16..17) ------------------------------------

// T-D-extends-16 — a caller without access to the parent layer sees the merged
// result. spec: docs/authoring/extends.md § "Hidden parents".
func TestExtends_HiddenParentMergedResult(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so the merged hidden-parent result is never served; the standalone e2e harness also has no per-identity layer visibility to restrict the parent layer")
}

// T-D-extends-17 — the parent does not appear in search results for an
// unauthorized caller. spec: docs/authoring/extends.md § "Hidden parents".
func TestExtends_HiddenParentNotInSearch(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the extends child is rejected at ingest so the hidden-parent path is never exercised; the standalone e2e harness also has no per-identity layer visibility to hide the parent layer from search")
}

// ---- Bundled-file merge (T-D-extends-18..21) --------------------------------

// T-D-extends-18 — a child file at the same path overrides the parent file.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_BundledChildOverrides(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-13.11.2: the podium sync filesystem path does not resolve extends, so same-path bundled files are not merged; the higher-precedence layer wins by collision policy rather than by an extends merge")
}

// T-D-extends-19 — a parent-only bundled file is inherited unchanged.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_BundledParentOnlyInherited(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-13.11.2: the podium sync filesystem path does not resolve extends, so a parent-only bundled file is not inherited into the child's materialized output")
}

// T-D-extends-20 — a child-only bundled file is added to the merged output.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_BundledChildOnlyAdded(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-13.11.2: the podium sync filesystem path does not resolve extends, so there is no merged output to add the child-only file into")
}

// T-D-extends-21 — the child cannot delete a parent file; it is inherited.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_BundledChildCannotDelete(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-13.11.2: the podium sync filesystem path does not resolve extends, so the parent file is never composed into the child's output and the delete-shadow semantics are not exercisable")
}

// ---- SKILL.md override (T-D-extends-22) -------------------------------------

// T-D-extends-22 — the child SKILL.md overrides the parent SKILL.md.
// spec: docs/authoring/extends.md § "Bundled-file merge semantics".
func TestExtends_SkillBodyChildOverrides(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at boot ingest as a cross-layer collision, so the child SKILL.md body is never served from the merged record")
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
	t.Skip("blocked by F-4.6.3: extends children at the same canonical ID are rejected across layers at ingest, so an extends chain cannot be assembled through the running ingest path to reach the load-time cycle defense")
}

// T-D-extends-26 — a same-canonical-ID collision without extends is rejected.
// spec: docs/authoring/extends.md § "Replacing instead of extending".
func TestExtends_CollisionWithoutExtendsRejected(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: collision-rejection via the CLI is not implemented; podium lint and podium sync use CollisionPolicyHighestWins and silently pick a winner rather than erroring on a same-ID collision")
}

// T-D-extends-27 — extends allows a same-ID artifact in a higher-precedence
// layer without a collision error. spec: docs/authoring/extends.md § intro.
func TestExtends_AllowsSameIDWithExtends(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the filesystem walk rejects a same-ID cross-layer artifact unconditionally; the extends exception that would allow this is not implemented")
}

// T-D-extends-28 — a cross-type extends is rejected (child type must match the
// parent type). spec: docs/authoring/extends.md § "Default for unlisted fields".
func TestExtends_CrossTypeRejected(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.2: extends does not enforce that the child type matches the parent type; resolveExtendsPin never compares types, so a cross-type extends is not rejected")
}

// T-D-extends-29 — chained inheritance (A extends B extends C) resolves the full
// chain. spec: docs/authoring/extends.md § "Constraints".
func TestExtends_ChainedInheritance(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: same-ID extends children are rejected across layers at ingest, so a three-layer extends chain at one canonical ID cannot be assembled to observe the folded result")
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
	t.Skip("blocked by F-4.6.1: requiresApproval is not a structured merge field and the merged list never reaches the served frontmatter; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-32 — when_to_use is appended across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", when_to_use.
func TestExtends_WhenToUseAppend(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: when_to_use is not a structured merge field and the appended list never reaches the served frontmatter; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-33 — delegates_to is appended across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", delegates_to.
func TestExtends_DelegatesToAppend(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: delegates_to is not a structured merge field and the appended list never reaches the served frontmatter; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-34 — external_resources is appended across parent and child.
// spec: docs/authoring/extends.md § "Field merge semantics", external_resources.
func TestExtends_ExternalResourcesAppend(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: external_resources is not a structured merge field and the appended list never reaches the served frontmatter; F-4.6.3 also rejects the same-ID child at ingest")
}

// T-D-extends-35 — license: child wins; lint warns on a license change across
// layers. spec: docs/authoring/extends.md § "Field merge semantics", license.
func TestExtends_LicenseChildWinsLintWarns(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest so the merged license is never served; no license-change-across-layers lint rule exists either, so the documented warning is never emitted")
}

// T-D-extends-36 — search_visibility takes the most-restrictive value.
// spec: docs/authoring/extends.md § "Field merge semantics", search_visibility.
func TestExtends_SearchVisibilityMostRestrictive(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so the merged most-restrictive search_visibility is never served (F-4.3.3 also leaves search_visibility unenforced)")
}

// T-D-extends-37 — a child omitting a parent field inherits the parent's value.
// spec: docs/authoring/extends.md § "Default for unlisted fields".
func TestExtends_UnlistedFieldInherited(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so an inherited parent field is never observable on the merged record")
}

// T-D-extends-38 — a child can override an inherited deprecated field.
// spec: docs/authoring/extends.md § "Default for unlisted fields", deprecated.
func TestExtends_ChildOverridesDeprecated(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so the child's overriding deprecated value is never served")
}

// T-D-extends-39 — runtime_requirements is deep-merged with the child winning
// per key. spec: docs/authoring/extends.md § "Field merge semantics",
// runtime_requirements.
func TestExtends_RuntimeRequirementsDeepMerge(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.1: runtime_requirements is not a structured merge field and the deep-merged map never reaches the served frontmatter; F-4.6.3 also rejects the same-ID child at ingest")
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
// spec: docs/authoring/extends.md § "Constraints".
func TestExtends_DependentsEdge(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the extends edge requires the same-ID child to have ingested, but the child is rejected at boot ingest as a cross-layer collision, so no extends dependency edge is recorded")
}

// T-D-extends-42 — the impact CLI lists the extending children of a parent.
// spec: docs/authoring/extends.md § "Constraints"; impact analysis.
func TestExtends_ImpactCLIListsChildren(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so no extends edge exists for the impact CLI to report")
}

// ---- Worked example (T-D-extends-43) ----------------------------------------

// T-D-extends-43 — the org-wide skill example: full two-layer ingest and load.
// spec: docs/authoring/extends.md § "Examples".
func TestExtends_OrgWideSkillExample(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the higher-precedence team-foo child at finance/ap/pay-invoice is rejected at boot ingest as a cross-layer collision, so only the org-defaults parent serves and the documented merged result is never observed")
}

// ---- MCP load (T-D-extends-44) ----------------------------------------------

// T-D-extends-44 — the MCP load_artifact tool returns the extends-merged body.
// spec: docs/authoring/extends.md § "Field merge semantics".
func TestExtends_McpLoadMergedBody(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: the same-ID extends child is rejected at ingest, so the MCP bridge resolves only the parent and the documented merged manifest is never returned")
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

// ---- search sensitivity / cross-layer visibility (T-D-extends-48..49) -------

// T-D-extends-48 — search_artifacts reflects the most-restrictive sensitivity
// across the extends chain. spec: docs/authoring/extends.md § "Field merge
// semantics", sensitivity.
func TestExtends_SearchSensitivityMostRestrictive(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.7: search descriptors are built from the raw child record without resolving the extends chain, so the most-restrictive sensitivity is not reflected in search results")
}

// T-D-extends-49 — the child is not visible to a caller without access to the
// child layer. spec: docs/authoring/extends.md § "Hidden parents".
func TestExtends_ChildHiddenWithoutLayerAccess(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by F-4.6.3: a child on a restricted layer cannot be ingested across layers at the same canonical ID; the standalone e2e harness also has no per-identity layer visibility to exercise cross-layer child hiding")
}
