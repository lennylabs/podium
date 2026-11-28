package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// WithTrustInstances installs the set of Sigstore instance names the registry
// configures, which the §4.7.11 policy rules read: a signer that names
// another instance is refused. serverboot passes the instances of
// registry.yaml. Without it the endpoint knows public-good alone.
//
// Spec: §4.7.11 (Policy rules)
func (e *LayerEndpoint) WithTrustInstances(fn layer.TrustInstances) *LayerEndpoint {
	e.trustInstances = fn
	return e
}

// WithTrustDefault installs PODIUM_LAYER_TRUST_DEFAULT, which the §4.7.11
// no-requirement rule reads at the endpoints: with it at warn or require, a
// registration or an update that leaves a layer with an empty mode and no
// requirement is refused.
//
// Spec: §4.7.11 (Policy rules)
func (e *LayerEndpoint) WithTrustDefault(mode string) *LayerEndpoint {
	e.trustDefault = mode
	return e
}

// WithTrustPolicyChange installs the §4.7.11 re-evaluation that a register or
// an update runs after it changed a layer's stored trust policy. It receives
// the layer as written, and the caller that made the change. A failure is
// the hook's to log: the change stands, because the next ingest cycle of the
// layer runs the re-evaluation again.
//
// Spec: §4.7.11 (Re-evaluation), §7.3.1 (Layer trust policy)
func (e *LayerEndpoint) WithTrustPolicyChange(fn func(ctx context.Context, cfg store.LayerConfig, caller string)) *LayerEndpoint {
	e.trustPolicyChanged = fn
	return e
}

// afterTrustWrite runs the re-evaluation hook when a write changed the
// stored trust policy of a layer that existed before it.
func (e *LayerEndpoint) afterTrustWrite(r *http.Request, before store.LayerConfig, existed bool, cfg store.LayerConfig) {
	if e.trustPolicyChanged == nil || !existed || layer.TrustPolicyEqual(before, cfg) {
		return
	}
	e.trustPolicyChanged(r.Context(), cfg, e.caller(r).Sub)
}

// WithDeclaredLayers installs the predicate that reports whether a layer of
// a tenant is declared in registry.yaml, whose declaration sets its trust
// policy at each start.
//
// Spec: §7.3.1 (Layer trust policy)
func (e *LayerEndpoint) WithDeclaredLayers(fn func(tenantID, id string) bool) *LayerEndpoint {
	e.declaredLayer = fn
	return e
}

// isDeclared reports whether id is a declared layer of tenantID.
func (e *LayerEndpoint) isDeclared(tenantID, id string) bool {
	return e.declaredLayer != nil && e.declaredLayer(tenantID, id)
}

// declaredTrustRefusal is the message of a trust member sent for a declared
// layer.
func declaredTrustRefusal(id string) string {
	return fmt.Sprintf("the trust policy of layer %s is declared in registry.yaml; change it in the declaration, which the registry applies at its next start", id)
}

// declaredRegistrationError reports why a registration under the ID of a
// declared layer is refused, or the empty string. stored is the stored layer
// (zero when exists is false), requested the policy the request carries, and
// cfg the layer the registration writes, which keeps the stored policy. A
// request policy that differs from the stored one is refused, because the
// next start re-applies the declaration. A source change is refused while a
// requirement is declared, because a binding the registry derived from the
// declared repository reads as one the declaration named. The kept policy is
// checked against the new source type with no default, as at start.
//
// Spec: §7.3.1 (Layer trust policy)
func (e *LayerEndpoint) declaredRegistrationError(stored store.LayerConfig, exists bool, p trustPatch, requested, cfg store.LayerConfig) string {
	if p.carried() && !layer.TrustPolicyEqual(requested, stored) {
		return declaredTrustRefusal(cfg.ID)
	}
	if exists && len(stored.TrustRequirements) > 0 &&
		(cfg.SourceType != stored.SourceType || reportedRepo(cfg) != reportedRepo(stored)) {
		return fmt.Sprintf("layer %s is declared in registry.yaml with a trust requirement, which was checked against its declared source; change its source in the declaration", cfg.ID)
	}
	kept := cfg.WithTrustPolicy(store.PolicyOf(stored))
	if err := layer.CheckTrustPolicy(kept, e.trustInstances, ""); err != nil {
		return err.Error()
	}
	return ""
}

// trustPatch is the §7.3.1 trust policy half of a register or update body,
// resolved by presence: a member the body carries is applied, and a member it
// omits keeps the stored value. Presence is what lets "trust_mode": "" return
// a layer to the deployment default and "trust_requirements": [] remove the
// requirement, which the non-empty-replaces reading of the other members
// cannot express.
//
// Spec: §7.3.1 (Patch semantics on the update body)
type trustPatch struct {
	mode  *string
	reqs  *[]store.TrustRequirement
	types *[]string
	root  *string
}

