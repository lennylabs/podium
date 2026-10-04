package e2e

// End-to-end coverage of the §4.7.8 per-tenant quota charging on the compiled
// binary. One multi-tenant trusted-headers registry on the standard stack
// serves per-run tenants, so the search and materialization buckets, the
// GET /v1/quota report, and the audit-volume gate are observed through the
// wiring Run() installs rather than through an in-process limiter.
//
// test/integration/auth_cross_tenant_quota_test.go builds one server and one
// limiter per org, so it cannot detect a limiter that charges every tenant to
// one bucket. This file and pkg/registry/server's quota_tenant_routing_test.go
// are the regression pins for shared-limiter isolation.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// qtSearchPath and qtLoadPath are the charged §5 meta-tool routes. The
// per-run tenants hold no artifact, so a load the limiter admits fails after
// the charge, which is all the load cases need.
const (
	qtSearchPath = "/v1/search_artifacts?query=quota"
	qtLoadPath   = "/v1/load_artifact?id=quota/absent"
)

// qtCaller builds the trusted-headers identity of sub in org. Every request
// carries the proxy secret, and an empty org sends no organization header.
func qtCaller(secret, sub, org string) http.Header {
	h := evHeaders(sub, "")
	h.Set("X-Podium-Proxy-Secret", secret)
	if org != "" {
		h.Set("X-Podium-User-Org", org)
	}
	return h
}

// qtCode returns the §6.10 error code of an envelope body, or "" when the
// body is not an error envelope.
func qtCode(body []byte) string {
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Code
}

// qtCreateTenant provisions a tenant with the given quota as the operator,
// deactivates it when the test ends so the shared Postgres keeps no active
// per-run tenant, and returns its ID.
func qtCreateTenant(t *testing.T, srv *serverProc, operator http.Header, name string, quota map[string]any) string {
	t.Helper()
	st, body := apiDoAs(t, http.MethodPost, srv.BaseURL+"/v1/admin/tenants", operator,
		map[string]any{"name": name, "quota": quota})
	apiWantStatus(t, st, 201, "create tenant "+name, body)
	id, _ := apiJSONObj(t, body)["id"].(string)
	if id == "" {
		t.Fatalf("create tenant %s returned no id: %s", name, body)
	}
	t.Cleanup(func() {
		if st, body := apiDoAs(t, http.MethodDelete, srv.BaseURL+"/v1/admin/tenants/"+id, operator, nil); st != 204 {
			t.Logf("deactivate tenant %s: HTTP %d: %s", name, st, body)
		}
	})
	return id
}

// qtBurstUntil sends up to max immediate GET requests to path as the caller
// and reports whether one returned 429 with the wanted code.
func qtBurstUntil(t *testing.T, srv *serverProc, as http.Header, path, code string, max int) bool {
	t.Helper()
	for i := 0; i < max; i++ {
		st, body := apiDoAs(t, http.MethodGet, srv.BaseURL+path, as, nil)
		if st == http.StatusTooManyRequests && qtCode(body) == code {
			return true
		}
	}
	return false
}

// qtWantNotThrottled fails the test when a GET of path as the caller returns
// 429, and returns the status and body otherwise.
func qtWantNotThrottled(t *testing.T, srv *serverProc, as http.Header, path, what string) (int, []byte) {
	t.Helper()
	st, body := apiDoAs(t, http.MethodGet, srv.BaseURL+path, as, nil)
	if st == http.StatusTooManyRequests {
		t.Fatalf("%s: HTTP 429 %s, want no quota refusal\nbody: %s", what, qtCode(body), body)
	}
	return st, body
}

// qtLimits returns the limits object GET /v1/quota reports for the caller.
func qtLimits(t *testing.T, srv *serverProc, as http.Header, who string) map[string]any {
	t.Helper()
	st, body := apiDoAs(t, http.MethodGet, srv.BaseURL+"/v1/quota", as, nil)
	apiWantStatus(t, st, 200, "quota as "+who, body)
	limits, _ := apiJSONObj(t, body)["limits"].(map[string]any)
	if limits == nil {
		t.Fatalf("quota as %s carries no limits: %s", who, body)
	}
	return limits
}

