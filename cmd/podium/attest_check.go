package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// checkOptions are the parsed flags and operand of `podium attest check`.
type checkOptions struct {
	layer        attestOptions
	requirements string
	roots        trustedRootFlags
	limits       attestLimits
	rootSettings rootSettings
}

// countedBundle is a bundle the check counts: every bundle that parses, or
// with --requirements every bundle the requirement accepts.
type countedBundle struct {
	name      string
	statement sign.AttestationStatement
}

// checkReport collects the findings of one check. failed is set by every
// finding that fails the check.
type checkReport struct {
	out    io.Writer
	failed bool
}

// fail writes a finding that fails the check.
func (r *checkReport) fail(format string, args ...any) {
	r.failed = true
	_, _ = fmt.Fprintf(r.out, format+"\n", args...)
}

// runAttestCheck is `podium attest check` with its environment, streams,
// and context injected. It exits 0 when the check passes, 1 when a condition
// fails or an input cannot be read or is refused, and 2 on a usage error.
//
// Spec: §7.3.5
func runAttestCheck(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	opts, code := parseCheckArgs(args, getenv, stderr)
	if code >= 0 {
		return code
	}
	insts := &checkInstances{ctx: ctx, files: opts.roots, settings: opts.rootSettings, stderr: stderr, built: map[string]sign.AttestationInstance{}}
	reqs, err := loadCheckRequirements(opts.requirements, insts)
	if err == nil {
		err = checkLayer(ctx, opts, reqs, insts, &checkReport{out: stdout}, stderr)
	}
	switch {
	case errors.Is(err, errCheckFailed):
		return 1
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	return 0
}

// errCheckFailed reports a check whose findings failed it; the findings are
// already written.
var errCheckFailed = errors.New("attest: the check failed")

// parseCheckArgs parses args. It returns code -1 with the options when the
// command proceeds, and the exit code otherwise, having written the reason.
func parseCheckArgs(args []string, getenv func(string) string, stderr io.Writer) (checkOptions, int) {
	flags := flag.NewFlagSet("attest check", flag.ContinueOnError)
	setUsage(flags, "Compare a layer's attestation directory with its packages.")
	flags.SetOutput(stderr)
	opts := checkOptions{roots: trustedRootFlags{}}
	root := flags.String("root", "", "the layer's registered root below <dir>")
	flags.StringVar(&opts.layer.commit, "commit", "", "read the layer's packages from this commit's tree")
	flags.StringVar(&opts.requirements, "requirements", "", "verify each bundle against the requirement of this requirements file")
	flags.Var(opts.roots, "trusted-root", "<instance>=<file>: the trusted root of a Sigstore instance (repeatable)")
	ops, err := parseOperands(flags, args)
	if err != nil {
		return opts, parseExit(err)
	}
	opts.layer.dir = "."
	switch len(ops) {
	case 0:
	case 1:
		opts.layer.dir = ops[0]
	default:
		_, _ = fmt.Fprintf(stderr, "error: podium attest check takes at most one <dir>, got %d\n", len(ops))
		return opts, 2
	}
	if opts.layer.root, opts.layer.layerName, err = parseLayerRoot(*root); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: --root: %s\n", oneLine(err.Error()))
		return opts, 2
	}
	if opts.limits, err = readAttestLimits(getenv); err == nil && opts.requirements != "" {
		opts.rootSettings, err = readRootSettings(getenv)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %s\n", oneLine(err.Error()))
		return opts, 2
	}
	return opts, -1
}

// loadCheckRequirements reads the requirements file at path, when one is
// named, and applies the §4.7.11 policy rules that read a requirement or a
// signer, with an instance known when the check has a trusted root for it.
//
// Spec: §4.7.11 (Requirements file), §7.3.5
func loadCheckRequirements(path string, insts *checkInstances) ([]store.TrustRequirement, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the requirements file: %w", err)
	}
	f, err := layer.ParseRequirementsFile(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := layer.CheckRequirements(f.TrustRequirements, insts.known); err != nil {
		return nil, fmt.Errorf("%s: %w; a signer's instance other than public-good needs --trusted-root <instance>=<file>", path, err)
	}
	return f.TrustRequirements, nil
}

// checkLayer reads the layer and its attestation directory and writes the
// findings. It returns errCheckFailed when a finding failed the check.
//
// Spec: §7.3.5
func checkLayer(ctx context.Context, opts checkOptions, reqs []store.TrustRequirement, insts *checkInstances, r *checkReport, stderr io.Writer) error {
	records, files, err := readAttestLayer(ctx, opts.layer, stderr)
	if err != nil {
		return err
	}
	layerSubject, content, err := attestLayerSubjects(ctx, opts.layer.layerName, records, files, stderr)
	if err != nil {
		return err
	}
	if opts.layer.commit != "" {
		if err := reportWorkTreeDiff(opts.layer, records, content, r.out); err != nil {
			return err
		}
	}
	bundles, err := readBundleDir(attestationDir(opts.layer.dir, opts.layer.root), opts.limits)
	if err != nil {
		return err
	}
	counted, err := countBundles(ctx, bundles, reqs, insts, opts.limits, r, stderr)
	if err != nil {
		return err
	}
	checkConditions(layerSubject, records, counted, r)
	if r.failed {
		return errCheckFailed
	}
	_, _ = fmt.Fprintf(r.out, "ok: the bundles list the layer subject %s and the %d packages of the layer\n", oneLine(layerSubject.Name), len(records))
	return nil
}

