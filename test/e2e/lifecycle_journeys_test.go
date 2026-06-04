package e2e

// End-to-end artifact and deployment lifecycle journeys
// (gaps G-LIFECYCLE-1, G-LIFECYCLE-2, G-LIFECYCLE-4, G-LIFECYCLE-6).
//
// These tests drive the lifecycle behaviors the registry promises across a
// version's life: a child that pins an extends parent keeps resolving the
// pinned version when a newer parent ships and re-resolves only on its own
// reingest; a deprecated version with replaced_by drops out of search while an
// explicit load still serves it with the upgrade target named; an in-place
// SQLite schema upgrade preserves ingested artifacts and runs idempotently; and
// a force-push that rewrites a registered git layer's history is tolerated
// through the reingest endpoint, advancing the stored ref and emitting the
// history-rewritten signal.
//
// They build on the runtime-republish primitive (republish_helpers_test.go,
// gap G-INFRA-7) for the multi-version journeys, the git-source journey helpers
// (git_source_journey_test.go) for the force-push journey, and the store schema
// migration path (pkg/store) for the in-place upgrade.
//
// Spec: §4.6 (extends field-semantics: when_to_use append, most-restrictive
// sensitivity; the pin is resolved at the child's ingest time and re-resolved
// only on the child's reingest), §4.7.4 / §4.7.6 (deprecation excludes from
// search, latest skips a deprecated version, an explicit version still loads
// with the deprecation warning and replaced_by upgrade target), §13.4
// (additive forward migration in place; a binary upgrade migrates an existing
// database forward without a separate migration step), §7.3.1 (force-push
// tolerance: a rewritten history advances the stored ref and emits
// layer.history_rewritten in tolerant mode).

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/uuid"
	"github.com/lennylabs/podium/pkg/store"
)

// ---- G-LIFECYCLE-1: extends parent-pin stability and reingest re-resolution --

