package layer_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.11 (Tenant trust floor) — a signer matches an entry on equal
// issuers and instances, an owner on a path boundary, an owner identifier,
// a ref, and an identity; any_source matches no owner entry and any_ref no
// ref entry.
func TestFloorSignerMatches(t *testing.T) {
	t.Parallel()
	owner := "https://github.com/acme"
	with := func(fn func(*store.TrustSigner)) store.TrustSigner { s := boundSigner(); fn(&s); return s }
	cases := []struct {
		name  string
		s     store.TrustSigner
		e     store.TrustFloorSigner
		match bool
	}{
		{"issuer alone", boundSigner(), store.TrustFloorSigner{Issuer: ghIssuer}, true},
		{"other issuer", boundSigner(), store.TrustFloorSigner{Issuer: "https://other"}, false},
		{"entry public-good named", boundSigner(), store.TrustFloorSigner{Issuer: ghIssuer, Instance: "public-good"}, true},
		{"signer of another instance, entry with none", with(func(s *store.TrustSigner) { s.Instance = "acme-sigstore" }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: owner}, false},
		{"signer and entry of that instance", with(func(s *store.TrustSigner) { s.Instance = "acme-sigstore" }), store.TrustFloorSigner{Issuer: ghIssuer, Instance: "acme-sigstore", SourceRepositoryOwner: owner}, true},
		{"repository under the owner", boundSigner(), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: owner}, true},
		{"owner named", with(func(s *store.TrustSigner) { s.SourceRepository = ""; s.SourceRepositoryOwner = owner }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: owner}, true},
		{"owner prefix of another owner", with(func(s *store.TrustSigner) { s.SourceRepository = "https://github.com/acme-tools/x" }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: owner}, false},
		{"two segments below the owner", with(func(s *store.TrustSigner) { s.SourceRepository = owner + "/x/y" }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: owner}, false},
		{"dot segment below the owner", with(func(s *store.TrustSigner) { s.SourceRepository = owner + "/.." }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: owner}, false},
		{"owner itself as repository", with(func(s *store.TrustSigner) { s.SourceRepository = owner + "/" }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: owner}, false},
		{"any_source against an owner", with(func(s *store.TrustSigner) {
			s.SourceRepository = ""
			s.AnySource = true
			s.SourceRepositoryOwner = owner
		}), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: owner}, false},
		{"owner identifier", with(func(s *store.TrustSigner) { s.SourceRepositoryOwnerID = "42" }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwnerID: "42"}, true},
		{"derived binding against an owner identifier", boundSigner(), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwnerID: "42"}, false},
		{"any_source against an owner identifier", with(func(s *store.TrustSigner) { s.AnySource = true; s.SourceRepositoryOwnerID = "42" }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwnerID: "42"}, false},
		{"ref", boundSigner(), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryRef: "refs/heads/main"}, true},
		{"other ref", boundSigner(), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryRef: "refs/heads/release"}, false},
		{"any_ref against a ref", with(func(s *store.TrustSigner) { s.SourceRepositoryRef = ""; s.AnyRef = true }), store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryRef: "refs/heads/main"}, false},
		{"identity", boundSigner(), store.TrustFloorSigner{Issuer: ghIssuer, Identity: ownWorkflow}, true},
		{"other identity", boundSigner(), store.TrustFloorSigner{Issuer: ghIssuer, Identity: ownWorkflow + "x"}, false},
	}
	for _, tc := range cases {
		if got := layer.FloorSignerMatches(tc.s, tc.e); got != tc.match {
			t.Errorf("%s: FloorSignerMatches = %v, want %v", tc.name, got, tc.match)
		}
	}
}

