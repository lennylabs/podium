package layer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lennylabs/podium/pkg/store"
)

// ErrTrustFloor wraps every refusal of a tenant trust floor value, so a
// caller tells a floor refusal from another failure with errors.Is.
var ErrTrustFloor = errors.New("tenant trust floor")

// TrustFloors returns the §4.7.11 trust floor of a tenant: the tenant
// object's members on a multi-tenant registry, and the deployment setting
// on a single-tenant one. An error means the floor could not be read, and
// every caller fails closed on it.
type TrustFloors func(ctx context.Context, tenantID string) (store.TrustFloor, error)

// Floor returns the floor of tenantID, the zero floor for a nil source.
func (f TrustFloors) Floor(ctx context.Context, tenantID string) (store.TrustFloor, error) {
	if f == nil {
		return store.TrustFloor{}, nil
	}
	return f(ctx, tenantID)
}

// The §4.7.11 constraints of a policy that the tenant floor makes invalid,
// with no_requirements for a mode the floor raises over a layer with no
// requirement.
const (
	ConstraintNoRequirements      = "no_requirements"
	ConstraintBelowFloor          = "below_floor"
	ConstraintMissingRequiredType = "missing_required_type"
	ConstraintSignerOutsideFloor  = "signer_outside_floor"
)

// FloorRefusal is a layer trust policy that does not meet the tenant floor.
// It matches ErrTrustPolicy under errors.Is, so the layer endpoints refuse
// it as any policy rule, and Constraint is the §6.10 details.constraint a
// refused cycle and the served outcome carry.
//
// Spec: §4.7.11 (Tenant trust floor, Policy that became invalid)
type FloorRefusal struct {
	Constraint string
	Message    string
}

func (e *FloorRefusal) Error() string { return ErrTrustPolicy.Error() + ": " + e.Message }

// Is reports ErrTrustPolicy.
func (e *FloorRefusal) Is(target error) bool { return target == ErrTrustPolicy }

// modeRank orders the trust modes none, warn, require. The empty mode ranks
// with none, and a value that is no mode ranks below none, so it is never
// read as meeting a floor.
func modeRank(m string) int {
	switch m {
	case "", TrustModeNone:
		return 0
	case TrustModeWarn:
		return 1
	case TrustModeRequire:
		return 2
	}
	return -1
}

// stricterMode returns the stricter of two modes, a for a tie.
func stricterMode(a, b string) string {
	if modeRank(b) > modeRank(a) {
		return b
	}
	return a
}

// CheckTrustFloor applies the §4.7.11 floor rules to f: a mode that is
// none, warn, require, or empty; required types the registry knows, each
// listed once; and entries that name an issuer, at most one owner member,
// and an instance the registry configures. It returns nil or an error
// wrapping ErrTrustFloor that names the member and the rule.
//
// Spec: §4.7.11 (Tenant trust floor)
func CheckTrustFloor(f store.TrustFloor, instances TrustInstances) error {
	if !ValidTrustMode(f.Mode) {
		return floorRefusal("mode %q is not none, warn, or require", f.Mode)
	}
	seen := map[string]bool{}
	for i, t := range f.RequireTypes {
		if !KnownArtifactType(t) {
			return floorRefusal("require_types[%d] %q is not an artifact type this registry knows", i, t)
		}
		if seen[t] {
			return floorRefusal("require_types lists %q twice", t)
		}
		seen[t] = true
	}
	for i, e := range f.Signers {
		switch {
		case e.Issuer == "":
			return floorRefusal("signers[%d] names no issuer", i)
		case e.SourceRepositoryOwner != "" && e.SourceRepositoryOwnerID != "":
			return floorRefusal("signers[%d] names source_repository_owner and source_repository_owner_id; an entry names one of them", i)
		case !instances.known(e.Instance):
			return floorRefusal("signers[%d] names the Sigstore instance %q, which this registry does not configure", i, e.Instance)
		}
	}
	return nil
}