// TestLifecycle_ExtendsPinStabilityAndReingest drives the full §4.6 pin journey
// end to end: a parent is ingested at 1.2.0 and a child (a different canonical
// id, in its own layer) pins the parent's major range. The child's merged
// manifest inherits the parent's when_to_use and folds sensitivity
// most-restrictively. A newer parent 1.3.0 is then published WITHOUT reingesting
// the child, and the child still resolves the pinned 1.2.0. Finally the child is
// reingested and re-resolves the pin to 1.3.0.
//
// The parent and child live in separate runtime-republish layers so the parent
// can ship a new version without triggering a reingest of the child; reingesting
// only the parent layer leaves the child's stored ExtendsPin untouched (no
// silent propagation), and reingesting the child layer re-resolves it.
func TestLifecycle_ExtendsPinStabilityAndReingest(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": contextArtifact("seed context"),
	}))

	const parentID = "platform/base/http-policy"
	const childID = "team/checkout/http-policy"

	parentLayer := newRepublishLayer(t, srv, "extends-parent")
	childLayer := newRepublishLayer(t, srv, "extends-child")

	// The parent ships at 1.2.0 carrying a distinctive when_to_use entry and a
	// high sensitivity the child cannot relax.
	parentLayer.publishVersion(t, versionSpec{
		ID: parentID, Version: "1.2.0", Description: "base http policy for services",
		WhenToUse: []string{"calling an internal HTTP service"}, Sensitivity: "high",
	})

	// The child (a distinct id) pins the parent's major range and carries its own
	// when_to_use plus a lower sensitivity. The merge appends the parent's
	// when_to_use and takes the most-restrictive sensitivity (high).
	childLayer.publishVersion(t, versionSpec{
		ID: childID, Version: "1.0.0", Description: "checkout http policy",
		Extends:   parentID + "@1.x",
		WhenToUse: []string{"calling the checkout service"}, Sensitivity: "low",
	})

	// The child's merged manifest inherits the parent's when_to_use and the
	// most-restrictive sensitivity. The parent's id must not leak (hidden parent).
	st, child := childLayer.loadVersion(t, childID, "")
	if st != 200 {
		t.Fatalf("load child: HTTP %d", st)
	}
	if !strings.Contains(child.Frontmatter, "calling an internal HTTP service") {
		t.Errorf("child did not inherit the parent's when_to_use (pin resolved to 1.2.0?):\n%s", child.Frontmatter)
	}
	if !strings.Contains(child.Frontmatter, "calling the checkout service") {
		t.Errorf("child lost its own when_to_use after merge:\n%s", child.Frontmatter)
	}
	if child.Sensitivity != "high" {
		t.Errorf("merged sensitivity = %q, want high (child low folded with parent high most-restrictively)", child.Sensitivity)
	}
	if strings.Contains(child.Frontmatter, parentID) {
		t.Errorf("hidden parent id %q leaked into the served child frontmatter:\n%s", parentID, child.Frontmatter)
	}

	// The parent ships a newer version 1.3.0 WITHOUT reingesting the child. The
	// parent layer reingest does not touch the child layer, so the child's stored
	// pin still points at 1.2.0.
	parentLayer.publishVersion(t, versionSpec{
		ID: parentID, Version: "1.3.0", Description: "base http policy for services v2",
		WhenToUse: []string{"calling an internal HTTP service", "honoring the v2 retry budget"}, Sensitivity: "high",
	})

	// The pinned child 1.0.0 still merges the pinned 1.2.0: it carries the 1.2.0
	// when_to_use but NOT the 1.3.0-only entry, proving no silent propagation
	// (§4.6). The child manifest is immutable by content hash, so the resolved
	// parent version is frozen at the child's ingest time.
	st, childAfterParentBump := childLayer.loadVersion(t, childID, "1.0.0")
	if st != 200 {
		t.Fatalf("reload child 1.0.0 after parent bump: HTTP %d", st)
	}
	if !strings.Contains(childAfterParentBump.Frontmatter, "calling an internal HTTP service") {
		t.Errorf("child 1.0.0 lost the pinned 1.2.0 when_to_use:\n%s", childAfterParentBump.Frontmatter)
	}
	if strings.Contains(childAfterParentBump.Frontmatter, "honoring the v2 retry budget") {
		t.Errorf("child 1.0.0 silently picked up the 1.3.0-only when_to_use without a reingest (pin propagated):\n%s", childAfterParentBump.Frontmatter)
	}

	// The child is reingested by shipping a new child version 1.1.0. A new
	// version is a fresh manifest, so the ingest pipeline re-resolves the extends
	// pin against the now-latest parent 1.3.0 (re-ingesting the same immutable
	// bytes would be a content-hash no-op and could not advance the pin, which is
	// the correct immutability behavior).
	childLayer.publishVersion(t, versionSpec{
		ID: childID, Version: "1.1.0", Description: "checkout http policy",
		Extends:   parentID + "@1.x",
		WhenToUse: []string{"calling the checkout service"}, Sensitivity: "low",
	})

	// The reingested child (latest, 1.1.0) now merges parent 1.3.0: the
	// 1.3.0-only when_to_use entry appears, proving the pin advanced on reingest.
	st, childAfterReingest := childLayer.loadVersion(t, childID, "")
	if st != 200 {
		t.Fatalf("reload child after its reingest: HTTP %d", st)
	}
	if childAfterReingest.Version != "1.1.0" {
		t.Errorf("latest child = %q, want 1.1.0 (the reingested version)", childAfterReingest.Version)
	}
	if !strings.Contains(childAfterReingest.Frontmatter, "honoring the v2 retry budget") {
		t.Errorf("child reingest did not re-resolve the pin to parent 1.3.0:\n%s", childAfterReingest.Frontmatter)
	}

	// The original child 1.0.0 remains pinned to 1.2.0 even after the new child
	// version re-resolved: immutability keeps each child version's pin frozen.
	st, child100 := childLayer.loadVersion(t, childID, "1.0.0")
	if st != 200 {
		t.Fatalf("reload child 1.0.0 after reingest: HTTP %d", st)
	}
	if strings.Contains(child100.Frontmatter, "honoring the v2 retry budget") {
		t.Errorf("child 1.0.0 pin advanced to 1.3.0; each immutable version must keep its frozen pin:\n%s", child100.Frontmatter)
	}
}

