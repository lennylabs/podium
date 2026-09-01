package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// errCode decodes the §6.10 error envelope and returns its code.
func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope %q: %v", body, err)
	}
	return env.Code
}

// errLayerLookup is the fault a layerFaultStore injects into the register
// handler's existence lookup. It is deliberately not store.ErrNotFound, which
// is the discrimination the lookup runs.
var errLayerLookup = errors.New("layer store unavailable")

// layerFaultStore fails one of the two reads the register handler's existence
// lookup makes, so a test can pin the refusal each failure produces. The
// shipped storetest.FaultStore overrides the tenant health call alone, so the
// layer reads reach the wrapped store through promotion and cannot be failed
// there.
type layerFaultStore struct {
	store.Store
	failGet     bool
	failDeleted bool
}

func (s *layerFaultStore) GetLayerConfig(ctx context.Context, tenantID, id string) (store.LayerConfig, error) {
	if s.failGet {
		return store.LayerConfig{}, errLayerLookup
	}
	return s.Store.GetLayerConfig(ctx, tenantID, id)
}

func (s *layerFaultStore) ListDeletedLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	if s.failDeleted {
		return nil, errLayerLookup
	}
	return s.Store.ListDeletedLayerConfigs(ctx, tenantID)
}

// Callers the layer-write cases drive. aliceID owns every seeded user-defined
// layer; bobID is a different verified subject; noSubject is the anonymous
// resolution §6.3.3 produces while an issuer's JWKS is unreachable.
var (
	aliceID   = layer.Identity{Sub: "alice", IsAuthenticated: true}
	bobID     = layer.Identity{Sub: "bob", IsAuthenticated: true}
	noSubject = layer.Identity{IsPublic: true}
)

