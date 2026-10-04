package e2e

// End-to-end coverage of §7.3.1 Tenant selection on the compiled binary. A
// multi-tenant registry serves each §7.3.1 layer request in the tenant §6.3.1
// selects for it, advertises a tenant-qualified webhook URL, refuses a layer
// write that resolves to no tenant, refuses §8.5 erasure outright, and reports
// the §7.3.4 manage_any_layer posture member in the routed tenant. These cases
// drive the serverboot mount site, which no in-process test reaches; the
// in-process suite in pkg/registry/server pins the routing logic on every
// platform.
//
// Every case runs on the standard stack and skips through msSkipIfNoStack
// without Postgres and S3. Subjects, tenant names, and layer IDs carry a
// per-run suffix, because the stack's database is shared across runs. No case
// provisions a bare globex, which TestEventStream_MultiTenantRouting relies on
// staying unprovisioned.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lennylabs/podium/pkg/registry/core"
)

// ltrRouterLog is the boot line serverboot writes when it installs the
// §6.3.1 tenant router.
const ltrRouterLog = "multi-tenant mode: routing requests by organization"

// ltrFixture is the multi-tenant trusted-headers registry the routing case
// boots: the server, the per-run proxy secret and suffix, and the per-run
// subjects and tenant name.
type ltrFixture struct {
	srv                       *serverProc
	secret, suffix            string
	carol, olivia, dave, erin string
	globex, globexID, initech string
}

// as builds the trusted-headers identity of sub routed by org.
func (f *ltrFixture) as(sub, org string) http.Header {
	h := evHeaders(sub, "")
	h.Set("X-Podium-Proxy-Secret", f.secret)
	if org != "" {
		h.Set("X-Podium-User-Org", org)
	}
	return h
}

// ltrSetup boots the multi-tenant trusted-headers registry with the web UI
// posture read mounted, carol-<s> as the bootstrap tenant's admin, and
// olivia-<s> as the instance operator.
func ltrSetup(t *testing.T) *ltrFixture {
	t.Helper()
	dsn, bucket, region := msSkipIfNoStack(t)
	suffix := randHex(6)
	f := &ltrFixture{
		secret:  "ltr-proxy-" + randHex(8),
		suffix:  suffix,
		carol:   "carol-" + suffix + "@acme.com",
		olivia:  "olivia-" + suffix + "@acme.com",
		dave:    "dave-" + suffix + "@acme.com",
		erin:    "erin-" + suffix + "@acme.com",
		globex:  "globex-" + suffix,
		initech: "initech-" + suffix,
	}
	_, pemPath := injKeyPair(t)
	f.srv = msStartStandardServerEnv(t, dsn, bucket, region, pemPath,
		"PODIUM_IDENTITY_PROVIDER=trusted-headers",
		"PODIUM_MULTI_TENANT=true",
		"PODIUM_WEB_UI=true",
		"PODIUM_TRUSTED_PROXY_SECRET="+f.secret,
		"PODIUM_BOOTSTRAP_ADMINS="+f.carol,
		"PODIUM_OPERATOR_ADMINS="+f.olivia,
	)
	return f
}

// ltrGitLayer is the registration body of a network git layer under the
// reserved .invalid domain, so no operation resolves to the file transport
// and no fetch is attempted. It carries no admin-only field.
func ltrGitLayer(id string) map[string]any {
	return map[string]any{
		"id":          id,
		"source_type": "git",
		"repo":        "https://git.invalid/acme/" + id + ".git",
		"ref":         "main",
	}
}

