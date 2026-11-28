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

// Spec: §4.7.11 (Tenant trust floor) — an owner entry matches a
// source_repository only on the path boundary: another owner with the same
// prefix, a deeper path, the owner itself, an empty or dot segment, and a
// case or escape variant do not match.
func TestFloorSignerMatches_OwnerBoundary(t *testing.T) {
	t.Parallel()
	e := store.TrustFloorSigner{Issuer: ghIssuer, SourceRepositoryOwner: "https://github.com/acme"}
	for _, r := range []string{
		"https://github.com/acme-tools/x", "https://github.com/acme/x/y", "https://github.com/acme/",
		"https://github.com/acme//x", "https://github.com/ACME/x", "https://github.com/acme", "https://github.com/acme/..",
		"https://github.com/acme/.", "https://github.com/acme/x/", "https://github.com/acme%2Fx",
	} {
		s := store.TrustSigner{Issuer: ghIssuer, Identity: "x", SourceRepository: r, SourceRepositoryRef: "refs/heads/main"}
		if layer.FloorSignerMatches(s, e) {
			t.Errorf("owner entry matched %q", r)
		}
	}
}

// Spec: §4.7.11 (Tenant trust floor) — an entry that names the identity
// and the ref of a shared signing workflow and no owner does not admit an
// any_source signer of that identity, which would admit every repository
// that calls the workflow.
func TestCheckFloor_IdentityEntryRefusesAnySource(t *testing.T) {
	t.Parallel()
	id := "https://github.com/acme/shared/.github/workflows/sign.yml@refs/heads/main"
	f := store.TrustFloor{Mode: layer.TrustModeRequire, Signers: []store.TrustFloorSigner{{Issuer: ghIssuer, Identity: id, SourceRepositoryRef: "refs/heads/main"}}}
	cfg := store.LayerConfig{SourceType: "git", Repo: "https://github.com/evil/x", TrustMode: layer.TrustModeRequire,
		TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: ghIssuer, Identity: id, AnySource: true, SourceRepositoryRef: "refs/heads/main"}}}}}
	if err := layer.CheckTrustPolicy(cfg, nil, ""); err != nil {
		t.Fatalf("policy refused: %v", err)
	}
	if r := layer.CheckFloor(cfg, f, ""); r == nil || r.Constraint != layer.ConstraintSignerOutsideFloor {
		t.Errorf("an any_source signer under an identity entry: %v, want signer_outside_floor", r)
	}
	cfg.TrustRequirements[0].Signers[0].AnySource = false
	cfg.TrustRequirements[0].Signers[0].SourceRepository = "https://github.com/acme/finance"
	if r := layer.CheckFloor(cfg, f, ""); r != nil {
		t.Errorf("a bound signer of that identity: %v, want nil", r)
	}
}

// Spec: §4.7.11 (Tenant trust floor) — a floor mode the registry cannot
// read fails closed: the floor source returns it as an error, and a check
// that holds it refuses every layer, an explicit none included, rather than
// read it as no minimum.
func TestTrustFloor_UnreadableModeFailsClosed(t *testing.T) {
	t.Parallel()
	for _, m := range []string{"REQUIRE", " require", "Require"} {
		f := store.TrustFloor{Mode: m}
		if r := layer.CheckFloor(store.LayerConfig{TrustMode: layer.TrustModeNone}, f, ""); r == nil || r.Constraint != layer.ConstraintBelowFloor {
			t.Errorf("floor mode %q admits an explicit none: %v", m, r)
		}
		src := layer.TrustFloors(func(context.Context, string) (store.TrustFloor, error) { return f, nil })
		if _, err := src.Floor(context.Background(), "acme"); !errors.Is(err, layer.ErrTrustFloor) {
			t.Errorf("floor source with mode %q = %v, want ErrTrustFloor", m, err)
		}
	}
}

// Spec: §4.7.11 (Tenant trust floor) — a floor that lists required types
// under the mode none or no mode enforces nothing, because a layer under
// none lists the type and verifies nothing, so it is refused; under warn
// it is admitted.
func TestCheckTrustFloor_RequireTypesNeedAMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", layer.TrustModeNone} {
		if err := layer.CheckTrustFloor(store.TrustFloor{Mode: mode, RequireTypes: []string{"hook"}}, nil); !errors.Is(err, layer.ErrTrustFloor) {
			t.Errorf("require_types under mode %q = %v, want a refusal", mode, err)
		}
	}
	if err := layer.CheckTrustFloor(store.TrustFloor{Mode: layer.TrustModeWarn, RequireTypes: []string{"hook"}}, nil); err != nil {
		t.Errorf("require_types under warn = %v", err)
	}
}