// newLayerWriteServer builds a layer endpoint over st whose admin arm admits
// or denies as admin says and whose identity resolver resolves caller. A
// refusal case needs both overrides: the bare NewLayerEndpoint installs an
// admin authorizer that admits every caller and a resolver that resolves no
// subject, so overriding one leaves the other admitting the request.
func newLayerWriteServer(t *testing.T, st store.Store, admin bool, caller layer.Identity, runner server.ReingestRunner) string {
	t.Helper()
	e := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAdminAuth(func(*http.Request) error {
			if admin {
				return nil
			}
			return server.ErrAdminRequired
		}).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return caller, nil })
	if runner != nil {
		e = e.WithReingestRunner(runner)
	}
	ts := httptest.NewServer(e.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// newLayerWriteStore returns a memory store with tenant "t" created.
func newLayerWriteStore(t *testing.T) store.Store {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return st
}

// seedLayer writes cfg directly through the store so the seeding is not itself
// subject to the gate under test.
func seedLayer(t *testing.T, st store.Store, cfg store.LayerConfig) {
	t.Helper()
	cfg.TenantID = "t"
	if cfg.SourceType == "" {
		cfg.SourceType = "local"
	}
	if cfg.LocalPath == "" {
		cfg.LocalPath = "/tmp/seed"
	}
	if err := st.PutLayerConfig(context.Background(), cfg); err != nil {
		t.Fatalf("PutLayerConfig(%s): %v", cfg.ID, err)
	}
}

// layerWriteOp is one gated write, with the store state it needs and the
// request that drives it.
type layerWriteOp struct {
	name string
	// tombstoned seeds the layer soft-deleted rather than live (restore).
	tombstoned bool
	do         func(t *testing.T, base string) (*http.Response, []byte)
}

func layerWriteOps() []layerWriteOp {
	return []layerWriteOp{
		{name: "unregister", do: func(t *testing.T, base string) (*http.Response, []byte) {
			return mustDelete(t, base, "/v1/layers?id=own")
		}},
		{name: "update", do: func(t *testing.T, base string) (*http.Response, []byte) {
			return putJSON(t, base, "/v1/layers/update?id=own", map[string]any{"ref": "release"})
		}},
		{name: "restore", tombstoned: true, do: func(t *testing.T, base string) (*http.Response, []byte) {
			return mustPost(t, base, "/v1/layers/restore?id=own", nil)
		}},
		{name: "reorder", do: func(t *testing.T, base string) (*http.Response, []byte) {
			return mustPost(t, base, "/v1/layers/reorder", map[string]any{"order": []string{"own"}})
		}},
		{name: "reingest", do: func(t *testing.T, base string) (*http.Response, []byte) {
			return mustPost(t, base, "/v1/layers/reingest?id=own", nil)
		}},
	}
}

// Spec: §7.3.1 — the layer-write authorization rule over a stored
// user-defined layer: its stored owner and a tenant admin are authorized, a
// different verified subject and a caller resolving no subject are refused
// with 403 auth.forbidden. The no-subject arm is the state §6.3.3 makes
// routine by treating a request as anonymous while an issuer's JWKS is
// unreachable.
func TestLayerWriteAuth_UserDefinedOwnerOrAdmin(t *testing.T) {
	t.Parallel()
	callers := []struct {
		name  string
		id    layer.Identity
		admin bool
		want  int
	}{
		{name: "owner", id: aliceID, want: http.StatusOK},
		{name: "other-subject", id: bobID, want: http.StatusForbidden},
		{name: "no-subject", id: noSubject, want: http.StatusForbidden},
		{name: "admin", id: bobID, admin: true, want: http.StatusOK},
	}
	for _, op := range layerWriteOps() {
		for _, c := range callers {
			t.Run(op.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				st := newLayerWriteStore(t)
				seedLayer(t, st, store.LayerConfig{ID: "own", UserDefined: true, Owner: "alice", Users: []string{"alice"}})
				if op.tombstoned {
					if err := st.DeleteLayerConfig(context.Background(), "t", "own"); err != nil {
						t.Fatalf("DeleteLayerConfig: %v", err)
					}
				}
				base := newLayerWriteServer(t, st, c.admin, c.id, nil)
				resp, body := op.do(t, base)
				if resp.StatusCode != c.want {
					t.Fatalf("%s status = %d, want %d: %s", op.name, resp.StatusCode, c.want, body)
				}
				if c.want == http.StatusForbidden && errCode(t, body) != "auth.forbidden" {
					t.Errorf("%s code = %q, want auth.forbidden", op.name, errCode(t, body))
				}
			})
		}
	}
}

// Spec: §7.3.1 — on a stored admin-defined layer a reingest is authorized to
// a tenant admin alone, whatever the stored owner field names, because that
// field is supplied by the requesting caller. A refused caller runs no
// ingest, and a break-glass body is refused on the same terms, because the
// gate runs before the pipeline and so bypasses no freeze window (§4.7.2).
func TestLayerWriteAuth_ReingestAdminDefined(t *testing.T) {
	t.Parallel()
	callers := []struct {
		name  string
		id    layer.Identity
		admin bool
		want  int
	}{
		{name: "authenticated-non-admin", id: bobID, want: http.StatusForbidden},
		{name: "stored-owner", id: aliceID, want: http.StatusForbidden},
		{name: "no-subject", id: noSubject, want: http.StatusForbidden},
		{name: "admin", id: bobID, admin: true, want: http.StatusOK},
	}
	bodies := []struct {
		name string
		body any
	}{
		{name: "plain", body: nil},
		{name: "break-glass", body: map[string]any{
			"break_glass": true, "justification": "incident 7", "approvers": []string{"ops", "sec"},
		}},
	}
	for _, c := range callers {
		for _, b := range bodies {
			t.Run(c.name+"/"+b.name, func(t *testing.T) {
				t.Parallel()
				st := newLayerWriteStore(t)
				// The stored Owner names alice on an admin-defined layer,
				// which is the caller-supplied field the rule disregards.
				seedLayer(t, st, store.LayerConfig{ID: "own", Owner: "alice"})
				var ran atomic.Int64
				runner := func(context.Context, store.LayerConfig, *server.BreakGlass) (*ingest.Result, error) {
					ran.Add(1)
					return &ingest.Result{Accepted: 1}, nil
				}
				base := newLayerWriteServer(t, st, c.admin, c.id, runner)
				resp, body := mustPost(t, base, "/v1/layers/reingest?id=own", b.body)
				if resp.StatusCode != c.want {
					t.Fatalf("reingest status = %d, want %d: %s", resp.StatusCode, c.want, body)
				}
				if c.want == http.StatusForbidden {
					if code := errCode(t, body); code != "auth.forbidden" {
						t.Errorf("reingest code = %q, want auth.forbidden", code)
					}
					if n := ran.Load(); n != 0 {
						t.Errorf("refused reingest ran the pipeline %d times, want 0", n)
					}
					return
				}
				if n := ran.Load(); n != 1 {
					t.Errorf("authorized reingest ran the pipeline %d times, want 1", n)
				}
			})
		}
	}
}

