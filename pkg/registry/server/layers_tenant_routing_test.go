package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// The routing fixture provisions active tenants A and B, the inactive tenant
// C, and the poisoned boot tenant P. The endpoint and the registry are bound to
// P, and no organization routes to it, so any store call, admin check, event,
// notification, or ingest that lands on P is a read or write left on the boot
// tenant rather than on the request's tenant.
const (
	trTenantA = "A"
	trTenantB = "B"
	trTenantC = "C"
	trPoison  = "poison-p"
	// trPoisonOwner owns P's admin-defined twins of the IDs case 4 registers.
	trPoisonOwner = "poison@p.invalid"
	// trUnknownOrg names no tenant.
	trUnknownOrg = "initech"
)

// trCaller is one fixture identity. The stub verifier reads it from the
// X-Test-User, X-Test-Org, and X-Test-Fail headers.
type trCaller struct {
	user, org string
	fail      bool
}

var (
	trCarol   = trCaller{user: "carol@acme.com", org: trTenantA}
	trDave    = trCaller{user: "dave@acme.com", org: trTenantB}
	trOliviaA = trCaller{user: "olivia@acme.com", org: trTenantA}
	trOliviaB = trCaller{user: "olivia@acme.com", org: trTenantB}
	trErin    = trCaller{user: "erin@acme.com"}
	trFrank   = trCaller{user: "frank@acme.com", org: trUnknownOrg}
	trAnon    = trCaller{}
	trFailed  = trCaller{user: "carol@acme.com", org: trTenantA, fail: true}
)

// trSubjects lists every fixture subject. P grants each of them admin and
// holds two user-defined layers owned by each, so an admin check or an
// owned-layer count left on P changes the outcome.
var trSubjects = []string{trCarol.user, trDave.user, trOliviaA.user, trErin.user, trFrank.user}

// headers returns the stub-verifier headers for c.
func (c trCaller) headers() map[string]string {
	h := map[string]string{}
	if c.user != "" {
		h["X-Test-User"] = c.user
	}
	if c.org != "" {
		h["X-Test-Org"] = c.org
	}
	if c.fail {
		h["X-Test-Fail"] = "1"
	}
	return h
}

// trVerify is the stub §6.3.2 verifier installed on the server and on the
// endpoint. X-Test-Fail selects a typed verification error.
func trVerify(r *http.Request) (layer.Identity, error) {
	if r.Header.Get("X-Test-Fail") != "" {
		return layer.Identity{}, &identity.UntrustedRuntimeError{Issuer: "https://runtime.acme.com", Reason: "signature mismatch"}
	}
	user := r.Header.Get("X-Test-User")
	return layer.Identity{
		Sub: user, Email: user, OrgID: r.Header.Get("X-Test-Org"),
		IsAuthenticated: user != "", IsPublic: user == "",
	}, nil
}

// trPostureIdentity mirrors serverboot's layerIdentityResolver: a verified
// caller resolves to its identity and a verification failure to the anonymous
// caller, so the posture read omits subject for it.
func trPostureIdentity(r *http.Request) layer.Identity {
	if id, err := trVerify(r); err == nil {
		return id
	}
	return layer.Identity{IsPublic: true}
}

// trCountingStore counts layer writes and admin-grant reads once armed, so a
// refusal can be shown to touch neither.
type trCountingStore struct {
	*store.Memory
	armed   atomic.Bool
	writes  atomic.Int64
	isAdmin atomic.Int64
}

func (s *trCountingStore) count(n *atomic.Int64) {
	if s.armed.Load() {
		n.Add(1)
	}
}

func (s *trCountingStore) PutLayerConfig(ctx context.Context, cfg store.LayerConfig) error {
	s.count(&s.writes)
	return s.Memory.PutLayerConfig(ctx, cfg)
}

func (s *trCountingStore) DeleteLayerConfig(ctx context.Context, tenantID, id string) error {
	s.count(&s.writes)
	return s.Memory.DeleteLayerConfig(ctx, tenantID, id)
}

func (s *trCountingStore) RestoreLayerConfig(ctx context.Context, tenantID, id string) error {
	s.count(&s.writes)
	return s.Memory.RestoreLayerConfig(ctx, tenantID, id)
}

func (s *trCountingStore) IsAdmin(ctx context.Context, userID, orgID string) (bool, error) {
	s.count(&s.isAdmin)
	return s.Memory.IsAdmin(ctx, userID, orgID)
}

// trOptions selects a fixture variant.
type trOptions struct {
	// rejectUnknown is the router's reject flag (a rejecting provider).
	rejectUnknown bool
	// noRouter builds the server without WithTenantRouter.
	noRouter bool
	// alwaysAdmit replaces the §4.7.2 admin gate with a callback that admits
	// every caller, as a public-mode or no-provider registry installs.
	alwaysAdmit bool
	// quotaA and quotaB are the tenants' MaxUserLayers quotas.
	quotaA, quotaB int
	// layers are seeded in A, B, or C; a non-nil DeletedAt seeds a tombstone.
	layers []store.LayerConfig
	// grants maps a tenant to the subjects holding an admin grant in it.
	grants map[string][]string
	// poisonAdminTwins names IDs P holds as admin-defined layers owned by
	// trPoisonOwner, the first live and the second tombstoned.
	poisonAdminTwins []string
}

// trFixture is the in-process multi-tenant registry under test.
type trFixture struct {
	st           *trCountingStore
	srv          *Server
	ep           *LayerEndpoint
	ts           *httptest.Server
	auditPath    string
	adminCalls   atomic.Int64
	endpointHits atomic.Int64
	failIngest   atomic.Bool

	// mu guards the records the stubs append from handler goroutines.
	mu       sync.Mutex
	scopes   []core.EventScope
	notes    []map[string]string
	ingested []store.LayerConfig
}

// trPoisonSnapshot is the state of P the fixture requires to be unchanged.
type trPoisonSnapshot struct {
	tenant  store.Tenant
	live    []store.LayerConfig
	deleted []store.LayerConfig
}

// newTenantRoutingFixture builds the multi-tenant endpoint over the poisoned
// boot tenant, mounted as serverboot mounts it: the layer routes and erasure
// through TenantRouted, the inbound webhook unwrapped, the posture read
// through TenantRoutedNoReject, and the meta-tool routes through Handler.
//
// Spec: §6.3.1, §7.3.1 (Tenant selection), §8.5
func newTenantRoutingFixture(t *testing.T, opts trOptions) *trFixture {
	t.Helper()
	f := &trFixture{st: &trCountingStore{Memory: store.NewMemory()}}
	f.seedTenants(t, opts)
	for _, lc := range opts.layers {
		trPut(t, f.st, lc)
	}
	for tenantID, subs := range opts.grants {
		for _, sub := range subs {
			trMust(t, f.st.GrantAdmin(context.Background(), store.AdminGrant{UserID: sub, OrgID: tenantID}))
		}
	}
	trSeedPoison(t, f.st, opts)
	before := f.poisonSnapshot(t)
	t.Cleanup(func() {
		if after := f.poisonSnapshot(t); !reflect.DeepEqual(before, after) {
			t.Errorf("boot tenant %s changed:\nbefore %+v\nafter  %+v", trPoison, before, after)
		}
	})
	f.st.armed.Store(true)
	f.build(t, opts)
	return f
}

