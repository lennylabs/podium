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
	// resolves to unless the tenant floor's mode is stricter.
	DefaultMode string
	// Floors returns the §4.7.11 trust floor of a tenant. Nil sets no
	// floor. A floor that cannot be read fails the cycle, the
	// re-evaluation, and the served-outcome decision that needs it.
	Floors layer.TrustFloors
	// MaxBundleBytes and MaxBundles are PODIUM_ATTESTATION_MAX_BYTES and
	// PODIUM_ATTESTATION_MAX_BUNDLES. Zero takes the default.
	MaxBundleBytes int64
	MaxBundles     int
	// DefaultVisibility is the deployment default visibility of a layer that
	// --layer-path bootstraps (PODIUM_DEFAULT_LAYER_VISIBILITY): the directory
	// visibility of a .layer-config that declares none, and the baseline of a
	// row whose visibility_source is default. The zero value admits no
	// caller, so a cycle without one fails closed.
	DefaultVisibility layer.Visibility
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
	// floor is the trust floor of the layer's tenant, which CheckPolicy
	// reads before it resolves mode.
	floor store.TrustFloor
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
	// policy is the policy key of the layer and whether this process holds
	// the recorded roots, which the re-evaluation decides after the claim and
	// the links the cycle writes record (§4.7.11 Re-evaluation).
	policy policyState
	// evaluated holds the bundles layer step 2 evaluated, which the cycle
	// links after a failed layer step under warn as after a passed one.
	evaluated []*snapshotBundle
	// storedTypes holds, for a policy that lists types, the types of the
	// live stored versions of the layer by ID (readStoredTypes).
	storedTypes map[string][]string
	// vis is the visibility rule of a cycle of a path-bootstrapped layer,
	// nil for every other layer (§4.7.11 Visibility of a path-bootstrapped
	// layer). rowCreated and visAudit are set by AtStart, and publish
	// receives the §7.6 event of a visibility change.
	vis        *visibilityStep
	rowCreated bool
	visAudit   AuditEmitterFunc
	publish    EventEmitter
}

// Cycle returns the attestation steps of a cycle that claimed lc with claim.
// Under the resolved mode none the cycle runs no attestation step and writes
// no record: it re-evaluates the layer's links and links the bundles of the
// attestation directory as unverified. A nil configuration under none
// returns nil, because it configures no instance to link under. Every other
// resolved mode runs the steps, so a stored mode the registry cannot read,
// such as REQUIRE, is refused by the policy check rather than read as none.
//
// A nil configuration fails closed as well: it configures no Sigstore
// instance and no trust default, so a layer whose own mode is above none
// gets a cycle whose policy check refuses it (unknown_instance or
// no_requirements) instead of an ingest that verifies nothing.
//
// Spec: §4.7.11 (Ingest verification, Policy that became invalid, Unverified links)
func (c *AttestationConfig) Cycle(lc store.LayerConfig, claim string) *AttestationCycle {
	mode := layer.ResolveTrustMode(lc.TrustMode, "", "")
	if c != nil {
		mode = layer.ResolveTrustMode(lc.TrustMode, "", c.DefaultMode)
	}
	if c == nil {
		if mode == layer.TrustModeNone {
			return nil
		}
		c = &AttestationConfig{}
	}
	return &AttestationCycle{cfg: c, layer: lc, claim: claim, mode: mode, clock: clock.Real{}}
}

// verifies reports whether the cycle runs the attestation steps, which is
// every resolved mode other than none.
func (a *AttestationCycle) verifies() bool {
	return a != nil && a.mode != layer.TrustModeNone
}

// WithLineage sets the lineage query of the cycle for the snapshot at
// reference. A cycle without one skips layer step 5.
func (a *AttestationCycle) WithLineage(reference string, fn LineageFunc) {
	a.reference, a.lineage = reference, fn
}

// Mode returns the cycle's resolved trust mode.
func (a *AttestationCycle) Mode() string { return a.mode }

// bind takes the clock, the audit emitter, and the event publisher of the
// ingest request.
func (a *AttestationCycle) bind(c clock.Clock, emit AuditEmitterFunc, publish EventEmitter) {
	if c != nil {
		a.clock = c
	}
	if emit != nil {
		a.audit = emit
	}
	a.publish = publish
}