// registerCaller is one point on the caller axis of the registration product.
type registerCaller struct {
	name  string
	id    layer.Identity
	admin bool
}

// registerIDState is one point on the stored-ID axis: the state the posted ID
// is in and the stored layer that holds it.
type registerIDState struct {
	name string
	// seed is nil when the ID names no stored layer.
	seed *store.LayerConfig
	// tombstoned soft-deletes the seeded layer, which leaves it inside the
	// §8.4 recovery window and therefore existing for the rule.
	tombstoned bool
}

// registerLookup is one point on the existence lookup's health axis.
type registerLookup struct {
	name        string
	failGet     bool
	failDeleted bool
}

// Spec: §7.3.1, §8.4 — the layer-write authorization rule as register
// applies it, over the product of the caller's relation to the stored layer,
// the stored layer's class, the posted ID's state in the store, the existence
// lookup's health, and the request body. PutLayerConfig is an upsert keyed on
// (tenant_id, id), so a registration under a stored ID is a write against
// that layer; a tombstoned ID inside the recovery window is stored for this
// rule, which a GetLayerConfig-only lookup gets wrong; a failed lookup is
// refused with 500 registry.unavailable rather than read as an unused ID; and
// an unused ID is admitted only to a caller the admin arm admits or one
// resolving a verified subject, which is what keeps a caller resolving no
// subject from minting a layer owned by a body-supplied subject.
func TestLayerRegister_TakeoverProduct(t *testing.T) {
	t.Parallel()
	callers := []registerCaller{
		{name: "stored-owner", id: aliceID},
		{name: "other-subject", id: bobID},
		{name: "admin", id: bobID, admin: true},
		{name: "no-subject", id: noSubject},
	}
	userDefined := store.LayerConfig{ID: "own", UserDefined: true, Owner: "alice", Users: []string{"alice"}}
	adminDefined := store.LayerConfig{ID: "own", Owner: "alice"}
	idStates := []registerIDState{
		{name: "live-user-defined", seed: &userDefined},
		{name: "tombstoned-user-defined", seed: &userDefined, tombstoned: true},
		{name: "live-admin-defined", seed: &adminDefined},
		{name: "tombstoned-admin-defined", seed: &adminDefined, tombstoned: true},
		{name: "unused"},
	}
	lookups := []registerLookup{
		{name: "healthy"},
		{name: "live-read-fails", failGet: true},
		// The tombstone scan runs only when the live read answers
		// store.ErrNotFound, so this fault is observable exactly on the ID
		// states the live set does not hold.
		{name: "tombstone-read-fails", failDeleted: true},
	}
	bodies := []struct {
		name  string
		extra map[string]any
	}{
		{name: "id-only"},
		{name: "asserts-user-defined", extra: map[string]any{"user_defined": true, "owner": "bob"}},
	}

	for _, c := range callers {
		for _, idState := range idStates {
			for _, lk := range lookups {
				for _, b := range bodies {
					t.Run(c.name+"/"+idState.name+"/"+lk.name+"/"+b.name, func(t *testing.T) {
						t.Parallel()
						mem := newLayerWriteStore(t)
						if idState.seed != nil {
							seedLayer(t, mem, *idState.seed)
							if idState.tombstoned {
								if err := mem.DeleteLayerConfig(context.Background(), "t", "own"); err != nil {
									t.Fatalf("DeleteLayerConfig: %v", err)
								}
							}
						}
						st := &layerFaultStore{Store: mem, failGet: lk.failGet, failDeleted: lk.failDeleted}
						base := newLayerWriteServer(t, st, c.admin, c.id, nil)

						req := map[string]any{"id": "own", "source_type": "local", "local_path": "/tmp/posted"}
						for k, v := range b.extra {
							req[k] = v
						}
						resp, body := mustPost(t, base, "/v1/layers", req)

						wantStatus, wantCode := wantRegisterOutcome(c, idState, lk)
						if resp.StatusCode != wantStatus {
							t.Fatalf("register status = %d, want %d: %s", resp.StatusCode, wantStatus, body)
						}
						if wantCode != "" {
							if code := errCode(t, body); code != wantCode {
								t.Errorf("register code = %q, want %q", code, wantCode)
							}
							assertLayerUnchanged(t, mem, idState)
							return
						}
						assertRegisteredOwner(t, mem, c, idState, b.extra != nil)
					})
				}
			}
		}
	}
}