// seedTenants provisions A, B, the deactivated C, and P.
func (f *trFixture) seedTenants(t *testing.T, opts trOptions) {
	t.Helper()
	ctx := context.Background()
	for _, tn := range []store.Tenant{
		{ID: trTenantA, Name: trTenantA, Quota: store.Quota{MaxUserLayers: opts.quotaA}},
		{ID: trTenantB, Name: trTenantB, Quota: store.Quota{MaxUserLayers: opts.quotaB}},
		{ID: trTenantC, Name: trTenantC},
		// P's cap stays nonzero, because effectiveLayerCap ignores a zero quota.
		{ID: trPoison, Name: trPoison, Quota: store.Quota{MaxUserLayers: 1}},
	} {
		trMust(t, f.st.CreateTenant(ctx, tn))
	}
	trMust(t, f.st.DeactivateTenant(ctx, trTenantC))
}

// trSeedPoison seeds P with an admin grant for every fixture subject, a public
// layer at P's highest order, two user-defined layers per subject, a twin of
// every A and B layer, and the admin-defined twins case 4 registers against.
func trSeedPoison(t *testing.T, st store.Store, opts trOptions) {
	t.Helper()
	ctx := context.Background()
	for _, sub := range trSubjects {
		trMust(t, st.GrantAdmin(ctx, store.AdminGrant{UserID: sub, OrgID: trPoison}))
	}
	trPut(t, st, store.LayerConfig{TenantID: trPoison, ID: "poison-p-public", SourceType: "git",
		Repo: trRepo(trPoison, "public"), Public: true, Order: 900})
	for i, sub := range trSubjects {
		for n := 1; n <= 2; n++ {
			lc := trUserLayer(trPoison, fmt.Sprintf("poison-p-user-%d-%d", i, n), sub, 100+i*10+n)
			trPut(t, st, lc)
		}
	}
	seen := map[string]bool{}
	for _, lc := range opts.layers {
		if (lc.TenantID != trTenantA && lc.TenantID != trTenantB) || seen[lc.ID] {
			continue
		}
		seen[lc.ID] = true
		twin := lc
		twin.TenantID = trPoison
		twin.Order = 500 + len(seen)
		twin.WebhookSecret = "poison-p-secret-" + lc.ID
		twin.Repo = trRepo(trPoison, lc.ID)
		trPut(t, st, twin)
	}
	for i, id := range opts.poisonAdminTwins {
		twin := store.LayerConfig{TenantID: trPoison, ID: id, SourceType: "git",
			Repo: trRepo(trPoison, id), Owner: trPoisonOwner, Order: 800 + i}
		if i == 1 {
			twin.DeletedAt = &time.Time{}
		}
		trPut(t, st, twin)
	}
}

// build wires the server, the endpoint, and the mux, and starts the server.
func (f *trFixture) build(t *testing.T, opts trOptions) {
	t.Helper()
	reg := core.New(f.st, trPoison, nil)
	srvOpts := []Option{WithIdentityVerifier(trVerify)}
	if !opts.noRouter {
		srvOpts = append(srvOpts, WithTenantRouter(f.resolve, opts.rejectUnknown))
	}
	f.srv = New(reg, srvOpts...)
	f.auditPath = filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(f.auditPath)
	trMust(t, err)
	f.ep = NewLayerEndpoint(f.st, trPoison, NewModeTracker()).
		WithTenantRouting().
		WithIdentityResolver(trVerify).
		WithAdminAuth(func(r *http.Request) error {
			f.adminCalls.Add(1)
			if opts.alwaysAdmit {
				return nil
			}
			return reg.AdminAuthorize(r.Context(), trPostureIdentity(r))
		}).
		WithAudit(sink).
		WithEraseSink(sink).
		WithEventPublisher(f.publish(t)).
		WithNotifier(f.notify(t)).
		WithReingestRunner(f.runIngest(t))
	layers := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.endpointHits.Add(1)
		f.ep.Handler().ServeHTTP(w, r)
	})
	mux := http.NewServeMux()
	mux.Handle("/v1/layers", f.srv.TenantRouted(layers))
	mux.Handle("/v1/layers/", f.srv.TenantRouted(layers))
	mux.Handle("/v1/admin/erase", f.srv.TenantRouted(f.ep.EraseHandler()))
	mux.Handle("/v1/ingest/webhook/", f.ep.WebhookHandler())
	mux.Handle(PathWebUISession, f.srv.TenantRoutedNoReject(SessionPosture{
		Identity: trPostureIdentity, Capabilities: f.ep.Capabilities,
	}.Handler()))
	mux.Handle("/", f.srv.Handler())
	f.ts = httptest.NewServer(mux)
	t.Cleanup(f.ts.Close)
}

// resolve mirrors serverboot's tenantResolver: an organization value naming
// an active provisioned tenant routes to it. P is excluded, so no request
// routes to the boot tenant.
func (f *trFixture) resolve(ctx context.Context, org string) (store.Tenant, bool) {
	org = strings.TrimSpace(org)
	if org == "" || org == trPoison {
		return store.Tenant{}, false
	}
	if tn, err := f.st.GetTenant(ctx, org); err == nil && tn.Active {
		return tn, true
	}
	return store.Tenant{}, false
}

