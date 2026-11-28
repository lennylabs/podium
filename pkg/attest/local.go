package attest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
)

// LocalPackage is one package that filesystem-source podium sync read from a
// layer directory, checked with no request and no served record (§13.11.3).
type LocalPackage struct {
	// ID is the package's canonical ID, its path under the layer root,
	// which selects the rule.
	ID string
	// SubjectName is the package's §4.7.6 subject name; SubjectErr is set
	// instead when the package has none.
	SubjectName string
	SubjectErr  error
	// PackageDigest is the §4.7.6 package digest of the files the walk read.
	PackageDigest string
	// Layer is the attestation material of the package's layer.
	Layer *LocalLayer
}

// LocalLayer is the attestation material of one layer directory: the
// expected layer subject, the layer digest, and the bundles of its
// attestation directory, read once for every package of the layer.
type LocalLayer struct {
	// SubjectName is the expected layer subject name, .podium/layer or
	// .podium/layer/<root>.
	SubjectName string
	// Digest is the §4.7.6 layer digest, empty when the layer has none
	// because a package of it has no subject name.
	Digest string
	// Bundles are the bundle files of the attestation directory.
	Bundles [][]byte
	// Err is the failure of reading the directory, which fails every
	// package of the layer that is checked above none.
	Err error
}

// VerifyLocal runs the §13.11.3 filesystem-source check on one package. The
// rule is selected by the package's ID. Under a mode above none each
// requirement of each rule the package meets is met as step 10 of the
// §4.7.11 attestation check states, with the package digest as the one
// candidate, and a bundle that a signer of each such rule accepts lists the
// layer subject with the layer digest. Under require a failure is the
// error, and under warn it is a warning in the result. The well-formed rule
// is the caller's, because it applies under every mode before the check.
//
// Spec: §13.11.3, §4.7.11 (Attestation check)
func (c *Checker) VerifyLocal(ctx context.Context, p LocalPackage) (Result, error) {
	res := c.resolve(p.ID)
	if !res.Resolution.Mode.AboveNone() {
		return res, nil
	}
	res.Checked = true
	return res.settle(c.checkLocal(ctx, p, &res))
}

// checkLocal runs the filesystem-source check steps for one package.
func (c *Checker) checkLocal(ctx context.Context, p LocalPackage, res *Result) error {
	plan, err := res.Resolution.Plan()
	res.Warnings = append(res.Warnings, plan.Warnings...)
	if err != nil {
		return err
	}
	if p.SubjectErr != nil || p.SubjectName == "" {
		return fail(CodeAttestationMissing, "", "the package has no subject name", p.SubjectErr)
	}
	res.SubjectName, res.Candidates = p.SubjectName, []string{p.PackageDigest}
	layer := p.Layer
	if layer == nil {
		layer = &LocalLayer{}
	}
	if layer.Err != nil {
		return layer.Err
	}
	if len(layer.Bundles) == 0 {
		return fail(CodeAttestationMissing, "", "the layer's attestation directory holds no bundle", nil)
	}
	bundles := make([]fetched, len(layer.Bundles))
	for i, b := range layer.Bundles {
		bundles[i] = fetched{digest: AttestationDigest(b), raw: b}
	}
	return c.runPlan(plan, res, func(m RuleMatch) error {
		if err := c.applyRule(ctx, m, bundles, res); err != nil {
			return err
		}
		return c.meetLayer(ctx, m, bundles, layer)
	})
}

// meetLayer checks that a bundle a signer of the rule accepts lists the
// layer subject with the layer digest. A signer that a rule trusts for one
// pattern therefore vouches for the artifact list toward the artifacts of
// that rule alone.
//
// Spec: §13.11.3
func (c *Checker) meetLayer(ctx context.Context, m RuleMatch, bundles []fetched, l *LocalLayer) error {
	if l.Digest != "" && l.SubjectName != "" {
		for _, req := range m.Rule().Requirements {
			_, err := c.meetRequirement(ctx, m.File, req, bundles, l.SubjectName, []string{l.Digest})
			if err == nil {
				return nil
			}
			if f, ok := FailureOf(err); ok && f.Code == CodeAttestationRootStale {
				return err
			}
		}
	}
	return fail(CodeAttestationMismatch, ConstraintLayerDigest,
		"no bundle that a signer of the rule accepts lists the layer subject with the layer digest", nil)
}

// ReadBundleDirectory reads the bundle files of the attestation directory
// of the layer rooted at fsys for a consumer, under the consumer's limits:
// every *.sigstore.json entry in name order, each read once, so the bytes
// returned are the bytes the consumer verifies. A missing directory holds
// no bundle. More entries than MaxBundles and a file over MaxBytes are a
// materialize.attestation_invalid Failure with the constraint limits, and a
// directory that cannot be listed, or an entry that is not a regular file
// the consumer can read, one with the constraint unreadable.
//
// Spec: §13.11.3
func ReadBundleDirectory(fsys fs.FS, lim Limits) ([][]byte, error) {
	if lim.MaxBundles <= 0 {
		lim.MaxBundles = DefaultMaxBundles
	}
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = DefaultMaxBundleBytes
	}
	entries, err := fs.ReadDir(fsys, AttestationDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fail(CodeAttestationInvalid, ConstraintUnreadable, "the attestation directory cannot be listed", err)
	}
	var names []string
	for _, e := range entries {
		if IsBundleFileName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) > lim.MaxBundles {
		return nil, fail(CodeAttestationInvalid, ConstraintLimits,
			fmt.Sprintf("the attestation directory holds %d bundle files, more than %s (%d)", len(names), MaxBundlesSetting, lim.MaxBundles), nil)
	}
	sort.Strings(names)
	out := make([][]byte, 0, len(names))
	for _, name := range names {
		raw, err := readBundleFile(fsys, path.Join(AttestationDir, name), lim.MaxBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

// readBundleFile reads one bundle entry that is a regular file of at most
// maxBytes bytes.
func readBundleFile(fsys fs.FS, name string, maxBytes int64) ([]byte, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fail(CodeAttestationInvalid, ConstraintUnreadable, "a bundle entry is not a regular file the consumer can read: "+ReportString(path.Base(name)), err)
	}
	tooLarge := func(n int64) error {
		return fail(CodeAttestationInvalid, ConstraintLimits,
			fmt.Sprintf("the bundle file %s holds %d bytes, more than %s (%d)", ReportString(path.Base(name)), n, MaxBytesSetting, maxBytes), nil)
	}
	if info.Size() > maxBytes {
		return nil, tooLarge(info.Size())
	}
	raw, err := readAtMost(fsys, name, maxBytes)
	if err != nil {
		return nil, fail(CodeAttestationInvalid, ConstraintUnreadable, "a bundle file cannot be read: "+ReportString(path.Base(name)), err)
	}
	if int64(len(raw)) > maxBytes {
		// The file grew between the stat and the read.
		return nil, tooLarge(int64(len(raw)))
	}
	return raw, nil
}

// readAtMost reads name, stopping one byte past limit, so a file that grows
// after its stat is not read whole.
func readAtMost(fsys fs.FS, name string, limit int64) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit+1))
}
