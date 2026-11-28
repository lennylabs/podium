package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
	synccfg "github.com/lennylabs/podium/pkg/sync"
)

// The §13.12 settings of the MCP server's consumer trust policy. Every one
// of them is a repository source (§4.7.11 Policy sources), because a
// harness reads the environment and the flags of the MCP server from
// project files that a repository commits.
const (
	envAttestationPolicy     = "PODIUM_ATTESTATION_POLICY"
	envAttestationPolicyFile = "PODIUM_ATTESTATION_POLICY_FILE"
	envAttestationRootFile   = "PODIUM_ATTESTATION_TRUSTED_ROOT_FILE"
	envRootMaxAge            = "PODIUM_SIGSTORE_ROOT_MAX_AGE"
	envRootRefresh           = "PODIUM_SIGSTORE_ROOT_REFRESH"
	envRootRefreshTimeout    = "PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT"
	envFetchTimeout          = "PODIUM_ATTESTATION_FETCH_TIMEOUT"
)

// rootCopyDir is the directory of the root copy of user-source rules under
// the account home.
var rootCopyDir = filepath.Join(".podium", "sigstore", "public-good")

// The notices the MCP server places in the attestation_warning member of
// every load result: a failed account lookup and a machine file that
// exists and fails its ownership check (§4.7.11 User home, Machine file).
const (
	constraintUserHome    = "user_home"
	constraintMachineFile = "machine_file"
	constraintOverlay     = "overlay"
	// constraintWorkspaceFile reports a workspace sync.yaml that does not
	// parse and that the start does not refuse for.
	constraintWorkspaceFile = "workspace_file"
)

// consumerAttestation is the MCP server's resolved §4.7.11 consumer trust
// policy: the combined policy of every source, the overlay setting of the
// user sources, the notices of every load result, and the public-good
// roots whose refresh the server runs.
type consumerAttestation struct {
	policy *attest.Policy
	// overlay is allow, deny, or empty, the strictest value the user's
	// sync.yaml and the machine file set.
	overlay string
	// notices are warning texts for every load result.
	notices []string
	// refreshers are the refreshed public-good roots in use.
	refreshers []*sign.TUFAttestationRoot
	// maxBytes is PODIUM_ATTESTATION_MAX_BYTES.
	maxBytes int64
	// fetchTimeout is PODIUM_ATTESTATION_FETCH_TIMEOUT, the deadline of one
	// bundle fetch; zero is attest.DefaultBundleFetchTimeout.
	fetchTimeout time.Duration
}

// attestationInputs are the settings and system paths the policy is built
// from.
type attestationInputs struct {
	getenv func(string) string
	// flagMode and flagFile are --attestation-policy and
	// --attestation-policy-file, or their configuration-file keys.
	flagMode, flagFile string
	paths              systemPaths
	// home is the account home, empty when homeErr is set.
	home    string
	homeErr error
	cwd     string
	now     time.Time
	stderr  func(format string, args ...any)
}

// policyBuilder accumulates the sources of the policy and the public-good
// roots their files verify under.
type policyBuilder struct {
	in      attestationInputs
	sources []attest.Source
	out     *consumerAttestation
	// userTUF is the refreshed public-good root under the account home,
	// built when a file first names public-good.
	userTUF *sign.TUFAttestationRoot
	// repoRoot is the public-good root of repository-source rules, built
	// when such a file first names public-good.
	repoRoot *sign.AttestationInstance
	// settings are the public-good settings of the environment.
	settings repoRootSettings
	// userRoot is the root setting of the user-source sync.yaml whose
	// policy file is being loaded.
	userRoot userRootOf
}

// repoRootSettings are the public-good root settings that a repository
// source sets: the variables of the MCP server's environment.
type repoRootSettings struct {
	rootFile         string
	maxAge           time.Duration
	refresh, timeout time.Duration
	// set reports that a refresh setting or the maximum age is set, which
	// gives repository-source rules a refresh of their own.
	set bool
}