// publish records each §7.6 event scope and fails on one under P.
func (f *trFixture) publish(t *testing.T) ingest.EventEmitter {
	return func(_ context.Context, scope core.EventScope, _ string, _ map[string]any) {
		if scope.TenantID == trPoison {
			t.Errorf("event published under the boot tenant: %+v", scope)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.scopes = append(f.scopes, scope)
	}
}

// notify records each §9.1 notification's tags and fails on one under P.
func (f *trFixture) notify(t *testing.T) NotificationFunc {
	return func(_ context.Context, _, _, _ string, tags map[string]string) {
		if tags["tenant"] == trPoison {
			t.Errorf("notification tagged with the boot tenant: %v", tags)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.notes = append(f.notes, tags)
	}
}

// runIngest records each ingested layer, fails on one under P, and fails the
// ingest while failIngest is set.
func (f *trFixture) runIngest(t *testing.T) ReingestRunner {
	return func(_ context.Context, cfg store.LayerConfig, _ *BreakGlass) (*ingest.Result, error) {
		if cfg.TenantID == trPoison {
			t.Errorf("ingest ran on the boot tenant's layer %s", cfg.ID)
		}
		f.mu.Lock()
		f.ingested = append(f.ingested, cfg)
		f.mu.Unlock()
		if f.failIngest.Load() {
			return nil, errors.New("simulated source failure")
		}
		return &ingest.Result{}, nil
	}
}

func (f *trFixture) recordedScopes() []core.EventScope {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.scopes)
}

func (f *trFixture) poisonSnapshot(t *testing.T) trPoisonSnapshot {
	t.Helper()
	ctx := context.Background()
	tn, err := f.st.GetTenant(ctx, trPoison)
	trMust(t, err)
	live, err := f.st.ListLayerConfigs(ctx, trPoison)
	trMust(t, err)
	deleted, err := f.st.ListDeletedLayerConfigs(ctx, trPoison)
	trMust(t, err)
	return trPoisonSnapshot{tenant: tn, live: live, deleted: deleted}
}

// do sends one request as c and fails the case when the response names the
// boot tenant.
func (f *trFixture) do(t *testing.T, c trCaller, method, path string, body any) (int, []byte) {
	t.Helper()
	status, data := trSend(t, f.ts.URL, c.headers(), method, path, body)
	if bytes.Contains(data, []byte(trPoison)) {
		t.Errorf("%s %s as %+v answered a body naming the boot tenant: %s", method, path, c, data)
	}
	return status, data
}

// expect sends one request and checks its status and, when wantCode is set,
// its §6.10 error code.
func (f *trFixture) expect(t *testing.T, c trCaller, method, path string, body any, wantStatus int, wantCode string) []byte {
	t.Helper()
	status, data := f.do(t, c, method, path, body)
	if status != wantStatus {
		t.Errorf("%s %s as %+v: status %d, want %d (body %s)", method, path, c, status, wantStatus, data)
	}
	if wantCode != "" {
		if code := trErrCode(data); code != wantCode {
			t.Errorf("%s %s as %+v: code %q, want %q", method, path, c, code, wantCode)
		}
	}
	return data
}

// trSend issues one request. A []byte body is sent as is and any other body
// is JSON-encoded. It reports failures with t.Errorf so it is safe to call
// from a goroutine.
func trSend(t *testing.T, base string, headers map[string]string, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rdr = bytes.NewReader(b)
	default:
		enc, err := json.Marshal(b)
		if err != nil {
			t.Errorf("encode body: %v", err)
			return 0, nil
		}
		rdr = bytes.NewReader(enc)
	}
	req, err := http.NewRequest(method, base+path, rdr)
	if err != nil {
		t.Errorf("new request: %v", err)
		return 0, nil
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("%s %s: %v", method, path, err)
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("read body: %v", err)
	}
	return resp.StatusCode, data
}

// trErrCode returns the §6.10 code of an error envelope.
func trErrCode(data []byte) string {
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return ""
	}
	return env.Code
}

// trListIDs decodes a {"layers": [...]} body into its sorted layer IDs.
func trListIDs(t *testing.T, data []byte) []string {
	t.Helper()
	var resp struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Errorf("decode layer list: %v (body %s)", err, data)
		return nil
	}
	ids := make([]string, 0, len(resp.Layers))
	for _, l := range resp.Layers {
		ids = append(ids, l.ID)
	}
	slices.Sort(ids)
	return ids
}

// trRegistered decodes a register or update response.
func trRegistered(t *testing.T, data []byte) LayerRegisterResponse {
	t.Helper()
	var resp LayerRegisterResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Errorf("decode layer response: %v (body %s)", err, data)
	}
	return resp
}

// trStoredIDs returns the sorted IDs of tenantID's live layers, or of its
// tombstoned layers when deleted is set.
func trStoredIDs(t *testing.T, st store.Store, tenantID string, deleted bool) []string {
	t.Helper()
	fetch := st.ListLayerConfigs
	if deleted {
		fetch = st.ListDeletedLayerConfigs
	}
	ls, err := fetch(context.Background(), tenantID)
	if err != nil {
		t.Errorf("list %s: %v", tenantID, err)
		return nil
	}
	ids := make([]string, 0, len(ls))
	for _, l := range ls {
		ids = append(ids, l.ID)
	}
	slices.Sort(ids)
	return ids
}

// trTenantRows returns tenantID's live and tombstoned rows.
func trTenantRows(t *testing.T, st store.Store, tenantID string) [2][]store.LayerConfig {
	t.Helper()
	ctx := context.Background()
	live, err := st.ListLayerConfigs(ctx, tenantID)
	trMust(t, err)
	deleted, err := st.ListDeletedLayerConfigs(ctx, tenantID)
	trMust(t, err)
	return [2][]store.LayerConfig{live, deleted}
}

func trRepo(tenantID, id string) string {
	return "https://git.invalid/" + tenantID + "/" + url.PathEscape(id)
}

// trUserLayer is a user-defined git layer owned by owner.
func trUserLayer(tenantID, id, owner string, order int) store.LayerConfig {
	return store.LayerConfig{TenantID: tenantID, ID: id, SourceType: "git", Repo: trRepo(tenantID, id),
		UserDefined: true, Owner: owner, Users: []string{owner}, Order: order}
}

// trTombstoned marks lc for seeding as a soft-deleted layer.
func trTombstoned(lc store.LayerConfig) store.LayerConfig {
	lc.DeletedAt = &time.Time{}
	return lc
}

// trPut stores lc and tombstones it when DeletedAt is set.
func trPut(t *testing.T, st store.Store, lc store.LayerConfig) {
	t.Helper()
	ctx := context.Background()
	deleted := lc.DeletedAt != nil
	lc.DeletedAt = nil
	lc.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	trMust(t, st.PutLayerConfig(ctx, lc))
	if deleted {
		trMust(t, st.DeleteLayerConfig(ctx, lc.TenantID, lc.ID))
	}
}

func trMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
}

func trReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// trListSeeds seeds carol's layers in A and dave's in B, including a layer ID
// both tenants hold and one tombstone each.
func trListSeeds() []store.LayerConfig {
	return []store.LayerConfig{
		trUserLayer(trTenantA, "carol-1", trCarol.user, 10),
		trUserLayer(trTenantA, "carol-2", trCarol.user, 20),
		trUserLayer(trTenantA, "team", trCarol.user, 30),
		trTombstoned(trUserLayer(trTenantA, "carol-old", trCarol.user, 40)),
		trUserLayer(trTenantB, "dave-1", trDave.user, 10),
		trUserLayer(trTenantB, "dave-2", trDave.user, 20),
		trUserLayer(trTenantB, "team", trDave.user, 30),
		trTombstoned(trUserLayer(trTenantB, "dave-old", trDave.user, 40)),
	}
}