// qtWantLimit fails the test unless the named limit holds want.
func qtWantLimit(t *testing.T, limits map[string]any, who, name string, want float64) {
	t.Helper()
	if got, _ := limits[name].(float64); got != want {
		t.Errorf("GET /v1/quota as %s: %s=%v, want %v (limits %v)", who, name, limits[name], want, limits)
	}
}

// Spec: §4.7.8, §6.3.1, §13.12 — a multi-tenant registry charges each search,
// load, and audit event to the request's routed tenant under that tenant's
// limits, resolved against the PODIUM_QUOTA_* deployment defaults: a positive
// tenant value is enforced and zero selects the default. A request that
// resolves to no tenant is charged at the defaults to the unrouted tenant. A
// PATCH to a tenant's quota applies to the next request with no restart, and
// GET /v1/quota reports the limits the limiter enforces.
// Spec: §7.3.1 — the reingest audit-volume gate keys on the stored layer's
// tenant and refuses with quota.audit_volume_exceeded once that tenant's
// budget is spent.
//
// The cases run in order on one server and share bucket state. Case (a)
// sends two of bob's searches rather than three because bob's bucket holds 3
// tokens refilling at 3 per second, and case (d) spends the third before one
// token can accrue.
func TestQuotaTenant_IsolationOnTheBinary(t *testing.T) {
	t.Parallel()
	dsn, bucket, region := msSkipIfNoStack(t)
	_, pemPath := injKeyPair(t)
	secret := "qt-proxy-" + randHex(8)
	srv := msStartStandardServerEnv(t, dsn, bucket, region, pemPath,
		"PODIUM_IDENTITY_PROVIDER=trusted-headers",
		"PODIUM_MULTI_TENANT=true",
		"PODIUM_TRUSTED_PROXY_SECRET="+secret,
		"PODIUM_OPERATOR_ADMINS=carol@acme.com",
		"PODIUM_BOOTSTRAP_ADMINS=",
		"PODIUM_QUOTA_SEARCH_QPS=3",
		"PODIUM_QUOTA_MATERIALIZE_RATE=3",
	)

	run := randHex(6)
	carol := qtCaller(secret, "carol@acme.com", "")
	acme, globex := "acme-"+run, "globex-"+run
	qtCreateTenant(t, srv, carol, acme, map[string]any{"search_qps": 1, "materialize_rate": 1})
	globexID := qtCreateTenant(t, srv, carol, globex, map[string]any{})
	alice := qtCaller(secret, "alice@acme.com", acme)
	bob := qtCaller(secret, "bob@acme.com", globex)

	t.Run("a_search_isolated", func(t *testing.T) {
		if !qtBurstUntil(t, srv, alice, qtSearchPath, "quota.search_qps_exceeded", 20) {
			t.Fatalf("alice's search burst never returned quota.search_qps_exceeded under acme search_qps 1\nlog:\n%s", srv.log())
		}
		for i := 0; i < 2; i++ {
			qtWantNotThrottled(t, srv, bob, qtSearchPath, "bob's search after alice's burst")
		}
	})

	t.Run("b_load_isolated", func(t *testing.T) {
		if !qtBurstUntil(t, srv, alice, qtLoadPath, "quota.materialize_rate_exceeded", 20) {
			t.Fatalf("alice's load burst never returned quota.materialize_rate_exceeded under acme materialize_rate 1")
		}
		for i := 0; i < 2; i++ {
			qtWantNotThrottled(t, srv, bob, qtLoadPath, "bob's load after alice's burst")
		}
	})

	t.Run("c_quota_reports_enforced_limits", func(t *testing.T) {
		qtWantLimit(t, qtLimits(t, srv, alice, "alice"), "alice", "search_qps", 1)
		bobLimits := qtLimits(t, srv, bob, "bob")
		qtWantLimit(t, bobLimits, "bob", "search_qps", 3)
		qtWantLimit(t, bobLimits, "bob", "materialize_rate", 3)

		st, body := apiDoAs(t, http.MethodGet, srv.BaseURL+"/v1/admin/tenants", carol, nil)
		apiWantStatus(t, st, 200, "list tenants", body)
		var list struct {
			Tenants []struct {
				ID    string         `json:"id"`
				Quota map[string]any `json:"quota"`
			} `json:"tenants"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("decode tenant list: %v\nbody: %s", err, body)
		}
		for _, tn := range list.Tenants {
			if tn.ID != globexID {
				continue
			}
			qtWantLimit(t, tn.Quota, "carol (globex record)", "search_qps", 0)
			qtWantLimit(t, tn.Quota, "carol (globex record)", "materialize_rate", 0)
			return
		}
		t.Fatalf("GET /v1/admin/tenants does not list %s (%s)", globex, globexID)
	})

	t.Run("d_unrouted_charged_at_defaults", func(t *testing.T) {
		unrouted := qtCaller(secret, "alice@acme.com", "initech-unprovisioned-"+run)
		if !qtBurstUntil(t, srv, unrouted, qtSearchPath, "quota.search_qps_exceeded", 20) {
			t.Fatalf("an unprovisioned org's burst never returned quota.search_qps_exceeded under the default of 3")
		}
		qtWantNotThrottled(t, srv, bob, qtSearchPath, "bob's search after the unrouted burst")
	})

	t.Run("e_patch_applies_without_restart", func(t *testing.T) {
		st, body := apiDoAs(t, http.MethodPatch, srv.BaseURL+"/v1/admin/tenants/"+globexID, carol,
			map[string]any{"quota": map[string]any{"search_qps": 1}})
		apiWantStatus(t, st, 200, "patch globex search_qps", body)
		if !qtBurstUntil(t, srv, bob, qtSearchPath, "quota.search_qps_exceeded", 5) {
			t.Fatalf("bob's burst after the PATCH to search_qps 1 never returned quota.search_qps_exceeded")
		}
	})

	t.Run("f_audit_volume_per_tenant", func(t *testing.T) {
		qtAuditVolumePerTenant(t, srv, carol, bob, secret, run)
	})
}

// qtAuditVolumePerTenant runs case (f). With PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY
// unset the deployment default is 0, so only initech-<run>'s own budget of 1
// is enforced. bob's globex events must not count against initech, and one
// of dave's own events must spend initech's budget. dave holds no admin
// grant, so his layer is an owner-arm user-defined layer with a network git
// source under .invalid, whose reingest passes the gate and then fails to
// fetch.
func qtAuditVolumePerTenant(t *testing.T, srv *serverProc, carol, bob http.Header, secret, run string) {
	t.Helper()
	initech := "initech-" + run
	qtCreateTenant(t, srv, carol, initech, map[string]any{"audit_volume_per_day": 1})
	dave := qtCaller(secret, "dave-"+run+"@acme.com", initech)
	layerID := "quota-av-" + run
	evRegisterUserLayer(t, srv, dave, layerID)

	// Case (e) left globex at search_qps 1 with bob's bucket spent. Spacing
	// the searches lets each pass the limiter and reach core.SearchArtifacts,
	// which emits the audit event the meter records under globex.
	for i := 0; i < 3; i++ {
		time.Sleep(1100 * time.Millisecond)
		st, body := apiDoAs(t, http.MethodGet, srv.BaseURL+qtSearchPath, bob, nil)
		apiWantStatus(t, st, 200, "bob's spaced search", body)
	}

	reingest := srv.BaseURL + "/v1/layers/reingest?id=" + layerID
	st, body := apiDoAs(t, http.MethodPost, reingest, dave, nil)
	if st == http.StatusTooManyRequests || qtCode(body) == "quota.audit_volume_exceeded" {
		t.Fatalf("dave's first reingest: HTTP %d %s; bob's globex events counted against initech\nbody: %s", st, qtCode(body), body)
	}

	st, body = apiDoAs(t, http.MethodGet, srv.BaseURL+qtSearchPath, dave, nil)
	apiWantStatus(t, st, 200, "dave's search", body)

	st, body = apiDoAs(t, http.MethodPost, reingest, dave, nil)
	if st != http.StatusTooManyRequests || qtCode(body) != "quota.audit_volume_exceeded" {
		t.Fatalf("dave's reingest after spending initech's budget: HTTP %d %s, want 429 quota.audit_volume_exceeded\nbody: %s\nlog:\n%s",
			st, qtCode(body), body, srv.log())
	}
}
