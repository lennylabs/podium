package sync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Attestation is the §4.7.11 consumer trust policy of one podium sync
// invocation and the settings the sync reads with it (§7.5 Author
// attestation): the combined policy, the bundle bounds, the expected layer
// roots of a filesystem source, the signer pinning of the lock file, and the
// overlay setting of the user's sync.yaml.
type Attestation struct {
	// Policy is the combined policy of every source; nil resolves every ID
	// to none. The well-formed rule and the request binding run whatever it
	// holds.
	Policy *attest.Policy
	// Limits are PODIUM_ATTESTATION_MAX_BYTES, which bounds each bundle a
	// server-source sync fetches, and PODIUM_ATTESTATION_MAX_BUNDLES, which
	// bounds the attestation directory of a filesystem source. A zero value
	// takes the §13.12 default.
	Limits attest.Limits
	// LayerRoots maps a filesystem-source layer ID to the root its content
	// was signed under, from defaults.attestation.layer_roots of the user's
	// sync.yaml (§7.5, §13.11.3).
	LayerRoots map[string]string
	// UserLayerRoots reports that the user's sync.yaml sets layer_roots.
	// The layer ID is a directory name the repository chooses, so every
	// layer that holds an artifact the user sources resolve to require must
	// then be listed (§13.11.3).
	UserLayerRoots bool
	// PinSigners refuses an artifact whose signers differ from those its
	// prior lock entries record (§7.5.3).
	PinSigners bool
	// Overlay is allow, deny, or empty, the defaults.attestation.overlay of
	// the user's sync.yaml (§7.5).
	Overlay string
}

// The values of Attestation.Overlay.
const (
	OverlayAllow = "allow"
	OverlayDeny  = "deny"
)

// AttestationFunc returns the consumer trust policy of a sync. The sync
// entry points receive it through their options and call it once, before
// the first registry request or filesystem read for the effective view, as
// they resolve the §4.7.10 delivery check. A nil AttestationFunc is a sync
// with no policy, under which every ID resolves to none.
type AttestationFunc func() (*Attestation, error)

// AttestationConfigError reports that the consumer trust policy could not
// be loaded. Its message is the underlying message, which leads with
// config.attestation_policy_unavailable. The CLI maps it to exit status 2.
type AttestationConfigError struct {
	// Err is the load failure.
	Err error
}

// Error returns the underlying message.
func (e *AttestationConfigError) Error() string { return e.Err.Error() }

// Unwrap returns the underlying error.
func (e *AttestationConfigError) Unwrap() error { return e.Err }

// resolveAttestation resolves f once for a load. A nil f, or a nil result,
// is a sync with no policy. A layer root the §4.7.11 rule for
// trust_layer_root refuses is a configuration error, because a layer signed
// under no valid root has no expected layer subject.
//
// Spec: §7.5 (Author attestation), §13.11.3
func resolveAttestation(f AttestationFunc) (*Attestation, error) {
	if f == nil {
		return &Attestation{}, nil
	}
	att, err := f()
	if err != nil {
		var cfgErr *AttestationConfigError
		if errors.As(err, &cfgErr) {
			return nil, err
		}
		return nil, &AttestationConfigError{Err: err}
	}
	if att == nil {
		return &Attestation{}, nil
	}
	for _, id := range slices.Sorted(maps.Keys(att.LayerRoots)) {
		if _, err := attest.CheckLayerRoot(att.LayerRoots[id]); err != nil {
			return nil, &AttestationConfigError{Err: fmt.Errorf("%s: defaults.attestation.layer_roots: layer %q: %w",
				attest.CodePolicyUnavailable, id, err)}
		}
	}
	return att, nil
}

// Checker returns the consumer checker of a load whose bundles come from
// bundles, nil for a filesystem source.
func (a *Attestation) Checker(bundles attest.BundleSource) *attest.Checker {
	var src attest.BundleSource
	if bundles != nil {
		src = attest.CachedBundles{Cache: &attest.MemoryCache{}, Source: bundles}
	}
	return &attest.Checker{Policy: a.Policy, Bundles: src, MaxBytes: a.Limits.MaxBytes}
}

// aboveNone reports whether the policy can check any ID.
func (a *Attestation) aboveNone() bool {
	return a.Policy != nil && a.Policy.AboveNone()
}

// recordCheck runs the §4.7.11 attestation check of one record: the
// consumer procedure on a served record, or the §13.11.3 check of a
// filesystem-source package.
type recordCheck func(ctx context.Context) (attest.Result, error)

// wellFormedPackage applies the §4.7.11 well-formed rule to the ID and the
// resource paths of a package a filesystem source or the workspace overlay
// read. The resource paths are checked in sorted order, so a package with
// several offending paths reports the same one on every run.
//
// Spec: §4.7.11 (Well-formed requests and records), §13.11.3
func wellFormedPackage(id string, resources map[string][]byte) error {
	if err := attest.CheckRequestedID(id); err != nil {
		return err
	}
	for _, p := range slices.Sorted(maps.Keys(resources)) {
		if err := attest.CheckResourcePath(p); err != nil {
			return err
		}
	}
	return nil
}

