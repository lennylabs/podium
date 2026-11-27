package store

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// TrustRequirement is one requirement of a layer trust policy (§4.7.11): the
// signers whose accepted bundles meet it and the predicate type their
// statements carry, when the requirement names one.
//
// Spec: §4.7.11 (Layer trust policy)
type TrustRequirement struct {
	Signers       []TrustSigner `json:"signers"`
	PredicateType string        `json:"predicate_type,omitempty"`
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
		out[i] = TrustRequirement{PredicateType: r.PredicateType}
		if len(r.Signers) > 0 {
			out[i].Signers = append([]TrustSigner(nil), r.Signers...)
		}
	}
	return out
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
