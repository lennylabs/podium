package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	stdsync "sync"
	"time"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/sync"
)

// The §13.12 settings of the consumer trust policy of podium sync and
// podium verify (§7.5 Author attestation).
const (
	attestPolicyEnv     = "PODIUM_ATTESTATION_POLICY"
	attestPolicyFileEnv = "PODIUM_ATTESTATION_POLICY_FILE"
)

// syncAttestationInputs are the settings and paths a podium sync or podium
// verify invocation builds its consumer trust policy from.
type syncAttestationInputs struct {
	ctx    context.Context
	getenv func(string) string
	// cwd resolves a relative PODIUM_ATTESTATION_POLICY_FILE, and startDir
	// is where the workspace sync.yaml files are discovered.
	cwd, startDir string
	// home locates ~/.podium/sync.yaml and the root copy; empty skips both.
	home   string
	stderr io.Writer
	now    time.Time
}

// newSyncAttestation builds the memoized §4.7.11 consumer trust policy
// loader of one podium sync invocation, the attestation counterpart of
// newSyncDeliveryCheck. Building it reads nothing; pkg/sync calls it at the
// start of a load, so it loads once however many targets, watch cycles, or
// override steps the invocation runs.
//
// Spec: §7.5 (Author attestation)
func newSyncAttestation(startDir string) sync.AttestationFunc {
	home, _ := os.UserHomeDir()
	in := syncAttestationInputs{ctx: context.Background(), getenv: os.Getenv, cwd: workingDir(),
		startDir: startDir, home: home, stderr: os.Stderr}
	var (
		once stdsync.Once
		att  *sync.Attestation
		err  error
	)
	return func() (*sync.Attestation, error) {
		once.Do(func() {
			in.now = time.Now()
			att, err = loadSyncAttestation(in)
		})
		return att, err
	}
}

// reportAttestationConfigError prints err as prefix + "error: <message>" and
// returns true when err is a failure to load the consumer trust policy.
func reportAttestationConfigError(err error, prefix string) bool {
	var cfgErr *sync.AttestationConfigError
	if !errors.As(err, &cfgErr) {
		return false
	}
	fmt.Fprintf(os.Stderr, "%serror: %v\n", prefix, cfgErr)
	return true
}

// policyUnavailable is a config.attestation_policy_unavailable error.
func policyUnavailable(format string, args ...any) error {
	return fmt.Errorf("%s: %s", attest.CodePolicyUnavailable, fmt.Sprintf(format, args...))
}

// syncPolicyBuilder accumulates the sources of the policy and the
// public-good roots their files verify under.
type syncPolicyBuilder struct {
	in       syncAttestationInputs
	settings rootSettings
	// maxAgeSet reports that the environment sets the maximum age, which
	// then applies to every rule.
	maxAgeSet bool
	// refreshed is the public-good root the invocation refreshes through
	// TUF, built when a file first needs it.
	refreshed *sign.TUFAttestationRoot
	envRoot   *sign.AttestationInstance
	sources   []attest.Source
}

// loadSyncAttestation builds the consumer trust policy of podium sync from
// its sources (§7.5 Author attestation): the environment and the user's
// ~/.podium/sync.yaml, which are user sources, and the workspace sync.yaml
// and sync.local.yaml, which are repository sources. Every failure is a
// config.attestation_policy_unavailable error.
//
// Spec: §7.5 (Author attestation), §4.7.11 (Policy sources, Trusted roots of
// a consumer, Policy load)
func loadSyncAttestation(in syncAttestationInputs) (*sync.Attestation, error) {
	limits, err := attest.ReadLimits(in.getenv)
	if err != nil {
		return nil, policyUnavailable("%v", err)
	}
	settings, err := readRootSettings(in.getenv)
	if err != nil {
		return nil, policyUnavailable("%v", err)
	}
	b := &syncPolicyBuilder{in: in, settings: settings, maxAgeSet: strings.TrimSpace(in.getenv(attestRootMaxAgeEnv)) != ""}
	att := &sync.Attestation{Limits: limits, LayerRoots: map[string]string{}}
	if err := b.environment(); err != nil {
		return nil, err
	}
	blocks, err := b.files(att)
	if err != nil {
		return nil, err
	}
	for _, blk := range blocks {
		for id, root := range blk.LayerRoots {
			att.LayerRoots[id] = root
		}
		att.PinSigners = att.PinSigners || blk.PinSigners
	}
	if att.Policy, err = attest.NewPolicy(b.sources...); err != nil {
		return nil, err
	}
	if att.Policy.AboveNone() && settings.rootFile != "" {
		// A root file the environment names refuses the invocation when it
		// cannot be read, whether or not a rule names public-good.
		if _, err := b.environmentRoot(); err != nil {
			return nil, err
		}
	}
	return att, nil
}