// ltrListIDs returns the layer IDs GET /v1/layers answers for the caller the
// headers name, failing the test on any status other than 200.
func ltrListIDs(t *testing.T, srv *serverProc, as http.Header, who string) map[string]bool {
	t.Helper()
	st, body := apiDoAs(t, http.MethodGet, srv.BaseURL+"/v1/layers", as, nil)
	apiWantStatus(t, st, http.StatusOK, who+" lists layers", body)
	var resp struct {
		Layers []struct {
			ID string `json:"id"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode %s's layer list: %v\nbody: %s", who, err, body)
	}
	ids := map[string]bool{}
	for _, l := range resp.Layers {
		ids[l.ID] = true
	}
	return ids
}

// ltrPosture is the part of the §7.3.4 posture read these cases assert.
type ltrPosture struct {
	Subject           string `json:"subject"`
	LayerCapabilities struct {
		ManageAnyLayer bool `json:"manage_any_layer"`
	} `json:"layer_capabilities"`
}

// ltrReadPosture reads GET /v1/ui/session as the caller the headers name and
// fails the test unless it answers 200.
func ltrReadPosture(t *testing.T, srv *serverProc, as http.Header, who string) ltrPosture {
	t.Helper()
	st, body := apiDoAs(t, http.MethodGet, srv.BaseURL+"/v1/ui/session", as, nil)
	apiWantStatus(t, st, http.StatusOK, who+" reads the session posture", body)
	var p ltrPosture
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode %s's posture: %v\nbody: %s", who, err, body)
	}
	return p
}

// ltrWantManage asserts the manage_any_layer member the posture read reports.
func ltrWantManage(t *testing.T, srv *serverProc, as http.Header, who string, want bool) {
	t.Helper()
	if got := ltrReadPosture(t, srv, as, who).LayerCapabilities.ManageAnyLayer; got != want {
		t.Errorf("%s: manage_any_layer = %v, want %v", who, got, want)
	}
}

// ltrEraseBody is a well-formed §8.5 erasure request naming userID.
func ltrEraseBody(userID string) map[string]any {
	return map[string]any{"user_id": userID, "salt": "ltr-salt-" + randHex(4)}
}

// Spec: §6.3.1 — a multi-tenant registry selects the tenant of each request
// from the caller's organization.
// Spec: §7.3.1 — the layer endpoints serve the tenant §6.3.1 selects per
// request (Tenant selection), a layer write that resolves to no tenant is
// refused with 403 auth.forbidden, and the webhook URL names the tenant ID.
// Spec: §7.3.4 — manage_any_layer is evaluated in the routed tenant.
// Spec: §8.5 — erasure on a multi-tenant registry is refused with 403
// auth.forbidden.
// Spec: §7.6 — a stream receives only the events of the tenant it resolves to.
func TestLayerEndpoint_MultiTenantRouting(t *testing.T) {
	t.Parallel()
	f := ltrSetup(t)
	carolH := f.as(f.carol, "default")
	daveH := f.as(f.dave, f.globex)
	erinH := f.as(f.erin, f.initech)

	ltrProvisionGlobex(t, f)
	ltrAdminRegistersAdminLayer(t, f, carolH)
	daveA, daveB := ltrDaveRegisters(t, f, carolH, daveH)
	ltrWebhookRoutes(t, f, daveH, daveA)
	ltrStreamIsolation(t, f, carolH, daveH, daveA, daveB)

	// Step 6: an unprovisioned org under trusted-headers resolves to no
	// tenant, so the list is empty and a write is refused.
	if ids := ltrListIDs(t, f.srv, erinH, "erin"); len(ids) != 0 {
		t.Errorf("erin (unprovisioned %s) lists %v, want none", f.initech, ids)
	}
	apiWantCodeAs(t, f.srv, http.MethodPost, "/v1/layers", erinH, ltrGitLayer("ltr-erin-"+f.suffix),
		http.StatusForbidden, "auth.forbidden", "erin registers a layer")

	// Step 7: the posture member follows the routed tenant, and erasure is
	// refused for the bootstrap tenant's admin without touching dave's rows.
	ltrWantManage(t, f.srv, carolH, "carol", true)
	ltrWantManage(t, f.srv, daveH, "dave", false)
	ltrWantManage(t, f.srv, erinH, "erin", false)
	apiWantCodeAs(t, f.srv, http.MethodPost, "/v1/admin/erase", carolH, ltrEraseBody(f.dave),
		http.StatusForbidden, "auth.forbidden", "carol erases dave")
	if ids := ltrListIDs(t, f.srv, daveH, "dave after the erase"); !ids[daveA] || !ids[daveB] {
		t.Errorf("dave's list after the refused erase = %v, want %s and %s", ids, daveA, daveB)
	}
}

// ltrProvisionGlobex is step 1: olivia provisions globex-<s>, and the
// registry keys it by the UUIDv5 the org name derives.
func ltrProvisionGlobex(t *testing.T, f *ltrFixture) {
	t.Helper()
	st, body := apiDoAs(t, http.MethodPost, f.srv.BaseURL+"/v1/admin/tenants",
		f.as(f.olivia, "default"), map[string]any{"name": f.globex})
	apiWantStatus(t, st, http.StatusCreated, "olivia provisions "+f.globex, body)
	var tenant struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &tenant); err != nil {
		t.Fatalf("decode provisioned tenant: %v\nbody: %s", err, body)
	}
	if want := core.OrgIDForName(f.globex); tenant.ID != want {
		t.Fatalf("provisioned tenant ID = %q, want the UUIDv5 %q", tenant.ID, want)
	}
	f.globexID = tenant.ID
}

// ltrAdminRegistersAdminLayer is step 2: carol's admin grant in the bootstrap
// tenant applies, so a registration with no admin-only field stores an
// admin-defined layer. Before the routing fix the admin check ran against the
// unrouted tenant and the same request answered 201 with user_defined true,
// so the status code alone does not detect the defect.
func ltrAdminRegistersAdminLayer(t *testing.T, f *ltrFixture, carolH http.Header) {
	t.Helper()
	id := "ltr-admin-" + f.suffix
	st, body := apiDoAs(t, http.MethodPost, f.srv.BaseURL+"/v1/layers", carolH, ltrGitLayer(id))
	apiWantStatus(t, st, http.StatusCreated, "carol registers "+id, body)
	t.Cleanup(func() {
		if st, body := apiDoAs(t, http.MethodDelete, f.srv.BaseURL+"/v1/layers?id="+id, carolH, nil); st != 200 && st != 204 {
			t.Logf("unregister %s: HTTP %d: %s", id, st, body)
		}
	})
	var resp struct {
		Layer struct {
			UserDefined bool `json:"user_defined"`
		} `json:"layer"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode carol's registration: %v\nbody: %s", err, body)
	}
	if resp.Layer.UserDefined {
		t.Errorf("carol's registration stored user_defined true; the bootstrap tenant's admin grant did not apply\nbody: %s", body)
	}
}

// ltrDaveRegisters is step 3: dave registers two personal layers in globex,
// each advertising a webhook URL qualified by the globex tenant ID, and each
// tenant's list holds its own layers alone. It returns dave's layer IDs.
func ltrDaveRegisters(t *testing.T, f *ltrFixture, carolH, daveH http.Header) (string, string) {
	t.Helper()
	first, second := "ltr-dave-a-"+f.suffix, "ltr-dave-b-"+f.suffix
	wantPath := "/v1/ingest/webhook/" + f.globexID + "/"
	for _, id := range []string{first, second} {
		if u := evRegisterUserLayer(t, f.srv, daveH, id); !strings.Contains(u, wantPath+id) {
			t.Errorf("dave's %s webhook_url = %q, want it to contain %q", id, u, wantPath+id)
		}
	}
	dave := ltrListIDs(t, f.srv, daveH, "dave")
	if len(dave) != 2 || !dave[first] || !dave[second] {
		t.Errorf("dave's list = %v, want exactly %s and %s", dave, first, second)
	}
	if carol := ltrListIDs(t, f.srv, carolH, "carol"); carol[first] || carol[second] {
		t.Errorf("carol's list = %v, want neither %s nor %s", carol, first, second)
	}
	return first, second
}

// ltrWebhookRoutes is step 4: the tenant-qualified webhook route checks the
// delivery signature in globex, answers an unknown tenant segment as an
// unknown layer, and a secret rotation advertises the globex URL again.
func ltrWebhookRoutes(t *testing.T, f *ltrFixture, daveH http.Header, layerID string) {
	t.Helper()
	path := "/v1/ingest/webhook/" + f.globexID + "/" + layerID
	badSig := http.Header{}
	badSig.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	apiWantCodeAs(t, f.srv, http.MethodPost, path, badSig, map[string]any{},
		http.StatusUnauthorized, "ingest.webhook_invalid", "a badly signed delivery to dave's webhook")
	unknown := "/v1/ingest/webhook/" + core.OrgIDForName("absent-"+f.suffix) + "/" + layerID
	apiWantCodeAs(t, f.srv, http.MethodPost, unknown, badSig, map[string]any{},
		http.StatusNotFound, "registry.not_found", "a delivery naming an unknown tenant")

	st, body := apiDoAs(t, http.MethodPost, f.srv.BaseURL+"/v1/layers/update?id="+layerID, daveH,
		map[string]any{"rotate_webhook_secret": true})
	apiWantStatus(t, st, http.StatusOK, "dave rotates the webhook secret of "+layerID, body)
	var resp struct {
		WebhookURL string `json:"webhook_url"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode dave's rotation: %v\nbody: %s", err, body)
	}
	if !strings.Contains(resp.WebhookURL, path) {
		t.Errorf("rotated webhook_url = %q, want it to contain %q", resp.WebhookURL, path)
	}
	if strings.Contains(resp.WebhookURL, core.OrgIDForName("default")) {
		t.Errorf("rotated webhook_url = %q names the default tenant's ID", resp.WebhookURL)
	}
}

// ltrStreamIsolation is step 5: dave's reorder publishes a layer.config_changed
// in globex, which dave's stream receives and carol's stream, routed to the
// bootstrap tenant, does not.
func ltrStreamIsolation(t *testing.T, f *ltrFixture, carolH, daveH http.Header, first, second string) {
	t.Helper()
	daveStream := openEventStreamAs(t, f.srv, daveH)
	carolStream := openEventStreamAs(t, f.srv, carolH)
	st, body := apiDoAs(t, http.MethodPost, f.srv.BaseURL+"/v1/layers/reorder", daveH,
		map[string]any{"order": []string{second, first}})
	apiWantStatus(t, st, http.StatusOK, "dave reorders his layers", body)
	if _, ok := evWaitForLayer(daveStream, "layer.config_changed", evBound, first, second); !ok {
		t.Fatalf("dave's stream received no reorder event within %s\nlog:\n%s", evBound, f.srv.log())
	}
	if ev, ok := evWaitForLayer(carolStream, "", evWindow, first, second); ok {
		t.Errorf("carol's stream received %q naming %v; a bootstrap-tenant subscriber must receive no globex event",
			ev.Event, ev.Data["layer"])
	}
}

// Spec: §6.3.1 — a multi-tenant registry with no identity provider installs
// no tenant router, so every request resolves to no tenant.
// Spec: §7.3.1 — Tenant selection: an unrouted list is empty and an unrouted
// layer write is refused with 403 auth.forbidden, which overrides the
// no-provider admission.
// Spec: §8.5 — erasure on a multi-tenant registry is refused with 403
// auth.forbidden.
// Spec: §7.3.4 — manage_any_layer is false for an unrouted request.
// Spec: §8.1 — an unrouted request event records no tenant, and the
// podium:unrouted binding reaches no record. The boot installs no tenant
// router, so this pins the serverboot multi-tenant label wiring.
//
// IMPLEMENTOR'S CHOICE (proposal 0047, TEST-3): the boot clears the
// identity provider msStandardEnv selects with an overriding empty
// PODIUM_IDENTITY_PROVIDER= entry. The spawned process sees the last value
// for a repeated key, the empty value selects no provider, and the boot log
// carries no router line, which the case asserts.
func TestLayerEndpoint_MultiTenantNoRouter(t *testing.T) {
	t.Parallel()
	dsn, bucket, region := msSkipIfNoStack(t)
	suffix := randHex(6)
	_, pemPath := injKeyPair(t)
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	srv := msStartStandardServerEnv(t, dsn, bucket, region, pemPath,
		"PODIUM_IDENTITY_PROVIDER=",
		"PODIUM_MULTI_TENANT=true",
		"PODIUM_WEB_UI=true",
		"PODIUM_AUDIT_LOG_PATH="+auditPath,
	)
	if strings.Contains(srv.log(), ltrRouterLog) {
		t.Fatalf("the boot log carries %q with no identity provider\nlog:\n%s", ltrRouterLog, srv.log())
	}

	if ids := ltrListIDs(t, srv, nil, "anonymous"); len(ids) != 0 {
		t.Errorf("the unrouted list = %v, want none", ids)
	}
	apiWantCodeAs(t, srv, http.MethodPost, "/v1/layers", nil, ltrGitLayer("ltr-nr-"+suffix),
		http.StatusForbidden, "auth.forbidden", "an unrouted registration")
	apiWantCodeAs(t, srv, http.MethodPost, "/v1/admin/erase", nil, ltrEraseBody("frank-"+suffix+"@acme.com"),
		http.StatusForbidden, "auth.forbidden", "an unrouted erase")
	if ids := ltrListIDs(t, srv, nil, "anonymous after the refusals"); len(ids) != 0 {
		t.Errorf("the unrouted list after the refusals = %v, want none", ids)
	}
	ltrWantManage(t, srv, nil, "anonymous", false)

	// The read's status is not asserted: the Postgres store refuses the
	// podium:unrouted binding as a schema name, so the unrouted search
	// answers 500. The core emits artifacts.searched on every return path,
	// so the record exists either way, and its label is what this pins.
	_, _ = apiDo(t, http.MethodGet, srv.BaseURL+"/v1/search_artifacts?query=ltr-nr-"+suffix, nil)
	ltrWantUnlabeledSearch(t, auditPath)
}

// ltrWantUnlabeledSearch waits for an artifacts.searched record in the audit
// file at path and asserts that it carries no tenant and that no record in
// the file carries the podium:unrouted binding.
func ltrWantUnlabeledSearch(t *testing.T, path string) {
	t.Helper()
	if !brPollContains(path, `"type":"artifacts.searched"`, 5*time.Second) {
		t.Fatalf("audit log missing artifacts.searched:\n%s", brReadOrEmpty(path))
	}
	raw := brReadOrEmpty(path)
	if strings.Contains(raw, "podium:unrouted") {
		t.Errorf("an audit record carries podium:unrouted:\n%s", raw)
	}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		var rec struct {
			Type   string  `json:"type"`
			Tenant *string `json:"tenant"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode audit line %q: %v", line, err)
		}
		if rec.Type == "artifacts.searched" && rec.Tenant != nil {
			t.Errorf("artifacts.searched carries tenant %q, want none", *rec.Tenant)
		}
	}
}

