package attest

import (
	"errors"
	"fmt"
)

// The §6.10 codes of consumer verification (§4.7.11).
const (
	CodeRequestMismatch      = "materialize.request_mismatch"
	CodeAttestationMissing   = "materialize.attestation_missing"
	CodeAttestationInvalid   = "materialize.attestation_invalid"
	CodeAttestationUntrusted = "materialize.attestation_untrusted"
	CodeAttestationMismatch  = "materialize.attestation_mismatch"
	CodeAttestationRootStale = "materialize.attestation_root_stale"
	CodePolicyUnavailable    = "config.attestation_policy_unavailable"
	CodeRegistryUnreachable  = "network.registry_unreachable"
	CodeOutputConflict       = "materialize.output_conflict"
)

// The details.constraint values of the consumer codes (§4.7.11, §6.10). A
// refusal of a bundle's verification carries its sign.AttestationReason
// instead.
const (
	ConstraintID              = "id"
	ConstraintVersion         = "version"
	ConstraintContentHash     = "content_hash"
	ConstraintBatchEntry      = "batch_entry"
	ConstraintIDForm          = "id_form"
	ConstraintResourcePath    = "resource_path"
	ConstraintDocumentSlot    = "document_slot"
	ConstraintDerivedField    = "derived_field"
	ConstraintAssignedVersion = "assigned_version"
	ConstraintSubject         = "subject"
	ConstraintPredicateType   = "predicate_type"
	ConstraintReferences      = "references"
	ConstraintLimits          = "limits"
	ConstraintFetch           = "fetch"
	ConstraintSigner          = "signer"
	ConstraintNoRule          = "no_rule"
	ConstraintNoRequirements  = "no_requirements"
	ConstraintUserRule        = "user_rule"
	ConstraintLayerDigest     = "layer_digest"
	ConstraintUnreadable      = "unreadable"
	ConstraintPinnedSigner    = "pinned_signer"
	ConstraintOverlay         = "overlay"
	ConstraintDuplicateID     = "duplicate_id"
	ConstraintFile            = "file"
	ConstraintEntry           = "entry"
	ConstraintLayerRootUnlist = "layer_root_unlisted"
)

// Failure is a refusal or a warning of the consumer procedure (§4.7.11): a
// §6.10 code and the details.constraint value that names the failed
// condition. Its message is Podium's own text and holds no string taken
// from a bundle or from a registry-asserted member, apart from what a
// caller passed in, so a consumer can place it in a log line. The warning
// text a consumer shows a model is WarningText, which carries only the
// requested ID, the code, and the constraint.
type Failure struct {
	// Code is the §6.10 code.
	Code string
	// Constraint is the details.constraint value, empty when the code
	// carries none.
	Constraint string
	// detail is Podium's description of the failure.
	detail string
	// cause is an underlying error for a debugging log line.
	cause error
}

// Error renders the code, the constraint, and the description.
func (f *Failure) Error() string {
	s := f.Code
	if f.Constraint != "" {
		s += " (" + f.Constraint + ")"
	}
	if f.detail != "" {
		s += ": " + f.detail
	}
	return s
}

// Unwrap returns the underlying cause.
func (f *Failure) Unwrap() error { return f.cause }

// WarningText is the §4.7.11 warning text of the failure for the requested
// ID: the ID, the code, and the constraint, and nothing taken from a bundle
// or from a registry-asserted member. The requested ID passed the
// well-formed rule, so it holds no control character.
//
// Spec: §4.7.11 (Attestation check)
func (f *Failure) WarningText(requestedID string) string {
	if f.Constraint == "" {
		return fmt.Sprintf("%s: %s", requestedID, f.Code)
	}
	return fmt.Sprintf("%s: %s (constraint %s)", requestedID, f.Code, f.Constraint)
}

// fail builds a Failure.
func fail(code, constraint, detail string, cause error) *Failure {
	return &Failure{Code: code, Constraint: constraint, detail: detail, cause: cause}
}

// FailureOf returns the Failure in err's chain, and false when there is
// none.
func FailureOf(err error) (*Failure, bool) {
	var f *Failure
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}

// NewFailure builds a Failure for a consumer condition that the consumer
// decides itself, such as the signer comparison of podium sync (§7.5.3) or
// an overlay artifact that is not applied (§7.5). detail is Podium's own
// text.
func NewFailure(code, constraint, detail string) *Failure {
	return fail(code, constraint, detail, nil)
}