// environment adds the environment of the process, a user source.
func (b *syncPolicyBuilder) environment() error {
	mode, err := attest.ParseMode(strings.TrimSpace(b.in.getenv(attestPolicyEnv)))
	if err != nil {
		return policyUnavailable("%s: %v", attestPolicyEnv, err)
	}
	src := attest.Source{Class: attest.UserSource, Name: "the environment", DefaultMode: mode}
	if file := strings.TrimSpace(b.in.getenv(attestPolicyFileEnv)); file != "" {
		if !filepath.IsAbs(file) {
			file = filepath.Join(b.in.cwd, file)
		}
		f, err := b.loadFile(file, b.environmentRoot)
		if err != nil {
			return err
		}
		src.Files = append(src.Files, f)
	}
	b.sources = append(b.sources, src)
	return nil
}

// files adds the defaults.attestation blocks of the user's sync.yaml and of
// the workspace files, and returns the blocks in precedence order, low to
// high. The user's file supplies the overlay setting.
func (b *syncPolicyBuilder) files(att *sync.Attestation) ([]*sync.AttestationBlock, error) {
	var blocks []*sync.AttestationBlock
	if b.in.home != "" {
		blk, err := sync.ReadAttestationBlock(filepath.Join(b.in.home, ".podium", "sync.yaml"))
		if err != nil {
			return nil, err
		}
		if blk != nil {
			if err := b.addBlock(blk, attest.UserSource, b.userRoot(blk)); err != nil {
				return nil, err
			}
			att.Overlay = blk.Overlay
			blocks = append(blocks, blk)
		}
	}
	ws, ok := sync.DiscoverWorkspace(b.in.startDir)
	if !ok || b.in.startDir == "" {
		return blocks, nil
	}
	for _, name := range []string{"sync.yaml", "sync.local.yaml"} {
		path := filepath.Join(ws, ".podium", name)
		if b.in.home != "" && sameFile(path, filepath.Join(b.in.home, ".podium", "sync.yaml")) {
			// A workspace at the home directory reads the user's file once,
			// as a user source.
			continue
		}
		blk, err := sync.ReadAttestationBlock(path)
		if err != nil {
			return nil, err
		}
		if blk == nil {
			continue
		}
		b.ignoreUserOnlyKeys(blk)
		if err := b.addBlock(blk, attest.RepositorySource, b.environmentRoot); err != nil {
			return nil, err
		}
		blocks = append(blocks, blk)
	}
	return blocks, nil
}

// ignoreUserOnlyKeys names on standard error the keys that take effect from
// the user's sync.yaml alone, which a workspace file sets.
func (b *syncPolicyBuilder) ignoreUserOnlyKeys(blk *sync.AttestationBlock) {
	for _, k := range []struct {
		name string
		set  bool
	}{{"trusted_root_file", blk.TrustedRootFile != ""}, {"root_max_age", blk.RootMaxAgeSet}, {"overlay", blk.Overlay != ""}} {
		if k.set {
			_, _ = fmt.Fprintf(b.in.stderr, "warning: %s: defaults.attestation.%s takes effect from ~/.podium/sync.yaml alone and is ignored here\n", blk.Path, k.name)
		}
	}
}

