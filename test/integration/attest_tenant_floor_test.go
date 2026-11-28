package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// The callers of the floor fixture: alice reads the layer, dave holds the
// per-tenant admin role of the default tenant, and olivia is the operator.
const (
	floorAlice  = "alice"
	floorDave   = "dave@acme.com"
	floorOlivia = "olivia@acme.com"
)

// floorFixture is the serving fixture's verified layer behind a
// multi-tenant registry: the tenant endpoints, the layer endpoint, and the
// meta-tool routes, all reading the tenant floor from the store.
type floorFixture struct {
	*servingFixture
	ts         *httptest.Server
	reevaluate atomic.Int64
}

// floorOrgHeader carries the organization of a floor fixture request.
const floorOrgHeader = "X-Test-Org"

// newFloorFixture wires a multi-tenant registry over the serving fixture's
// store, as serverboot wires one: the floor of a tenant is its tenant
// object's, the tenant endpoints know the github instance, and a floor
// change re-evaluates the tenant's layers.
func newFloorFixture(t *testing.T) *floorFixture {
	t.Helper()
	ctx := context.Background()
	f := &floorFixture{servingFixture: newServingFixture(t, store.NewMemory())}
	st, cfg := f.st, f.cfg
	if err := st.GrantOperator(ctx, floorOlivia); err != nil {
		t.Fatal(err)
	}
	if err := st.GrantAdmin(ctx, store.AdminGrant{UserID: floorDave, OrgID: "default"}); err != nil {
		t.Fatal(err)
	}
	cfg.Floors = func(ctx context.Context, tenantID string) (store.TrustFloor, error) {
		tn, err := st.GetTenant(ctx, tenantID)
		if errors.Is(err, store.ErrTenantNotFound) {
			return store.TrustFloor{}, nil
		}
		return tn.TrustFloor, err
	}
	known := layer.TrustInstances(func(n string) bool { return n == "github" })
	verify := func(r *http.Request) (layer.Identity, error) {
		user := r.Header.Get(asUserHeader)
		return layer.Identity{Sub: user, Email: user, OrgID: r.Header.Get(floorOrgHeader), IsAuthenticated: user != ""}, nil
	}
	reg := core.New(st, "unrouted", nil).WithAttestationPolicyCheck(cfg.PolicyConstraint)
	srv := server.New(reg,
		server.WithIdentityVerifier(verify),
		server.WithTenantRouter(func(ctx context.Context, org string) (store.Tenant, bool) {
			tn, err := st.GetTenant(ctx, org)
			return tn, err == nil && tn.Active
		}, true),
		server.WithTrustFloorInstances(known),
		server.WithTenantFloorChange(func(ctx context.Context, tenantID, _ string) {
			f.reevaluate.Add(1)
			layers, err := st.ListLayerConfigs(ctx, tenantID)
			if err != nil {
				t.Errorf("list layers: %v", err)
				return
			}
			for _, lc := range layers {
				if err := cfg.Reevaluate(ctx, st, lc, nil, nil); err != nil {
					t.Errorf("re-evaluate %s: %v", lc.ID, err)
				}
			}
		}))
	ep := server.NewLayerEndpoint(st, "unrouted", server.NewModeTracker()).
		WithTenantRouting().
		WithIdentityResolver(verify).
		WithAdminAuth(func(r *http.Request) error {
			id, _ := verify(r)
			return reg.AdminAuthorize(r.Context(), id)
		}).
		WithTrustInstances(known).
		WithTrustFloors(cfg.Floors)
	mux := http.NewServeMux()
	mux.Handle("/v1/layers", srv.TenantRouted(ep.Handler()))
	mux.Handle("/v1/layers/", srv.TenantRouted(ep.Handler()))
	mux.Handle("/", srv.Handler())
	f.ts = httptest.NewServer(mux)
	t.Cleanup(f.ts.Close)
	return f
}