// fsLayer is one filesystem-source layer as the walk read it: every package
// it found, before any refusal, and its tree, from which the attestation
// material is computed once, when the first package of the layer is
// checked.
type fsLayer struct {
	layer   filesystem.Layer
	records []filesystem.ArtifactRecord
	fsys    fs.FS
	local   *attest.LocalLayer
}

// attestation returns the layer's attestation material: the expected layer
// subject, the layer digest of the packages the walk read, and the bundles
// of its attestation directory, read once under the limits.
//
// Spec: §13.11.3
func (l *fsLayer) attestation(att *Attestation) *attest.LocalLayer {
	if l.local != nil {
		return l.local
	}
	local := &attest.LocalLayer{}
	// resolveAttestation refused a root that CheckLayerRoot refuses, so the
	// name is always set.
	local.SubjectName, _ = attest.LayerSubjectName(att.LayerRoots[l.layer.ID])
	if in, err := filesystem.LayerDigestInput(l.records, l.fsys); err == nil {
		local.Digest = attest.LayerDigest(in)
	}
	local.Bundles, local.Err = attest.ReadBundleDirectory(l.fsys, att.Limits)
	l.local = local
	return local
}

// localCheck returns the check of one filesystem-source package.
func localCheck(c *attest.Checker, att *Attestation, l *fsLayer, rec filesystem.ArtifactRecord) recordCheck {
	return func(ctx context.Context) (attest.Result, error) {
		if err := att.layerListed(l.layer.ID, rec.ID); err != nil {
			return attest.Result{}, err
		}
		p := attest.LocalPackage{ID: rec.ID, PackageDigest: rec.PackageDigest}
		p.SubjectName, p.SubjectErr = rec.SubjectName()
		if att.aboveNone() {
			p.Layer = l.attestation(att)
		}
		return c.VerifyLocal(ctx, p)
	}
}

// layerListed refuses an artifact that the user sources resolve to require
// in a layer that the user's layer_roots does not list, once the user sets
// layer_roots: a repository that renames its layer directory would
// otherwise escape the root the user expects.
//
// Spec: §13.11.3 (Layer subject)
func (a *Attestation) layerListed(layerID, id string) error {
	if !a.UserLayerRoots || a.Policy == nil {
		return nil
	}
	if _, ok := a.LayerRoots[layerID]; ok || a.Policy.Resolve(id).UserMode != attest.ModeRequire {
		return nil
	}
	return attest.NewFailure(attest.CodeAttestationMismatch, attest.ConstraintLayerRootUnlist,
		"the user's layer_roots lists no root for the artifact's layer")
}

// refusalDrop is the dropped-artifact entry of a record that the
// well-formed rule, the request binding, or the attestation check refused.
// Its reason is Podium's own text, which names no string a registry served.
func refusalDrop(id string, err error) layer.Drop {
	f, ok := attest.FailureOf(err)
	if !ok {
		f = attest.NewFailure(attest.CodeAttestationInvalid, "", err.Error())
	}
	d := layer.Drop{ArtifactID: id, Code: f.Code, Reason: f.Error()}
	if f.Constraint != "" {
		d.Details = map[string]any{"constraint": f.Constraint}
	}
	return d
}

// checkSelected runs the §4.7.11 checks on the records a workspace run
// selected, before anything is written. A record that the well-formed rule
// or the request binding refused at load, or that the attestation check
// refuses under require, is left out of the returned records, added to the
// result's dropped artifacts with its code, and named in the returned set,
// so the run keeps its previous output and lock entries. A record the
// attestation check refused is also returned as blocked, because the paths
// its output would write stay closed to other IDs of the run. A failure under
// warn is a warning of the result. Each kept record carries the package
// digest and the accepted bundles its lock entries record, and its signers
// are compared with those the prior lock records (§7.5.3).
//
// Spec: §7.5 (Author attestation), §7.5.3, §13.11.3
func checkSelected(ctx context.Context, records []materialRecord, prior *LockFile, att *Attestation, res *Result) (kept []materialRecord, refused map[string]bool, blocked []materialRecord) {
	refused = map[string]bool{}
	priorSigners := lockSigners(prior)
	checked := make([]materialRecord, 0, len(records))
	for _, rec := range records {
		loaded := rec.refusal == nil
		if err := checkRecord(ctx, &rec, priorSigners, att, res); err != nil {
			if loaded {
				// The record passed the load checks, so its content is
				// the content of its ID, and its output stays blocked
				// (§7.5 Output paths of distinct IDs).
				blocked = append(blocked, rec)
			}
			// An ID is reported once, with the refusal of its first refused
			// record.
			if !refused[rec.ID] {
				res.Dropped = append(res.Dropped, refusalDrop(rec.ID, err))
			}
			refused[rec.ID] = true
			continue
		}
		checked = append(checked, rec)
	}
	// A record whose ID another record refused is not written either, so an
	// ID is never both written and refused in one run.
	return withoutIDs(checked, refused), refused, blocked
}

