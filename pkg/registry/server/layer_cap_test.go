package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// registerUserLayer POSTs a user-defined layer owned by owner and
// returns the HTTP status and the error code (empty on success).
func registerUserLayer(t *testing.T, base, id, owner string) (int, string) {
	t.Helper()
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": id, "source_type": "local", "local_path": "/tmp/" + id,
		"user_defined": true, "owner": owner,
	})
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &env)
	return resp.StatusCode, env.Code
}

func countUserLayers(t *testing.T, st store.Store, owner string) int {
	t.Helper()
	all, err := st.ListLayerConfigs(context.Background(), "t")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	n := 0
	for _, l := range all {
		if l.UserDefined && l.Owner == owner {
			n++
		}
	}
	return n
}

// Spec: §7.3.1 / §1.4 (F-1.4.1) — the default cap is 3 user-defined
// layers per identity; the 4th registration is rejected with a
// quota.layer_count_exceeded error at HTTP 429, and the rejected layer
// is not persisted.
func TestLayerCap_DefaultThreePerIdentity(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	for i, id := range []string{"a", "b", "c"} {
		status, code := registerUserLayer(t, base, id, "alice")
		if status != http.StatusCreated {
			t.Fatalf("layer %d (%s): status %d code %q, want 201", i+1, id, status, code)
		}
	}
	status, code := registerUserLayer(t, base, "d", "alice")
	if status != http.StatusTooManyRequests {
		t.Fatalf("4th layer: status %d, want 429", status)
	}
	if code != "quota.layer_count_exceeded" {
		t.Errorf("4th layer: code %q, want quota.layer_count_exceeded", code)
	}
	if got := countUserLayers(t, st, "alice"); got != 3 {
		t.Errorf("stored user layers for alice = %d, want 3 (rejected layer must not persist)", got)
	}
}

// Spec: §7.3.1 — the cap is per identity, so a second owner can still
// register up to the cap even after the first owner is at the limit.
func TestLayerCap_PerIdentityBuckets(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	for _, id := range []string{"a", "b", "c"} {
		if status, code := registerUserLayer(t, base, "alice-"+id, "alice"); status != http.StatusCreated {
			t.Fatalf("alice %s: status %d code %q", id, status, code)
		}
	}
	// Alice is at the cap; bob is a distinct identity and starts fresh.
	if status, code := registerUserLayer(t, base, "bob-a", "bob"); status != http.StatusCreated {
		t.Fatalf("bob first layer: status %d code %q, want 201", status, code)
	}
	// Alice's 4th still fails.
	if status, _ := registerUserLayer(t, base, "alice-d", "alice"); status != http.StatusTooManyRequests {
		t.Errorf("alice 4th: status %d, want 429", status)
	}
}

// Spec: §7.3.1 — re-registering an already-owned layer (same id) is an
// update, not an additional layer, so it does not count against the cap.
func TestLayerCap_ReRegisterSameIDIsNotCounted(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	for _, id := range []string{"a", "b", "c"} {
		if status, code := registerUserLayer(t, base, id, "alice"); status != http.StatusCreated {
			t.Fatalf("%s: status %d code %q", id, status, code)
		}
	}
	// Re-posting an existing owned id must succeed (the owner stays at 3).
	if status, code := registerUserLayer(t, base, "b", "alice"); status != http.StatusCreated {
		t.Errorf("re-register b: status %d code %q, want 201", status, code)
	}
}

// Spec: §1.4 / §4.4 — the cap is configurable per tenant via the tenant
// quota. A tenant quota of 1 rejects the second registration.
func TestLayerCap_TenantQuotaOverride(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{
		ID:    "t",
		Quota: store.Quota{MaxUserLayers: 1},
	}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	if status, code := registerUserLayer(t, ts.URL, "a", "alice"); status != http.StatusCreated {
		t.Fatalf("first layer: status %d code %q, want 201", status, code)
	}
	status, code := registerUserLayer(t, ts.URL, "b", "alice")
	if status != http.StatusTooManyRequests || code != "quota.layer_count_exceeded" {
		t.Errorf("second layer: status %d code %q, want 429 quota.layer_count_exceeded", status, code)
	}
}

// Spec: §7.3.1 — WithMaxUserLayers takes precedence over the tenant
// quota and the default, and a negative value disables the cap entirely.
func TestLayerCap_NegativeDisablesCap(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	// Tenant quota would cap at 2, but the explicit override disables it.
	_ = st.CreateTenant(context.Background(), store.Tenant{
		ID: "t", Quota: store.Quota{MaxUserLayers: 2},
	})
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithMaxUserLayers(-1)
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if status, code := registerUserLayer(t, ts.URL, id, "alice"); status != http.StatusCreated {
			t.Fatalf("layer %s with cap disabled: status %d code %q, want 201", id, status, code)
		}
	}
}

// Spec: §7.3.1 — WithMaxUserLayers wins over the default. A cap of 1
// rejects the second registration even though the default is 3.
func TestLayerCap_WithMaxUserLayersOverridesDefault(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithMaxUserLayers(1)
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()

	if status, _ := registerUserLayer(t, ts.URL, "a", "alice"); status != http.StatusCreated {
		t.Fatalf("first layer: status %d, want 201", status)
	}
	if status, code := registerUserLayer(t, ts.URL, "b", "alice"); status != http.StatusTooManyRequests {
		t.Errorf("second layer: status %d code %q, want 429", status, code)
	}
}

// Spec: §7.3.1 — the cap applies only to user-defined layers. Admin-
// defined layers (UserDefined=false) are not counted against it, so an
// admin can register more than the default of 3.
func TestLayerCap_AdminLayersNotCapped(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	for _, id := range []string{"adm-a", "adm-b", "adm-c", "adm-d", "adm-e"} {
		resp, body := mustPost(t, base, "/v1/layers", map[string]any{
			"id": id, "source_type": "local", "local_path": "/tmp/" + id,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("admin layer %s: status %d body=%s", id, resp.StatusCode, body)
		}
	}
}