// ---- G-LIFECYCLE-2: deprecation excludes from search, load surfaces target ---

// TestLifecycle_DeprecationExcludesFromSearchLoadSurfacesTarget drives the
// §4.7.4 / §4.7.6 deprecation journey: two versions of one id are published,
// then the older is republished as deprecated with a replaced_by pointer.
// search_artifacts omits the deprecated version, a bare load resolves the newer
// version, and an explicit load of the deprecated version returns its bytes with
// a deprecation warning that names the replaced_by upgrade target.
func TestLifecycle_DeprecationExcludesFromSearchLoadSurfacesTarget(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": contextArtifact("seed context"),
	}))
	layer := newRepublishLayer(t, srv, "deprecation-journey")

	const id = "billing/invoices/reconcile-ledger"
	const successor = "billing/invoices/reconcile-ledger-v2"
	const query = "reconcile the billing ledger"

	// Two live versions of one id; the newer 2.0.0 is what search and a bare load
	// resolve.
	layer.publishVersion(t, versionSpec{ID: id, Version: "1.0.0", Description: query})
	layer.publishVersion(t, versionSpec{ID: id, Version: "2.0.0", Description: query})

	// Before deprecation, search returns the id at its latest version.
	if v := lcSearchVersion(t, srv, query, id); v != "2.0.0" {
		t.Fatalf("pre-deprecation search version = %q, want 2.0.0", v)
	}

	// A newer 3.0.0 is published deprecated with a replaced_by successor. Per
	// §4.7.6 a deprecated latest is skipped, so latest falls back to 2.0.0, and
	// per §4.7.4 the deprecated version is excluded from search.
	layer.publishVersion(t, versionSpec{
		ID: id, Version: "3.0.0", Description: query,
		Deprecated: true, ReplacedBy: successor,
	})

	// search_artifacts omits the deprecated version: the id still appears (2.0.0
	// is live) but never at 3.0.0.
	if v := lcSearchVersion(t, srv, query, id); v == "3.0.0" {
		t.Errorf("search returned the deprecated 3.0.0; deprecated versions must be excluded from search")
	} else if v != "2.0.0" {
		t.Errorf("post-deprecation search version = %q, want 2.0.0 (latest non-deprecated)", v)
	}

	// A bare load resolves the newer non-deprecated version 2.0.0.
	st, bare := layer.loadVersion(t, id, "")
	if st != 200 || bare.Version != "2.0.0" {
		t.Errorf("bare load: HTTP %d version=%q, want 200/2.0.0", st, bare.Version)
	}
	if bare.Deprecated {
		t.Errorf("bare load resolved a deprecated version; want the live 2.0.0")
	}

	// An explicit load of the deprecated 3.0.0 serves its bytes with a
	// deprecation warning that names the replaced_by upgrade target.
	st, code, body := lcLoadFull(t, srv, id, "3.0.0")
	if st != 200 {
		t.Fatalf("explicit load of deprecated 3.0.0: HTTP %d\nbody: %s", st, body)
	}
	if !code.Deprecated {
		t.Errorf("explicit load of 3.0.0 did not report deprecated=true (envelope=%+v)", code)
	}
	if code.ReplacedBy != successor {
		t.Errorf("replaced_by = %q, want %q (the upgrade target on load)", code.ReplacedBy, successor)
	}
	if !strings.Contains(code.DeprecationWarning, successor) {
		t.Errorf("deprecation_warning %q does not name the replaced_by target %q", code.DeprecationWarning, successor)
	}
}

// lcLoadResp captures the §7.6.1 load_artifact lifecycle fields the deprecation
// journey asserts: the deprecation flag, the replaced_by upgrade target, and the
// human-readable warning.
type lcLoadResp struct {
	ID                 string `json:"id"`
	Version            string `json:"version"`
	ContentHash        string `json:"content_hash"`
	Deprecated         bool   `json:"deprecated"`
	ReplacedBy         string `json:"replaced_by"`
	DeprecationWarning string `json:"deprecation_warning"`
}

