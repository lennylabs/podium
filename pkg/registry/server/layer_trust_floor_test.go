package server_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 (Layer trust policy), §4.7.11 (Tenant trust floor) — an
// update that carries a trust member below the tenant floor is refused and
// stores nothing, an update that carries none is admitted under the same
// floor, and a floor the endpoint cannot read refuses a registration with
// 500 registry.unavailable.
func TestLayerTrust_FloorAtTheEndpoints(t *testing.T) {
	t.Parallel()
	var floorErr error
	floor := store.TrustFloor{}
	h := newTrustHarness(t, layer.Identity{Sub: "admin", IsAuthenticated: true}, true, func(e *server.LayerEndpoint) *server.LayerEndpoint {
		return e.WithTrustFloors(func(context.Context, string) (store.TrustFloor, error) { return floor, floorErr })
	})
	if resp, body := mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"trust_mode": "none"})); resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d: %s", resp.StatusCode, body)
	}
	floor = store.TrustFloor{Mode: "warn"}
	resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"trust_mode": "", "ref": "release"})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "resolves to warn through the tenant trust floor") || h.stored(t, "finance").Ref != "podium/signed" {
		t.Errorf("update below the floor = %d: %s", resp.StatusCode, body)
	}
	if resp, body := mustPut(t, h.base, "/v1/layers/update?id=finance", map[string]any{"ref": "release"}); resp.StatusCode != http.StatusOK {
		t.Errorf("update without a trust member = %d: %s", resp.StatusCode, body)
	}
	floorErr = errors.New("floor refused")
	resp, body = mustPost(t, h.base, "/v1/layers", gitTrustBody(map[string]any{"id": "other", "trust_mode": "none"}))
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "registry.unavailable") {
		t.Errorf("register under an unreadable floor = %d: %s", resp.StatusCode, body)
	}
}