// CheckPolicy runs the re-evaluation of the layer's links and then refuses
// the cycle of a layer whose resolved policy is invalid with
// ingest.attestation_policy_invalid, after it writes the refusal to the
// attestation record. A layer whose policy is invalid is not re-evaluated.
// It runs once per cycle, after the claim and before the snapshot is read; a
// second call does nothing. superseded reports a write that a later claim
// refused, which ends the cycle as a superseded one. onWrite runs before the
// re-evaluation writes and the record write, which are store writes of the
// cycle.
//
// Spec: §4.7.11 (Re-evaluation, Policy that became invalid, Attestation record)
func (a *AttestationCycle) CheckPolicy(ctx context.Context, st store.Store, onWrite func()) (superseded bool, err error) {
	if a.checked {
		return false, nil
	}
	a.checked = true
	if err := a.readFloor(ctx); err != nil {
		return false, err
	}
	failure := a.policyFailure()
	if failure == nil {
		return a.reevaluate(ctx, st, onWrite)
	}
	failure.Refusal = true
	if superseded, err := a.writeFailure(ctx, st, failure, onWrite); superseded || err != nil {
		return superseded, err
	}
	a.emitFailed(failure, a.reference)
	return false, failure
}

// readFloor reads the trust floor of the layer's tenant and resolves the
// cycle's mode under it, because an empty trust_mode resolves to the
// stricter of the floor's mode and the trust default. A floor that cannot
// be read fails the cycle before it writes anything.
//
// Spec: §4.7.11 (Tenant trust floor)
func (a *AttestationCycle) readFloor(ctx context.Context) error {
	floor, err := a.cfg.Floors.Floor(ctx, a.layer.TenantID)
	if err != nil {
		return fmt.Errorf("ingest: read the trust floor of tenant %s for layer %s: %w", a.layer.TenantID, a.layer.ID, err)
	}
	a.floor = floor
	a.mode = layer.ResolveTrustMode(a.layer.TrustMode, floor.Mode, a.cfg.DefaultMode)
	return nil
}

// policyFailure applies the per-cycle policy rules: a resolved mode above
// none with no requirement, a signer whose instance the registry no longer
// configures, and in every mode the tenant floor (below_floor,
// missing_required_type, signer_outside_floor). Any other rule a stored
// policy breaks, which the layer endpoints refuse, fails closed under the
// constraint policy_rule.
func (a *AttestationCycle) policyFailure() *AttestationError {
	if a.mode != layer.TrustModeNone {
		if f := a.modeFailure(); f != nil {
			return f
		}
	}
	if r := layer.CheckFloor(a.layer, a.floor, a.cfg.DefaultMode); r != nil {
		return attestFailure(CodeAttestationPolicyInvalid, r.Constraint,
			fmt.Sprintf("the trust policy of layer %s does not meet the tenant trust floor: %s", a.layer.ID, r.Message))
	}
	if a.mode == layer.TrustModeNone {
		return nil
	}
	known := func(name string) bool { _, ok := a.cfg.Instances[name]; return ok }
	if err := layer.CheckTrustPolicy(a.layer, known, a.cfg.DefaultMode); err != nil {
		return attestFailure(CodeAttestationPolicyInvalid, "policy_rule",
			fmt.Sprintf("the trust policy of layer %s breaks a rule: %v", a.layer.ID, err))
	}
	return nil
}

// modeFailure applies the rules of a mode above none: a mode the registry
// can read, a requirement, and configured instances.
func (a *AttestationCycle) modeFailure() *AttestationError {
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
	return nil
}

// PolicyConstraint returns the constraint of the policy rule that the
// resolved trust policy of lc breaks under the configured instances, the
// trust default, and the floor of lc's tenant as the running process reads
// it now, and the empty string for a valid policy. Serving reads it to
// decide the outcome of a verified version, and a floor update to list the
// layers it makes invalid, with the rules a cycle applies when it refuses
// an invalid policy. A floor that cannot be read is an error.
//
// Spec: §4.7.11 (Policy that became invalid, Served outcome)
func (c *AttestationConfig) PolicyConstraint(ctx context.Context, lc store.LayerConfig) (string, error) {
	a := c.Cycle(lc, "")
	if a == nil {
		return "", nil
	}
	if err := a.readFloor(ctx); err != nil {
		return "", err
	}
	if f := a.policyFailure(); f != nil {
		return f.Constraint(), nil
	}
	return "", nil
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
	a.noteVisibility(ctx, w.Visibility)
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
	if !a.verifies() {
		return false, nil
	}
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
	// bundles are the bundles layer step 2 evaluated, which the cycle links.
	bundles []*snapshotBundle
	// requireTypes is the layer's trust_require_types, which keeps the
	// stored versions of a listed type from removal after a failed layer
	// step (withholdsRemoval).
	requireTypes []string
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