// checkRecord checks one selected record and fills its lock members. It
// returns the refusal of the record, nil when the record is written.
func checkRecord(ctx context.Context, rec *materialRecord, prior map[string][]LockSigner, att *Attestation, res *Result) error {
	if rec.refusal != nil {
		return rec.refusal
	}
	if rec.check == nil {
		// The workspace overlay is exempt from the check (§6.4).
		return nil
	}
	r, err := rec.check(ctx)
	if err != nil {
		return err
	}
	for _, w := range r.Warnings {
		res.Warnings = append(res.Warnings, w.WarningText(rec.ID))
	}
	rec.PackageDigest = r.PackageDigest
	rec.Attestations = lockAttestations(r.Accepted)
	if !r.Checked {
		return nil
	}
	return compareSigners(rec, prior[rec.ID], att, res)
}

// compareSigners compares the signers of a checked record's accepted
// bundles with those its prior lock entries record, reports a change, and
// refuses it under pin_signers. A prior entry that records no signer, which
// is a first check or a lock of version 1, has nothing to compare.
//
// Spec: §7.5.3 (Signer changes)
func compareSigners(rec *materialRecord, prior []LockSigner, att *Attestation, res *Result) error {
	if len(prior) == 0 {
		return nil
	}
	now := signerSet(rec.Attestations)
	added, removed := diffSigners(now, prior)
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	res.SignerChanges = append(res.SignerChanges, SignerChange{ID: rec.ID, Added: added, Removed: removed})
	if att.PinSigners {
		return attest.NewFailure(attest.CodeAttestationUntrusted, attest.ConstraintPinnedSigner,
			"the signers differ from those the prior lock entries record, and pin_signers is set")
	}
	return nil
}

// SignerChange is an artifact whose signers differ from those its prior
// lock entries record (§7.5.3): the signers the run verified that the lock
// does not record, and the reverse.
type SignerChange struct {
	ID      string
	Added   []LockSigner
	Removed []LockSigner
}

// String renders the change for standard error.
func (c SignerChange) String() string {
	return fmt.Sprintf("%s: signers added [%s], removed [%s]", c.ID, joinSigners(c.Added), joinSigners(c.Removed))
}

// joinSigners renders a list of signers.
func joinSigners(ss []LockSigner) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.String()
	}
	return strings.Join(out, ", ")
}

// lockAttestations returns the lock items of the accepted bundles, in the
// order of the requirements they met, one per bundle digest.
func lockAttestations(accepted []attest.AcceptedBundle) []LockAttestation {
	var out []LockAttestation
	seen := map[string]bool{}
	for _, a := range accepted {
		key := a.Digest + "|" + a.Instance + "|" + a.Issuer + "|" + a.Identity
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, LockAttestation{
			Digest: a.Digest, PredicateType: a.PredicateType,
			Signer: LockSigner{Instance: a.Instance, Issuer: a.Issuer, Identity: a.Identity},
		})
	}
	return out
}

// lockSigners returns, per artifact ID, the set of signers the prior lock's
// entries record.
func lockSigners(prior *LockFile) map[string][]LockSigner {
	out := map[string][]LockSigner{}
	if prior == nil {
		return out
	}
	byID := map[string][]LockAttestation{}
	for _, la := range prior.Artifacts {
		byID[la.ID] = append(byID[la.ID], la.Attestations...)
	}
	for id, atts := range byID {
		if s := signerSet(atts); len(s) > 0 {
			out[id] = s
		}
	}
	return out
}

// signerSet returns the distinct signers of atts in sorted order.
func signerSet(atts []LockAttestation) []LockSigner {
	seen := map[LockSigner]bool{}
	var out []LockSigner
	for _, a := range atts {
		if !seen[a.Signer] {
			seen[a.Signer] = true
			out = append(out, a.Signer)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// diffSigners returns the signers of now that prior lacks, and the reverse.
func diffSigners(now, prior []LockSigner) (added, removed []LockSigner) {
	in := func(set []LockSigner, s LockSigner) bool { return slices.Contains(set, s) }
	for _, s := range now {
		if !in(prior, s) {
			added = append(added, s)
		}
	}
	for _, s := range prior {
		if !in(now, s) {
			removed = append(removed, s)
		}
	}
	return added, removed
}

// overlayApplies reports whether the workspace overlay applies to an ID,
// and the warning of an artifact it does not apply to: under deny the
// overlay applies to no ID, under allow to every ID, and otherwise to every
// ID that the user sources do not resolve to require.
//
// Spec: §7.5 (Author attestation), §6.4
func (a *Attestation) overlayApplies(id string) (bool, string) {
	switch {
	case a.Overlay == OverlayDeny:
		return false, ""
	case a.Overlay == OverlayAllow || a.Policy == nil:
		return true, ""
	}
	if a.Policy.Resolve(id).UserMode != attest.ModeRequire {
		return true, ""
	}
	f := attest.NewFailure(attest.CodeAttestationUntrusted, attest.ConstraintOverlay, "")
	return false, f.WarningText(id) + "; the overlay artifact is not applied"
}
