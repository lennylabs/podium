package sign

import (
	"errors"
	"slices"
)

// AttestationReason names the §4.7.11 acceptance condition an author
// attestation bundle failed. It is one value of a fixed set and carries no
// text from the bundle or from a library error, so a caller can place it in
// an audit event or an error envelope unchanged.
type AttestationReason string

// The §4.7.11 refusal reasons, in the order the spec lists them.
const (
	ReasonParse                AttestationReason = "parse"
	ReasonMediaType            AttestationReason = "media_type"
	ReasonPayload              AttestationReason = "payload"
	ReasonReservedSubject      AttestationReason = "reserved_subject"
	ReasonTimestampCondition   AttestationReason = "timestamp_condition"
	ReasonTime                 AttestationReason = "time"
	ReasonChain                AttestationReason = "chain"
	ReasonCertificateTimestamp AttestationReason = "certificate_timestamp"
	ReasonLogEntry             AttestationReason = "log_entry"
	ReasonSignature            AttestationReason = "signature"
	ReasonIssuer               AttestationReason = "issuer"
	ReasonIdentity             AttestationReason = "identity"
	ReasonSourceBinding        AttestationReason = "source_binding"
	ReasonRefBinding           AttestationReason = "ref_binding"
	ReasonPredicateType        AttestationReason = "predicate_type"
)

// attestationReasonOrder is the §4.7.11 list order.
var attestationReasonOrder = []AttestationReason{
	ReasonParse, ReasonMediaType, ReasonPayload, ReasonReservedSubject,
	ReasonTimestampCondition, ReasonTime, ReasonChain, ReasonCertificateTimestamp,
	ReasonLogEntry, ReasonSignature, ReasonIssuer, ReasonIdentity,
	ReasonSourceBinding, ReasonRefBinding, ReasonPredicateType,
}

// LaterAttestationReason returns whichever of a and b comes later in the
// §4.7.11 list, which is the reason reported when the signers of a policy
// refuse one bundle for different reasons. A value outside the list ranks
// before every listed value.
//
// Spec: §4.7.11.
func LaterAttestationReason(a, b AttestationReason) AttestationReason {
	if slices.Index(attestationReasonOrder, b) > slices.Index(attestationReasonOrder, a) {
		return b
	}
	return a
}

// ErrAttestationRefused is wrapped by every refusal of an author
// attestation bundle, whatever its reason.
var ErrAttestationRefused = errors.New("attestation bundle refused")

// AttestationRefusal is the error a refused bundle yields. Reason is the
// §4.7.11 value; the message holds Podium's own description of the failed
// condition, and the library error, when there is one, is reachable through
// errors.Unwrap for a debugging log line only.
type AttestationRefusal struct {
	Reason AttestationReason
	detail string
	cause  error
}

// Error names the reason and Podium's description of the failure. It
// carries no bundle text and no library text.
func (e *AttestationRefusal) Error() string {
	return ErrAttestationRefused.Error() + ": " + string(e.Reason) + ": " + e.detail
}

// Unwrap returns ErrAttestationRefused and the underlying cause.
func (e *AttestationRefusal) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrAttestationRefused}
	}
	return []error{ErrAttestationRefused, e.cause}
}

// refuse builds an AttestationRefusal with a Podium-authored detail.
func refuse(reason AttestationReason, detail string, cause error) error {
	return &AttestationRefusal{Reason: reason, detail: detail, cause: cause}
}

// AttestationReasonOf returns the reason of an AttestationRefusal in err's
// chain, and false when err is no refusal.
func AttestationReasonOf(err error) (AttestationReason, bool) {
	var r *AttestationRefusal
	if errors.As(err, &r) {
		return r.Reason, true
	}
	return "", false
}