// Spec: §6.3.1 / §7.3.1 — Case 1: a routed caller lists its own tenant's layers
// alone, on the live arm and on the soft-deleted arm.
func TestLayerTenantRouting_RoutedList(t *testing.T) {
	t.Parallel()
	f := newTenantRoutingFixture(t, trOptions{layers: trListSeeds()})
	cases := []struct {
		name    string
		caller  trCaller
		path    string
		want    []string
		foreign string
	}{
		{"dave live", trDave, "/v1/layers", []string{"dave-1", "dave-2", "team"}, "git.invalid/A/"},
		{"carol live", trCarol, "/v1/layers", []string{"carol-1", "carol-2", "team"}, "git.invalid/B/"},
		{"dave deleted", trDave, "/v1/layers?deleted=true", []string{"dave-old"}, "git.invalid/A/"},
		{"carol deleted", trCarol, "/v1/layers?deleted=true", []string{"carol-old"}, "git.invalid/B/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := f.expect(t, tc.caller, http.MethodGet, tc.path, nil, http.StatusOK, "")
			if got := trListIDs(t, data); !slices.Equal(got, tc.want) {
				t.Errorf("layers = %v, want %v", got, tc.want)
			}
			if bytes.Contains(data, []byte(tc.foreign)) {
				t.Errorf("list names the other tenant's layers: %s", data)
			}
		})
	}
}

// trWriteSequence registers two fresh layers as c in c's tenant, then updates,
// reorders, unregisters, restores, and reingests them. firstOrder, when
// nonzero, is the order the first registration must be stored with. It uses
// t.Errorf alone so it can run in a goroutine.
func (f *trFixture) writeSequence(t *testing.T, c trCaller, prefix string, firstOrder int) {
	one, two := prefix+"-one", prefix+"-two"
	for i, id := range []string{one, two} {
		status, data := f.do(t, c, http.MethodPost, "/v1/layers", map[string]any{
			"id": id, "source_type": "git", "repo": trRepo(c.org, id),
		})
		if status != http.StatusCreated {
			t.Errorf("register %s: status %d (body %s)", id, status, data)
			return
		}
		if got := trRegistered(t, data).Layer.Order; firstOrder != 0 && got != firstOrder+i*10 {
			t.Errorf("register %s stored order %d, want %d", id, got, firstOrder+i*10)
		}
	}
	f.expect(t, c, http.MethodPut, "/v1/layers/update?id="+one, map[string]any{"ref": "v2"}, http.StatusOK, "")
	data := f.expect(t, c, http.MethodPost, "/v1/layers/reorder", map[string]any{"order": []string{two, one}}, http.StatusOK, "")
	if got, want := trListIDs(t, data), trStoredIDs(t, f.st, c.org, false); !slices.Equal(got, want) {
		t.Errorf("reorder in %s returned %v, want the tenant's layers %v", c.org, got, want)
	}
	f.expect(t, c, http.MethodDelete, "/v1/layers?id="+one, nil, http.StatusOK, "")
	if live := trListIDs(t, f.expect(t, c, http.MethodGet, "/v1/layers", nil, http.StatusOK, "")); slices.Contains(live, one) {
		t.Errorf("unregistered %s is still live in %s: %v", one, c.org, live)
	}
	if gone := trListIDs(t, f.expect(t, c, http.MethodGet, "/v1/layers?deleted=true", nil, http.StatusOK, "")); !slices.Contains(gone, one) {
		t.Errorf("unregistered %s is absent from %s's deleted list: %v", one, c.org, gone)
	}
	f.expect(t, c, http.MethodPost, "/v1/layers/restore?id="+one, nil, http.StatusOK, "")
	f.expect(t, c, http.MethodPost, "/v1/layers/reingest?id="+two, nil, http.StatusOK, "")
}

// Spec: §6.3.1 / §7.3.1 / §8.5 / §4.7.2 — Case 2: every layer write a routed
// caller issues acts in its own tenant. An erasure acts in the routed tenant
// alone: the admin check reads that tenant's grants, the purge reaches that
// tenant's layers, and the redaction reaches that tenant's audit records. A
// request that resolves to no tenant is refused before the admin check, any
// store access, and any audit rewrite.
func TestLayerTenantRouting_RoutedWritesAndErase(t *testing.T) {
	t.Parallel()
	seeds := []store.LayerConfig{
		trUserLayer(trTenantA, "carol-1", trCarol.user, 10),
		trUserLayer(trTenantA, "carol-2", trCarol.user, 20),
	}
	f := newTenantRoutingFixture(t, trOptions{alwaysAdmit: true, layers: seeds})

	t.Run("sequential B writes", func(t *testing.T) {
		before := trTenantRows(t, f.st, trTenantA)
		f.writeSequence(t, trDave, "b1", 10)
		if after := trTenantRows(t, f.st, trTenantA); !reflect.DeepEqual(before, after) {
			t.Errorf("A rows changed by B's writes:\nbefore %+v\nafter  %+v", before, after)
		}
		trAssertScopes(t, f.recordedScopes(), map[string]string{"b1": trTenantB})
		if !slices.ContainsFunc(f.recordedScopes(), func(s core.EventScope) bool {
			return slices.Equal(s.Layers, []string{"b1-two", "b1-one"})
		}) {
			t.Errorf("no reorder event recorded: %+v", f.recordedScopes())
		}
	})

	t.Run("parallel A and B writes", func(t *testing.T) {
		var wg sync.WaitGroup
		for _, c := range []struct {
			caller trCaller
			prefix string
		}{{trCarol, "a2"}, {trDave, "b2"}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.writeSequence(t, c.caller, c.prefix, 0)
			}()
		}
		wg.Wait()
		trAssertNoCrossover(t, f.st, trTenantA, "b")
		trAssertNoCrossover(t, f.st, trTenantB, "a2")
		trAssertScopes(t, f.recordedScopes(), map[string]string{"a2": trTenantA, "b1": trTenantB, "b2": trTenantB})
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, cfg := range f.ingested {
			if want := map[byte]string{'a': trTenantA, 'b': trTenantB}[cfg.ID[0]]; cfg.TenantID != want {
				t.Errorf("reingest of %s ran under %s, want %s", cfg.ID, cfg.TenantID, want)
			}
		}
	})

	// The erased user owns layers in A and B, so an erase that ran in the
	// wrong tenant, or a refused erase that ran at all, changes a row the
	// assertions read.
	eraseSeeds := append(slices.Clone(seeds), trUserLayer(trTenantB, "carol-b", trCarol.user, 50))
	t.Run("B admin erase purges only carol-b", func(t *testing.T) {
		g := newTenantRoutingFixture(t, trOptions{grants: map[string][]string{trTenantB: {trDave.user}}, layers: eraseSeeds})
		g.assertScopedErase(t, trDave, trCarol.user)
	})
	// §4.7.2: the admin check runs in the routed tenant, so a grant held in
	// another tenant, or in the boot tenant P that trSeedPoison grants to every
	// subject, does not admit the caller. An admin check evaluated against the
	// bound tenant or an unrouted context would admit olivia here.
	t.Run("erase refused for a caller who is admin only in another tenant", func(t *testing.T) {
		g := newTenantRoutingFixture(t, trOptions{grants: map[string][]string{trTenantB: {trOliviaB.user}}, layers: eraseSeeds})
		g.assertEraseRefusedByAdminCheck(t, trOliviaA, trCarol.user)
	})
	t.Run("erase refused for a caller who is admin only in the boot tenant", func(t *testing.T) {
		g := newTenantRoutingFixture(t, trOptions{layers: eraseSeeds})
		g.assertEraseRefusedByAdminCheck(t, trOliviaA, trCarol.user)
	})
	// erin carries no organization and stays unrouted. An always-admit
	// callback does not override the requireTenant refusal, which runs before
	// the callback is consulted.
	t.Run("erase refused for an unrouted caller under an always-admit callback", func(t *testing.T) {
		g := newTenantRoutingFixture(t, trOptions{alwaysAdmit: true, layers: eraseSeeds})
		g.assertEraseRefused(t, trErin, trCarol.user)
	})

	// §6.3.1: a rejecting provider answers an organization that names no
	// provisioned tenant with 401 auth.tenant_unknown before the handler runs.
	t.Run("erase refused for an unknown org under a rejecting provider", func(t *testing.T) {
		g := newTenantRoutingFixture(t, trOptions{rejectUnknown: true, alwaysAdmit: true, layers: seeds})
		logBefore := trReadFile(t, g.auditPath)
		rowsA := trTenantRows(t, g.st, trTenantA)
		g.expect(t, trFrank, http.MethodPost, "/v1/admin/erase",
			map[string]any{"user_id": trCarol.user, "salt": "s"}, http.StatusUnauthorized, "auth.tenant_unknown")
		if !bytes.Equal(logBefore, trReadFile(t, g.auditPath)) {
			t.Errorf("refused erase rewrote the audit file")
		}
		if n := g.st.writes.Load(); n != 0 {
			t.Errorf("refused erase wrote %d layer rows", n)
		}
		if !reflect.DeepEqual(rowsA, trTenantRows(t, g.st, trTenantA)) {
			t.Errorf("refused erase changed A's rows")
		}
	})

	t.Run("single-tenant erase control", trSingleTenantEraseControl)
}