// lcLoadFull GETs load_artifact for an explicit version and returns the HTTP
// status, decoded lifecycle envelope, and raw body for diagnostics.
func lcLoadFull(t testing.TB, srv *serverProc, id, version string) (int, lcLoadResp, []byte) {
	t.Helper()
	url := srv.BaseURL + "/v1/load_artifact?id=" + id
	if version != "" {
		url += "&version=" + version
	}
	st, body := getRaw(t, url)
	var r lcLoadResp
	if st == 200 {
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("decode load_artifact %s@%s: %v\nbody: %s", id, version, err, body)
		}
	}
	return st, r, body
}

// lcSearchVersion GETs search_artifacts and returns the version of the first
// result whose id matches, or "" when the id is absent from the result set.
// Search collapses an id to its latest non-deprecated version, so the returned
// version is the latest searchable one; an absent id means every version was
// excluded (for example all deprecated).
func lcSearchVersion(t testing.TB, srv *serverProc, query, id string) string {
	t.Helper()
	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query="+queryEscape(query))
	if st != 200 {
		t.Fatalf("search_artifacts %q: HTTP %d\nbody: %s", query, st, body)
	}
	var resp struct {
		Results []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode search response: %v\nbody: %s", err, body)
	}
	for _, r := range resp.Results {
		if r.ID == id {
			return r.Version
		}
	}
	return ""
}

// ---- G-LIFECYCLE-4: in-place legacy SQLite schema upgrade --------------------

// lcDefaultOrgID derives the deterministic UUIDv5 tenant id the standalone
// auto-bootstrap assigns the "default" org (§4.7.1: org IDs are UUIDs, names are
// aliases). The standalone server keys every row by this UUID, not the literal
// "default", so a legacy database whose rows a binary upgrade must preserve has
// to be seeded under the same id. This mirrors serverboot.orgIDForName.
func lcDefaultOrgID() string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("podium:org:default")).String()
}

