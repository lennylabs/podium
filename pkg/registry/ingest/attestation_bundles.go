package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// snapshotBundle is one bundle file of the attestation directory of a
// snapshot, with what each requirement of the policy decided about it.
type snapshotBundle struct {
	raw    []byte
	digest string
	// verified holds the verification under each instance the policy names
	// that the bundle met, and refusal the reason of each that it did not.
	// A key-based signer's verification is held under keyVerification of
	// its key, which no instance name can equal.
	verified map[string]*sign.VerifiedAttestation
	refusal  map[string]sign.AttestationReason
	// accepted holds, per requirement, the verification under which a signer
	// of that requirement accepted the bundle, nil when none did. covering
	// is the entry of the covering requirement, which the layer step reads.
	accepted []*sign.VerifiedAttestation
	covering *sign.VerifiedAttestation
	// reason is the refusal reason that comes last among the signers that
	// refused the bundle, and reasons the same per requirement.
	reason  sign.AttestationReason
	reasons []sign.AttestationReason
}

// acceptedByAny reports whether a requirement accepts the bundle.
func (b *snapshotBundle) acceptedByAny() bool {
	for _, v := range b.accepted {
		if v != nil {
			return true
		}
	}
	return false
}

// readBundles applies layer step 1 to the attestation directory of the
// snapshot: it reads every *.sigstore.json entry, refuses more entries than
// MaxBundles and a file over MaxBundleBytes with the constraint limits, and
// returns the bundles in name order. A missing directory holds no bundle. A
// directory path the cycle cannot list, and an entry it cannot read as a
// regular file (a directory, a dangling link, a link to a directory), fail
// the step with the constraint unreadable rather than the cycle, so the
// layer's record states why and a start continues. Each bundle's attestation
// digest is computed over the bytes the cycle read, which are the bytes it
// verifies.
//
// Spec: §4.7.11 (Layer step)
func (a *AttestationCycle) readBundles(files fs.FS) ([]*snapshotBundle, *AttestationError, error) {
	entries, err := fs.ReadDir(files, attest.AttestationDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, unreadable(attest.AttestationDir, "the attestation directory cannot be listed"), nil
	}
	var names []string
	for _, e := range entries {
		if attest.IsBundleFileName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	maxBundles, maxBytes := a.limits()
	if len(names) > maxBundles {
		return nil, attestFailure(CodeAttestationInvalid, "limits",
			fmt.Sprintf("the attestation directory holds %d bundle files, more than PODIUM_ATTESTATION_MAX_BUNDLES (%d)", len(names), maxBundles),
			"bundles", len(names), "max_bundles", maxBundles), nil
	}
	sort.Strings(names)
	out := make([]*snapshotBundle, 0, len(names))
	for _, name := range names {
		p := path.Join(attest.AttestationDir, name)
		info, err := fs.Stat(files, p)
		if err != nil || !info.Mode().IsRegular() {
			return nil, unreadable(name, "the bundle entry is not a regular file the cycle can read"), nil
		}
		if info.Size() > maxBytes {
			return nil, oversized(name, info.Size(), maxBytes), nil
		}
		raw, err := fs.ReadFile(files, p)
		if err != nil {
			return nil, unreadable(name, "the bundle file cannot be read"), nil
		}
		if int64(len(raw)) > maxBytes {
			return nil, oversized(name, int64(len(raw)), maxBytes), nil
		}
		out = append(out, &snapshotBundle{raw: raw, digest: attest.AttestationDigest(raw)})
	}
	return out, nil, nil
}

// unreadable is the step 1 failure of an attestation directory path or a
// bundle entry that the cycle cannot read.
func unreadable(name, why string) *AttestationError {
	return attestFailure(CodeAttestationInvalid, "unreadable", why+": "+attest.ReportString(name),
		"file", attest.ReportString(name))
}

// oversized is the limits failure of one bundle file.
func oversized(name string, size, maxBytes int64) *AttestationError {
	return attestFailure(CodeAttestationInvalid, "limits",
		fmt.Sprintf("the bundle file %s holds %d bytes, more than PODIUM_ATTESTATION_MAX_BYTES (%d)", attest.ReportString(name), size, maxBytes),
		"file", attest.ReportString(name), "bytes", size, "max_bytes", maxBytes)
}

// limits returns the configured bundle limits, or their defaults.
func (a *AttestationCycle) limits() (maxBundles int, maxBytes int64) {
	maxBundles, maxBytes = a.cfg.MaxBundles, a.cfg.MaxBundleBytes
	if maxBundles <= 0 {
		maxBundles = attest.DefaultMaxBundles
	}
	if maxBytes <= 0 {
		maxBytes = attest.DefaultMaxBundleBytes
	}
	return maxBundles, maxBytes
}

// evaluate applies layer step 2 to b: it verifies the bundle under the
// instance of each signer of each requirement, once per instance, and
// records the requirements that accept it and the reason of each refusal,
// for the bundle and for each requirement. An error other than a refusal of
// the bundle, such as a trusted root that cannot be read, is returned.
//
// Spec: §4.7.11 (Layer step, Refusal reasons)
func (a *AttestationCycle) evaluate(ctx context.Context, b *snapshotBundle, covering int) error {
	reqs := a.layer.TrustRequirements
	b.verified = map[string]*sign.VerifiedAttestation{}
	b.refusal = map[string]sign.AttestationReason{}
	b.accepted = make([]*sign.VerifiedAttestation, len(reqs))
	b.reasons = make([]sign.AttestationReason, len(reqs))
	for r, req := range reqs {
		for _, s := range req.Signers {
			v, reason, err := a.verifyFor(ctx, b, s)
			if err != nil {
				return err
			}
			if v != nil {
				reason = signerReason(v, s, req)
			}
			if reason == "" {
				if b.accepted[r] == nil {
					b.accepted[r] = v
				}
				continue
			}
			b.reason = sign.LaterAttestationReason(b.reason, reason)
			b.reasons[r] = sign.LaterAttestationReason(b.reasons[r], reason)
		}
	}
	b.covering = b.accepted[covering]
	return nil
}

// verifyFor verifies b for the signer s once per verification: under the
// signer's Sigstore instance for a certificate signer, and under its public
// key for a key-based signer, which reads no trusted root.
//
// Spec: §4.7.11 (Layer step, Key-based signers)
func (a *AttestationCycle) verifyFor(ctx context.Context, b *snapshotBundle, s store.TrustSigner) (*sign.VerifiedAttestation, sign.AttestationReason, error) {
	if !layer.IsKeySigner(s) {
		return a.verifyUnder(ctx, b, s.Instance)
	}
	key, err := sign.ParseAttestationPublicKey(s.PublicKey)
	if err != nil {
		// Unreachable through a cycle: the policy check refuses a key the
		// rules refuse before the snapshot is read. A signer that reaches
		// here with one accepts nothing.
		return nil, sign.ReasonSignature, nil
	}
	name := keyVerification(key.ID())
	if v, ok := b.verified[name]; ok {
		return v, "", nil
	}
	if r, ok := b.refusal[name]; ok {
		return nil, r, nil
	}
	v, err := sign.VerifyKeySignedAttestation(b.raw, key)
	if err != nil {
		reason, ok := sign.AttestationReasonOf(err)
		if !ok {
			return nil, "", err
		}
		b.refusal[name] = reason
		return nil, reason, nil
	}
	b.verified[name] = v
	return v, "", nil
}

// keyVerification is the name under which a snapshot bundle holds its
// verification under a key: the key's identifier after a prefix with a
// colon, which no instance name holds (§13.12).
func keyVerification(id string) string { return "key:" + id }

// verifyUnder verifies b under the instance name once and returns the
// verification or the refusal reason.
func (a *AttestationCycle) verifyUnder(ctx context.Context, b *snapshotBundle, name string) (*sign.VerifiedAttestation, sign.AttestationReason, error) {
	inst, _ := a.instance(name)
	if v, ok := b.verified[inst.Name]; ok {
		return v, "", nil
	}
	if r, ok := b.refusal[inst.Name]; ok {
		return nil, r, nil
	}
	v, err := sign.VerifyAttestationBundle(ctx, b.raw, inst)
	if err != nil {
		reason, ok := sign.AttestationReasonOf(err)
		if !ok {
			return nil, "", err
		}
		b.refusal[inst.Name] = reason
		return nil, reason, nil
	}
	b.verified[inst.Name] = v
	return v, "", nil
}

// signerReason applies the identity comparison, the bindings, and the
// requirement's predicate type to a verified bundle, and returns the refusal
// reason or "" when the signer accepts it.
func signerReason(v *sign.VerifiedAttestation, s store.TrustSigner, req store.TrustRequirement) sign.AttestationReason {
	if err := v.AcceptsSigner(layer.VerifierSigner(s)); err != nil {
		if reason, ok := sign.AttestationReasonOf(err); ok {
			return reason
		}
		return sign.ReasonSignature
	}
	if req.PredicateType != "" && v.Statement.PredicateType != req.PredicateType {
		return sign.ReasonPredicateType
	}
	return ""
}

// lists reports whether the bundle's statement lists name with the digest
// digest, written sha256:<hex>.
func lists(v *sign.VerifiedAttestation, name, digest string) bool {
	return v.Statement.Lists(name, trimDigest(digest))
}

// trimDigest returns the hexadecimal part of a sha256:<hex> digest.
func trimDigest(d string) string {
	const prefix = "sha256:"
	if len(d) > len(prefix) && d[:len(prefix)] == prefix {
		return d[len(prefix):]
	}
	return d
}

// logIndex reads the log index of the bundle's log entry, for the audit
// event, or -1 when it carries none. The member lies outside the signed
// bytes, so it is reported and never compared.
func logIndex(raw []byte) int64 {
	var doc struct {
		VerificationMaterial struct {
			TLogEntries []struct {
				LogIndex json.Number `json:"logIndex"`
			} `json:"tlogEntries"`
		} `json:"verificationMaterial"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.VerificationMaterial.TLogEntries) == 0 {
		return -1
	}
	n, err := doc.VerificationMaterial.TLogEntries[0].LogIndex.Int64()
	if err != nil {
		return -1
	}
	return n
}
