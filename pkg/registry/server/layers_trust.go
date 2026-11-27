package server

import (
	"encoding/json"
	"fmt"

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

// trustPatch is the §7.3.1 trust policy half of an update body, resolved by
// presence: a member the body carries is applied, and a member it omits keeps
// the stored value. Presence is what lets "trust_mode": "" return a layer to
// the deployment default and "trust_requirements": [] remove the
// requirement, which the non-empty-replaces reading of the other members
// cannot express.
//
// Spec: §7.3.1 (Patch semantics on the update body)
type trustPatch struct {
	mode *string
	reqs *[]store.TrustRequirement
	root *string
}

// decodeTrustPatch reads the trust policy members out of an update body by
// exact key. A requirement or signer that holds an unknown member fails its
// decode (store.TrustRequirement.UnmarshalJSON), which the caller refuses.
//
// Spec: §7.3.1 (Patch semantics on the update body)
func decodeTrustPatch(body []byte) (trustPatch, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return trustPatch{}, fmt.Errorf("layer update: decode body: %w", err)
	}
	var p trustPatch
	if v, ok := raw["trust_mode"]; ok {
		var m string
		if err := json.Unmarshal(v, &m); err != nil {
			return trustPatch{}, fmt.Errorf("layer update: trust_mode: %w", err)
		}
		p.mode = &m
	}
	if v, ok := raw["trust_requirements"]; ok {
		var reqs []store.TrustRequirement
		if err := json.Unmarshal(v, &reqs); err != nil {
			return trustPatch{}, fmt.Errorf("layer update: trust_requirements: %w", err)
		}
		p.reqs = &reqs
	}
	if v, ok := raw["trust_layer_root"]; ok {
		var root string
		if err := json.Unmarshal(v, &root); err != nil {
			return trustPatch{}, fmt.Errorf("layer update: trust_layer_root: %w", err)
		}
		p.root = &root
	}
	return p, nil
}

// carried reports whether the body carried a trust policy member.
func (p trustPatch) carried() bool {
	return p.mode != nil || p.reqs != nil || p.root != nil
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
	if err := layer.CheckTrustPolicy(cfg, e.trustInstances); err != nil {
		return err.Error()
	}
	return ""
}

// trustEventFields returns the §8.1 event fields that record a trust policy
// change from before to after: the previous and the new mode, requirements,
// and trust_layer_root. It returns nil when the policy did not change, so an
// event of another change carries no trust fields. The requirements are
// recorded as JSON, which carries any use of any_source and any_ref.
//
// Spec: §4.7.11 (Policy changes)
func trustEventFields(before, after store.LayerConfig) map[string]string {
	if layer.TrustPolicyEqual(before, after) {
		return nil
	}
	return map[string]string{
		"trust_mode_before":         before.TrustMode,
		"trust_mode":                after.TrustMode,
		"trust_requirements_before": requirementsJSON(before.TrustRequirements),
		"trust_requirements":        requirementsJSON(after.TrustRequirements),
		"trust_layer_root_before":   before.TrustLayerRoot,
		"trust_layer_root":          after.TrustLayerRoot,
	}
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