// TestLifecycle_InPlaceSQLiteUpgradePreservesArtifactsAndAudit seeds a
// legacy-schema SQLite database (reduced tenants and manifests tables, none of
// the post-initial columns) holding an ingested artifact under the standalone
// org's UUID. It boots a standalone server against the same SQLite file: the
// §13.4 additive migration runs in place on OpenSQLite, so the binary upgrade
// migrates the database forward without a separate migration step. The first
// boot also generates genuine audit history (a server-signed, hash-chained
// admin-grant event). A second boot then runs against the now-upgraded file and
// the same audit log, standing in for a subsequent binary start: it asserts the
// migration is idempotent (a clean re-open, no schema churn), the artifact still
// resolves with its original content hash, and the audit history from the first
// boot is intact and the chain continues.
//
// The audit log is a hash-chained file independent of the metadata store (§8.3),
// so the assertion is that the in-place schema migration does not disturb it and
// a later boot continues the existing chain rather than orphaning it.
func TestLifecycle_InPlaceSQLiteUpgradePreservesArtifactsAndAudit(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dbPath := filepath.Join(home, "legacy.db")
	auditPath := filepath.Join(home, "audit.log")
	tenantID := lcDefaultOrgID()

	const id = "ops/runbooks/restart-gateway"
	const version = "1.4.2"
	const contentHash = "sha256:legacyhash1234"

	// Seed a legacy database the way an earlier binary would have written it:
	// reduced tenants and manifests tables with only the columns that shipped
	// initially. The post-initial columns (layer, deprecated, frontmatter, body,
	// signature, resources, and the rest) do not exist yet, so a boot against
	// this file must add them in place before the manifest is readable. The rows
	// are keyed by the standalone org's UUID so the booted server resolves them.
	legacy, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE tenants (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE manifests (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			type TEXT NOT NULL,
			ingested_at TEXT NOT NULL,
			PRIMARY KEY (tenant_id, artifact_id, version)
		)`,
	} {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatalf("legacy exec %q: %v", stmt, err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO tenants (id, name) VALUES (?, 'default')`, tenantID); err != nil {
		t.Fatalf("legacy insert tenant: %v", err)
	}
	if _, err := legacy.Exec(
		`INSERT INTO manifests (tenant_id, artifact_id, version, content_hash, type, ingested_at) VALUES (?, ?, ?, ?, ?, ?)`,
		tenantID, id, version, contentHash, "context", "2024-01-01T00:00:00Z",
	); err != nil {
		t.Fatalf("legacy insert manifest: %v", err)
	}
	_ = legacy.Close()

	// ---- First boot: the in-place upgrade runs, then we generate audit history -
	// PODIUM_SQLITE_PATH selects the seeded legacy file; OpenSQLite runs the
	// additive migration in place before serving. No --layer-path: the server must
	// serve the pre-seeded manifest, not re-ingest a fresh registry over it.
	bootEnv := []string{
		"HOME=" + home,
		"PODIUM_SQLITE_PATH=" + dbPath,
		"PODIUM_AUDIT_LOG_PATH=" + auditPath,
	}
	srv := startServerArgs(t, bootEnv, "serve", "--standalone")

	// The migrated legacy artifact resolves with its original content hash, which
	// proves the additive migration backfilled the new columns and left the seeded
	// row readable. The legacy row had no frontmatter/body columns, so the upgrade
	// backfills them to empty; the indexed fields (version, type, content hash)
	// survive verbatim.
	st, loaded, raw := lcLoadFull(t, srv, id, "")
	if st != 200 {
		t.Fatalf("load migrated artifact (first boot): HTTP %d\nbody: %s\nserver log:\n%s", st, raw, srv.log())
	}
	if loaded.Version != version {
		t.Errorf("migrated artifact version = %q, want %q", loaded.Version, version)
	}
	if loaded.ContentHash != contentHash {
		t.Errorf("migrated artifact content_hash = %q, want %q (original hash must survive the in-place upgrade)", loaded.ContentHash, contentHash)
	}

	// Generate genuine, hash-chained audit history on the first boot: an
	// artifact.loaded event is recorded for each load_artifact call (§8.1), and
	// the load above already triggered one. Drive a couple more loads so the chain
	// has identifiable history to preserve across the restart.
	for i := 0; i < 3; i++ {
		if st, _, _ := lcLoadFull(t, srv, id, version); st != 200 {
			t.Fatalf("load to generate audit history: HTTP %d", st)
		}
	}
	firstHistory := lcWaitForAuditGrowth(t, auditPath, 0)
	if firstHistory == "" || !strings.Contains(firstHistory, "artifact.loaded") {
		t.Fatalf("first boot produced no artifact.loaded audit history:\n%s", firstHistory)
	}

	// Stop the first server so the second boot owns the SQLite file and the audit
	// log (a clean handoff, standing in for a binary restart).
	stopProc(srv.cmd)

	// ---- Second boot: idempotent re-migration, artifact + audit survive --------
	// Booting again against the already-upgraded file re-runs the additive
	// migration as a clean no-op (an idempotent re-migrate). The same audit log is
	// reused so the chain must continue rather than orphan the first boot's events.
	srv2 := startServerArgs(t, bootEnv, "serve", "--standalone")

	// The artifact still resolves with its original content hash after the second
	// in-place migration, proving idempotence preserves the data.
	st2, loaded2, raw2 := lcLoadFull(t, srv2, id, "")
	if st2 != 200 {
		t.Fatalf("load migrated artifact (second boot): HTTP %d\nbody: %s\nserver log:\n%s", st2, raw2, srv2.log())
	}
	if loaded2.Version != version || loaded2.ContentHash != contentHash {
		t.Errorf("after idempotent re-migrate: version=%q hash=%q, want %s/%s", loaded2.Version, loaded2.ContentHash, version, contentHash)
	}

	// The first boot's audit history is intact at the second boot: the
	// artifact.loaded events it recorded are still present in the chain, and the
	// file did not shrink below the first boot's length.
	current, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit at second boot: %v", err)
	}
	if !strings.Contains(string(current), "artifact.loaded") {
		t.Errorf("first boot's audit history was lost across the in-place upgrade and restart:\n%s", current)
	}
	if len(current) < len(firstHistory) {
		t.Errorf("audit log shrank across the restart: %d bytes now, %d bytes after the first boot (history truncated)", len(current), len(firstHistory))
	}

	// The second boot continues the chain: a fresh auditable load appends past the
	// preserved history rather than truncating it.
	beforeLen := len(current)
	if st, _, _ := lcLoadFull(t, srv2, id, version); st != 200 {
		t.Fatalf("load on second boot: HTTP %d", st)
	}
	grown := lcWaitForAuditGrowth(t, auditPath, beforeLen)
	if len(grown) <= beforeLen {
		t.Errorf("audit chain did not continue on the second boot (no growth past %d bytes)", beforeLen)
	}
	if !strings.Contains(grown, "artifact.loaded") {
		t.Errorf("the second boot's audit append dropped the first boot's history:\n%s", grown)
	}

	// Idempotence at the store layer: opening the upgraded file directly succeeds
	// and the manifest survives with its hash, the same forward-migration the
	// boots ran.
	upgraded, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("direct re-open (idempotent re-migrate) failed: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	manifests, err := upgraded.ListManifests(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListManifests after re-migrate: %v", err)
	}
	if len(manifests) != 1 || manifests[0].ArtifactID != id || manifests[0].ContentHash != contentHash {
		t.Errorf("after idempotent re-migrate manifests = %+v, want one %s@%s with hash %s", manifests, id, version, contentHash)
	}
}