// assertEraseRefused sends an erase of user as caller and checks the
// unrouted refusal: 403 auth.forbidden with no admin-callback call, the
// user's A and B layers still live, every A and B row unchanged, and a
// byte-identical audit file.
//
// Spec: §8.5, §6.3.1
func (f *trFixture) assertEraseRefused(t *testing.T, caller trCaller, user string) {
	t.Helper()
	f.assertEraseRefusedAfter(t, caller, user, 0)
}

// assertEraseRefusedByAdminCheck is the routed counterpart of
// assertEraseRefused: the request reaches the routed tenant, the admin
// callback runs exactly once and refuses, and nothing changes.
//
// Spec: §8.5, §4.7.2
func (f *trFixture) assertEraseRefusedByAdminCheck(t *testing.T, caller trCaller, user string) {
	t.Helper()
	f.assertEraseRefusedAfter(t, caller, user, 1)
}

// assertEraseRefusedAfter sends an erase of user as caller, expects 403
// auth.forbidden after wantAdminCalls admin-callback calls, and checks that
// the user's layers, the A and B rows, and the audit file are unchanged.
func (f *trFixture) assertEraseRefusedAfter(t *testing.T, caller trCaller, user string, wantAdminCalls int64) {
	t.Helper()
	ownedBefore := f.ownedLayers(t, user)
	if !slices.Contains(ownedBefore, trTenantA+"/carol-1") || !slices.Contains(ownedBefore, trTenantB+"/carol-b") {
		t.Fatalf("erase target %s owns %v, want layers in both A and B", user, ownedBefore)
	}
	rowsA, rowsB := trTenantRows(t, f.st, trTenantA), trTenantRows(t, f.st, trTenantB)
	logBefore := trReadFile(t, f.auditPath)
	f.adminCalls.Store(0)
	f.expect(t, caller, http.MethodPost, "/v1/admin/erase",
		map[string]any{"user_id": user, "salt": "s"}, http.StatusForbidden, "auth.forbidden")
	if n := f.adminCalls.Load(); n != wantAdminCalls {
		t.Errorf("admin callback ran %d times on a refused erase, want %d", n, wantAdminCalls)
	}
	if after := f.ownedLayers(t, user); !slices.Equal(ownedBefore, after) {
		t.Errorf("refused erase changed %s's layers: before %v, after %v", user, ownedBefore, after)
	}
	if !reflect.DeepEqual(rowsA, trTenantRows(t, f.st, trTenantA)) || !reflect.DeepEqual(rowsB, trTenantRows(t, f.st, trTenantB)) {
		t.Errorf("refused erase changed tenant rows")
	}
	if !bytes.Equal(logBefore, trReadFile(t, f.auditPath)) {
		t.Errorf("refused erase rewrote the audit file")
	}
}

// ownedLayers returns the tenant-qualified IDs of user's live layers in A
// and B.
func (f *trFixture) ownedLayers(t *testing.T, user string) []string {
	t.Helper()
	var ids []string
	for _, tenantID := range []string{trTenantA, trTenantB} {
		for _, lc := range trTenantRows(t, f.st, tenantID)[0] {
			if lc.Owner == user {
				ids = append(ids, tenantID+"/"+lc.ID)
			}
		}
	}
	return ids
}