// countBundles reports the limits of the directory and returns the bundles
// the check counts: without requirements every bundle that parses, and with
// them every bundle the requirement accepts.
//
// Spec: §7.3.5
func countBundles(ctx context.Context, bundles []dirBundle, reqs []store.TrustRequirement, insts *checkInstances, limits attestLimits, r *checkReport, stderr io.Writer) ([]countedBundle, error) {
	if len(bundles) > limits.maxBundles {
		r.fail("limits: the attestation directory holds %d bundle files, above %s (%d)", len(bundles), attestMaxBundlesEnv, limits.maxBundles)
	}
	if len(reqs) == 0 {
		_, _ = fmt.Fprintln(r.out, "note: no signature was verified; --requirements <file> verifies each bundle against a layer's requirement")
	}
	var counted []countedBundle
	for _, b := range bundles {
		switch {
		case b.oversize:
			r.fail("limits: %s %s", oneLine(b.name), b.problem(limits))
			continue
		case b.irregular, b.parseErr != nil:
			_, _ = fmt.Fprintf(stderr, "warning: %s %s, so it counts as no bundle\n", oneLine(b.name), b.problem(limits))
			continue
		}
		if len(reqs) > 0 {
			ok, reason, err := acceptBundle(ctx, b.raw, reqs[0], insts)
			if err != nil {
				return nil, err
			}
			if !ok {
				_, _ = fmt.Fprintf(r.out, "refused: %s: %s\n", oneLine(b.name), reason)
				continue
			}
		}
		counted = append(counted, countedBundle{name: b.name, statement: b.summary.Statement})
	}
	return counted, nil
}

// acceptBundle verifies raw against each signer of req under the signer's
// instance, as layer step 2 of ingest does. It returns true when a signer
// accepts the bundle and the predicate type meets req, and otherwise the
// reason that comes last in the §4.7.11 list among the signers' refusals.
// An error that is no refusal of the bundle, such as a trusted root past its
// maximum age, is returned.
//
// Spec: §4.7.11, §7.3.5
func acceptBundle(ctx context.Context, raw []byte, req store.TrustRequirement, insts *checkInstances) (bool, sign.AttestationReason, error) {
	type result struct {
		v      *sign.VerifiedAttestation
		reason sign.AttestationReason
	}
	byInstance := map[string]result{}
	var reason sign.AttestationReason
	for _, s := range req.Signers {
		name := s.Instance
		if name == "" {
			name = sign.PublicGoodInstance
		}
		res, ok := byInstance[name]
		if !ok {
			inst, err := insts.instance(name)
			if err != nil {
				return false, "", err
			}
			v, err := sign.VerifyAttestationBundle(ctx, raw, inst)
			if err != nil {
				rr, isRefusal := sign.AttestationReasonOf(err)
				if !isRefusal {
					return false, "", err
				}
				res.reason = rr
			}
			res.v = v
			byInstance[name] = res
		}
		if res.v == nil {
			reason = sign.LaterAttestationReason(reason, res.reason)
			continue
		}
		if err := res.v.AcceptsSigner(layer.VerifierSigner(s)); err != nil {
			rr, _ := sign.AttestationReasonOf(err)
			reason = sign.LaterAttestationReason(reason, rr)
			continue
		}
		if req.PredicateType != "" && res.v.Statement.PredicateType != req.PredicateType {
			reason = sign.LaterAttestationReason(reason, sign.ReasonPredicateType)
			continue
		}
		return true, "", nil
	}
	return false, reason, nil
}

// checkConditions writes a finding for a layer subject that no counted
// bundle lists with the layer digest, and for each package that no counted
// bundle lists with its package digest, with the package's resolution.
//
// Spec: §7.3.5
func checkConditions(layerSubject attest.Subject, records []filesystem.ArtifactRecord, counted []countedBundle, r *checkReport) {
	if !listedBy(counted, layerSubject) {
		r.fail("missing: no bundle lists the layer subject %s with the layer digest %s", oneLine(layerSubject.Name), layerSubject.Digest)
	}
	for i := range records {
		// attestLayerSubjects refused a record with no subject name.
		name, _ := records[i].SubjectName()
		s := attest.Subject{Name: name, Digest: records[i].PackageDigest}
		if listedBy(counted, s) {
			continue
		}
		r.fail("missing: no bundle lists %s with the package digest %s", oneLine(name), s.Digest)
		writeResolution(r.out, &records[i])
		for _, d := range listedDigests(counted, name) {
			_, _ = fmt.Fprintf(r.out, "  listed with: sha256:%s\n", oneLine(d))
		}
	}
}