// loadConsumerAttestation builds the MCP server's consumer trust policy from
// every source (§4.7.11 Policy sources): the flags and the configuration
// file, the environment, the workspace sync.yaml and sync.local.yaml, all
// repository sources, and the user's own sync.yaml under the account home
// and the machine file, the user sources. A source that names a file that
// does not load, a sync.yaml whose attestation block does not decode, and an
// invalid root setting under a policy above none refuse the start with
// config.attestation_policy_unavailable.
//
// Spec: §4.7.11 (Policy sources, User home, Machine file, Root copy of
// user-source rules, Policy load)
func loadConsumerAttestation(in attestationInputs) (*consumerAttestation, error) {
	b := &policyBuilder{in: in, out: &consumerAttestation{}}
	// The MCP server reads no attestation directory, so only the bundle
	// size limit applies to it.
	limits, err := attest.ReadLimits(func(k string) string {
		if k == attest.MaxBundlesSetting {
			return ""
		}
		return in.getenv(k)
	})
	if err != nil {
		return nil, policyRefusal("%v", err)
	}
	b.out.maxBytes = limits.MaxBytes
	if b.out.fetchTimeout, err = readFetchTimeout(in.getenv); err != nil {
		return nil, err
	}
	b.settings, err = readRepoRootSettings(in.getenv)
	if err != nil {
		return nil, err
	}
	if err := b.repositorySources(); err != nil {
		return nil, err
	}
	if err := b.userSources(); err != nil {
		return nil, err
	}
	b.out.policy, err = attest.NewPolicy(b.sources...)
	if err != nil {
		return nil, err
	}
	if b.out.policy.AboveNone() && b.settings.rootFile != "" {
		// A set root file that cannot be read refuses the start under a
		// policy above none, whether or not a rule names public-good.
		if _, err := b.repositoryPublicGood(); err != nil {
			return nil, err
		}
	}
	return b.out, nil
}

// policyRefusal is a config.attestation_policy_unavailable refusal of the
// start.
func policyRefusal(format string, args ...any) error {
	return fmt.Errorf("%s: %s", attest.CodePolicyUnavailable, fmt.Sprintf(format, args...))
}

// readFetchTimeout reads PODIUM_ATTESTATION_FETCH_TIMEOUT: a duration above
// zero, and the default when unset or blank. The registry client of the MCP
// server has no deadline of its own, so the bundle fetch takes this one.
//
// Spec: §6.2, §4.7.11 (Attestation check, step 8)
func readFetchTimeout(getenv func(string) string) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(envFetchTimeout))
	if raw == "" {
		return attest.DefaultBundleFetchTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, policyRefusal("%s must be a duration above zero, such as 30s, got %q", envFetchTimeout, raw)
	}
	return d, nil
}

// readRepoRootSettings reads the public-good root settings of the
// environment.
//
// Spec: §13.12, §4.7.11 (Trusted roots of a consumer)
func readRepoRootSettings(getenv func(string) string) (repoRootSettings, error) {
	s := repoRootSettings{
		rootFile: strings.TrimSpace(getenv(envAttestationRootFile)),
		maxAge:   sign.DefaultRootMaxAge, refresh: sign.DefaultRootRefresh, timeout: sign.DefaultRootRefreshTimeout,
	}
	for _, d := range []struct {
		name      string
		dst       *time.Duration
		allowZero bool
	}{
		{envRootMaxAge, &s.maxAge, true},
		{envRootRefresh, &s.refresh, false},
		{envRootRefreshTimeout, &s.timeout, false},
	} {
		raw := strings.TrimSpace(getenv(d.name))
		if raw == "" {
			continue
		}
		v, err := time.ParseDuration(raw)
		if err != nil || v < 0 || v == 0 && !d.allowZero {
			return s, policyRefusal("%s must be a duration above zero, got %q", d.name, raw)
		}
		*d.dst = v
		s.set = true
	}
	// The registry's floor: a refresh more often than once a minute, or
	// more often than its own deadline, would send request bursts to the
	// TUF repository or overlap refreshes.
	if s.refresh < sign.MinRootRefresh || s.refresh < s.timeout {
		return s, policyRefusal("%s must be at least %s and at least %s, got %s", envRootRefresh, sign.MinRootRefresh, envRootRefreshTimeout, s.refresh)
	}
	return s, nil
}