// CheckFloor applies the §4.7.11 floor of a tenant to the trust policy of
// one of its layers, with def the trust default an empty mode also
// resolves to. It returns nil when the policy meets the floor, and
// otherwise the refusal of the first rule it breaks: a mode the floor
// raises over a layer with no requirement, an explicit mode below the
// floor's mode, a required type the layer does not list, and a signer that
// matches no entry of a non-empty signer list. Callers derive the source
// binding first, because the signer rule reads the derived members.
//
// Spec: §4.7.11 (Tenant trust floor, Policy that became invalid)
func CheckFloor(cfg store.LayerConfig, f store.TrustFloor, def string) *FloorRefusal {
	mode := ResolveTrustMode(cfg.TrustMode, f.Mode, def)
	if modeRank(mode) > 0 && len(cfg.TrustRequirements) == 0 {
		return &FloorRefusal{Constraint: ConstraintNoRequirements, Message: noRequirementText(cfg.TrustMode, mode, f.Mode, def)}
	}
	if modeRank(mode) < modeRank(f.Mode) {
		return &FloorRefusal{Constraint: ConstraintBelowFloor, Message: fmt.Sprintf(
			"trust_mode %q is below the tenant trust floor's mode %s", cfg.TrustMode, f.Mode)}
	}
	for _, t := range f.RequireTypes {
		if !slices.Contains(cfg.TrustRequireTypes, t) {
			return &FloorRefusal{Constraint: ConstraintMissingRequiredType, Message: fmt.Sprintf(
				"trust_require_types does not list %q, which the tenant trust floor requires", t)}
		}
	}
	if len(f.Signers) == 0 {
		return nil
	}
	for i, req := range cfg.TrustRequirements {
		for j, s := range req.Signers {
			if !floorAdmits(f.Signers, s) && IsKeySigner(s) {
				return &FloorRefusal{Constraint: ConstraintSignerOutsideFloor, Message: fmt.Sprintf(
					"trust_requirements[%d].signers[%d] is a key-based signer, which matches no signer entry of the tenant trust floor, because every entry names an issuer", i, j)}
			}
			if !floorAdmits(f.Signers, s) {
				return &FloorRefusal{Constraint: ConstraintSignerOutsideFloor, Message: fmt.Sprintf(
					"trust_requirements[%d].signers[%d] (issuer %q, identity %q) matches no signer entry of the tenant trust floor%s",
					i, j, s.Issuer, s.Identity, ownerIDHint(f.Signers, s))}
			}
		}
	}
	return nil
}

// noRequirementText is the message of a mode above none with no
// requirement, naming what raised an empty mode.
func noRequirementText(own, mode, floorMode, def string) string {
	if own != "" {
		return fmt.Sprintf("trust_mode %s needs a requirement in trust_requirements", own)
	}
	source := "the tenant trust floor"
	if modeRank(def) > modeRank(floorMode) {
		source = "PODIUM_LAYER_TRUST_DEFAULT"
	}
	return fmt.Sprintf("trust_mode is empty and resolves to %s through %s, which needs a requirement in trust_requirements; add one or set trust_mode", mode, source)
}

// floorAdmits reports whether s matches an entry of entries.
func floorAdmits(entries []store.TrustFloorSigner, s store.TrustSigner) bool {
	for _, e := range entries {
		if FloorSignerMatches(s, e) {
			return true
		}
	}
	return false
}

// ownerIDHint names --trust-source-owner-id when an entry that the signer's
// issuer and instance meet names an owner identifier and the signer names
// none, which is the case of a derived binding under such a floor.
func ownerIDHint(entries []store.TrustFloorSigner, s store.TrustSigner) string {
	if s.SourceRepositoryOwnerID != "" {
		return ""
	}
	for _, e := range entries {
		if e.SourceRepositoryOwnerID != "" && e.Issuer == s.Issuer && instanceName(e.Instance) == instanceName(s.Instance) {
			return "; the floor names an owner identifier, which a signer names with --trust-source-owner-id (source_repository_owner_id)"
		}
	}
	return ""
}

// FloorSignerMatches reports whether the layer signer s matches the floor
// entry e: equal issuers, equal instances with an empty instance read as
// public-good, and for each member the entry names, the signer's matching
// member. An owner entry matches the signer's source_repository_owner, or a
// source_repository that is the owner followed by / and one path segment.
// A signer that sets any_source matches no entry that names an owner or an
// owner identifier, and one that sets any_ref matches no entry that names a
// ref. Every comparison is byte for byte.
//
// Spec: §4.7.11 (Tenant trust floor)
func FloorSignerMatches(s store.TrustSigner, e store.TrustFloorSigner) bool {
	// A key-based signer names no issuer, and every entry names one.
	if IsKeySigner(s) || s.Issuer != e.Issuer || instanceName(s.Instance) != instanceName(e.Instance) {
		return false
	}
	if e.SourceRepositoryOwner != "" && (s.AnySource || !ownerMatches(s, e.SourceRepositoryOwner)) {
		return false
	}
	if e.SourceRepositoryOwnerID != "" && (s.AnySource || s.SourceRepositoryOwnerID != e.SourceRepositoryOwnerID) {
		return false
	}
	if e.SourceRepositoryRef != "" && (s.AnyRef || s.SourceRepositoryRef != e.SourceRepositoryRef) {
		return false
	}
	return e.Identity == "" || s.Identity == e.Identity
}

// ownerMatches reports whether s names owner: as its source_repository_owner,
// or through a source_repository that is owner followed by / and one path
// segment that holds no / and is neither . nor .., so a repository of
// another owner whose name begins with owner's, such as acme-tools under
// acme, does not match.
func ownerMatches(s store.TrustSigner, owner string) bool {
	if s.SourceRepositoryOwner == owner {
		return true
	}
	rest, ok := strings.CutPrefix(s.SourceRepository, owner+"/")
	return ok && rest != "" && rest != "." && rest != ".." && !strings.Contains(rest, "/")
}

// floorRefusal builds an error wrapping ErrTrustFloor.
func floorRefusal(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrTrustFloor, fmt.Sprintf(format, args...))
}