// addBlock adds one sync.yaml block as a source of class.
func (b *syncPolicyBuilder) addBlock(blk *sync.AttestationBlock, class attest.SourceClass, root func() (sign.AttestationInstance, error)) error {
	src := attest.Source{Class: class, Name: blk.Path, DefaultMode: blk.Policy}
	if blk.PolicyFile != "" {
		f, err := b.loadFile(blk.PolicyFile, root)
		if err != nil {
			return err
		}
		src.Files = append(src.Files, f)
	}
	b.sources = append(b.sources, src)
	return nil
}

// loadFile loads a policy file, building the public-good instance through
// root only when a signer of the file names it, so a policy of file
// instances and keys makes no TUF request.
func (b *syncPolicyBuilder) loadFile(path string, root func() (sign.AttestationInstance, error)) (*attest.PolicyFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, policyUnavailable("policy file %s: cannot read the file: %v", path, err)
	}
	doc, err := attest.ParsePolicyDocument(raw)
	if err != nil {
		return nil, policyUnavailable("policy file %s: %v", path, err)
	}
	var pg sign.AttestationInstance
	if namesPublicGood(doc) {
		if pg, err = root(); err != nil {
			return nil, policyUnavailable("policy file %s: the public-good root: %v", path, err)
		}
	}
	f, err := attest.NewPolicyFile(path, doc, pg, func(file string) (*sign.AttestationRoot, error) {
		if !filepath.IsAbs(file) {
			file = filepath.Join(filepath.Dir(path), file)
		}
		return sign.LoadAttestationRootFile(file)
	}, b.in.now)
	if err != nil {
		return nil, policyUnavailable("policy file %s: %v", path, err)
	}
	return f, nil
}

// namesPublicGood reports whether a signer of doc names public-good.
func namesPublicGood(doc *attest.PolicyDocument) bool {
	for _, r := range doc.Rules {
		for _, req := range r.Requirements {
			for _, s := range req.Signers {
				if s.Instance == sign.PublicGoodInstance {
					return true
				}
			}
		}
	}
	return false
}

// environmentRoot returns the public-good instance of the environment's
// root settings, which every rule verifies under unless the user's
// sync.yaml sets a root for its own rules: the file of
// PODIUM_ATTESTATION_TRUSTED_ROOT_FILE, or the root the invocation
// refreshes under PODIUM_SIGSTORE_ROOT_MAX_AGE.
func (b *syncPolicyBuilder) environmentRoot() (sign.AttestationInstance, error) {
	if b.envRoot != nil {
		return *b.envRoot, nil
	}
	var inst sign.AttestationInstance
	var err error
	if b.settings.rootFile != "" {
		inst, err = publicGoodFromFile(b.settings.rootFile, b.in.now)
	} else {
		inst, err = b.refreshedRoot(b.settings.maxAge)
	}
	if err != nil {
		return sign.AttestationInstance{}, err
	}
	b.envRoot = &inst
	return inst, nil
}

// userRoot returns the public-good root of the rules of the policy file of
// the user's sync.yaml: the environment's settings where they are set, and
// otherwise the trusted_root_file and the root_max_age of that file.
func (b *syncPolicyBuilder) userRoot(blk *sync.AttestationBlock) func() (sign.AttestationInstance, error) {
	return func() (sign.AttestationInstance, error) {
		switch {
		case b.settings.rootFile != "":
			return b.environmentRoot()
		case blk.TrustedRootFile != "":
			return publicGoodFromFile(blk.TrustedRootFile, b.in.now)
		case blk.RootMaxAgeSet && !b.maxAgeSet:
			return b.refreshedRoot(blk.RootMaxAge)
		}
		return b.environmentRoot()
	}
}