// Spec: §4.7.11 (Tenant trust floor, Policy that became invalid) — a layer
// meets the floor or is refused with the constraint of the rule it breaks.
func TestCheckFloor_Constraints(t *testing.T) {
	t.Parallel()
	signed := policy("warn", boundSigner())
	floor := store.TrustFloor{Mode: "warn", RequireTypes: []string{"hook"},
		Signers: []store.TrustFloorSigner{{Issuer: ghIssuer, SourceRepositoryOwnerID: "42"}}}
	withTypes := func(cfg store.LayerConfig, types ...string) store.LayerConfig {
		cfg.TrustRequireTypes = types
		return cfg
	}
	cases := []struct {
		name, want string
		cfg        store.LayerConfig
		floor      store.TrustFloor
		def        string
	}{
		{"no floor", "", signed, store.TrustFloor{}, ""},
		{"mode met", "", signed, store.TrustFloor{Mode: "warn"}, ""},
		{"empty mode raised", "", policy("", boundSigner()), store.TrustFloor{Mode: "require"}, ""},
		{"explicit none below", layer.ConstraintBelowFloor, policy("none"), store.TrustFloor{Mode: "warn"}, ""},
		{"warn below require", layer.ConstraintBelowFloor, signed, store.TrustFloor{Mode: "require"}, ""},
		{"unreadable mode below", layer.ConstraintBelowFloor, policy("REQUIRE", boundSigner()), store.TrustFloor{Mode: "warn"}, ""},
		{"floor raises an empty mode with no requirement", layer.ConstraintNoRequirements, policy(""), store.TrustFloor{Mode: "warn"}, ""},
		{"default raises an empty mode with no requirement", layer.ConstraintNoRequirements, policy(""), store.TrustFloor{}, "warn"},
		{"explicit mode with no requirement", layer.ConstraintNoRequirements, policy("warn"), store.TrustFloor{}, ""},
		{"type missing", layer.ConstraintMissingRequiredType, signed, store.TrustFloor{RequireTypes: []string{"hook"}}, ""},
		{"type listed", "", withTypes(signed, "hook"), store.TrustFloor{RequireTypes: []string{"hook"}}, ""},
		{"signer outside", layer.ConstraintSignerOutsideFloor, withTypes(signed, "hook"), floor, ""},
		{"signer admitted", "", withTypes(policy("warn", store.TrustSigner{Issuer: ghIssuer, Identity: ownWorkflow, SourceRepositoryOwnerID: "42", AnyRef: true}), "hook"), floor, ""},
		{"no requirement under a signer list", "", policy("none"), store.TrustFloor{Signers: floor.Signers}, ""},
	}
	for _, tc := range cases {
		got := layer.CheckFloor(tc.cfg, tc.floor, tc.def)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: CheckFloor = %v, want nil", tc.name, got)
		case tc.want != "" && (got == nil || got.Constraint != tc.want || !errors.Is(got, layer.ErrTrustPolicy)):
			t.Errorf("%s: CheckFloor = %v, want the constraint %s", tc.name, got, tc.want)
		}
	}
	if r := layer.CheckFloor(withTypes(signed, "hook"), floor, ""); r == nil || !strings.Contains(r.Error(), "--trust-source-owner-id") {
		t.Errorf("a derived binding under an owner identifier entry: %v, want the --trust-source-owner-id hint", r)
	}
	if r := layer.CheckFloor(policy(""), store.TrustFloor{}, "require"); r == nil || !strings.Contains(r.Message, "PODIUM_LAYER_TRUST_DEFAULT") {
		t.Errorf("a default-raised mode: %v, want the default named", r)
	}
}

// Spec: §4.7.11 (Tenant trust floor) — a floor is refused for a mode that
// is no mode, an unknown or repeated type, and an entry with no issuer,
// with both owner members, or with an instance the registry does not
// configure.
func TestCheckTrustFloor_Rules(t *testing.T) {
	t.Parallel()
	known := layer.TrustInstances(func(n string) bool { return n == "github" })
	bad := []store.TrustFloor{
		{Mode: "REQUIRE"},
		{RequireTypes: []string{"hooks"}},
		{RequireTypes: []string{"hook", "hook"}},
		{Signers: []store.TrustFloorSigner{{Identity: "x"}}},
		{Signers: []store.TrustFloorSigner{{Issuer: ghIssuer, SourceRepositoryOwner: "o", SourceRepositoryOwnerID: "1"}}},
		{Signers: []store.TrustFloorSigner{{Issuer: ghIssuer, Instance: "acme-sigstore"}}},
	}
	for _, f := range bad {
		if err := layer.CheckTrustFloor(f, known); !errors.Is(err, layer.ErrTrustFloor) {
			t.Errorf("CheckTrustFloor(%+v) = %v, want ErrTrustFloor", f, err)
		}
	}
	good := store.TrustFloor{Mode: "require", RequireTypes: []string{"hook", "mcp-server"},
		Signers: []store.TrustFloorSigner{{Issuer: ghIssuer, Instance: "github", SourceRepositoryOwnerID: "1"}, {Issuer: ghIssuer}}}
	if err := layer.CheckTrustFloor(good, known); err != nil {
		t.Errorf("CheckTrustFloor(%+v) = %v, want nil", good, err)
	}
	if err := layer.CheckTrustFloor(store.TrustFloor{}, nil); err != nil {
		t.Errorf("CheckTrustFloor(zero) = %v, want nil", err)
	}
	var none layer.TrustFloors
	if f, err := none.Floor(context.Background(), "t"); err != nil || !f.IsZero() {
		t.Errorf("nil TrustFloors.Floor = %+v, %v, want the zero floor", f, err)
	}
}
