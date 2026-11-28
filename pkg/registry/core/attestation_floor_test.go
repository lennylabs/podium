package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// listFailStore fails ListLayerConfigs.
type listFailStore struct{ store.Store }

func (listFailStore) ListLayerConfigs(context.Context, string) ([]store.LayerConfig, error) {
	return nil, errors.New("list refused")
}

// Spec: §4.7.11 (Tenant trust floor, Served outcome), §7.3.3 — a policy
// check that cannot decide, such as one whose tenant floor cannot be read,
// fails the load of a verified version rather than serve it as verified.
// InvalidTrustLayers lists the live layers the check finds invalid with
// their constraints, none without a check, and fails on a failed list or
// check.
func TestAttestationServing_FloorDecisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, _ := attFixture(t)
	for _, lc := range []store.LayerConfig{{TenantID: "t", ID: "L", SourceType: "local"}, {TenantID: "t", ID: "M", SourceType: "local", TrustMode: "warn"}} {
		if err := st.PutLayerConfig(ctx, lc); err != nil {
			t.Fatal(err)
		}
	}
	var checkErr error
	check := func(_ context.Context, lc store.LayerConfig) (string, error) {
		if lc.TrustMode == "warn" {
			return "below_floor", checkErr
		}
		return "", checkErr
	}
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).WithAttestationPolicyCheck(check)
	invalid, err := reg.InvalidTrustLayers(ctx, "t")
	if err != nil || len(invalid) != 1 || invalid[0] != (core.InvalidTrustLayer{ID: "M", Constraint: "below_floor"}) {
		t.Errorf("InvalidTrustLayers = %+v, %v", invalid, err)
	}
	checkErr = errors.New("floor refused")
	if _, err := reg.LoadArtifact(ctx, layer.Identity{IsPublic: true}, "a/x", core.LoadArtifactOptions{}); !errors.Is(err, core.ErrUnavailable) {
		t.Errorf("load under an undecidable policy = %v, want ErrUnavailable", err)
	}
	if _, err := reg.InvalidTrustLayers(ctx, "t"); !errors.Is(err, core.ErrUnavailable) {
		t.Errorf("InvalidTrustLayers under a failed check = %v", err)
	}
	if _, err := core.New(listFailStore{st}, "t", nil).WithAttestationPolicyCheck(check).InvalidTrustLayers(ctx, "t"); !errors.Is(err, core.ErrUnavailable) {
		t.Errorf("InvalidTrustLayers under a failed list = %v", err)
	}
	if out, err := core.New(st, "t", nil).InvalidTrustLayers(ctx, "t"); err != nil || out == nil || len(out) != 0 {
		t.Errorf("InvalidTrustLayers without a check = %#v, %v, want an empty list", out, err)
	}
}