// call sends a JSON request as user of the default organization and returns
// the status and the body.
func (f *floorFixture) call(t *testing.T, method, path, user, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, f.ts.URL+path, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(asUserHeader, user)
	req.Header.Set(floorOrgHeader, "default")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// outcome loads finance/pay as alice and returns its served outcome.
func (f *floorFixture) outcome(t *testing.T) *core.ServedOutcome {
	t.Helper()
	status, raw := f.call(t, http.MethodGet, "/v1/load_artifact?id=finance/pay", floorAlice, "")
	var load provenance
	if status != http.StatusOK || json.Unmarshal(raw, &load) != nil || load.AttestationOutcome == nil {
		t.Fatalf("load = %d: %s", status, raw)
	}
	return load.AttestationOutcome
}

// patchFloor sends a floor update as user and decodes the response.
func (f *floorFixture) patchFloor(t *testing.T, user, body string) (int, map[string]any) {
	t.Helper()
	status, raw := f.call(t, http.MethodPatch, "/v1/admin/tenants/default", user, body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return status, out
}

// floorBody is a floor update whose one signer entry has the members entry.
func floorBody(mode, entry string) string {
	return `{"trust_floor_mode":"` + mode + `","trust_floor_signers":[{"issuer":"` + sh.DefaultIssuer + `"` + entry + `}]}`
}

// Spec: §4.7.11 (Tenant trust floor, Policy that became invalid, Served
// outcome, Re-evaluation), §7.3.3, §7.3.1 (Layer trust policy) — on a
// multi-tenant registry the per-tenant admin cannot set a floor and the
// operator can. A floor whose signer entry the layer's signer does not
// match, by identity or by instance, makes the layer invalid: the update
// lists it in invalid_layers, a load serves its verified version as failed
// with ingest.attestation_policy_invalid before any cycle runs, a cycle is
// refused with the constraint and stores nothing, and a registration below
// the floor is refused. A floor the signer matches lists no layer, and the
// load reports verified again. A floor change re-evaluates the tenant's
// layers, and an update that leaves the floor runs none.
func TestTenantFloor_MultiTenantEndpointsCycleAndServing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFloorFixture(t)
	if o := f.outcome(t); o.Outcome != "verified" {
		t.Fatalf("baseline outcome %+v", o)
	}

	status, _ := f.patchFloor(t, floorDave, floorBody("require", `,"instance":"github"`))
	if tn, _ := f.st.GetTenant(ctx, "default"); status != http.StatusForbidden || !tn.TrustFloor.IsZero() {
		t.Fatalf("admin floor update = %d, floor %+v", status, tn.TrustFloor)
	}

	for _, tc := range []struct {
		name, entry, constraint string
	}{
		{"another identity", `,"instance":"github","identity":"https://other"`, "signer_outside_floor"},
		{"public-good entry for a github signer", `,"identity":"` + sh.DefaultSAN + `"`, "signer_outside_floor"},
		{"matching entry", `,"instance":"github","identity":"` + sh.DefaultSAN + `"`, ""},
	} {
		status, out := f.patchFloor(t, floorOlivia, floorBody("require", tc.entry))
		invalid, _ := out["invalid_layers"].([]any)
		if status != http.StatusOK || out["trust_floor_mode"] != "require" || invalid == nil && tc.constraint != "" {
			t.Fatalf("%s: operator floor update = %d %v", tc.name, status, out)
		}
		o := f.outcome(t)
		if tc.constraint == "" {
			if len(invalid) != 0 || o.Outcome != "verified" {
				t.Errorf("%s: invalid_layers %v, outcome %+v, want none and verified", tc.name, invalid, o)
			}
			continue
		}
		want := map[string]any{"id": "team", "constraint": tc.constraint}
		if len(invalid) != 1 || !jsonEqual(invalid[0], want) {
			t.Errorf("%s: invalid_layers = %v, want [%v]", tc.name, invalid, want)
		}
		if o.Outcome != "failed" || o.Failure == nil || o.Failure.Code != ingest.CodeAttestationPolicyInvalid ||
			o.Failure.Constraint != tc.constraint || o.Failure.Scope != "layer" {
			t.Errorf("%s: served outcome %+v failure %+v", tc.name, o, o.Failure)
		}
	}
	if n := f.reevaluate.Load(); n != 3 {
		t.Errorf("floor changes ran %d re-evaluations, want 3", n)
	}
	if status, out := f.patchFloor(t, floorOlivia, `{"quota":{"storage_bytes":5}}`); status != http.StatusOK || out["invalid_layers"] != nil || f.reevaluate.Load() != 3 {
		t.Errorf("quota update = %d %v, re-evaluations %d", status, out, f.reevaluate.Load())
	}

	status, raw := f.call(t, http.MethodPost, "/v1/layers", floorDave,
		`{"id":"scratch","source_type":"local","local_path":"`+t.TempDir()+`","trust_mode":"warn","trust_requirements":[{"signers":[{"issuer":"`+sh.DefaultIssuer+`","identity":"`+sh.DefaultSAN+`","instance":"github"}]}]}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "below the tenant trust floor's mode require") {
		t.Errorf("registration below the floor = %d: %s", status, raw)
	}

	status, _ = f.patchFloor(t, floorOlivia, floorBody("require", `,"instance":"github","identity":"https://other"`))
	claim, _, claimed, err := ingest.ClaimLayer(ctx, f.st, "default", "team")
	if err != nil || status != http.StatusOK {
		t.Fatalf("claim %v, floor %d", err, status)
	}
	before, _ := f.st.ListManifests(ctx, "default")
	_, err = ingest.Ingest(ctx, f.st, ingest.Request{TenantID: "default", LayerID: "team", IngestClaim: claim,
		Files: source.ConfinedFS(claimed.LocalPath), Attestation: f.cfg.Cycle(claimed, claim)})
	var ae *ingest.AttestationError
	after, _ := f.st.ListManifests(ctx, "default")
	if !errors.Is(err, ingest.ErrAttestationRefused) || !errors.As(err, &ae) || ae.Code != ingest.CodeAttestationPolicyInvalid ||
		ae.Constraint() != "signer_outside_floor" || len(after) != len(before) {
		t.Errorf("cycle under the floor: %v, versions %d -> %d", err, len(before), len(after))
	}
	if lc, _ := f.st.GetLayerConfig(ctx, "default", "team"); lc.Status != ingest.CodeAttestationPolicyInvalid {
		t.Errorf("record status %q", lc.Status)
	}
}

// Spec: §7.3.3, §4.7.11 (Tenant trust floor) — a create and an update that
// carry a floor breaking a floor rule, a null mode, a misspelled floor
// member, or a member twice under different letter case are refused with
// 400 registry.invalid_argument and store nothing; a create stores a valid
// floor, and the tenant object reports it.
func TestTenantFloor_EndpointRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFloorFixture(t)
	for _, body := range []string{
		floorBody("enforce", ""),
		floorBody("warn", `,"instance":"acme-sigstore"`),
		floorBody("warn", `,"source_repository_owner":"o","source_repository_owner_id":"1"`),
		floorBody("warn", `,"source_repository":"x"`),
		`{"trust_floor_mode":null}`,
		`{"trust_floor_mod":"warn"}`,
		`{"trust_floor_mode":"warn","Trust_Floor_Mode":"none"}`,
		`{"trust_floor_require_types":["hooks"]}`,
		`{"trust_floor_require_types":"hook"}`,
	} {
		if status, out := f.patchFloor(t, floorOlivia, body); status != http.StatusBadRequest || out["code"] != "registry.invalid_argument" {
			t.Errorf("PATCH %s = %d %v", body, status, out)
		}
		if status, raw := f.call(t, http.MethodPost, "/v1/admin/tenants", floorOlivia, `{"name":"globex",`+strings.TrimPrefix(body, "{")); status != http.StatusBadRequest {
			t.Errorf("POST %s = %d %s", body, status, raw)
		}
	}
	if tn, _ := f.st.GetTenant(ctx, "default"); !tn.TrustFloor.IsZero() {
		t.Errorf("a refused update stored the floor %+v", tn.TrustFloor)
	}
	status, raw := f.call(t, http.MethodPost, "/v1/admin/tenants", floorOlivia,
		`{"name":"initech","trust_floor_mode":"warn","trust_floor_require_types":["hook"],"trust_floor_signers":null}`)
	var out map[string]any
	if status != http.StatusCreated || json.Unmarshal(raw, &out) != nil || out["trust_floor_mode"] != "warn" ||
		!jsonEqual(out["trust_floor_require_types"], []any{"hook"}) || out["trust_floor_signers"] != nil {
		t.Errorf("create with a floor = %d %s", status, raw)
	}
}

// jsonEqual compares two decoded JSON values through their encodings.
func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