// wantRegisterOutcome is the outcome rule the registration product asserts:
// a failed lookup refuses with 500 registry.unavailable; a stored ID takes
// the arm its class selects; an unused ID admits a caller the admin arm
// admits or one resolving a verified subject. The request body changes no
// outcome at any point.
func wantRegisterOutcome(c registerCaller, idState registerIDState, lk registerLookup) (int, string) {
	storedMissing := idState.seed == nil || idState.tombstoned
	if lk.failGet || (lk.failDeleted && storedMissing) {
		return http.StatusInternalServerError, "registry.unavailable"
	}
	if c.admin {
		return http.StatusCreated, ""
	}
	if idState.seed == nil {
		if c.id.IsAuthenticated && c.id.Sub != "" {
			return http.StatusCreated, ""
		}
		return http.StatusForbidden, "auth.forbidden"
	}
	if idState.seed.UserDefined && c.id.Sub == idState.seed.Owner && c.id.IsAuthenticated {
		return http.StatusCreated, ""
	}
	return http.StatusForbidden, "auth.forbidden"
}

// assertLayerUnchanged confirms a refusal wrote nothing: a seeded layer keeps
// its stored owner, class, and source, a tombstoned one is still tombstoned,
// and an unused ID is still unused.
func assertLayerUnchanged(t *testing.T, st store.Store, idState registerIDState) {
	t.Helper()
	ctx := context.Background()
	got, err := st.GetLayerConfig(ctx, "t", "own")
	if idState.seed == nil || idState.tombstoned {
		if err == nil {
			t.Fatalf("refused register left a live layer: %+v", got)
		}
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetLayerConfig: %v", err)
		}
	}
	if idState.seed == nil {
		return
	}
	if idState.tombstoned {
		deleted, err := st.ListDeletedLayerConfigs(ctx, "t")
		if err != nil {
			t.Fatalf("ListDeletedLayerConfigs: %v", err)
		}
		for _, l := range deleted {
			if l.ID == "own" {
				return
			}
		}
		t.Fatal("refused register cleared the tombstone the recovery window holds")
	}
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.Owner != idState.seed.Owner || got.UserDefined != idState.seed.UserDefined || got.LocalPath != "/tmp/seed" {
		t.Errorf("refused register rewrote the stored layer: %+v", got)
	}
}

