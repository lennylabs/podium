package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/spi"
	"github.com/lennylabs/podium/pkg/store"
)

// The §6.10 codes of the attestation steps.
const (
	CodeAttestationMissing       = "ingest.attestation_missing"
	CodeAttestationMismatch      = "ingest.attestation_mismatch"
	CodeAttestationStale         = "ingest.attestation_stale"
	CodeAttestationInvalid       = "ingest.attestation_invalid"
	CodeAttestationPolicyInvalid = "ingest.attestation_policy_invalid"
	CodeAttestationRootStale     = "ingest.attestation_root_stale"
)

// ErrAttestationRefused is wrapped by every refusal of a cycle by an
// attestation step: an invalid trust policy, and a layer-step failure under
// require. A start that receives it from a startup ingest continues with the
// next layer, and the reingest endpoint answers it with the refusal's code
// and details.
//
// Spec: §4.7.11 (Ingest verification, Startup ingest)
var ErrAttestationRefused = errors.New("ingest: attestation refused")

// AttestationError is a refusal or a failure of an attestation step: a §6.10
// code with its details object. Unwrap yields ErrAttestationRefused for a
// refusal and the structured envelope, so the reingest endpoint writes the
// code and the details as they are.
type AttestationError struct {
	Code    string
	Details map[string]any
	Message string
	// Refusal marks a refusal of the cycle rather than a layer-step failure
	// that a warn cycle carries on from.
	Refusal bool
}

func (e *AttestationError) Error() string { return e.Message }

// Unwrap yields the structured envelope and, for a refusal,
// ErrAttestationRefused.
func (e *AttestationError) Unwrap() []error {
	out := []error{&spi.Error{Code: e.Code, Message: e.Message, Details: e.Details}}
	if e.Refusal {
		out = append(out, ErrAttestationRefused)
	}
	return out
}

// Constraint returns details.constraint, empty when the failure has none.
func (e *AttestationError) Constraint() string {
	c, _ := e.Details["constraint"].(string)
	return c
}

// attestFailure builds a layer-step failure with the constraint and the
// further details members kv, given as name and value pairs.
func attestFailure(code, constraint, message string, kv ...any) *AttestationError {
	d := map[string]any{}
	if constraint != "" {
		d["constraint"] = constraint
	}
	for i := 0; i+1 < len(kv); i += 2 {
		d[kv[i].(string)] = kv[i+1]
	}
	return &AttestationError{Code: code, Details: boundDetails(d), Message: code + ": " + message}
}

// maxDetailsBytes is the bound of the JSON encoding of a details object.
const maxDetailsBytes = 32 * 1024

// boundDetails keeps the JSON encoding of d below maxDetailsBytes. It
// measures the encoding with <, >, and & escaped as six bytes each, which is
// the longest form any reader writes, and while it is too long it drops the
// last entry of the longest list member and sets truncated. The members a
// cycle writes besides its lists are bounded reported strings and numbers,
// so dropping list entries always reaches the bound.
//
// Spec: §4.7.11 (Reported strings)
func boundDetails(d map[string]any) map[string]any {
	for {
		b, err := json.Marshal(d)
		if err != nil || len(b) < maxDetailsBytes {
			return d
		}
		key, n := "", 0
		for k, v := range d {
			if l := listLen(v); l > n || l == n && l > 0 && k < key {
				key, n = k, l
			}
		}
		if n == 0 {
			return d
		}
		d[key] = dropLast(d[key])
		d["truncated"] = true
	}
}

// listLen returns the number of entries of a list member, 0 for any other.
func listLen(v any) int {
	switch l := v.(type) {
	case []string:
		return len(l)
	case []map[string]any:
		return len(l)
	case []map[string]string:
		return len(l)
	}
	return 0
}

// dropLast returns a list member without its last entry.
func dropLast(v any) any {
	switch l := v.(type) {
	case []string:
		return l[:len(l)-1]
	case []map[string]any:
		return l[:len(l)-1]
	case []map[string]string:
		return l[:len(l)-1]
	}
	return v
}

// AttestationConfig is the registry's §4.7.11 ingest verification setup,
// shared by every cycle.
type AttestationConfig struct {
	// Instances are the configured Sigstore instances by name.
	Instances map[string]sign.AttestationInstance
	// DefaultMode is PODIUM_LAYER_TRUST_DEFAULT, which an empty trust_mode
	// resolves to.
	DefaultMode string
	// MaxBundleBytes and MaxBundles are PODIUM_ATTESTATION_MAX_BYTES and
	// PODIUM_ATTESTATION_MAX_BUNDLES. Zero takes the default.
	MaxBundleBytes int64
	MaxBundles     int
	// OnPublicGood is called before a cycle verifies under a policy that
	// names a signer of the public-good instance. The registry starts the
	// refresh of that instance's trusted root there, so a registry that never
	// verifies on public-good makes no request to the TUF repository.
	OnPublicGood func()
}