// refreshedRoot returns the refreshed public-good root under maxAge. The
// invocation refreshes once through TUF into .podium/sigstore/public-good/
// under the platform home and falls back to the copy there or the embedded
// copy when the refresh fails; each maximum age is a view of that one root.
func (b *syncPolicyBuilder) refreshedRoot(maxAge time.Duration) (sign.AttestationInstance, error) {
	if b.refreshed == nil {
		if b.in.home == "" {
			return sign.AttestationInstance{}, errors.New("no home directory holds the public-good root copy")
		}
		ci := &checkInstances{ctx: b.in.ctx, settings: rootSettings{timeout: b.settings.timeout, maxAge: 0}, stderr: b.in.stderr}
		inst, err := ci.refreshedPublicGoodIn(b.in.home)
		if err != nil {
			return sign.AttestationInstance{}, err
		}
		src, ok := inst.Root.(*sign.TUFAttestationRoot)
		if !ok {
			return sign.AttestationInstance{}, errors.New("the refreshed public-good root is not a TUF root")
		}
		b.refreshed = src
	}
	return sign.AttestationInstance{Name: sign.PublicGoodInstance, Root: maxAgeRoot{src: b.refreshed, maxAge: maxAge, now: time.Now},
		TransparencyLog: true, CertificateTransparency: true}, nil
}

// publicGoodFromFile loads a public-good root from a file, which must list
// both kinds of log, as the registry requires.
func publicGoodFromFile(file string, now time.Time) (sign.AttestationInstance, error) {
	r, err := sign.LoadAttestationRootFile(file)
	if err != nil {
		return sign.AttestationInstance{}, err
	}
	if err := sign.CheckPublicGoodRoot(r); err != nil {
		return sign.AttestationInstance{}, fmt.Errorf("%s: %w", file, err)
	}
	return sign.AttestationInstance{Name: sign.PublicGoodInstance, Root: sign.FileAttestationRoot{Root: r, ReadAt: now},
		TransparencyLog: true, CertificateTransparency: true}, nil
}

// maxAgeRoot is the refreshed public-good root under one maximum age, so the
// rules of the user's sync.yaml and every other rule share one refresh and
// each applies its own limit. 0 removes the limit.
type maxAgeRoot struct {
	src    *sign.TUFAttestationRoot
	maxAge time.Duration
	now    func() time.Time
}

// AttestationRoot returns the root in use, or sign.ErrAttestationRootStale
// when it is older than the maximum age. An unknown root time, which the
// embedded copy of a source build has, is older than every limit.
//
// Spec: §4.7.11 (Maximum age, Trusted roots of a consumer)
func (r maxAgeRoot) AttestationRoot(ctx context.Context) (*sign.AttestationRoot, error) {
	root, err := r.src.AttestationRoot(ctx)
	if err != nil {
		return nil, err
	}
	at := r.src.Status().Time
	if r.maxAge > 0 && (at.IsZero() || r.now().Sub(at) > r.maxAge) {
		return nil, fmt.Errorf("%w: the public-good root in use is older than its maximum age (%s)", sign.ErrAttestationRootStale, r.maxAge)
	}
	return root, nil
}

// Status reports the shared root in use.
func (r maxAgeRoot) Status() sign.AttestationRootStatus { return r.src.Status() }

// sameFile reports whether a and b name one existing file.
func sameFile(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}

// targetChecks are the memoized resolvers of one --config run that every
// kind: workspace target passes to pkg/sync: the §4.7.10 delivery check and
// the §4.7.11 consumer trust policy.
type targetChecks struct {
	delivery    sync.DeliveryCheckFunc
	attestation sync.AttestationFunc
}

// reportAttestationNotes writes the §4.7.11 warnings of a run and the
// signer changes of its lock (§7.5.3) on w.
//
// Spec: §7.5 (Author attestation), §7.5.3 (Signer changes)
func reportAttestationNotes(w io.Writer, warnings []string, changes []sync.SignerChange) {
	for _, s := range warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", s)
	}
	for _, c := range changes {
		_, _ = fmt.Fprintf(w, "signer change: %s\n", c)
	}
}