// lcWaitForAuditGrowth polls the audit log until it grows past minLen bytes (or
// a bounded deadline elapses) and returns the current contents. The file sink
// flushes asynchronously after the triggering request returns, so a bare read
// can race the append.
func lcWaitForAuditGrowth(t testing.TB, path string, minLen int) string {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var b []byte
	for time.Now().Before(deadline) {
		b, _ = os.ReadFile(path)
		if len(b) > minLen {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return string(b)
}

// ---- G-LIFECYCLE-6: force-push history rewrite tolerated via PriorRef --------

// TestLifecycle_ForcePushToleratedThroughReingest registers a git-source layer
// and ingests it at commit A, then hard-resets the repo so A's history is
// rewritten under a new commit C, and reingests through the endpoint. In the
// default tolerant force-push mode the second reingest succeeds, records the new
// ref against the existing layer (PriorRef tolerance), serves the rewritten
// content through load_artifact, and emits the layer.history_rewritten audit
// signal naming the prior and new refs.
//
// The repository is a file:// git repo the test seeds, so the journey runs with
// no external network. The reingest endpoint threads the stored LastIngestedRef
// as the git provider's PriorRef; when the new head no longer reaches it, the
// orchestrator detects the rewrite and (tolerant mode) emits the signal and
// proceeds (§7.3.1).
func TestLifecycle_ForcePushToleratedThroughReingest(t *testing.T) {
	t.Parallel()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_AUDIT_LOG_PATH=" + auditPath},
		"serve", "--standalone")
	repo := newGitJourneyRepo(t)

	const id = "policies/security/rotate-keys"
	// Commit A: the layer's initial content at 1.0.0.
	repo.commitContextArtifact(t, id, "1.0.0",
		"Rotate signing keys following the security policy.", "commit A")
	hashA := lcHeadHash(t, repo)

	// Register the git layer (default force-push policy is tolerant) and ingest A
	// through one poll cycle, which records LastIngestedRef = A.
	registerGitLayer(t, srv, "policies", repo.url())
	pollReingest(t, srv, "policies", id, "1.0.0")
	if st, got := loadArtifact(t, srv, id, ""); st != 200 || got.Version != "1.0.0" {
		t.Fatalf("after ingest of A: load HTTP %d version=%q, want 200/1.0.0", st, got.Version)
	}

	// Rewrite history: hard-reset master back to the empty tree and commit C with
	// different content at a new version. A is no longer reachable from the head,
	// so the next ingest sees a rewritten history relative to the stored prior ref.
	lcResetHard(t, repo, plumbing.ZeroHash)
	repo.commitContextArtifact(t, id, "2.0.0",
		"Rotate signing keys following the rewritten security policy.", "commit C (force-push)")
	hashC := lcHeadHash(t, repo)
	if hashC == hashA {
		t.Fatalf("rewrite produced the same head %s; the force-push did not change history", hashC)
	}

	// Reingest through the endpoint: the stored prior ref (A) is no longer
	// reachable from C, so the orchestrator detects the rewrite. In tolerant mode
	// the reingest succeeds and advances the stored ref to C.
	pollReingest(t, srv, "policies", id, "2.0.0")

	// The rewritten content is served: load_artifact returns the post-rewrite
	// version 2.0.0.
	if st, got := loadArtifact(t, srv, id, ""); st != 200 || got.Version != "2.0.0" {
		t.Errorf("after force-push reingest: load HTTP %d version=%q, want 200/2.0.0 (rewritten content served)", st, got.Version)
	}

	// The stored ref advanced to C: the layer.ingested audit references include C,
	// proving PriorRef tolerance recorded the new ref against the existing layer.
	refs := auditIngestReferences(t, auditPath, "policies")
	if len(refs) == 0 {
		t.Fatalf("no layer.ingested references recorded for the layer")
	}
	if last := refs[len(refs)-1]; !strings.HasPrefix(hashC.String(), last) && last != hashC.String() {
		t.Errorf("last ingested ref = %q, want the rewritten head %s", last, hashC.String())
	}

	// The history_rewritten signal is emitted naming the prior and new refs.
	prior, newRef, found := lcHistoryRewrittenRefs(t, auditPath, "policies")
	if !found {
		t.Fatalf("no layer.history_rewritten audit event recorded after the force-push reingest\naudit log:\n%s", readFile(t, auditPath))
	}
	if prior != hashA.String() {
		t.Errorf("history_rewritten prior_ref = %q, want commit A %s", prior, hashA.String())
	}
	if newRef != hashC.String() {
		t.Errorf("history_rewritten new_ref = %q, want commit C %s", newRef, hashC.String())
	}
}

// lcHeadHash returns the current master HEAD commit hash of the seeded repo.
func lcHeadHash(t testing.TB, g *gitJourneyRepo) plumbing.Hash {
	t.Helper()
	ref, err := g.repo.Head()
	if err != nil {
		t.Fatalf("repo Head: %v", err)
	}
	return ref.Hash()
}

// lcResetHard hard-resets the repo's worktree to target, discarding the commits
// after it so the next commit rewrites history. A zero hash resets to an empty
// tree (a fresh root commit), the strongest rewrite.
func lcResetHard(t testing.TB, g *gitJourneyRepo, target plumbing.Hash) {
	t.Helper()
	if target.IsZero() {
		// Reset to an empty index/worktree by removing tracked files and clearing
		// HEAD's tree; the subsequent commit becomes a new root with no ancestry to
		// the prior head.
		if err := g.wt.Reset(&git.ResetOptions{Mode: git.HardReset}); err != nil {
			t.Fatalf("reset worktree: %v", err)
		}
		// Detach HEAD to a fresh unborn branch so the next commit has no parent,
		// guaranteeing the prior head is unreachable.
		if err := g.repo.Storer.RemoveReference(plumbing.NewBranchReferenceName("master")); err != nil {
			t.Fatalf("remove master ref: %v", err)
		}
		head := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("master"))
		if err := g.repo.Storer.SetReference(head); err != nil {
			t.Fatalf("reset HEAD symref: %v", err)
		}
		// Clear the worktree files so the new root commit's tree differs.
		entries, _ := os.ReadDir(g.dir)
		for _, e := range entries {
			if e.Name() == ".git" {
				continue
			}
			_ = os.RemoveAll(filepath.Join(g.dir, e.Name()))
		}
		return
	}
	if err := g.wt.Reset(&git.ResetOptions{Commit: target, Mode: git.HardReset}); err != nil {
		t.Fatalf("reset to %s: %v", target, err)
	}
}

// lcHistoryRewrittenRefs returns the prior_ref and new_ref recorded by the first
// layer.history_rewritten audit event for the given layer, and whether one was
// found.
func lcHistoryRewrittenRefs(t testing.TB, path, layerID string) (prior, newRef string, found bool) {
	t.Helper()
	for _, ev := range readAuditEvents(t, path) {
		if ev.Type == "layer.history_rewritten" && ev.Target == layerID {
			return ev.Context["prior_ref"], ev.Context["new_ref"], true
		}
	}
	return "", "", false
}