// LineageFunc sends the §9.1 lineage query of a cycle for the snapshot
// reference: base is the recorded attested commit, empty for none.
type LineageFunc func(ctx context.Context, base string, commits []string) ([]source.LineageAnswer, error)

// AttestationCycle carries the attestation steps of one ingest cycle of a
// layer whose resolved mode is warn or require. The layer is the one the
// cycle's claim read, so its record holds the mark and the commit the cycle
// compares against.
type AttestationCycle struct {
	cfg   *AttestationConfig
	layer store.LayerConfig
	claim string
	mode  string
	// lineage and reference are set for a provider that answers lineage
	// queries (WithLineage).
	lineage   LineageFunc
	reference string
	// checked records that the invalid-policy check ran, so the orchestrator
	// can run it before the snapshot and Ingest does not repeat it.
	checked bool
	// clock dates the record write, and audit receives the §8.1 events.
	clock clock.Clock
	audit AuditEmitterFunc
	// wrote records that the cycle made its record write.
	wrote bool
}

// Cycle returns the attestation steps of a cycle that claimed lc with claim,
// or nil when the layer's resolved mode is none, in which case the cycle runs
// no attestation step and writes no record. Every other resolved mode yields
// a cycle, so a stored mode the registry cannot read, such as REQUIRE, is
// refused by the policy check rather than read as none.
//
// A nil configuration fails closed as well: it configures no Sigstore
// instance and no trust default, so a layer whose own mode is above none
// gets a cycle whose policy check refuses it (unknown_instance or
// no_requirements) instead of an ingest that verifies nothing.
//
// Spec: §4.7.11 (Ingest verification, Policy that became invalid)
func (c *AttestationConfig) Cycle(lc store.LayerConfig, claim string) *AttestationCycle {
	if c == nil {
		c = &AttestationConfig{}
	}
	mode := layer.ResolveTrustMode(lc.TrustMode, c.DefaultMode)
	if mode == layer.TrustModeNone {
		return nil
	}
	return &AttestationCycle{cfg: c, layer: lc, claim: claim, mode: mode, clock: clock.Real{}}
}

// WithLineage sets the lineage query of the cycle for the snapshot at
// reference. A cycle without one skips layer step 5.
func (a *AttestationCycle) WithLineage(reference string, fn LineageFunc) {
	a.reference, a.lineage = reference, fn
}

// Mode returns the cycle's resolved trust mode.
func (a *AttestationCycle) Mode() string { return a.mode }

// bind takes the clock and the audit emitter of the ingest request.
func (a *AttestationCycle) bind(c clock.Clock, emit AuditEmitterFunc) {
	if c != nil {
		a.clock = c
	}
	if emit != nil {
		a.audit = emit
	}
}

// CheckPolicy refuses the cycle of a layer whose resolved policy is invalid
// with ingest.attestation_policy_invalid, after it writes the refusal to the
// attestation record. It runs once per cycle, after the claim and before the
// snapshot is read; a second call does nothing. superseded reports a record
// write that a later claim refused, which ends the cycle as a superseded one.
// onWrite runs before the record write, which is a store write of the cycle.
//
// Spec: §4.7.11 (Policy that became invalid, Attestation record)
func (a *AttestationCycle) CheckPolicy(ctx context.Context, st store.Store, onWrite func()) (superseded bool, err error) {
	if a.checked {
		return false, nil
	}
	a.checked = true
	failure := a.policyFailure()
	if failure == nil {
		return false, nil
	}
	failure.Refusal = true
	if superseded, err := a.writeFailure(ctx, st, failure, onWrite); superseded || err != nil {
		return superseded, err
	}
	a.emitFailed(failure, a.reference)
	return false, failure
}

// policyFailure applies the per-cycle policy rules: a resolved mode above
// none with no requirement, and a signer whose instance the registry no
// longer configures. Any other rule a stored policy breaks, which the layer
// endpoints refuse, fails closed under the constraint policy_rule.
func (a *AttestationCycle) policyFailure() *AttestationError {
	if !layer.ValidTrustMode(a.mode) {
		return attestFailure(CodeAttestationPolicyInvalid, "policy_rule",
			fmt.Sprintf("layer %s has the trust mode %q, which is not none, warn, or require", a.layer.ID, a.mode),
			"trust_mode", attest.ReportString(a.mode))
	}
	if len(a.layer.TrustRequirements) == 0 {
		return attestFailure(CodeAttestationPolicyInvalid, "no_requirements",
			fmt.Sprintf("layer %s resolves to trust mode %s and has no requirement", a.layer.ID, a.mode))
	}
	for _, req := range a.layer.TrustRequirements {
		for _, s := range req.Signers {
			if _, ok := a.instance(s.Instance); !ok {
				return attestFailure(CodeAttestationPolicyInvalid, "unknown_instance",
					fmt.Sprintf("a signer of layer %s names the Sigstore instance %q, which this registry does not configure", a.layer.ID, s.Instance),
					"instance", attest.ReportString(s.Instance))
			}
		}
	}
	known := func(name string) bool { _, ok := a.cfg.Instances[name]; return ok }
	if err := layer.CheckTrustPolicy(a.layer, known, a.cfg.DefaultMode); err != nil {
		return attestFailure(CodeAttestationPolicyInvalid, "policy_rule",
			fmt.Sprintf("the trust policy of layer %s breaks a rule: %v", a.layer.ID, err))
	}
	return nil
}

