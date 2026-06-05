package integration

// Cross-tenant search and quota non-interference over a shared Postgres.
//
// auth_org_isolation_test.go proves load_artifact, shared-id content-hash, and
// search isolation for two orgs over one shared Postgres, and
// http_api_test.go's TestHTTPAPI_SearchQPSQuota throttles a single tenant, but
// no test drives one org above its search QPS budget while a second org stays
// served, and the dependency-edge, scope-preview, and audit surfaces were not
// asserted cross-org. This adds the cross-tenant quota
// and resource-class isolation rather than re-listing org-scoped reads.
//
// Two registries are stood up over one shared Postgres database, one pinned to
// org acme and one to org globex, each behind the injected-session-token
// verifier and each with its own §4.7.8 search-QPS limiter. The §4.7.8 limiter
// keys a leaky token bucket per tenant, so exhausting acme's bucket must not
// touch globex's. The journey asserts:
//
//   - acme is throttled with quota.search_qps_exceeded once its budget is
//     spent, while globex, searching in budget against its own limiter, stays
//     served (HTTP 200);
//   - acme reading globex's dependency edges sees an org-scoped empty set (no
//     globex edge leaks into acme's schema) while each org sees its own edge;
//   - acme's scope-preview counts only acme's artifacts, never globex's; and
//   - each org's audit stream records only its own calls.
//
// Gated on PODIUM_POSTGRES_DSN: schema-per-org isolation is a Postgres property
// (SQLite is single-schema), so the test skips cleanly without a live DSN.
// Identifiers are prefixed xtq* to avoid collisions in package integration.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// xtqServer builds a registry pinned to tenantID over the shared store st,
// behind the injected-session-token verifier, with a per-tenant search-QPS
// limiter at searchQPS and a file-backed audit sink at auditPath. It reuses the
// orgiso* verifier and runtime-key wiring from auth_org_isolation_test.go.
func xtqServer(t *testing.T, st store.Store, tenantID string, priv *rsa.PrivateKey, searchQPS int, auditPath string) *httptest.Server {
	t.Helper()
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "shared", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	sink, err := audit.NewFileSink(auditPath)
	if err != nil {
		t.Fatalf("audit sink for %s: %v", tenantID, err)
	}
	reg = reg.WithAudit(xtqAuditEmitter(sink))
	keys := identity.NewRuntimeKeyRegistry()
	if err := keys.Register(identity.RuntimeKey{Issuer: orgisoIssuer, Algorithm: "RS256", Key: &priv.PublicKey}); err != nil {
		t.Fatalf("Register runtime key: %v", err)
	}
	srv := server.New(reg,
		server.WithIdentityVerifier(orgisoVerifier(keys)),
		server.WithQuotaLimiter(server.NewQuotaLimiter(server.QuotaLimits{SearchQPS: searchQPS})),
		server.WithAudit(sink),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// xtqAuditEmitter adapts a FileSink to the core.AuditEmitter the registry calls,
// so the read-path events (artifacts.searched, artifacts.dependents_of) land in
// the per-tenant log this test reads back.
func xtqAuditEmitter(sink *audit.FileSink) core.AuditEmitter {
	return func(ctx context.Context, e core.AuditEvent) {
		_ = sink.Append(ctx, audit.Event{
			Type:      audit.EventType(e.Type),
			Timestamp: time.Now().UTC(),
			Caller:    e.Caller,
			Target:    e.Target,
			Context:   e.Context,
		})
	}
}

// Spec: §4.7.8 (per-tenant search-QPS budget enforced with
// quota.search_qps_exceeded; the limiter keys a bucket per tenant), §4.7.1
// (the tenant boundary is the org; a read scoped to one org cannot reach
// another org's rows), §3.5 (scope preview is the caller's effective view),
// §8.1 (the audit stream records the caller's calls).
func TestAuthCrossTenantQuota_NonInterferenceOverSharedPostgres(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres-backed cross-tenant quota test")
	}
	pg, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	// ResetForTest wipes every org schema on the shared database. Hold the
	// whole-database lock across the reset, the seed, and every later assertion
	// (this test has a refill sleep window) so no sibling reset lands mid-flight.
	lockPostgresReset(t)
	if err := pg.ResetForTest(context.Background()); err != nil {
		t.Fatalf("ResetForTest: %v", err)
	}

	ctx := context.Background()
	const tenantAcme = "xtq-acme"
	const tenantGlobex = "xtq-globex"
	const searchQPS = 3 // a low budget so a short burst exhausts it deterministically
	for _, id := range []string{tenantAcme, tenantGlobex} {
		if err := pg.CreateTenant(ctx, store.Tenant{
			ID: id, Name: id,
			Quota: store.Quota{SearchQPS: searchQPS},
		}); err != nil {
			t.Fatalf("CreateTenant(%s): %v", id, err)
		}
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Each org owns a depended-on artifact and a dependent that extends it, so a
	// dependency edge exists per org. The artifact ids are org-prefixed so a
	// cross-org dependents query targets an id absent from the other schema.
	mustPut := func(tenant, artifactID, desc string) {
		t.Helper()
		if err := pg.PutManifest(ctx, store.ManifestRecord{
			TenantID: tenant, ArtifactID: artifactID, Version: "1.0.0",
			ContentHash: "sha256:" + artifactID, Type: "context", Description: desc,
			Layer: "shared", IngestedAt: base,
		}); err != nil {
			t.Fatalf("PutManifest(%s/%s): %v", tenant, artifactID, err)
		}
	}
	mustPutEdge := func(tenant, from, to string) {
		t.Helper()
		if err := pg.PutDependency(ctx, tenant, store.DependencyEdge{From: from, To: to, Kind: "extends"}); err != nil {
			t.Fatalf("PutDependency(%s %s->%s): %v", tenant, from, to, err)
		}
	}
	mustPut(tenantAcme, "acme/base-policy", "acme base policy")
	mustPut(tenantAcme, "acme/derived-policy", "acme derived policy")
	mustPutEdge(tenantAcme, "acme/derived-policy", "acme/base-policy")
	mustPut(tenantGlobex, "globex/base-policy", "globex base policy")
	mustPut(tenantGlobex, "globex/derived-policy", "globex derived policy")
	mustPutEdge(tenantGlobex, "globex/derived-policy", "globex/base-policy")

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	acmeAudit := filepath.Join(t.TempDir(), "acme-audit.log")
	globexAudit := filepath.Join(t.TempDir(), "globex-audit.log")
	acmeSrv := xtqServer(t, pg, tenantAcme, priv, searchQPS, acmeAudit)
	globexSrv := xtqServer(t, pg, tenantGlobex, priv, searchQPS, globexAudit)

	acmeToken := orgisoSign(t, priv, orgisoClaims("alice@acme.com", "acme"))
	globexToken := orgisoSign(t, priv, orgisoClaims("bob@globex.com", "globex"))

	// ---- Quota non-interference ---------------------------------------------
	// Exhaust acme's search budget with a tight burst against acme alone. The
	// bucket capacity equals the QPS, so a burst well past the capacity drives at
	// least one acme request into quota.search_qps_exceeded.
	var acmeThrottled bool
	var acmeThrottleCode string
	for i := 0; i < searchQPS*4; i++ {
		st, body := orgisoGet(t, acmeSrv.URL+"/v1/search_artifacts?query=policy", acmeToken)
		if st == http.StatusTooManyRequests {
			acmeThrottled = true
			acmeThrottleCode = xtqErrCode(body)
			break
		}
	}
	if !acmeThrottled {
		t.Fatalf("acme search was never throttled after %d rapid requests; the QPS budget did not engage", searchQPS*4)
	}
	if acmeThrottleCode != "quota.search_qps_exceeded" {
		t.Errorf("acme throttle code = %q, want quota.search_qps_exceeded", acmeThrottleCode)
	}
	// acme stays throttled on the immediately-following request (the bucket is
	// spent), confirming the exhaustion is not a one-off transient.
	if st, _ := orgisoGet(t, acmeSrv.URL+"/v1/search_artifacts?query=policy", acmeToken); st != http.StatusTooManyRequests {
		t.Errorf("acme follow-up search = %d, want 429 (budget still spent)", st)
	}
	// globex, searching in budget against its own separate limiter, stays served
	// even while acme is throttled. globex's bucket was never touched by acme's
	// burst, so a small number of in-budget globex searches all succeed. A
	// shared (cross-tenant) limiter would have rejected these.
	for i := 0; i < searchQPS; i++ {
		if gst, gbody := orgisoGet(t, globexSrv.URL+"/v1/search_artifacts?query=policy", globexToken); gst != http.StatusOK {
			t.Fatalf("globex in-budget search #%d while acme throttled = %d, want 200 (cross-tenant quota leak)\nbody: %s", i, gst, gbody)
		}
	}
	// Let acme's bucket refill so the remaining read-surface assertions are not
	// rejected by the spent budget.
	time.Sleep(1100 * time.Millisecond)

	// ---- Dependency-edge isolation ------------------------------------------
	// acme reads its own dependent edge.
	if edges := xtqDependents(t, acmeSrv.URL, "acme/base-policy", acmeToken); !xtqHasEdge(edges, "acme/derived-policy", "acme/base-policy") {
		t.Errorf("acme dependents of acme/base-policy missing its own edge: %v", edges)
	}
	// acme reading globex's artifact id sees an org-scoped empty set: globex's
	// edge lives in the globex schema and never surfaces in acme's view.
	if edges := xtqDependents(t, acmeSrv.URL, "globex/base-policy", acmeToken); len(edges) != 0 {
		t.Errorf("acme dependents of globex/base-policy = %v, want empty (cross-org edge leak)", edges)
	}
	// globex sees its own edge (the mirror).
	if edges := xtqDependents(t, globexSrv.URL, "globex/base-policy", globexToken); !xtqHasEdge(edges, "globex/derived-policy", "globex/base-policy") {
		t.Errorf("globex dependents of globex/base-policy missing its own edge: %v", edges)
	}

	// ---- Scope-preview isolation --------------------------------------------
	// acme's scope preview counts only acme's two artifacts; globex's count is
	// independent. A cross-org leak would inflate acme's count to include
	// globex's artifacts.
	acmeCount := xtqScopePreviewCount(t, acmeSrv.URL, acmeToken)
	globexCount := xtqScopePreviewCount(t, globexSrv.URL, globexToken)
	if acmeCount != 2 {
		t.Errorf("acme scope-preview artifact_count = %d, want 2 (its own artifacts only)", acmeCount)
	}
	if globexCount != 2 {
		t.Errorf("globex scope-preview artifact_count = %d, want 2 (its own artifacts only)", globexCount)
	}

	// ---- Audit-stream isolation ---------------------------------------------
	// Each org's audit stream records its own caller's calls and never the other
	// org's. acme's log carries alice@acme.com; it must not carry globex's caller
	// bob@globex.com nor globex's own dependent artifact id (globex/derived-policy,
	// which acme never named in any request). The org-prefixed *target* an org
	// deliberately queries cross-org is excluded from this check because the audit
	// event correctly records the id the caller typed; the isolation signal is the
	// caller identity and the other org's result data, neither of which crosses.
	acmeLog := xtqReadFile(acmeAudit)
	globexLog := xtqReadFile(globexAudit)
	if !strings.Contains(acmeLog, "alice@acme.com") {
		t.Errorf("acme audit log missing its own caller:\n%s", acmeLog)
	}
	for _, leaked := range []string{"bob@globex.com", "globex/derived-policy"} {
		if strings.Contains(acmeLog, leaked) {
			t.Errorf("acme audit log leaked globex data %q:\n%s", leaked, acmeLog)
		}
	}
	if !strings.Contains(globexLog, "bob@globex.com") {
		t.Errorf("globex audit log missing its own caller:\n%s", globexLog)
	}
	for _, leaked := range []string{"alice@acme.com", "acme/derived-policy"} {
		if strings.Contains(globexLog, leaked) {
			t.Errorf("globex audit log leaked acme data %q:\n%s", leaked, globexLog)
		}
	}
}

// xtqErrCode extracts the registry error code from a JSON error body.
func xtqErrCode(body []byte) string {
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Code
}

// xtqDependents queries the dependents of artifactID as token and returns the
// edge list ([]{from,to,kind}).
func xtqDependents(t *testing.T, baseURL, artifactID, token string) []map[string]string {
	t.Helper()
	st, body := orgisoGet(t, baseURL+"/v1/dependents?id="+artifactID, token)
	if st != http.StatusOK {
		t.Fatalf("dependents(%s) = %d\nbody: %s", artifactID, st, body)
	}
	var resp struct {
		Edges []map[string]string `json:"edges"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode dependents: %v (body=%s)", err, body)
	}
	return resp.Edges
}

// xtqHasEdge reports whether edges contains a from->to edge.
func xtqHasEdge(edges []map[string]string, from, to string) bool {
	for _, e := range edges {
		if e["from"] == from && e["to"] == to {
			return true
		}
	}
	return false
}

// xtqScopePreviewCount returns the artifact_count from the scope-preview
// response for token.
func xtqScopePreviewCount(t *testing.T, baseURL, token string) int {
	t.Helper()
	st, body := orgisoGet(t, baseURL+"/v1/scope/preview", token)
	if st != http.StatusOK {
		t.Fatalf("scope/preview = %d\nbody: %s", st, body)
	}
	var resp struct {
		ArtifactCount int `json:"artifact_count"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode scope preview: %v (body=%s)", err, body)
	}
	return resp.ArtifactCount
}

// xtqReadFile reads a file without failing the test (for log assertions).
func xtqReadFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}
