package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// pausingTenantStore holds the first GetTenant of target until release
// closes, after the read, so a second PATCH can write in between.
type pausingTenantStore struct {
	store.Store
	target  string
	once    sync.Once
	read    chan struct{}
	release chan struct{}
}

func (p *pausingTenantStore) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	t, err := p.Store.GetTenant(ctx, id)
	if id == p.target {
		first := false
		p.once.Do(func() { first = true })
		if first {
			close(p.read)
			<-p.release
		}
	}
	return t, err
}

// changingTenantStore refuses every conditional tenant write as changed.
type changingTenantStore struct{ store.Store }

func (changingTenantStore) UpdateTenantIf(context.Context, store.Tenant, store.Tenant) error {
	return store.ErrTenantChanged
}

// floorTenantServer boots a multi-tenant server over st as the operator and
// returns the URL of the acme.com tenant.
func floorTenantServer(t *testing.T, st store.Store, mem *store.Memory) string {
	t.Helper()
	ctx := context.Background()
	if err := mem.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := mem.GrantOperator(ctx, operatorCaller.Sub); err != nil {
		t.Fatal(err)
	}
	target := core.OrgIDForName("acme.com")
	if err := mem.CreateTenant(ctx, store.Tenant{ID: target, Name: "acme.com"}); err != nil {
		t.Fatal(err)
	}
	srv := server.New(core.New(st, "default", nil),
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return operatorCaller }),
		server.WithTenantRouter(func(context.Context, string) (store.Tenant, bool) { return store.Tenant{ID: "default"}, true }, true),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL + "/v1/admin/tenants/" + target
}

// Spec: §7.3.3, §4.7.11 (Tenant trust floor) — a PATCH that carries no
// floor member and read the tenant before another PATCH raised the floor
// does not write the earlier floor back: its conditional write is refused,
// it reads the tenant again, and it applies its quota to the raised floor.
func TestTenants_ConcurrentPatchKeepsTheRaisedFloor(t *testing.T) {
	t.Parallel()
	mem := store.NewMemory()
	ps := &pausingTenantStore{Store: mem, target: core.OrgIDForName("acme.com"), read: make(chan struct{}), release: make(chan struct{})}
	url := floorTenantServer(t, ps, mem)
	done := make(chan int)
	go func() {
		code, _ := tenantHTTP(t, http.MethodPatch, url, `{"quota":{"storage_bytes":5}}`)
		done <- code
	}()
	<-ps.read
	if code, body := tenantHTTP(t, http.MethodPatch, url, `{"trust_floor_mode":"require"}`); code != http.StatusOK {
		t.Fatalf("floor PATCH = %d %s", code, body)
	}
	close(ps.release)
	code := <-done
	got, err := mem.GetTenant(context.Background(), ps.target)
	if code != http.StatusOK || err != nil || got.TrustFloor.Mode != "require" || got.Quota.StorageBytes != 5 {
		t.Errorf("quota PATCH = %d; stored floor %q quota %d (%v), want require and 5", code, got.TrustFloor.Mode, got.Quota.StorageBytes, err)
	}
}

// Spec: §7.3.3 — a PATCH whose conditional write finds the tenant changed
// on every attempt answers 503 registry.unavailable, and a body that
// carries a floor member twice is refused with 400 and stores nothing.
func TestTenants_PatchRefusalsStoreNothing(t *testing.T) {
	t.Parallel()
	mem := store.NewMemory()
	url := floorTenantServer(t, changingTenantStore{mem}, mem)
	if code, body := tenantHTTP(t, http.MethodPatch, url, `{"trust_floor_mode":"require"}`); code != http.StatusServiceUnavailable ||
		!strings.Contains(string(body), "registry.unavailable") {
		t.Errorf("PATCH under constant change = %d %s", code, body)
	}
	mem2 := store.NewMemory()
	url = floorTenantServer(t, mem2, mem2)
	for _, body := range []string{`{"trust_floor_mode":"require","trust_floor_mode":""}`, `{"trust_floor_mode":"require","TRUST_FLOOR_MODE":""}`} {
		code, out := tenantHTTP(t, http.MethodPatch, url, body)
		got, _ := mem2.GetTenant(context.Background(), core.OrgIDForName("acme.com"))
		if code != http.StatusBadRequest || !strings.Contains(string(out), "carried more than once") || !got.TrustFloor.IsZero() {
			t.Errorf("PATCH %s = %d %s, floor %+v", body, code, out, got.TrustFloor)
		}
	}
}