// repositorySources adds the sources a repository can set: the explicit
// configuration of the MCP server, its environment, and the sync.yaml and
// sync.local.yaml of the workspace.
func (b *policyBuilder) repositorySources() error {
	if err := b.addSource(attest.RepositorySource, "the podium-mcp configuration", b.in.flagMode, b.in.flagFile, ""); err != nil {
		return err
	}
	env := b.in.getenv
	if err := b.addSource(attest.RepositorySource, "the environment", strings.TrimSpace(env(envAttestationPolicy)),
		strings.TrimSpace(env(envAttestationPolicyFile)), ""); err != nil {
		return err
	}
	ws, ok := synccfg.DiscoverWorkspace(b.in.cwd)
	if !ok || b.in.home != "" && sameDir(ws, b.in.home) {
		// A workspace that is the account home is the user's own file,
		// which userSources reads as a user source.
		return nil
	}
	for _, name := range []string{"sync.yaml", "sync.local.yaml"} {
		path := filepath.Join(ws, ".podium", name)
		cfg, err := synccfg.ReadConfigFile(path)
		if err != nil {
			if err := b.unreadableWorkspaceFile(ws, path, err); err != nil {
				return err
			}
			continue
		}
		if cfg == nil {
			continue
		}
		a, err := cfg.Defaults.AttestationDefaults()
		if err != nil {
			return policyRefusal("%s: %v", path, err)
		}
		for _, k := range a.Set() {
			if slices.Contains(synccfg.UserOnlyAttestationKeys, k) {
				b.in.stderr("WARN: %s sets defaults.attestation.%s, which takes effect only from ~/.podium/sync.yaml and the machine file; ignored\n", path, k)
			}
		}
		if err := b.addSource(attest.RepositorySource, path, a.Policy, synccfg.ResolvePath(path, a.PolicyFile), ""); err != nil {
			return err
		}
	}
	return nil
}

// unreadableWorkspaceFile handles a workspace sync.yaml that cannot be read
// or parsed. The start is refused only when the workspace is the working
// directory and the user of the process owns the file. A file of an
// ancestor workspace, such as /tmp/.podium/sync.yaml above a directory under
// /tmp, and a file another user owns, can be placed by another local user;
// they hold only repository-source rules, which no user rule depends on, so
// they are ignored with a notice rather than stopping every start below
// them.
//
// Spec: §4.7.11 (User sources of the MCP server, Keys)
func (b *policyBuilder) unreadableWorkspaceFile(ws, path string, err error) error {
	if sameDir(ws, b.in.cwd) && ownedByProcessUser(path) {
		return policyRefusal("%v", err)
	}
	b.in.stderr("WARN: ignoring the workspace file %s, which does not parse and is not this user's file in the working directory: %v\n", path, err)
	if !slices.Contains(b.out.notices, noticeText(constraintWorkspaceFile)) {
		b.out.notices = append(b.out.notices, noticeText(constraintWorkspaceFile))
	}
	return nil
}

// readUserSync reads a user-source sync.yaml. A file that does not parse,
// and a key outside the schema at the top level or under defaults, refuse
// the start, because the protective policy it may hold is unknown. A
// missing file is nil.
//
// Spec: §4.7.11 (User sources of the MCP server, Known keys)
func readUserSync(path string) (*synccfg.SyncConfig, error) {
	cfg, err := synccfg.ReadConfigFile(path)
	if err != nil {
		return nil, policyRefusal("%v", err)
	}
	if cfg == nil {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		err = synccfg.CheckKnownKeys(raw)
	}
	if err != nil {
		return nil, policyRefusal("%s: %v", path, err)
	}
	return cfg, nil
}

