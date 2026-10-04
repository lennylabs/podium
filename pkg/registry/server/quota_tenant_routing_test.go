package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// quotaOrgHeader names the request header the routing fixture reads as the
// caller's organization. A request without it resolves to an anonymous
// caller, which routing leaves unrouted.
const quotaOrgHeader = "X-Test-Org"

const (
	quotaTenantA = "tenant-a"
	quotaTenantB = "tenant-b"
)

// quotaRoutingFixture is one server with one shared limiter and a tenant
// router. Tenant A stores search and materialization rates of 1, tenant B
// stores zeros, and the limiter's deployment defaults are 2, so a limit of 1
// observed for A can only come from the record routing carried.
type quotaRoutingFixture struct {
	url string
	st  store.Store
}

// newQuotaRoutingFixture boots the fixture. The identity resolver maps the
// quotaOrgHeader value to an authenticated caller in that organization, which
// is the only caller withTenantRouting routes. The router returns the stored
// record, so a store update reaches the next request's routing.
func newQuotaRoutingFixture(t *testing.T) *quotaRoutingFixture {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	for _, tn := range []store.Tenant{
		{ID: quotaTenantA, Name: quotaTenantA, Active: true, Quota: store.Quota{SearchQPS: 1, MaterializeRate: 1}},
		{ID: quotaTenantB, Name: quotaTenantB, Active: true},
	} {
		if err := st.CreateTenant(ctx, tn); err != nil {
			t.Fatalf("CreateTenant %s: %v", tn.ID, err)
		}
	}
	resolveID := func(r *http.Request) layer.Identity {
		org := r.Header.Get(quotaOrgHeader)
		if org == "" {
			return layer.Identity{}
		}
		return layer.Identity{Sub: "alice@" + org, OrgID: org, IsAuthenticated: true}
	}
	route := func(ctx context.Context, org string) (store.Tenant, bool) {
		tn, err := st.GetTenant(ctx, org)
		if err != nil {
			return store.Tenant{}, false
		}
		return tn, true
	}
	srv := server.New(core.New(st, tenantUnrouted, nil),
		server.WithIdentityResolver(resolveID),
		server.WithTenantRouter(route, true),
		server.WithQuotaLimiter(server.NewQuotaLimiter(server.QuotaLimits{SearchQPS: 2, MaterializeRate: 2})),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &quotaRoutingFixture{url: ts.URL, st: st}
}

// get issues a GET as the given organization; an empty org sends no header.
func (f *quotaRoutingFixture) get(t *testing.T, org, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.url+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if org != "" {
		req.Header.Set(quotaOrgHeader, org)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, body
}

// isQuotaRefusal reports whether a response is the 429 envelope with code.
func isQuotaRefusal(t *testing.T, status int, body []byte, code string) bool {
	t.Helper()
	return status == http.StatusTooManyRequests && tenantErrCode(t, body) == code
}

// exhaust repeats path as org until the limiter refuses it with code and
// asserts that exactly want calls were admitted first. The admitted count is
// the enforced burst, so it distinguishes a tenant's own rate from the
// deployment default. No admitted call may be a quota refusal of any kind.
func (f *quotaRoutingFixture) exhaust(t *testing.T, org, path, code string, want int) {
	t.Helper()
	const maxCalls = 10
	for admitted := 0; admitted < maxCalls; admitted++ {
		status, body := f.get(t, org, path)
		if isQuotaRefusal(t, status, body, code) {
			if admitted != want {
				t.Errorf("%s as %q: %d calls admitted before %s, want %d", path, org, admitted, code, want)
			}
			return
		}
		if status == http.StatusTooManyRequests {
			t.Fatalf("%s as %q: 429 with code %q, want %q", path, org, tenantErrCode(t, body), code)
		}
	}
	t.Fatalf("%s as %q: no %s after %d calls", path, org, code, maxCalls)
}

// wantStatus asserts one call's status.
func (f *quotaRoutingFixture) wantStatus(t *testing.T, org, path string, want int) {
	t.Helper()
	if status, body := f.get(t, org, path); status != want {
		t.Errorf("%s as %q = %d (%s), want %d", path, org, status, body, want)
	}
}

// wantRefused asserts one call is the 429 envelope with code.
func (f *quotaRoutingFixture) wantRefused(t *testing.T, org, path, code string) {
	t.Helper()
	if status, body := f.get(t, org, path); !isQuotaRefusal(t, status, body, code) {
		t.Errorf("%s as %q = %d (%s), want 429 %s", path, org, status, body, code)
	}
}

// wantAdmitted asserts one call is not refused with code. The fixture holds
// no artifact, so an admitted load_artifact still fails after the charge.
func (f *quotaRoutingFixture) wantAdmitted(t *testing.T, org, path, code string) {
	t.Helper()
	if status, body := f.get(t, org, path); isQuotaRefusal(t, status, body, code) {
		t.Errorf("%s as %q refused with %s, want admitted", path, org, code)
	}
}

const (
	searchArtifactsPath = "/v1/search_artifacts?query=x"
	searchDomainsPath   = "/v1/search_domains?query=x"
	loadArtifactPath    = "/v1/load_artifact?id=x"
	codeSearchQPS       = "quota.search_qps_exceeded"
	codeMaterialize     = "quota.materialize_rate_exceeded"
)

// Spec: §4.7.8, §6.3.1 — one shared limiter charges each routed request to
// its own tenant under the record routing carried. Tenant A's search budget
// of 1 is spent by search_artifacts and then refuses search_domains, which
// charges the same bucket, while tenant B's two calls fit its deployment
// default of 2. A charge site keyed on anything but the request's tenant
// would admit A's search_domains or refuse one of B's calls.
func TestQuotaRouting_SearchChargedPerTenant(t *testing.T) {
	t.Parallel()
	f := newQuotaRoutingFixture(t)

	f.exhaust(t, quotaTenantA, searchArtifactsPath, codeSearchQPS, 1)
	f.wantRefused(t, quotaTenantA, searchDomainsPath, codeSearchQPS)
	f.wantStatus(t, quotaTenantB, searchDomainsPath, http.StatusOK)
	f.wantStatus(t, quotaTenantB, searchArtifactsPath, http.StatusOK)
}

// Spec: §4.7.8, §6.3.1 — load_artifact charges the request's tenant against
// its materialization rate. The charge precedes the artifact lookup, so A's
// burst ends in quota.materialize_rate_exceeded although no artifact exists,
// and B's admitted loads are asserted only as not refused by the quota.
func TestQuotaRouting_LoadChargedPerTenant(t *testing.T) {
	t.Parallel()
	f := newQuotaRoutingFixture(t)

	f.exhaust(t, quotaTenantA, loadArtifactPath, codeMaterialize, 1)
	f.wantAdmitted(t, quotaTenantB, loadArtifactPath, codeMaterialize)
	f.wantAdmitted(t, quotaTenantB, loadArtifactPath, codeMaterialize)
}

// Spec: §4.7.8, §6.3.1 — an unauthenticated request is charged to the
// unrouted tenant at the deployment defaults. Spending that bucket leaves A
// and B untouched, so each one's first search passes with no refill wait.
func TestQuotaRouting_UnroutedBucketIsSeparate(t *testing.T) {
	t.Parallel()
	f := newQuotaRoutingFixture(t)

	f.exhaust(t, "", searchArtifactsPath, codeSearchQPS, 2)
	f.wantStatus(t, quotaTenantA, searchArtifactsPath, http.StatusOK)
	f.wantStatus(t, quotaTenantB, searchArtifactsPath, http.StatusOK)
}

// quotaReport is the GET /v1/quota body the routing tests read.
type quotaReport struct {
	TenantID string `json:"tenant_id"`
	Limits   struct {
		SearchQPS       int `json:"search_qps"`
		MaterializeRate int `json:"materialize_rate"`
	} `json:"limits"`
}

// readQuota decodes GET /v1/quota as org.
func (f *quotaRoutingFixture) readQuota(t *testing.T, org string) quotaReport {
	t.Helper()
	status, body := f.get(t, org, "/v1/quota")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/quota as %q = %d (%s)", org, status, body)
	}
	var rep quotaReport
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("decode quota: %v\nbody: %s", err, body)
	}
	return rep
}

