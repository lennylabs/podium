package store

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// TrustRequirement is one requirement of a layer trust policy (§4.7.11): the
// signers whose accepted bundles meet it and the predicate type their
// statements carry, when the requirement names one. CoversLayer marks the
// covering requirement of a policy with several, and Optional an additional
// requirement whose bundles are linked and whose absence fails nothing.
//
// Spec: §4.7.11 (Layer trust policy)
type TrustRequirement struct {
	Signers       []TrustSigner `json:"signers"`
	PredicateType string        `json:"predicate_type,omitempty"`
	CoversLayer   bool          `json:"covers_layer,omitempty"`
	Optional      bool          `json:"optional,omitempty"`
}

// TrustSigner is a signer of a requirement (§4.7.11): an OIDC issuer and a
// certificate identity, the source and ref binding members, and the Sigstore
// instance whose trusted root verifies its bundles. An empty member is not
// named, and an empty Instance is public-good.
//
// Spec: §4.7.11 (Layer trust policy)
type TrustSigner struct {
	Issuer                  string `json:"issuer"`
	Identity                string `json:"identity"`
	SourceRepository        string `json:"source_repository,omitempty"`
	SourceRepositoryOwner   string `json:"source_repository_owner,omitempty"`
	SourceRepositoryID      string `json:"source_repository_id,omitempty"`
	SourceRepositoryOwnerID string `json:"source_repository_owner_id,omitempty"`
	AnySource               bool   `json:"any_source,omitempty"`
	SourceRepositoryRef     string `json:"source_repository_ref,omitempty"`
	AnyRef                  bool   `json:"any_ref,omitempty"`
	Instance                string `json:"instance,omitempty"`
}

// UnmarshalJSON decodes a requirement and refuses a member of another name.
// A misspelled member would otherwise be dropped without an error, and a
// dropped predicate_type widens what the requirement accepts.
//
// Spec: §4.7.11 (Layer trust policy)
func (r *TrustRequirement) UnmarshalJSON(b []byte) error {
	type plain TrustRequirement
	var p plain
	if err := decodeStrict(b, &p); err != nil {
		return fmt.Errorf("trust requirement: %w", err)
	}
	*r = TrustRequirement(p)
	return nil
}

// UnmarshalJSON decodes a signer and refuses a member of another name, so a
// misspelled binding member or instance is an error rather than a signer
// with no binding or a signer of public-good.
//
// Spec: §4.7.11 (Layer trust policy)
func (s *TrustSigner) UnmarshalJSON(b []byte) error {
	type plain TrustSigner
	var p plain
	if err := decodeStrict(b, &p); err != nil {
		return fmt.Errorf("trust signer: %w", err)
	}
	*s = TrustSigner(p)
	return nil
}

// decodeStrict decodes one JSON value into v and refuses an unknown member.
func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// cloneTrustRequirements returns a deep copy of reqs, nil for an empty list,
// so a stored policy shares no slice with the caller that wrote or read it.
func cloneTrustRequirements(reqs []TrustRequirement) []TrustRequirement {
	if len(reqs) == 0 {
		return nil
	}
	out := make([]TrustRequirement, len(reqs))
	for i, r := range reqs {
		out[i] = r
		out[i].Signers = nil
		if len(r.Signers) > 0 {
			out[i].Signers = append([]TrustSigner(nil), r.Signers...)
		}
	}
	return out
}

// cloneRequireTypes returns a copy of types, nil for an empty list, so a
// stored list shares no slice with its caller and an empty list and one
// never set are one stored value.
func cloneRequireTypes(types []string) []string {
	if len(types) == 0 {
		return nil
	}
	return append([]string(nil), types...)
}

// requireTypesColumn returns the trust_require_types column the SQL stores
// write: the JSON array, or the empty string for no type.
func requireTypesColumn(types []string) (string, error) {
	if len(types) == 0 {
		return "", nil
	}
	b, err := json.Marshal(types)
	if err != nil {
		return "", fmt.Errorf("store: encode trust require types: %w", err)
	}
	return string(b), nil
}

// parseRequireTypes reads the trust_require_types column back. The empty
// string, which every row written before the column existed holds, lists no
// type.
func parseRequireTypes(col string) ([]string, error) {
	if col == "" {
		return nil, nil
	}
	var types []string
	if err := json.Unmarshal([]byte(col), &types); err != nil {
		return nil, fmt.Errorf("store: decode trust require types: %w", err)
	}
	return cloneRequireTypes(types), nil
}

// trustRequirementsColumn returns the trust_requirements column the SQL stores
// write: the JSON array, or the empty string for no requirement.
func trustRequirementsColumn(reqs []TrustRequirement) (string, error) {
	if len(reqs) == 0 {
		return "", nil
	}
	b, err := json.Marshal(reqs)
	if err != nil {
		return "", fmt.Errorf("store: encode trust requirements: %w", err)
	}
	return string(b), nil
}

// parseTrustRequirements reads the trust_requirements column back. The empty
// string, which every row written before the column existed holds, is no
// requirement.
func parseTrustRequirements(col string) ([]TrustRequirement, error) {
	if col == "" {
		return nil, nil
	}
	var reqs []TrustRequirement
	if err := json.Unmarshal([]byte(col), &reqs); err != nil {
		return nil, fmt.Errorf("store: decode trust requirements: %w", err)
	}
	return cloneTrustRequirements(reqs), nil
}
