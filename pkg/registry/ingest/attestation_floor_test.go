package ingest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// withFloor installs floor as the trust floor of every tenant of f.
func (f *attestFixture) withFloor(floor store.TrustFloor, err error) {
	f.cfg.Floors = func(context.Context, string) (store.TrustFloor, error) { return floor, err }
}

// Spec: §4.7.11 (Tenant trust floor, Policy that became invalid) — the
// floor constraints refuse a cycle in every mode: an explicit none below a
// floor of warn is refused as below_floor and writes its record, a layer
// that omits a required type as missing_required_type, and the served
// constraint follows the floor of the moment. An empty mode resolves to
// the floor's mode, so a signed layer with an empty mode under a floor of
// require verifies and stores.
func TestAttestFloor_CycleConstraints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newAttestFixture(t, "none")
	f.withFloor(store.TrustFloor{Mode: "warn"}, nil)
	code, details := refusal(t, func() error { _, err := f.cycle(); return err }())
	if code != ingest.CodeAttestationPolicyInvalid || details["constraint"] != "below_floor" || len(f.stored()) != 0 ||
		f.record().Status != ingest.CodeAttestationPolicyInvalid {
		t.Fatalf("explicit none below the floor: %s %v, stored %v, record %q", code, details, f.stored(), f.record().Status)
	}

	f.setPolicy("warn", emailSigner())
	f.withFloor(store.TrustFloor{Mode: "warn", RequireTypes: []string{"hook"}}, nil)
	f.signLayer(time.Minute)
	code, details = refusal(t, func() error { _, err := f.cycle(); return err }())
	lc, _ := f.st.GetLayerConfig(ctx, "tenant-1", "team")
	served, err := f.cfg.PolicyConstraint(ctx, lc)
	if code != ingest.CodeAttestationPolicyInvalid || details["constraint"] != "missing_required_type" || served != "missing_required_type" || err != nil {
		t.Fatalf("missing type: %s %v, served %q %v", code, details, served, err)
	}

	f.setPolicy("", emailSigner())
	f.withFloor(store.TrustFloor{Mode: "require"}, nil)
	if _, err := f.cycle(); err != nil || len(f.stored()) != 2 || f.record().Status != "ok" {
		t.Fatalf("empty mode under a require floor: %v, stored %v, record %q", err, f.stored(), f.record().Status)
	}
}

// Spec: §4.7.11 (Tenant trust floor) — a floor that cannot be read fails
// the cycle before it writes anything, and the served-outcome decision and
// a claim-less re-evaluation return the failure rather than a decision.
func TestAttestFloor_UnreadableFloorFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newAttestFixture(t, "none")
	f.withFloor(store.TrustFloor{}, errors.New("floor refused"))
	_, err := f.cycle()
	if err == nil || errors.Is(err, ingest.ErrAttestationRefused) || len(f.stored()) != 0 || f.record().Status != "" {
		t.Fatalf("cycle under an unreadable floor: %v, stored %v, record %q", err, f.stored(), f.record().Status)
	}
	lc, _ := f.st.GetLayerConfig(ctx, "tenant-1", "team")
	if _, err := f.cfg.PolicyConstraint(ctx, lc); err == nil {
		t.Errorf("PolicyConstraint under an unreadable floor = nil error")
	}
	if err := f.cfg.Reevaluate(ctx, f.st, lc, nil, nil); err == nil {
		t.Errorf("Reevaluate under an unreadable floor = nil error")
	}
	var none *ingest.AttestationConfig
	if c, err := none.PolicyConstraint(ctx, store.LayerConfig{TrustMode: "none"}); c != "" || err != nil {
		t.Errorf("a nil configuration decides %q, %v", c, err)
	}
}

// Spec: §4.7.11 (Tenant trust floor) — a stored floor whose mode the
// registry cannot read, as a store edited by hand holds it, fails closed as
// an unreadable floor: the served decision returns an error for an explicit
// none and an empty mode, and a cycle fails before it writes anything,
// rather than read the floor as no minimum.
func TestAttestFloor_UnreadableStoredModeFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newAttestFixture(t, "none")
	f.withFloor(store.TrustFloor{Mode: "REQUIRE"}, nil)
	for _, mode := range []string{"none", ""} {
		if c, err := f.cfg.PolicyConstraint(ctx, store.LayerConfig{TenantID: "tenant-1", ID: "team", TrustMode: mode}); err == nil {
			t.Errorf("layer mode %q under the floor mode REQUIRE decided %q, want an error", mode, c)
		}
	}
	if _, err := f.cycle(); err == nil || errors.Is(err, ingest.ErrAttestationRefused) || len(f.stored()) != 0 || f.record().Status != "" {
		t.Errorf("cycle under the floor mode REQUIRE: %v, stored %v, record %q", err, f.stored(), f.record().Status)
	}
}