// assertRegisteredOwner confirms an admitted registration stores the owner
// the rule names: the resolved subject where the registration resolves to a
// user-defined layer, and the body-supplied owner on an admin-defined one.
func assertRegisteredOwner(t *testing.T, st store.Store, c registerCaller, idState registerIDState, bodyAssertsUserDefined bool) {
	t.Helper()
	got, err := st.GetLayerConfig(context.Background(), "t", "own")
	if err != nil {
		t.Fatalf("GetLayerConfig after an admitted register: %v", err)
	}
	// The class the handler resolves: the body's assertion, or the
	// promotion an authenticated non-admin caller gets.
	userDefined := bodyAssertsUserDefined || (!c.admin && c.id.IsAuthenticated && c.id.Sub != "")
	if got.UserDefined != userDefined {
		t.Fatalf("stored UserDefined = %v, want %v", got.UserDefined, userDefined)
	}
	want := "bob" // the body-supplied owner on the asserting body
	if !bodyAssertsUserDefined {
		want = ""
	}
	if userDefined && c.id.IsAuthenticated && c.id.Sub != "" {
		want = c.id.Sub
	}
	if got.Owner != want {
		t.Errorf("stored Owner = %q, want %q", got.Owner, want)
	}
	_ = idState
}

// Spec: §7.3.1, §8.4 — the recovery window is not a window in which a layer
// ID can be taken over. alice registers a user-defined layer and unregisters
// it, bob's registration under that ID is refused, and alice's restore still
// recovers the layer, which is what asserts that the refusal preserved the
// tombstone as well as that it refused.
func TestLayerRegister_RecoveryWindowSequence(t *testing.T) {
	t.Parallel()
	st := newLayerWriteStore(t)
	aliceBase := newLayerWriteServer(t, st, false, aliceID, nil)

	resp, body := mustPost(t, aliceBase, "/v1/layers", map[string]any{
		"id": "shared-id", "source_type": "local", "local_path": "/tmp/alice", "user_defined": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("alice register status = %d: %s", resp.StatusCode, body)
	}
	resp, body = mustDelete(t, aliceBase, "/v1/layers?id=shared-id")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alice unregister status = %d: %s", resp.StatusCode, body)
	}

	bobBase := newLayerWriteServer(t, st, false, bobID, nil)
	resp, body = mustPost(t, bobBase, "/v1/layers", map[string]any{
		"id": "shared-id", "source_type": "local", "local_path": "/tmp/bob", "user_defined": true,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bob re-registration status = %d, want 403: %s", resp.StatusCode, body)
	}
	if code := errCode(t, body); code != "auth.forbidden" {
		t.Errorf("bob re-registration code = %q, want auth.forbidden", code)
	}

	resp, body = mustPost(t, aliceBase, "/v1/layers/restore?id=shared-id", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alice restore status = %d, want 200: %s", resp.StatusCode, body)
	}
	got, err := st.GetLayerConfig(context.Background(), "t", "shared-id")
	if err != nil {
		t.Fatalf("GetLayerConfig after restore: %v", err)
	}
	if got.Owner != "alice" || got.LocalPath != "/tmp/alice" {
		t.Errorf("restored layer = %+v, want alice's layer at /tmp/alice", got)
	}
}

// Spec: §7.3.1 — the write paths raise no authentication error of their own.
// They read the caller through the endpoint's swallowing helper, so a
// credential the resolver reports as failing verification resolves the
// anonymous-public caller and is refused with 403 auth.forbidden, which is the
// disposition those paths took before the resolver carried an error.
func TestLayerWriteAuth_UnverifiedCredentialStaysForbidden(t *testing.T) {
	t.Parallel()
	st := newLayerWriteStore(t)
	seedLayer(t, st, store.LayerConfig{ID: "own", UserDefined: true, Owner: "alice", Users: []string{"alice"}})
	e := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithAdminAuth(func(*http.Request) error { return server.ErrAdminRequired }).
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
			return layer.Identity{}, identity.ErrTokenExpired
		})
	ts := httptest.NewServer(e.Handler())
	t.Cleanup(ts.Close)

	resp, body := putJSON(t, ts.URL, "/v1/layers/update?id=own", map[string]any{"ref": "release"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("update status = %d, want 403: %s", resp.StatusCode, body)
	}
	if code := errCode(t, body); code != "auth.forbidden" {
		t.Errorf("update code = %q, want auth.forbidden", code)
	}
}
