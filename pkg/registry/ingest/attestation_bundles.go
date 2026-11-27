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
	verified map[string]*sign.VerifiedAttestation
	refusal  map[string]sign.AttestationReason
	// accepted holds, per requirement, the verification under which a signer
	// of that requirement accepted the bundle, nil when none did.
	accepted []*sign.VerifiedAttestation
	// reason is the refusal reason that comes last among the signers that
	// refused the bundle.
	reason sign.AttestationReason
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
// snapshot: it reads every *.sigstore.json file, refuses more files than
// MaxBundles and a file over MaxBundleBytes with the constraint limits, and
// returns the bundles in file name order. A missing directory holds no
// bundle. Each bundle's attestation digest is computed over the bytes the
// cycle read, which are the bytes it verifies.
//
// Spec: §4.7.11 (Layer step)
func (a *AttestationCycle) readBundles(files fs.FS) ([]*snapshotBundle, *AttestationError, error) {
	entries, err := fs.ReadDir(files, attest.AttestationDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("ingest: read the attestation directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && attest.IsBundleFileName(e.Name()) {
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
		if info, err := fs.Stat(files, p); err == nil && info.Size() > maxBytes {
			return nil, oversized(name, info.Size(), maxBytes), nil
		}
		raw, err := fs.ReadFile(files, p)
		if err != nil {
			return nil, nil, fmt.Errorf("ingest: read bundle %s: %w", attest.ReportString(name), err)
		}
		if int64(len(raw)) > maxBytes {
			return nil, oversized(name, int64(len(raw)), maxBytes), nil
		}
		out = append(out, &snapshotBundle{raw: raw, digest: attest.AttestationDigest(raw)})
	}
	return out, nil, nil
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
// records the requirements that accept it and the reason of each refusal. An
// error other than a refusal of the bundle, such as a trusted root that
// cannot be read, is returned.
//
// Spec: §4.7.11 (Layer step, Refusal reasons)
func (a *AttestationCycle) evaluate(ctx context.Context, b *snapshotBundle) error {
	reqs := a.layer.TrustRequirements
	b.verified = map[string]*sign.VerifiedAttestation{}
	b.refusal = map[string]sign.AttestationReason{}
	b.accepted = make([]*sign.VerifiedAttestation, len(reqs))
	for r, req := range reqs {
		for _, s := range req.Signers {
			v, reason, err := a.verifyUnder(ctx, b, s.Instance)
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
		}
	}
	return nil
}

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
