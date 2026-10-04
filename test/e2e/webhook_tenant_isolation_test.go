package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// wtiFixture is the booted multi-tenant registry the receiver-tenancy tests
// share: a trusted-headers standard-stack server, two TLS sinks, the per-run
// subjects and tenant name, and the receivers carol and bob registered.
type wtiFixture struct {
	srv          *serverProc
	proxySecret  string
	sinkSecret   string
	suffix       string
	carol, bob   string
	globex       string
	sinkA, sinkB *notificationSink
	ra, rb       string
}

// as builds the trusted-headers identity of sub routed by org. An empty org
// sends no X-Podium-User-Org, which leaves the request on the unrouted tenant.
func (f *wtiFixture) as(sub, org string) http.Header {
	h := evHeaders(sub, "")
	h.Set("X-Podium-Proxy-Secret", f.proxySecret)
	if org != "" {
		h.Set("X-Podium-User-Org", org)
	}
	return h
}

// carolH and bobH are the routed identities of the two tenant admins.
func (f *wtiFixture) carolH() http.Header { return f.as(f.carol, "default") }
func (f *wtiFixture) bobH() http.Header   { return f.as(f.bob, f.globex) }

// wtiSetup runs steps 1-4 of the proposal 0046 TEST-3 suite. It boots a
// multi-tenant trusted-headers registry on the standard stack with carol as
// the bootstrap tenant's admin and dave as the instance operator, has dave
// provision globex-<suffix>, seeds bob's admin grant in that tenant directly
// in Postgres, and has carol and bob each register one receiver pointed at
// their own TLS sink. Subjects and the tenant name carry a per-run suffix so
// the shared database does not collide across runs.
func wtiSetup(t *testing.T) *wtiFixture {
	t.Helper()
	dsn, bucket, region := msSkipIfNoStack(t)
	suffix := randHex(6)
	f := &wtiFixture{
		proxySecret: "wti-proxy-" + randHex(8),
		sinkSecret:  "wti-sink-" + randHex(8),
		suffix:      suffix,
		carol:       "carol-" + suffix + "@acme.com",
		bob:         "bob-" + suffix + "@acme.com",
		globex:      "globex-" + suffix,
	}
	f.sinkA = newNotificationSink(t, withSinkTLS(), withSinkSecret(f.sinkSecret))
	f.sinkB = newNotificationSink(t, withSinkTLS(), withSinkSecret(f.sinkSecret))

	dave := "dave-" + suffix + "@acme.com"
	_, pemPath := injKeyPair(t)
	env := append([]string{
		"PODIUM_IDENTITY_PROVIDER=trusted-headers",
		"PODIUM_MULTI_TENANT=true",
		"PODIUM_TRUSTED_PROXY_SECRET=" + f.proxySecret,
		"PODIUM_BOOTSTRAP_ADMINS=" + f.carol,
		"PODIUM_OPERATOR_ADMINS=" + dave,
	}, sinkTrustEnv(t, f.sinkA, f.sinkB)...)
	f.srv = msStartStandardServerEnv(t, dsn, bucket, region, pemPath, env...)

	orgID := wtiProvisionTenant(t, f, dave)
	wtiGrantAdmin(t, dsn, f.bob, orgID)

	f.ra = wtiCreateReceiver(t, f, f.carolH(), f.sinkA, "carol")
	f.rb = wtiCreateReceiver(t, f, f.bobH(), f.sinkB, "bob")
	return f
}

// wtiProvisionTenant has the operator provision globex-<suffix> through the
// §7.3.3 tenant API and returns the org ID the registry assigned.
func wtiProvisionTenant(t *testing.T, f *wtiFixture, operator string) string {
	t.Helper()
	st, body := apiDoAs(t, http.MethodPost, f.srv.BaseURL+"/v1/admin/tenants",
		f.as(operator, "default"), map[string]any{"name": f.globex})
	apiWantStatus(t, st, http.StatusCreated, "provision "+f.globex, body)
	var tenant struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &tenant); err != nil || tenant.ID == "" {
		t.Fatalf("decode provisioned tenant (error %v)\nbody: %s", err, body)
	}
	return tenant.ID
}

// wtiGrantAdmin seeds userID's admin grant in orgID on the stack's Postgres
// store. No API grants a new tenant's first admin: handleAdminGrants calls
// requireAdmin against the routed tenant, which holds no grant yet.
func wtiGrantAdmin(t *testing.T, dsn, userID, orgID string) {
	t.Helper()
	st, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open postgres to seed the admin grant: %v", err)
	}
	defer func() { _ = st.Close() }()
	if err := st.GrantAdmin(context.Background(), store.AdminGrant{UserID: userID, OrgID: orgID}); err != nil {
		t.Fatalf("seed admin grant for %s in %s: %v", userID, orgID, err)
	}
}