// assertScopedErase seeds one record of user labeled A, one labeled B, and
// one unlabeled, has caller (routed to B and admin there) erase user, and
// checks the §8.5 tenant scope: only B's layer is purged, A's rows are
// unchanged, B's records lose user, A's and the unlabeled records are
// unchanged apart from hash and prev_hash, and user.erased names B and the
// caller over a chain that verifies.
//
// Spec: §8.5, §8.6, §8.1
func (f *trFixture) assertScopedErase(t *testing.T, caller trCaller, user string) {
	t.Helper()
	ctx := context.Background()
	for _, tenant := range []string{trTenantA, trTenantB, ""} {
		trMust(t, f.ep.auditFile.Append(ctx, audit.Event{Type: audit.EventArtifactLoaded,
			Caller: user, Target: "skill/x", Tenant: tenant, Timestamp: time.Now().UTC()}))
	}
	before := trAuditRecords(t, f.auditPath)
	rowsA := trTenantRows(t, f.st, trTenantA)
	data := f.expect(t, caller, http.MethodPost, "/v1/admin/erase",
		map[string]any{"user_id": user, "salt": "s"}, http.StatusOK, "")
	var resp struct {
		Purged   []string `json:"layers_purged"`
		Redacted int      `json:"audit_events_redacted"`
	}
	trMust(t, json.Unmarshal(data, &resp))
	// The B-labeled seed and the layer.user_registered record of the purge.
	if !slices.Equal(resp.Purged, []string{"carol-b"}) || resp.Redacted != 2 {
		t.Errorf("erase = %+v, want carol-b purged and 2 records redacted", resp)
	}
	if !reflect.DeepEqual(rowsA, trTenantRows(t, f.st, trTenantA)) {
		t.Errorf("B's erase changed A's rows")
	}
	if gone := trStoredIDs(t, f.st, trTenantB, true); !slices.Contains(gone, "carol-b") {
		t.Errorf("carol-b is not tombstoned in B: %v", gone)
	}
	after := trAuditRecords(t, f.auditPath)
	trAssertRecordsScoped(t, before, after, trTenantB, user)
	erased := after[len(after)-1]
	if erased["type"] != string(audit.EventUserErased) || erased["tenant"] != trTenantB ||
		erased["caller"].(map[string]any)["identity"] != caller.user {
		t.Errorf("last record = %v, want user.erased in %s by %s", erased, trTenantB, caller.user)
	}
	verify, err := audit.NewFileSink(f.auditPath)
	trMust(t, err)
	if err := verify.Verify(ctx); err != nil {
		t.Errorf("Verify after erase: %v", err)
	}
}

// trAssertRecordsScoped compares the records an erase started from with the
// rewritten file: a record labeled tenant no longer names user, and every
// other record is unchanged apart from its chain fields. The records the
// erase appended carry tenant and no longer name user.
func trAssertRecordsScoped(t *testing.T, before, after []map[string]any, tenant, user string) {
	t.Helper()
	if len(after) < len(before) {
		t.Fatalf("erase dropped records: %d before, %d after", len(before), len(after))
	}
	for i, rec := range after {
		enc, err := json.Marshal(rec)
		trMust(t, err)
		named := bytes.Contains(enc, []byte(user))
		if i >= len(before) || rec["tenant"] == tenant {
			if named || rec["tenant"] != tenant {
				t.Errorf("record %d in the erased tenant: %s", i, enc)
			}
			continue
		}
		if !reflect.DeepEqual(trWithoutChain(before[i]), trWithoutChain(rec)) {
			t.Errorf("record %d outside %s changed:\nbefore %v\nafter  %v", i, tenant, before[i], rec)
		}
	}
}

// trAuditRecords parses every line of the audit file at path.
func trAuditRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(trReadFile(t, path)), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("parse audit line %s: %v", line, err)
		}
		recs = append(recs, rec)
	}
	return recs
}

// trWithoutChain returns rec without the hash and prev_hash keys, which a
// chain rewrite recomputes on every record after the first change.
func trWithoutChain(rec map[string]any) map[string]any {
	out := maps.Clone(rec)
	delete(out, "hash")
	delete(out, "prev_hash")
	return out
}