// instance returns the configured instance a signer names; an empty name is
// public-good.
func (a *AttestationCycle) instance(name string) (sign.AttestationInstance, bool) {
	if name == "" {
		name = sign.PublicGoodInstance
	}
	inst, ok := a.cfg.Instances[name]
	return inst, ok
}

// writeFailure writes the record of a cycle that the layer step, the policy
// check, or a fetch deadline failed: the status and the details, with the
// mark and the commit left as they are.
//
// Spec: §4.7.11 (Attestation record)
func (a *AttestationCycle) writeFailure(ctx context.Context, st store.Store, f *AttestationError, onWrite func()) (superseded bool, err error) {
	return a.write(ctx, st, store.AttestationWrite{Status: f.Code, Details: f.Details}, onWrite)
}

// write makes the cycle's one record write. It returns superseded when a
// later claim, or a mark the record no longer holds, refuses it.
func (a *AttestationCycle) write(ctx context.Context, st store.Store, w store.AttestationWrite, onWrite func()) (superseded bool, err error) {
	if a.wrote {
		return false, nil
	}
	a.wrote = true
	if onWrite != nil {
		onWrite()
	}
	w.CheckedAt = a.clock.Now().UTC()
	switch err := st.WriteLayerAttestation(ctx, a.layer.TenantID, a.layer.ID, a.claim, w); {
	case errors.Is(err, store.ErrIngestSuperseded):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("ingest: write the attestation record of layer %s: %w", a.layer.ID, err)
	}
	return false, nil
}

// RecordSourceFailure writes the record of a cycle whose Git source call
// reached the PODIUM_GIT_FETCH_TIMEOUT deadline, with
// ingest.source_unreachable as the status and the failure's details. It is
// the only source failure the record holds; for every other error it does
// nothing and returns false.
//
// Spec: §4.7.11 (Attestation record), §7.3.1 (Git fetch: Deadline)
func (a *AttestationCycle) RecordSourceFailure(ctx context.Context, st store.Store, cause error, onWrite func()) (recorded bool, err error) {
	e, ok := spi.AsError(cause)
	if !ok || e.Details["constraint"] != source.FetchDeadlineConstraint {
		return false, nil
	}
	if _, err := a.write(ctx, st, store.AttestationWrite{Status: e.Code, Details: e.Details}, onWrite); err != nil {
		return true, err
	}
	return true, nil
}

// attestationVerdict is what a cycle's attestation steps decided.
type attestationVerdict struct {
	// failure is the layer-step failure, nil when the step passed.
	failure *AttestationError
	// layerTime and commit are the attested time and commit of the layer
	// bundle of a passed step; commit is empty when its certificate records
	// none.
	layerTime time.Time
	commit    string
	// drops are the artifacts the artifact step drops, by ID.
	drops map[string]*AttestationError
	// outcomes is the attestation outcome of each artifact the cycle kept.
	outcomes map[string]AttestationOutcome
}

// AttestationOutcome is the §4.7.11 attestation outcome of one artifact
// version a cycle evaluated: verified, or failed with the code, the scope,
// the constraint, and, for the scope artifact, the position of the failed
// requirement.
type AttestationOutcome struct {
	Outcome     string
	Code        string
	Scope       string
	Constraint  string
	Requirement int
}

// The attestation outcomes of a version.
const (
	OutcomeVerified = "verified"
	OutcomeFailed   = "failed"
)

// AttestationReport is the attestation result of a cycle under warn or
// require, which the cycle's Result carries.
type AttestationReport struct {
	// Mode is the resolved trust mode.
	Mode string
	// Status is ok or the code of the layer-step failure, and Details its
	// details object.
	Status  string
	Details map[string]any
	// LayerAttestedTime and AttestedCommit are the values a passed layer step
	// recorded.
	LayerAttestedTime time.Time
	AttestedCommit    string
	// Outcomes is the attestation outcome of each artifact the cycle kept, by
	// ID. A later change stores it with the version.
	Outcomes map[string]AttestationOutcome
}