// decodeTrustPatch reads the trust policy members out of a register or update
// body. A member matches without regard to letter case, as encoding/json
// matches the flat request, so both operations read the same member. A body
// that carries one member under two spellings, a member whose name reads as
// a trust member and is none (layer.IsTrustLikeName), and a null trust_mode
// or trust_layer_root are refused. A requirement or signer that holds an
// unknown member fails its decode (store.TrustRequirement.UnmarshalJSON),
// and a trust_require_types that is not an array of strings fails as well.
//
// Spec: §7.3.1 (Layer trust policy), §7.3.1 (Patch semantics on the update body)
func decodeTrustPatch(body []byte) (trustPatch, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return trustPatch{}, fmt.Errorf("layer request: decode body: %w", err)
	}
	found := map[string]json.RawMessage{}
	for key, v := range raw {
		member, ok := trustMember(key)
		if !ok {
			if layer.IsTrustLikeName(key) {
				return trustPatch{}, fmt.Errorf("layer request: %q is not a trust policy member; the members are %s", key, strings.Join(layer.TrustMemberNames, ", "))
			}
			continue
		}
		if _, dup := found[member]; dup {
			return trustPatch{}, fmt.Errorf("layer request: %s is carried more than once under different letter case", member)
		}
		found[member] = v
	}
	var p trustPatch
	var err error
	if p.mode, err = trustString(found, "trust_mode"); err != nil {
		return trustPatch{}, err
	}
	if p.root, err = trustString(found, "trust_layer_root"); err != nil {
		return trustPatch{}, err
	}
	if v, ok := found["trust_requirements"]; ok {
		var reqs []store.TrustRequirement
		if err := json.Unmarshal(v, &reqs); err != nil {
			return trustPatch{}, fmt.Errorf("layer request: trust_requirements: %w", err)
		}
		p.reqs = &reqs
	}
	if v, ok := found["trust_require_types"]; ok {
		var types []string
		if err := json.Unmarshal(v, &types); err != nil {
			return trustPatch{}, fmt.Errorf("layer request: trust_require_types: %w", err)
		}
		p.types = &types
	}
	return p, nil
}

// trustMember returns the trust member key names without regard to case.
func trustMember(key string) (string, bool) {
	for _, m := range layer.TrustMemberNames {
		if strings.EqualFold(key, m) {
			return m, true
		}
	}
	return "", false
}

// trustString decodes the string member name of found, refusing null, which
// neither trust_mode nor trust_layer_root holds.
func trustString(found map[string]json.RawMessage, name string) (*string, error) {
	v, ok := found[name]
	if !ok {
		return nil, nil
	}
	if string(bytes.TrimSpace(v)) == "null" {
		return nil, fmt.Errorf("layer request: %s is null; send a string, such as \"\" for the default", name)
	}
	var out string
	if err := json.Unmarshal(v, &out); err != nil {
		return nil, fmt.Errorf("layer request: %s: %w", name, err)
	}
	return &out, nil
}

// carried reports whether the body carried a trust policy member.
func (p trustPatch) carried() bool {
	return p.mode != nil || p.reqs != nil || p.types != nil || p.root != nil
}

// applyTo overlays the carried members onto cfg and derives the §4.7.11
// source binding for the requirements the patch carried, against the stored
// source type and repo, which an update never changes. A stored requirement
// the patch did not carry is kept as stored.
//
// Spec: §7.3.1 (Layer trust policy), §4.7.11 (Derived source binding)
func (p trustPatch) applyTo(cfg store.LayerConfig) store.LayerConfig {
	if p.mode != nil {
		cfg.TrustMode = *p.mode
	}
	if p.root != nil {
		cfg.TrustLayerRoot = *p.root
	}
	if p.reqs != nil {
		cfg.TrustRequirements = layer.DeriveSourceBinding(cfg.SourceType, cfg.Repo, nilIfNoRequirement(*p.reqs))
	}
	if p.types != nil && len(*p.types) > 0 {
		cfg.TrustRequireTypes = *p.types
	} else if p.types != nil {
		cfg.TrustRequireTypes = nil
	}
	return cfg
}

// nilIfNoRequirement normalizes an empty requirement list to nil, so a
// removed requirement and one never set are one stored value.
func nilIfNoRequirement(reqs []store.TrustRequirement) []store.TrustRequirement {
	if len(reqs) == 0 {
		return nil
	}
	return reqs
}

// trustPolicyError reports why the policy cfg holds breaks a §4.7.11 policy
// rule, or the empty string when it breaks none.
//
// Spec: §4.7.11 (Policy rules), §7.3.1 (Layer trust policy)
func (e *LayerEndpoint) trustPolicyError(cfg store.LayerConfig) string {
	if err := layer.CheckTrustPolicy(cfg, e.trustInstances, e.trustDefault); err != nil {
		return err.Error()
	}
	return ""
}

// trustEventFields returns the §8.1 event fields that record a trust policy
// change from before to after: the previous and the new mode, requirements,
// listed types, and trust_layer_root. It returns nil when the policy did not change, so an
// event of another change carries no trust fields. The requirements are
// recorded as JSON, which carries any use of any_source and any_ref.
//
// Spec: §4.7.11 (Policy changes)
func trustEventFields(before, after store.LayerConfig) map[string]string {
	if layer.TrustPolicyEqual(before, after) {
		return nil
	}
	return map[string]string{
		"trust_mode_before":          before.TrustMode,
		"trust_mode":                 after.TrustMode,
		"trust_requirements_before":  requirementsJSON(before.TrustRequirements),
		"trust_requirements":         requirementsJSON(after.TrustRequirements),
		"trust_require_types_before": typesJSON(before.TrustRequireTypes),
		"trust_require_types":        typesJSON(after.TrustRequireTypes),
		"trust_layer_root_before":    before.TrustLayerRoot,
		"trust_layer_root":           after.TrustLayerRoot,
	}
}

// typesJSON encodes a type list for an event field, "[]" for none.
func typesJSON(types []string) string {
	if len(types) == 0 {
		return "[]"
	}
	// A list of strings always encodes.
	b, _ := json.Marshal(types)
	return string(b)
}

// requirementsJSON encodes reqs for an event field, "[]" for none.
func requirementsJSON(reqs []store.TrustRequirement) string {
	if len(reqs) == 0 {
		return "[]"
	}
	b, err := json.Marshal(reqs)
	if err != nil {
		// A requirement holds strings and booleans alone, which always
		// encode; the guard keeps an impossible failure visible.
		return fmt.Sprintf("unencodable: %v", err)
	}
	return string(b)
}