// trSingleTenantEraseControl is the control for case 2: a router-less endpoint
// without WithTenantRouting, bound to an ordinary tenant S, acts in S with no
// routed tenant, erases the user's layers, and redacts its audit file, so the
// unrouted refusal reads the endpoint's routing mode alone.
//
// Spec: §8.5
func trSingleTenantEraseControl(t *testing.T) {
	const tenantS = "S"
	st := store.NewMemory()
	trMust(t, st.CreateTenant(context.Background(), store.Tenant{ID: tenantS, Name: tenantS}))
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(auditPath)
	trMust(t, err)
	ep := NewLayerEndpoint(st, tenantS, NewModeTracker()).
		WithIdentityResolver(trVerify).WithAudit(sink).WithEraseSink(sink)
	mux := http.NewServeMux()
	mux.Handle("/v1/layers", ep.Handler())
	mux.Handle("/v1/admin/erase", ep.EraseHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	status, data := trSend(t, ts.URL, trCarol.headers(), http.MethodPost, "/v1/layers",
		map[string]any{"id": "carol-s", "source_type": "git", "repo": trRepo(tenantS, "carol-s"), "user_defined": true})
	if status != http.StatusCreated {
		t.Fatalf("register: status %d (body %s)", status, data)
	}
	logBefore := trReadFile(t, auditPath)
	status, data = trSend(t, ts.URL, trDave.headers(), http.MethodPost, "/v1/admin/erase",
		map[string]any{"user_id": trCarol.user, "salt": "s"})
	if status != http.StatusOK {
		t.Fatalf("single-tenant erase: status %d (body %s)", status, data)
	}
	var resp struct {
		Purged   []string `json:"layers_purged"`
		Redacted int      `json:"audit_events_redacted"`
	}
	trMust(t, json.Unmarshal(data, &resp))
	if !slices.Equal(resp.Purged, []string{"carol-s"}) || resp.Redacted == 0 {
		t.Errorf("single-tenant erase = %+v, want carol-s purged and events redacted", resp)
	}
	if bytes.Equal(logBefore, trReadFile(t, auditPath)) {
		t.Errorf("single-tenant erase left the audit file unchanged")
	}
	if live := trStoredIDs(t, st, tenantS, false); len(live) != 0 {
		t.Errorf("single-tenant erase left layers %v", live)
	}
}

// trAssertScopes checks that every recorded event scope names layers of one
// prefix and carries the tenant prefixTenant maps that prefix to.
func trAssertScopes(t *testing.T, scopes []core.EventScope, prefixTenant map[string]string) {
	t.Helper()
	if len(scopes) == 0 {
		t.Fatalf("no events recorded")
	}
	for _, s := range scopes {
		if len(s.Layers) == 0 {
			t.Errorf("event scope names no layer: %+v", s)
			continue
		}
		prefix, _, _ := strings.Cut(s.Layers[0], "-")
		if want, ok := prefixTenant[prefix]; !ok || s.TenantID != want {
			t.Errorf("event on %v scoped to %q, want %q", s.Layers, s.TenantID, want)
		}
	}
}

// trAssertNoCrossover checks that tenantID holds no live or tombstoned layer
// whose ID starts with foreign.
func trAssertNoCrossover(t *testing.T, st store.Store, tenantID, foreign string) {
	t.Helper()
	for _, deleted := range []bool{false, true} {
		for _, id := range trStoredIDs(t, st, tenantID, deleted) {
			if strings.HasPrefix(id, foreign) {
				t.Errorf("tenant %s holds the other tenant's layer %s", tenantID, id)
			}
		}
	}
}

// Spec: §4.7.2 / §7.3.1 — Case 3: the admin gate reads the routed tenant's
// grants, so an admin of A registers admin-defined layers in A alone.
func TestLayerTenantRouting_AdminGatePerTenant(t *testing.T) {
	t.Parallel()
	shared := store.LayerConfig{TenantID: trTenantB, ID: "shared", SourceType: "git", Repo: trRepo(trTenantB, "shared"), Order: 10}
	f := newTenantRoutingFixture(t, trOptions{
		grants: map[string][]string{trTenantA: {trOliviaA.user}},
		layers: []store.LayerConfig{shared},
	})
	reg := func(c trCaller, id string) LayerRegisterResponse {
		data := f.expect(t, c, http.MethodPost, "/v1/layers",
			map[string]any{"id": id, "source_type": "git", "repo": trRepo(c.org, id)}, http.StatusCreated, "")
		return trRegistered(t, data)
	}
	if got := reg(trOliviaA, "olivia-a"); got.Layer.UserDefined {
		t.Errorf("admin of A registered a user-defined layer in A: %+v", got.Layer)
	}
	if got := reg(trOliviaB, "olivia-b"); !got.Layer.UserDefined || got.Layer.Owner != trOliviaB.user {
		t.Errorf("registration in B = %+v, want a user-defined layer owned by olivia", got.Layer)
	}
	ls, err := f.st.ListLayerConfigs(context.Background(), trTenantB)
	trMust(t, err)
	for _, l := range ls {
		if !l.UserDefined && l.ID != "shared" {
			t.Errorf("B holds a new admin-defined layer %s", l.ID)
		}
	}
	before, err := f.st.GetLayerConfig(context.Background(), trTenantB, "shared")
	trMust(t, err)
	f.expect(t, trOliviaB, http.MethodPut, "/v1/layers/update?id=shared", map[string]any{"ref": "v9"},
		http.StatusForbidden, "auth.forbidden")
	after, err := f.st.GetLayerConfig(context.Background(), trTenantB, "shared")
	trMust(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("refused update changed shared: %+v -> %+v", before, after)
	}
}

// Spec: §7.3.1 — Case 4: the user-defined layer cap, the
// owned-layer count, and the registration lookup all read the routed tenant.
func TestLayerTenantRouting_UserLayerCapPerTenant(t *testing.T) {
	t.Parallel()
	daveInA := trCaller{user: trDave.user, org: trTenantA}
	f := newTenantRoutingFixture(t, trOptions{
		quotaA: 1, quotaB: 2,
		layers: []store.LayerConfig{
			trUserLayer(trTenantA, "dave-a1", trDave.user, 10),
			trUserLayer(trTenantA, "dave-a2", trDave.user, 20),
		},
		poisonAdminTwins: []string{"dave-b1", "dave-b2"},
	})
	for _, id := range []string{"dave-b1", "dave-b2"} {
		data := f.expect(t, trDave, http.MethodPost, "/v1/layers",
			map[string]any{"id": id, "source_type": "git", "repo": trRepo(trTenantB, id)}, http.StatusCreated, "")
		if got := trRegistered(t, data).Layer; !got.UserDefined || got.Owner != trDave.user {
			t.Errorf("register %s = %+v, want dave's user-defined layer", id, got)
		}
	}
	data := f.expect(t, daveInA, http.MethodPost, "/v1/layers",
		map[string]any{"id": "dave-a3", "source_type": "git", "repo": trRepo(trTenantA, "dave-a3")},
		http.StatusTooManyRequests, "quota.layer_count_exceeded")
	var env struct {
		Details struct {
			Limit   int `json:"limit"`
			Current int `json:"current"`
		} `json:"details"`
	}
	trMust(t, json.Unmarshal(data, &env))
	if env.Details.Limit != 1 || env.Details.Current != 2 {
		t.Errorf("details = %+v, want limit 1 and current 2", env.Details)
	}
}

// Spec: §6.3.1 / §6.10 — Case 5: under a rejecting provider a verified
// organization naming no tenant is refused with auth.tenant_unknown before the
// endpoint runs; under a non-rejecting one it reaches the endpoint unrouted.
func TestLayerTenantRouting_UnknownOrgRejected(t *testing.T) {
	t.Parallel()
	f := newTenantRoutingFixture(t, trOptions{rejectUnknown: true, layers: trListSeeds()})
	data := f.expect(t, trFrank, http.MethodGet, "/v1/layers", nil, http.StatusUnauthorized, "auth.tenant_unknown")
	var env struct {
		Details map[string]any `json:"details"`
	}
	trMust(t, json.Unmarshal(data, &env))
	if env.Details["token_org_id"] != trUnknownOrg {
		t.Errorf("details.token_org_id = %v, want %q", env.Details["token_org_id"], trUnknownOrg)
	}
	if n := f.endpointHits.Load(); n != 0 {
		t.Errorf("endpoint ran %d times for a refused organization", n)
	}

	g := newTenantRoutingFixture(t, trOptions{layers: trListSeeds()})
	data = g.expect(t, trFrank, http.MethodGet, "/v1/layers", nil, http.StatusOK, "")
	if ids := trListIDs(t, data); len(ids) != 0 {
		t.Errorf("unrouted list = %v, want none", ids)
	}
	if n := g.endpointHits.Load(); n != 1 {
		t.Errorf("endpoint ran %d times, want 1", n)
	}
}

// Spec: §7.3.1 — Case 6: under a router a credential that fails verification
// reaches the endpoint, which answers list and reorder with the verification
// envelope and the other writes with auth.forbidden.
func TestLayerTenantRouting_CredentialFailure(t *testing.T) {
	t.Parallel()
	f := newTenantRoutingFixture(t, trOptions{rejectUnknown: true, layers: trListSeeds()})
	f.expect(t, trFailed, http.MethodGet, "/v1/layers", nil, http.StatusUnauthorized, "auth.untrusted_runtime")
	f.expect(t, trFailed, http.MethodPost, "/v1/layers/reorder", map[string]any{"order": []string{"carol-1"}},
		http.StatusUnauthorized, "auth.untrusted_runtime")
	f.expect(t, trFailed, http.MethodPut, "/v1/layers/update?id=carol-1", map[string]any{"ref": "v2"},
		http.StatusForbidden, "auth.forbidden")
	if n := f.endpointHits.Load(); n != 3 {
		t.Errorf("endpoint ran %d times, want 3", n)
	}
}

// trUnroutedWrites is every layer write plus erasure, aimed at layers A holds
// so a write that fell through to a tenant would find its target. The erase
// row pins the requireTenant-before-authAdmin order: an unrouted erase is
// refused with no admin call and no store write, so a callback that admits
// every caller never reaches the tenant-scoped purge (§8.5, §6.3.1).
var trUnroutedWrites = []struct {
	name, method, path string
	body               any
}{
	{"register", http.MethodPost, "/v1/layers", map[string]any{"id": "unrouted-x", "source_type": "git", "repo": trRepo("x", "unrouted-x")}},
	{"update", http.MethodPut, "/v1/layers/update?id=carol-1", map[string]any{"ref": "v2"}},
	{"restore", http.MethodPost, "/v1/layers/restore?id=carol-old", nil},
	{"unregister", http.MethodDelete, "/v1/layers?id=carol-1", nil},
	{"reorder", http.MethodPost, "/v1/layers/reorder", map[string]any{"order": []string{"carol-1"}}},
	{"reingest", http.MethodPost, "/v1/layers/reingest?id=carol-1", nil},
	{"erase", http.MethodPost, "/v1/admin/erase", map[string]any{"user_id": "carol@acme.com", "salt": "s"}},
}

// assertUntouched checks that no layer row was written, no admin grant was
// read, and the admin callback never ran.
func (f *trFixture) assertUntouched(t *testing.T) {
	t.Helper()
	if n := f.st.writes.Load(); n != 0 {
		t.Errorf("store recorded %d layer writes", n)
	}
	if n := f.st.isAdmin.Load(); n != 0 {
		t.Errorf("store recorded %d admin-grant reads", n)
	}
	if n := f.adminCalls.Load(); n != 0 {
		t.Errorf("admin callback ran %d times", n)
	}
}

// Spec: §7.3.1 / §8.5 — Case 8: a request routed to no
// tenant lists no layers and is refused every write and erasure with
// auth.forbidden before the admin callback, even one that admits every caller.
func TestLayerTenantRouting_UnroutedRefusal(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		caller trCaller
	}{{"anonymous", trAnon}, {"no org", trErin}, {"unknown org", trFrank}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newTenantRoutingFixture(t, trOptions{alwaysAdmit: true, layers: trListSeeds()})
			data := f.expect(t, c.caller, http.MethodGet, "/v1/layers", nil, http.StatusOK, "")
			if ids := trListIDs(t, data); len(ids) != 0 {
				t.Errorf("unrouted list = %v, want none", ids)
			}
			for _, w := range trUnroutedWrites {
				f.expect(t, c.caller, w.method, w.path, w.body, http.StatusForbidden, "auth.forbidden")
			}
			f.assertUntouched(t)
		})
	}
}