// wtiCreateReceiver registers an unfiltered receiver pointed at sink as the
// caller the headers name, asserts 201, and returns the receiver ID.
func wtiCreateReceiver(t *testing.T, f *wtiFixture, as http.Header, sink *notificationSink, who string) string {
	t.Helper()
	st, body := apiDoAs(t, http.MethodPost, f.srv.BaseURL+"/v1/webhooks", as,
		map[string]any{"url": sink.URL() + "/hook", "secret": f.sinkSecret})
	apiWantStatus(t, st, http.StatusCreated, who+" creates a receiver", body)
	var rec webhookReceiver
	if err := json.Unmarshal(body, &rec); err != nil || rec.ID == "" {
		t.Fatalf("decode %s's receiver (error %v)\nbody: %s", who, err, body)
	}
	return rec.ID
}

// wtiListIDs returns the receiver IDs GET /v1/webhooks answers for the caller
// the headers name, failing the test on any status other than 200.
func wtiListIDs(t *testing.T, f *wtiFixture, as http.Header, who string) map[string]bool {
	t.Helper()
	st, body := apiDoAs(t, http.MethodGet, f.srv.BaseURL+"/v1/webhooks", as, nil)
	apiWantStatus(t, st, http.StatusOK, who+" lists receivers", body)
	var resp struct {
		Receivers []webhookReceiver `json:"receivers"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode %s's receiver list: %v\nbody: %s", who, err, body)
	}
	ids := map[string]bool{}
	for _, r := range resp.Receivers {
		ids[r.ID] = true
	}
	return ids
}

// wtiWantLists asserts that carol's list holds ra and not rb, and that bob's
// list holds rb and not ra.
func wtiWantLists(t *testing.T, f *wtiFixture, when string) {
	t.Helper()
	carol := wtiListIDs(t, f, f.carolH(), "carol")
	if !carol[f.ra] || carol[f.rb] {
		t.Errorf("%s: carol's list = %v, want %s and not %s", when, carol, f.ra, f.rb)
	}
	bob := wtiListIDs(t, f, f.bobH(), "bob")
	if !bob[f.rb] || bob[f.ra] {
		t.Errorf("%s: bob's list = %v, want %s and not %s", when, bob, f.rb, f.ra)
	}
}

// wtiWantCode asserts the status and the §6.10 error code of one request.
func wtiWantCode(t *testing.T, f *wtiFixture, method, path string, as http.Header, body any, wantStatus int, wantCode, what string) {
	t.Helper()
	apiWantCodeAs(t, f.srv, method, path, as, body, wantStatus, wantCode, what)
}

// apiWantCodeAs sends one request to srv as the caller the headers name and
// asserts its status and its §6.10 error code. It returns the response body.
func apiWantCodeAs(t *testing.T, srv *serverProc, method, path string, as http.Header, body any, wantStatus int, wantCode, what string) []byte {
	t.Helper()
	st, resp := apiDoAs(t, method, srv.BaseURL+path, as, body)
	apiWantStatus(t, st, wantStatus, what, resp)
	if code := envelopeCode(t, resp); code != wantCode {
		t.Errorf("%s: code = %q, want %q\nbody: %s", what, code, wantCode, resp)
	}
	return resp
}

// wtiChangedNaming returns the layer.config_changed deliveries sink received
// that name one of layerIDs. Filtering by the per-run layer IDs keeps a
// default-tenant event another test publishes on the shared database out of
// the count.
func wtiChangedNaming(sink *notificationSink, layerIDs ...string) []recordedDelivery {
	var out []recordedDelivery
	for _, d := range sink.all() {
		ev, _ := d.Body["event"].(string)
		data, _ := d.Body["data"].(map[string]any)
		if ev != "layer.config_changed" {
			continue
		}
		line := registryEventLine{Event: ev, Data: data}
		for _, id := range layerIDs {
			if evNamesLayer(line, id) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// wtiReorder registers two user-defined layers as the caller the headers
// name and reorders them, which publishes one layer.config_changed in the
// caller's routed tenant. It returns the two layer IDs.
func wtiReorder(t *testing.T, f *wtiFixture, as http.Header, who, prefix string) (string, string) {
	t.Helper()
	first, second := prefix+"-a-"+f.suffix, prefix+"-b-"+f.suffix
	for _, id := range []string{first, second} {
		evRegisterUserLayer(t, f.srv, as, id)
	}
	st, body := apiDoAs(t, http.MethodPost, f.srv.BaseURL+"/v1/layers/reorder", as,
		map[string]any{"order": []string{second, first}})
	apiWantStatus(t, st, http.StatusOK, who+" reorders the layers", body)
	return first, second
}

// wtiWantOneChanged waits for the layer.config_changed naming layerIDs on
// want, then watches other for evWindow, and asserts that want received
// exactly one validly signed delivery and other received none.
func wtiWantOneChanged(t *testing.T, f *wtiFixture, want, other *notificationSink, wantName, otherName string, layerIDs ...string) {
	t.Helper()
	deadline := time.Now().Add(evBound)
	for len(wtiChangedNaming(want, layerIDs...)) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	// The window for the other sink starts after the receiving sink has the
	// event, so a misrouted delivery fired alongside it has had the full
	// window to land.
	time.Sleep(evWindow)
	if got := wtiChangedNaming(other, layerIDs...); len(got) != 0 {
		t.Errorf("%s received %d layer.config_changed deliveries naming %v, want none: %+v", otherName, len(got), layerIDs, got)
	}
	changed := wtiChangedNaming(want, layerIDs...)
	if len(changed) != 1 {
		t.Fatalf("%s received %d layer.config_changed deliveries naming %v, want exactly 1: %+v\nlog:\n%s",
			wantName, len(changed), layerIDs, changed, f.srv.log())
	}
	if !changed[0].SigValid {
		t.Errorf("%s's layer.config_changed carries no valid X-Podium-Signature: %+v", wantName, changed[0])
	}
}

// Spec: §7.3.2 — each receiver belongs to the tenant whose admin registered
// it, the receiver CRUD endpoints read and write only the receivers of the
// tenant the request resolves to, and an id that names another tenant's
// receiver is answered as an unknown id.
// Spec: §6.3.1 — a multi-tenant trusted-headers registry routes each request
// by the X-Podium-User-Org the gateway asserts, and leaves an unprovisioned
// org and an absent org on the unrouted tenant, which holds no admin grant.
// Spec: §4.7.1 — the org name `default` resolves to the bootstrap tenant's ID.
//
// The test opens no TLS connection to a sink, so it runs on every platform
// with a standard stack.
func TestWebhookReceivers_MultiTenantCRUDIsolation(t *testing.T) {
	t.Parallel()
	f := wtiSetup(t)

	// Step 5: each tenant's list holds its own receiver alone, and carol
	// addressing bob's receiver gets the unknown-id answers.
	wtiWantLists(t, f, "after create")
	rbPath := "/v1/webhooks/" + f.rb
	wtiWantCode(t, f, http.MethodGet, rbPath, f.carolH(), nil,
		http.StatusNotFound, "registry.not_found", "carol GETs bob's receiver")
	wtiWantCode(t, f, http.MethodPut, rbPath, f.carolH(), map[string]any{"disabled": true},
		http.StatusNotFound, "registry.not_found", "carol PUTs bob's receiver")
	st, body := apiDoAs(t, http.MethodDelete, f.srv.BaseURL+rbPath, f.carolH(), nil)
	apiWantStatus(t, st, http.StatusNoContent, "carol DELETEs bob's receiver", body)
	st, body = apiDoAs(t, http.MethodGet, f.srv.BaseURL+rbPath, f.bobH(), nil)
	apiWantStatus(t, st, http.StatusOK, "bob GETs his receiver after carol's DELETE", body)
	var rec webhookReceiver
	if err := json.Unmarshal(body, &rec); err != nil || rec.ID != f.rb || rec.Disabled {
		t.Errorf("bob's receiver after carol's PUT and DELETE = %+v (decode error %v), want %s enabled\nbody: %s", rec, err, f.rb, body)
	}

	// Step 7: an unprovisioned org and an absent org both fall to the
	// unrouted tenant, which holds no admin grant, so requireAdmin refuses
	// before any store access.
	for name, h := range map[string]http.Header{
		"unprovisioned org": f.as(f.carol, "initech-"+f.suffix),
		"no org":            f.as(f.carol, ""),
	} {
		wtiWantCode(t, f, http.MethodGet, "/v1/webhooks", h, nil,
			http.StatusForbidden, "auth.forbidden", "carol with "+name+" lists receivers")
		wtiWantCode(t, f, http.MethodGet, "/v1/webhooks/"+f.ra, h, nil,
			http.StatusForbidden, "auth.forbidden", "carol with "+name+" GETs her receiver")
	}
	wtiWantLists(t, f, "after the refusals")
}

// Spec: §7.3.2 — the registry delivers an event only to the receivers of the
// tenant the event belongs to.
// Spec: §7.3.1 — the layer endpoints act in the tenant §6.3.1 selects per
// request, so a layer event belongs to the tenant whose layer it names.
//
// The test runs both directions on the binary. carol reorders two personal
// layers in the bootstrap tenant, and only sink A receives that
// layer.config_changed. bob then reorders two personal layers in
// globex-<suffix>, and only sink B receives that one. Personal-layer
// registration publishes layer.user_registered rather than
// layer.config_changed, so each reorder is the only layer.config_changed that
// names its layers. The deliveries are matched by the per-run layer IDs.
func TestWebhookReceivers_MultiTenantDeliveryIsolation(t *testing.T) {
	t.Parallel()
	f := wtiSetup(t)

	requireSubprocessTLSTrust(t)
	carolA, carolB := wtiReorder(t, f, f.carolH(), "carol", "wti")
	wtiWantOneChanged(t, f, f.sinkA, f.sinkB, "sink A (default receiver)", "sink B (globex receiver)", carolA, carolB)

	bobA, bobB := wtiReorder(t, f, f.bobH(), "bob", "wti-globex")
	wtiWantOneChanged(t, f, f.sinkB, f.sinkA, "sink B (globex receiver)", "sink A (default receiver)", bobA, bobB)
}
