package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// This file carries the §4.7.11 tenant trust floor through the §7.3.3
// tenant object: the three floor members, their presence semantics on
// create and update, the floor rules, and the floor update's re-evaluation
// and invalid_layers report.

// The tenant object members that hold the floor.
const (
	floorModeMember    = "trust_floor_mode"
	floorTypesMember   = "trust_floor_require_types"
	floorSignersMember = "trust_floor_signers"
)

var floorMemberNames = []string{floorModeMember, floorTypesMember, floorSignersMember}

// WithTrustFloorInstances installs the set of Sigstore instance names the
// registry configures, which a floor entry's instance names. Without it the
// tenant endpoints know public-good alone.
//
// Spec: §4.7.11 (Tenant trust floor)
func WithTrustFloorInstances(fn layer.TrustInstances) Option {
	return func(s *Server) { s.floorInstances = fn }
}

// WithTenantFloorChange installs the §4.7.11 re-evaluation that a tenant
// update runs after it changed the tenant's floor, with the tenant and the
// caller. A failure is the hook's to log: the floor stands, because each
// layer's next ingest cycle runs the re-evaluation again.
//
// Spec: §4.7.11 (Re-evaluation), §7.3.3
func WithTenantFloorChange(fn func(ctx context.Context, tenantID, caller string)) Option {
	return func(s *Server) { s.floorChanged = fn }
}

// floorPatch is the floor half of a tenant create or update body, resolved
// by presence: a member the body carries is applied, and one it omits keeps
// the stored value.
type floorPatch struct {
	mode    *string
	types   *[]string
	signers *[]store.TrustFloorSigner
}

// carried reports whether the body carried a floor member.
func (p floorPatch) carried() bool {
	return p.mode != nil || p.types != nil || p.signers != nil
}

// applyTo overlays the carried members onto f, with an empty list as nil.
func (p floorPatch) applyTo(f store.TrustFloor) store.TrustFloor {
	if p.mode != nil {
		f.Mode = *p.mode
	}
	if p.types != nil {
		f.RequireTypes = nil
		if len(*p.types) > 0 {
			f.RequireTypes = *p.types
		}
	}
	if p.signers != nil {
		f.Signers = nil
		if len(*p.signers) > 0 {
			f.Signers = *p.signers
		}
	}
	return f
}

// decodeFloorPatch reads the floor members out of a tenant body decoded as
// raw. A member matches its name without regard to letter case, and the
// handlers refuse a member carried twice (repeatedFloorMember) before they
// call it. A member whose name reads as a trust member and is none of the
// three (layer.IsTrustLikeName) is refused, so a misspelled floor member is
// never dropped. A
// null mode is refused, and a null list is the empty list. An entry that
// holds a member of another name fails its decode.
//
// Spec: §7.3.3, §4.7.11 (Tenant trust floor)
func decodeFloorPatch(raw map[string]json.RawMessage) (floorPatch, error) {
	found := map[string]json.RawMessage{}
	for key, v := range raw {
		member := ""
		for _, m := range floorMemberNames {
			if strings.EqualFold(key, m) {
				member = m
			}
		}
		if member == "" {
			if layer.IsTrustLikeName(key) {
				return floorPatch{}, fmt.Errorf("%q is not a tenant member; the trust floor members are %s", key, strings.Join(floorMemberNames, ", "))
			}
			continue
		}
		found[member] = v
	}
	var p floorPatch
	if v, ok := found[floorModeMember]; ok {
		if string(bytes.TrimSpace(v)) == "null" {
			return floorPatch{}, fmt.Errorf("%s is null; send a string, such as \"\" for no minimum", floorModeMember)
		}
		var mode string
		if err := json.Unmarshal(v, &mode); err != nil {
			return floorPatch{}, fmt.Errorf("%s: %w", floorModeMember, err)
		}
		p.mode = &mode
	}
	if v, ok := found[floorTypesMember]; ok {
		var types []string
		if err := json.Unmarshal(v, &types); err != nil {
			return floorPatch{}, fmt.Errorf("%s: %w", floorTypesMember, err)
		}
		p.types = &types
	}
	if v, ok := found[floorSignersMember]; ok {
		var signers []store.TrustFloorSigner
		if err := json.Unmarshal(v, &signers); err != nil {
			return floorPatch{}, fmt.Errorf("%s: %w", floorSignersMember, err)
		}
		p.signers = &signers
	}
	return p, nil
}

// checkFloor applies the §4.7.11 floor rules to f under the configured
// instances, with the message naming the tenant object member.
func (s *Server) checkFloor(f store.TrustFloor) error {
	if err := layer.CheckTrustFloor(f, s.floorInstances); err != nil {
		return fmt.Errorf("trust floor: %w", err)
	}
	return nil
}

// floorEventFields returns the §8.1 tenant.managed fields that record a
// floor change from before to after, and nil when the floor did not change.
func floorEventFields(before, after store.TrustFloor) map[string]string {
	b, a := floorJSON(before), floorJSON(after)
	if a == b {
		return nil
	}
	return map[string]string{"trust_floor_before": b, "trust_floor": a}
}

// floorJSON encodes a floor for an event field. A floor holds strings
// alone, so it always encodes.
func floorJSON(f store.TrustFloor) string {
	out, _ := json.Marshal(f.Clone())
	return string(out)
}

// repeatedFloorMember refuses a body object that carries one floor member
// twice, under the same name or under different letter case. A decode into
// a map keeps the last of two members silently, which would let a body that
// raises the floor and lowers it again in a later member pass as either.
// A body that is not an object is left to the decode, which refuses it.
//
// Spec: §7.3.3
func repeatedFloorMember(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := tok.(string)
		for _, m := range floorMemberNames {
			if strings.EqualFold(key, m) {
				if seen[m] {
					return fmt.Errorf("%s is carried more than once", m)
				}
				seen[m] = true
			}
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil
		}
	}
	return nil
}