// ltrBearer is the Authorization header of an oidc-jwt token the test IdP
// signs for sub, routed by org.
func ltrBearer(t *testing.T, idp *oidcTestIdP, sub, org string) http.Header {
	t.Helper()
	tok := idp.token(t, jwt.MapClaims{
		"iss":    idp.srv.URL,
		"aud":    oidcAudience,
		"exp":    time.Now().Add(time.Hour).Unix(),
		"sub":    sub,
		"email":  sub,
		"org_id": org,
	})
	h := http.Header{}
	h.Set("Authorization", "Bearer "+tok)
	return h
}

// Spec: §6.3.1 — under oidc-jwt a verified token whose organization names no
// provisioned tenant is refused with 401 auth.tenant_unknown, and
// details.token_org_id names the organization.
// Spec: §7.3.1 — the layer routes are tenant-routed (Tenant selection).
// Spec: §8.5 — the erase route is not tenant-routed, so every caller of a
// multi-tenant registry gets 403 auth.forbidden.
// Spec: §7.3.4 — the posture read refuses no request and reports
// manage_any_layer false for a caller that resolves to no tenant.
//
// The responses here differ only by the serverboot wrapper each route is
// mounted through, so this case pins the mount choices. It runs on the CI
// Linux lane and skips on darwin through requireCustomTrustStore.
func TestLayerEndpoint_MultiTenantUnknownOrgRejected(t *testing.T) {
	t.Parallel()
	requireCustomTrustStore(t)
	dsn, bucket, region := msSkipIfNoStack(t)
	idp := startOIDCTestIdP(t, "")
	_, pemPath := injKeyPair(t)
	srv := msStartStandardServerEnv(t, dsn, bucket, region, pemPath,
		"PODIUM_MULTI_TENANT=true",
		"PODIUM_WEB_UI=true",
		"PODIUM_IDENTITY_PROVIDER=oidc-jwt",
		"PODIUM_OAUTH_ISSUER="+idp.srv.URL,
		"PODIUM_OAUTH_AUDIENCE="+oidcAudience,
		"SSL_CERT_FILE="+idp.caFile,
	)
	suffix := randHex(6)

	// Step 1: the control token verifies and routes to the bootstrap tenant,
	// so the 401 below comes from the unknown org alone.
	alice := ltrBearer(t, idp, "alice@acme.com", "default")
	ltrListIDs(t, srv, alice, "alice")
	ltrWantManage(t, srv, alice, "alice", true)

	// Step 2: the layer routes reject the unknown org.
	frankID := "frank-" + suffix + "@acme.com"
	initech := "initech-" + suffix
	frank := ltrBearer(t, idp, frankID, initech)
	body := apiWantCodeAs(t, srv, http.MethodGet, "/v1/layers", frank, nil,
		http.StatusUnauthorized, "auth.tenant_unknown", "frank lists layers")
	var env struct {
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Details["token_org_id"] != initech {
		t.Errorf("auth.tenant_unknown details = %v (decode error %v), want token_org_id %q\nbody: %s", env.Details, err, initech, body)
	}

	// Step 3: the erase route is not tenant-routed. A 401 here means it was
	// mounted through TenantRouted.
	apiWantCodeAs(t, srv, http.MethodPost, "/v1/admin/erase", frank, ltrEraseBody(frankID),
		http.StatusForbidden, "auth.forbidden", "frank erases")

	// Step 4: the posture read refuses no request. A 401 here means it was
	// mounted through TenantRouted.
	p := ltrReadPosture(t, srv, frank, "frank")
	if p.Subject != frankID || p.LayerCapabilities.ManageAnyLayer {
		t.Errorf("frank's posture = %+v, want subject %q and manage_any_layer false", p, frankID)
	}
}