// userSources adds the user's own sync.yaml under the account home and the
// machine file, and the notices of a failed lookup and of a machine file
// that fails its check.
func (b *policyBuilder) userSources() error {
	var overlays []string
	if b.in.homeErr != nil {
		b.in.stderr("WARN: the account database gives no home directory for this user (%v); ~/.podium/sync.yaml is not read\n", b.in.homeErr)
		b.out.notices = append(b.out.notices, noticeText(constraintUserHome))
	} else {
		path := filepath.Join(b.in.home, ".podium", "sync.yaml")
		cfg, err := readUserSync(path)
		if err != nil {
			return err
		}
		if cfg != nil {
			ov, err := b.addUserFile(path, cfg)
			if err != nil {
				return err
			}
			overlays = append(overlays, ov)
		}
	}
	mf, err := synccfg.ReadMachineConfig(b.in.paths.machine)
	switch {
	case errors.Is(err, synccfg.ErrMachineFileUnsafe):
		b.in.stderr("WARN: ignoring the machine sync.yaml: %v\n", err)
		b.out.notices = append(b.out.notices, noticeText(constraintMachineFile))
	case err != nil:
		return policyRefusal("the machine sync.yaml: %v", err)
	case mf != nil:
		ov, err := b.addUserFile(mf.Path, mf.Config)
		if err != nil {
			return err
		}
		overlays = append(overlays, ov)
	}
	b.out.overlay = strictestOverlay(overlays)
	return nil
}

// addUserFile adds one user-source sync.yaml and returns its overlay value.
// Its trusted_root_file and root_max_age apply to the rules of the policy
// file it names.
func (b *policyBuilder) addUserFile(path string, cfg *synccfg.SyncConfig) (string, error) {
	a, err := cfg.Defaults.AttestationDefaults()
	if err != nil {
		return "", policyRefusal("%s: %v", path, err)
	}
	maxAge := sign.DefaultRootMaxAge
	if a.RootMaxAge != "" {
		d, err := time.ParseDuration(strings.TrimSpace(a.RootMaxAge))
		if err != nil || d < 0 {
			return "", policyRefusal("%s: defaults.attestation.root_max_age must be a duration of zero or more, got %q", path, a.RootMaxAge)
		}
		maxAge = d
	}
	b.userRoot = userRootOf{rootFile: synccfg.ResolvePath(path, a.TrustedRootFile), maxAge: maxAge}
	return a.Overlay, b.addSource(attest.UserSource, path, a.Policy, synccfg.ResolvePath(path, a.PolicyFile), path)
}

// addSource adds one source with its default mode and its policy file. A
// source that sets neither adds nothing. owner is the user-source sync.yaml
// whose root settings apply to the file's rules, empty for a repository
// source.
func (b *policyBuilder) addSource(class attest.SourceClass, name, mode, file, owner string) error {
	if mode == "" && file == "" {
		return nil
	}
	m, err := attest.ParseMode(mode)
	if err != nil {
		return policyRefusal("%s: %v", name, err)
	}
	src := attest.Source{Class: class, Name: name, DefaultMode: m}
	if file != "" {
		pg := b.repositoryPublicGood
		if class == attest.UserSource {
			root := b.userRoot
			pg = func() (sign.AttestationInstance, error) { return b.userPublicGood(owner, root) }
		}
		f, err := attest.LoadPolicyFileWith(file, pg, b.in.now)
		if err != nil {
			return fmt.Errorf("%s (named by %s)", err.Error(), name)
		}
		src.Files = []*attest.PolicyFile{f}
	}
	b.sources = append(b.sources, src)
	return nil
}

// userRootOf is the public-good root setting of one user-source sync.yaml.
type userRootOf struct {
	rootFile string
	maxAge   time.Duration
}

// userPublicGood returns the public-good instance of the rules of a
// user-source file: the root that its sync.yaml names, or the refreshed
// root under the account home with the maximum age that its sync.yaml
// sets. No repository-source setting changes either.
func (b *policyBuilder) userPublicGood(owner string, r userRootOf) (sign.AttestationInstance, error) {
	if r.rootFile != "" {
		return publicGoodFromFile(r.rootFile, b.in.now)
	}
	src, err := b.userRefresher()
	if err != nil {
		return sign.AttestationInstance{}, err
	}
	return publicGoodInstance(src.WithMaxAge(r.maxAge)), nil
}

// userRefresher returns the refreshed public-good root under the account
// home, building it on first use. With no account home the TUF state stays
// in memory and nothing is written.
func (b *policyBuilder) userRefresher() (*sign.TUFAttestationRoot, error) {
	if b.userTUF != nil {
		return b.userTUF, nil
	}
	cfg := b.tufConfig(sign.DefaultRootRefresh, sign.DefaultRootRefreshTimeout, sign.DefaultRootMaxAge)
	if b.in.home == "" {
		cfg.Memory = true
	} else {
		cfg.Dir = filepath.Join(b.in.home, rootCopyDir)
	}
	src, err := sign.NewTUFAttestationRoot(cfg)
	if err != nil {
		return nil, err
	}
	b.userTUF = src
	b.out.refreshers = append(b.out.refreshers, src)
	return src, nil
}