// writeResolution writes the resolution state of a package and, for a
// resolved one, the resolved fields its package digest frames, so a
// reviewer reads the values a signature would attest.
//
// Spec: §4.7.6, §7.3.5
func writeResolution(w io.Writer, rec *filesystem.ArtifactRecord) {
	if rec.Unparseable != "" {
		_, _ = fmt.Fprintf(w, "  state: unresolved (%s)\n", oneLine(rec.Unparseable))
		return
	}
	f := rec.Fields
	_, _ = fmt.Fprintf(w, "  state: resolved\n  type: %q\n  sensitivity: %q\n  deprecated: %t\n  replaced_by: %q\n",
		f.Type, f.Sensitivity, f.Deprecated, f.ReplacedBy)
}

// listedBy reports whether a counted bundle lists s.
func listedBy(counted []countedBundle, s attest.Subject) bool {
	hex := strings.TrimPrefix(s.Digest, "sha256:")
	for _, b := range counted {
		if b.statement.Lists(s.Name, hex) {
			return true
		}
	}
	return false
}

// listedDigests returns the distinct sha256 members under which counted
// bundles list name, in byte order.
func listedDigests(counted []countedBundle, name string) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range counted {
		for _, s := range b.statement.Subjects {
			d, ok := s.Digest["sha256"]
			if s.Name != name || !ok || seen[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// reportWorkTreeDiff lists each package whose digest in the directory on
// disk differs from its digest in the commit, with the paths that differ,
// each package only one of them holds, and each layer file that differs.
// The list changes no exit status.
//
// Spec: §7.3.5
func reportWorkTreeDiff(opts attestOptions, commitRecords []filesystem.ArtifactRecord, commitContent attest.Layer, w io.Writer) error {
	layerPath, err := filepath.Abs(filepath.Join(opts.dir, filepath.FromSlash(opts.root)))
	if err != nil {
		return err
	}
	// A bare repository has no working tree to compare.
	if info, err := os.Stat(layerPath); err != nil || !info.IsDir() {
		_, _ = fmt.Fprintf(w, "no working tree at %s to compare with commit %s\n", oneLine(layerPath), oneLine(opts.commit))
		return nil
	}
	wtRecords, wtFiles, err := filesystem.LayerRecords(filesystem.Layer{ID: filepath.Base(layerPath), Path: layerPath})
	if err != nil {
		return fmt.Errorf("read the working tree: %w", err)
	}
	wtLayerFiles, err := filesystem.LayerFiles(wtFiles)
	if err != nil {
		return err
	}
	lines := packageDiff(commitRecords, wtRecords)
	for _, p := range differingPaths(commitContent.Files, wtLayerFiles) {
		lines = append(lines, "  layer file "+oneLine(p))
	}
	if len(lines) == 0 {
		_, _ = fmt.Fprintf(w, "the working tree matches commit %s\n", oneLine(opts.commit))
		return nil
	}
	_, _ = fmt.Fprintf(w, "the working tree differs from commit %s:\n%s\n", oneLine(opts.commit), strings.Join(lines, "\n"))
	return nil
}

// packageDiff returns one line per package whose digest differs between the
// commit and the working tree, or that only one of them holds, in ID order.
func packageDiff(commit, worktree []filesystem.ArtifactRecord) []string {
	byID := func(recs []filesystem.ArtifactRecord) map[string]*filesystem.ArtifactRecord {
		m := make(map[string]*filesystem.ArtifactRecord, len(recs))
		for i := range recs {
			m[recs[i].ID] = &recs[i]
		}
		return m
	}
	c, wt := byID(commit), byID(worktree)
	ids := make([]string, 0, len(c)+len(wt))
	for id := range c {
		ids = append(ids, id)
	}
	for id := range wt {
		if c[id] == nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var lines []string
	for _, id := range ids {
		cr, wr := c[id], wt[id]
		switch {
		case wr == nil:
			lines = append(lines, "  package "+oneLine(id)+": only in the commit")
		case cr == nil:
			lines = append(lines, "  package "+oneLine(id)+": only in the working tree")
		case cr.PackageDigest != wr.PackageDigest:
			paths := differingPaths(cr.FileSums, wr.FileSums)
			for i := range paths {
				paths[i] = oneLine(paths[i])
			}
			lines = append(lines, "  package "+oneLine(id)+": "+strings.Join(paths, ", "))
		}
	}
	return lines
}

// differingPaths returns the paths that a or b holds with another digest or
// that only one of them holds, in byte order.
func differingPaths(a, b map[string]attest.Sum) []string {
	var out []string
	for p, s := range a {
		if t, ok := b[p]; !ok || t != s {
			out = append(out, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