// posture reads GET /v1/ui/session as c.
func (f *trFixture) posture(t *testing.T, c trCaller) (subject string, manageAny bool) {
	t.Helper()
	data := f.expect(t, c, http.MethodGet, PathWebUISession, nil, http.StatusOK, "")
	var body struct {
		Subject string `json:"subject"`
		Caps    struct {
			ManageAnyLayer bool `json:"manage_any_layer"`
		} `json:"layer_capabilities"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Errorf("decode posture: %v (body %s)", err, data)
	}
	return body.Subject, body.Caps.ManageAnyLayer
}

// Spec: §7.3.4 / §7.3.1 / §13.10 — Case 10: the posture read
// evaluates manage_any_layer in the routed tenant, reports it false for an
// unrouted request, and refuses no request.
func TestLayerTenantRouting_PostureRead(t *testing.T) {
	t.Parallel()
	t.Run("admin grant per tenant", func(t *testing.T) {
		t.Parallel()
		f := newTenantRoutingFixture(t, trOptions{grants: map[string][]string{trTenantA: {trOliviaA.user}}})
		if _, ok := f.posture(t, trOliviaA); !ok {
			t.Errorf("olivia routed to A: manage_any_layer false, want true")
		}
		if _, ok := f.posture(t, trOliviaB); ok {
			t.Errorf("olivia routed to B: manage_any_layer true, want false")
		}
	})
	t.Run("unrouted under an admitting callback", func(t *testing.T) {
		t.Parallel()
		f := newTenantRoutingFixture(t, trOptions{alwaysAdmit: true})
		for _, c := range []trCaller{trAnon, trErin, trFrank} {
			if _, ok := f.posture(t, c); ok {
				t.Errorf("%+v: manage_any_layer true, want false", c)
			}
		}
		if _, ok := f.posture(t, trDave); !ok {
			t.Errorf("routed B caller: manage_any_layer false, want true")
		}
		// §13.10: the panel offers this caller registration, and the registry
		// refuses it.
		if sub, _ := f.posture(t, trFrank); sub != trFrank.user {
			t.Errorf("unknown-org subject = %q, want %q", sub, trFrank.user)
		}
		f.adminCalls.Store(0)
		f.expect(t, trFrank, http.MethodPost, "/v1/layers",
			map[string]any{"id": "frank-x", "source_type": "git", "repo": trRepo("x", "frank-x")},
			http.StatusForbidden, "auth.forbidden")
		f.assertUntouched(t)
	})
	t.Run("rejecting provider", func(t *testing.T) {
		t.Parallel()
		f := newTenantRoutingFixture(t, trOptions{rejectUnknown: true, alwaysAdmit: true})
		sub, ok := f.posture(t, trFrank)
		if sub != trFrank.user || ok {
			t.Errorf("posture = (%q, %v), want (%q, false)", sub, ok, trFrank.user)
		}
		f.expect(t, trFrank, http.MethodGet, "/v1/layers", nil, http.StatusUnauthorized, "auth.tenant_unknown")
	})
}

// Spec: §6.3.1 / §7.3.1 / §8.5 — Case 11: a multi-tenant
// endpoint behind a server with no tenant router leaves every request
// unrouted: it lists nothing, refuses registration and erasure, writes no row,
// and reports manage_any_layer false.
func TestLayerTenantRouting_NoRouter(t *testing.T) {
	t.Parallel()
	f := newTenantRoutingFixture(t, trOptions{noRouter: true, alwaysAdmit: true, layers: trListSeeds()})
	assertEmpty := func() {
		data := f.expect(t, trDave, http.MethodGet, "/v1/layers", nil, http.StatusOK, "")
		var compact bytes.Buffer
		if err := json.Compact(&compact, data); err != nil || compact.String() != `{"layers":[]}` {
			t.Errorf("list = %s, want {\"layers\":[]}", data)
		}
	}
	assertEmpty()
	f.expect(t, trDave, http.MethodPost, "/v1/layers",
		map[string]any{"id": "dave-x", "source_type": "git", "repo": trRepo(trTenantB, "dave-x")},
		http.StatusForbidden, "auth.forbidden")
	// With no router the request carries no routed tenant even though dave's
	// organization names B, so requireTenant refuses the erase before the
	// admin callback, which admits every caller here, and before any write.
	f.expect(t, trDave, http.MethodPost, "/v1/admin/erase",
		map[string]any{"user_id": trDave.user, "salt": "s"}, http.StatusForbidden, "auth.forbidden")
	assertEmpty()
	if n := f.st.writes.Load(); n != 0 {
		t.Errorf("store recorded %d layer writes", n)
	}
	if _, ok := f.posture(t, trDave); ok {
		t.Errorf("manage_any_layer true with no router, want false")
	}
}
