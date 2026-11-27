package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// AttestationReference is one element of the §7.2 attestations member: a
// link of the served version. Signer and AttestedAt are set on a verified
// link only.
//
// Spec: §7.2 (Attestation members), §4.7.11 (Attestation storage)
type AttestationReference struct {
	Digest        string             `json:"digest"`
	PredicateType string             `json:"predicate_type"`
	Verdict       string             `json:"verdict"`
	Signer        *AttestationSigner `json:"signer,omitempty"`
	AttestedAt    string             `json:"attested_at,omitempty"`
}

// AttestationSigner is the signer of a verified link: the instance under
// which its bundle verified and the issuer and identity of its certificate.
type AttestationSigner struct {
	Instance string `json:"instance"`
	Issuer   string `json:"issuer"`
	Identity string `json:"identity"`
}

// ServedOutcome is the §7.2 attestation_outcome member: the outcome the
// registry serves for a version, with its failure on a failed version.
//
// Spec: §7.2 (Attestation members), §4.7.11 (Attestation outcome, Served outcome)
type ServedOutcome struct {
	Outcome     string                    `json:"outcome"`
	Failure     *store.AttestationFailure `json:"failure,omitempty"`
	EvaluatedAt string                    `json:"evaluated_at,omitempty"`
}

// AttestationPolicyCheck returns the constraint of the §4.7.11 policy rule
// that the resolved trust policy of a stored layer breaks, and the empty
// string for a valid policy. serverboot installs the check ingest applies,
// with the trust default and the configured instances.
type AttestationPolicyCheck func(store.LayerConfig) string

// codeAttestationPolicyInvalid is the §6.10 code a verified version is
// served with while its layer's policy is invalid.
const codeAttestationPolicyInvalid = "ingest.attestation_policy_invalid"

// WithAttestationPolicyCheck installs the §4.7.11 policy validity check that
// serving applies to a verified version. Without it the registry serves the
// stored outcome.
//
// Spec: §4.7.11 (Served outcome)
func (r *Registry) WithAttestationPolicyCheck(fn AttestationPolicyCheck) *Registry {
	r.attestationPolicy = fn
	return r
}

// attachProvenance sets the §7.2 attestation members of a load result from
// the stored links and the stored outcome of rec.
//
// Spec: §7.2 (Attestation members)
func (r *Registry) attachProvenance(ctx context.Context, res *LoadArtifactResult, rec store.ManifestRecord) error {
	tenant := r.TenantFor(ctx)
	links, err := r.store.ListAttestationLinks(ctx, tenant, store.LinkFilter{ArtifactID: rec.ArtifactID, Version: rec.Version})
	if err != nil {
		return fmt.Errorf("%w: read the attestations of %s@%s: %v", ErrUnavailable, rec.ArtifactID, rec.Version, err)
	}
	for _, l := range links {
		ref := AttestationReference{Digest: l.Digest, PredicateType: l.PredicateType, Verdict: l.Verdict}
		if l.Verdict == store.VerdictVerified {
			ref.Signer = &AttestationSigner{Instance: l.Instance, Issuer: l.SignerIssuer, Identity: l.SignerIdentity}
			if !l.AttestedAt.IsZero() {
				ref.AttestedAt = l.AttestedAt.UTC().Format(time.RFC3339Nano)
			}
		}
		res.Attestations = append(res.Attestations, ref)
	}
	o, err := r.servedOutcome(ctx, tenant, rec)
	if err != nil {
		return err
	}
	res.AttestationOutcome = o
	return nil
}

// servedOutcome returns the outcome the registry serves for rec, nil for
// not_evaluated. While the resolved policy of the version's layer is
// invalid, a verified version is served as failed with
// ingest.attestation_policy_invalid, the constraint of the rule that fails,
// and the scope layer. The decision reads the layer as it is stored now and
// changes no stored outcome. A layer the store no longer holds has no policy
// the registry can decide, so its verified versions are served as failed
// under policy_rule, which fails closed.
//
// Spec: §4.7.11 (Served outcome)
func (r *Registry) servedOutcome(ctx context.Context, tenant string, rec store.ManifestRecord) (*ServedOutcome, error) {
	o := rec.Outcome()
	if o.Outcome == "" || o.Outcome == store.OutcomeNotEvaluated {
		return nil, nil
	}
	out := &ServedOutcome{Outcome: o.Outcome, Failure: o.Failure}
	if o.EvaluatedAt != nil {
		out.EvaluatedAt = o.EvaluatedAt.UTC().Format(time.RFC3339Nano)
	}
	if o.Outcome != store.OutcomeVerified || r.attestationPolicy == nil {
		return out, nil
	}
	constraint := "policy_rule"
	lc, err := r.store.GetLayerConfig(ctx, tenant, rec.Layer)
	switch {
	case err == nil:
		constraint = r.attestationPolicy(lc)
	case !errors.Is(err, store.ErrNotFound):
		return nil, fmt.Errorf("%w: read layer %s: %v", ErrUnavailable, rec.Layer, err)
	}
	if constraint != "" {
		out.Outcome = store.OutcomeFailed
		out.Failure = &store.AttestationFailure{Code: codeAttestationPolicyInvalid, Scope: "layer", Constraint: constraint}
	}
	return out, nil
}

// LoadAttestation returns the bytes of the stored bundle digest to a caller
// who can see a version that links it: a served version of the request's
// tenant whose layer is visible to the caller under the caller's read
// scope. The check reads the layer from the manifest row of each linked
// version. Every other request, including one for a digest the registry does
// not store, returns ErrNotFound, so a caller who sees no linked version
// cannot tell whether the bundle exists.
//
// Spec: §7.3.5 (Serving bundles)
func (r *Registry) LoadAttestation(ctx context.Context, id layer.Identity, digest string) ([]byte, error) {
	notFound := fmt.Errorf("%w: attestation %s", ErrNotFound, digest)
	if !validAttestationDigest(digest) {
		return nil, notFound
	}
	tenant := r.TenantFor(ctx)
	links, err := r.store.ListAttestationLinks(ctx, tenant, store.LinkFilter{Digest: digest})
	if err != nil {
		return nil, fmt.Errorf("%w: read the links of attestation %s: %v", ErrUnavailable, digest, err)
	}
	if len(links) == 0 {
		return nil, notFound
	}
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	seen := make(map[[2]string]bool, len(visible))
	for _, m := range visible {
		seen[[2]string{m.ArtifactID, m.Version}] = true
	}
	linked := false
	for _, l := range links {
		if seen[[2]string{l.ArtifactID, l.Version}] {
			linked = true
			break
		}
	}
	if !linked {
		return nil, notFound
	}
	a, err := r.store.GetAttestation(ctx, tenant, digest)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, notFound
	case err != nil:
		return nil, fmt.Errorf("%w: read attestation %s: %v", ErrUnavailable, digest, err)
	}
	return a.Bundle, nil
}

// validAttestationDigest reports whether d is sha256: followed by 64
// lowercase hexadecimal digits, the form of an attestation digest.
func validAttestationDigest(d string) bool {
	const prefix = "sha256:"
	if len(d) != len(prefix)+64 || d[:len(prefix)] != prefix {
		return false
	}
	for _, c := range d[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