// Spec: §4.7.8, §6.3.1 — GET /v1/quota reports the limits the limiter
// enforces for the same request. A reports its stored rate of 1. B reports
// the deployment defaults of 2 while its stored record still holds zeros.
func TestQuotaRouting_QuotaReportsEnforcedLimits(t *testing.T) {
	t.Parallel()
	f := newQuotaRoutingFixture(t)

	a := f.readQuota(t, quotaTenantA)
	if a.TenantID != quotaTenantA || a.Limits.SearchQPS != 1 {
		t.Errorf("quota as A = %+v, want tenant_id %s and search_qps 1", a, quotaTenantA)
	}
	b := f.readQuota(t, quotaTenantB)
	if b.TenantID != quotaTenantB || b.Limits.SearchQPS != 2 || b.Limits.MaterializeRate != 2 {
		t.Errorf("quota as B = %+v, want tenant_id %s and search_qps 2, materialize_rate 2", b, quotaTenantB)
	}
	stored, err := f.st.GetTenant(context.Background(), quotaTenantB)
	if err != nil {
		t.Fatalf("GetTenant B: %v", err)
	}
	if stored.Quota.SearchQPS != 0 || stored.Quota.MaterializeRate != 0 {
		t.Errorf("stored B quota = %+v, want zero rates", stored.Quota)
	}
}

// Spec: §4.7.8, §6.3.1 — each request's limit comes from the record routing
// read for that request. After A is refused, a store update sets A's search
// QPS to -1, which exempts A, and A's next immediate search passes.
func TestQuotaRouting_RecordUpdateAppliesToNextRequest(t *testing.T) {
	t.Parallel()
	f := newQuotaRoutingFixture(t)

	f.exhaust(t, quotaTenantA, searchArtifactsPath, codeSearchQPS, 1)
	ctx := context.Background()
	rec, err := f.st.GetTenant(ctx, quotaTenantA)
	if err != nil {
		t.Fatalf("GetTenant A: %v", err)
	}
	rec.Quota.SearchQPS = -1
	if err := f.st.UpdateTenant(ctx, rec); err != nil {
		t.Fatalf("UpdateTenant A: %v", err)
	}
	f.wantStatus(t, quotaTenantA, searchArtifactsPath, http.StatusOK)
}