// repositoryPublicGood returns the public-good instance of the rules of a
// repository-source file: the root of PODIUM_ATTESTATION_TRUSTED_ROOT_FILE,
// held in memory; a refresh of its own under the environment's settings
// that starts from the user's copy, reads it, and never writes it; or, with
// no setting, the user's refreshed root.
func (b *policyBuilder) repositoryPublicGood() (sign.AttestationInstance, error) {
	if b.repoRoot != nil {
		return *b.repoRoot, nil
	}
	var inst sign.AttestationInstance
	switch s := b.settings; {
	case s.rootFile != "":
		i, err := publicGoodFromFile(s.rootFile, b.in.now)
		if err != nil {
			return inst, policyRefusal("%s: %v", envAttestationRootFile, err)
		}
		inst = i
	case s.set:
		cfg := b.tufConfig(s.refresh, s.timeout, s.maxAge)
		if b.in.home == "" {
			cfg.Memory = true
		} else {
			cfg.Dir, cfg.ReadOnly = filepath.Join(b.in.home, rootCopyDir), true
		}
		src, err := sign.NewTUFAttestationRoot(cfg)
		if err != nil {
			return inst, policyRefusal("the public-good root settings: %v", err)
		}
		b.out.refreshers = append(b.out.refreshers, src)
		inst = publicGoodInstance(src)
	default:
		src, err := b.userRefresher()
		if err != nil {
			return inst, err
		}
		inst = publicGoodInstance(src.WithMaxAge(sign.DefaultRootMaxAge))
	}
	b.repoRoot = &inst
	return inst, nil
}

// tufConfig is the configuration of a refreshed public-good root.
func (b *policyBuilder) tufConfig(refresh, timeout, maxAge time.Duration) sign.TUFRootConfig {
	embeddedAt, _ := buildinfo.ReleaseTime()
	return sign.TUFRootConfig{
		MirrorURL: b.in.paths.tufMirror, EmbeddedTime: embeddedAt,
		Refresh: refresh, Timeout: timeout, MaxAge: maxAge,
		Logf: func(format string, args ...any) { b.in.stderr("WARN: "+format+"\n", args...) },
	}
}

// publicGoodFromFile loads a public-good root from a file, under the load
// rules and the public-good rule.
func publicGoodFromFile(path string, now time.Time) (sign.AttestationInstance, error) {
	r, err := sign.LoadAttestationRootFile(path)
	if err != nil {
		return sign.AttestationInstance{}, err
	}
	if err := sign.CheckPublicGoodRoot(r); err != nil {
		return sign.AttestationInstance{}, fmt.Errorf("%s: %w", path, err)
	}
	return publicGoodInstance(sign.FileAttestationRoot{Root: r, ReadAt: now}), nil
}

// publicGoodInstance is the public-good instance over a root source.
func publicGoodInstance(src sign.AttestationRootSource) sign.AttestationInstance {
	return sign.AttestationInstance{Name: sign.PublicGoodInstance, Root: src, TransparencyLog: true, CertificateTransparency: true}
}

// strictestOverlay combines the overlay values of the user's sync.yaml and
// the machine file: deny is stricter than allow, and a file that leaves the
// key unset takes no part.
//
// Spec: §4.7.11 (Machine file, Combination)
func strictestOverlay(values []string) string {
	out := ""
	for _, v := range values {
		switch v {
		case "deny":
			return "deny"
		case "allow":
			out = "allow"
		}
	}
	return out
}

// noticeText is the warning text of a notice: the code and the
// constraint, and no ID, because the notice is about the configuration of
// the process.
func noticeText(constraint string) string {
	return fmt.Sprintf("%s (constraint %s)", attest.CodePolicyUnavailable, constraint)
}

// sameDir reports whether two paths name one directory.
func sameDir(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	return aerr == nil && berr == nil && os.SameFile(ai, bi)
}
