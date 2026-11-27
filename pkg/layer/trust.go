package layer

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// The §4.7.11 trust modes. The empty mode resolves to the deployment
// default.
const (
	TrustModeNone    = "none"
	TrustModeWarn    = "warn"
	TrustModeRequire = "require"
)

// MaxTrustRequirements is the number of requirements a policy holds in this
// release. Several requirements, optional requirements, and the covering
// marker arrive with a later phase, and a policy that names more is refused
// rather than read as fewer.
const MaxTrustRequirements = 1

// ErrTrustPolicy wraps every refusal of a layer trust policy, so a caller
// tells a policy refusal from another failure with errors.Is.
var ErrTrustPolicy = errors.New("layer trust policy")

// workflowsPath is the path below a repository URL that holds the GitHub
// Actions workflows a derived source binding admits.
const workflowsPath = "/.github/workflows/"

// ValidTrustMode reports whether m is a §4.7.11 trust mode or the empty
// value, which resolves to the default.
//
// Spec: §4.7.11 (Layer trust policy)
func ValidTrustMode(m string) bool {
	switch m {
	case "", TrustModeNone, TrustModeWarn, TrustModeRequire:
		return true
	}
	return false
}

// ResolveTrustMode returns mode, or def for an empty mode, or none when both
// are empty. The tenant floor of a later phase resolves between the two.
//
// Spec: §4.7.11 (Layer trust policy)
func ResolveTrustMode(mode, def string) string {
	switch {
	case mode != "":
		return mode
	case def != "":
		return def
	}
	return TrustModeNone
}

// TrustInstances reports whether a Sigstore instance name is configured.
// A nil value knows public-good alone.
type TrustInstances func(name string) bool

// known reports whether name is configured. The empty name is public-good,
// which every registry configures.
func (k TrustInstances) known(name string) bool {
	if name == "" || name == sign.PublicGoodInstance {
		return true
	}
	return k != nil && k(name)
}

// CheckTrustPolicy applies the §4.7.11 policy rules to the policy cfg holds,
// read against its source type. It returns nil or an error wrapping
// ErrTrustPolicy that names the member and the rule. Callers derive the
// source binding (DeriveSourceBinding) before they check, because the rules
// read the derived members.
//
// Spec: §4.7.11 (Policy rules)
func CheckTrustPolicy(cfg store.LayerConfig, instances TrustInstances) error {
	if !ValidTrustMode(cfg.TrustMode) {
		return trustRefusal("trust_mode %q is not none, warn, or require", cfg.TrustMode)
	}
	if n := len(cfg.TrustRequirements); n > MaxTrustRequirements {
		return trustRefusal("trust_requirements holds %d requirements; several requirements are not available yet, so a policy holds at most %d", n, MaxTrustRequirements)
	}
	if (cfg.TrustMode == TrustModeWarn || cfg.TrustMode == TrustModeRequire) && len(cfg.TrustRequirements) == 0 {
		return trustRefusal("trust_mode %s needs a requirement in trust_requirements", cfg.TrustMode)
	}
	for i, req := range cfg.TrustRequirements {
		if err := checkRequirement(fmt.Sprintf("trust_requirements[%d]", i), req, instances); err != nil {
			return err
		}
	}
	return checkTrustLayerRoot(cfg.SourceType, cfg.TrustLayerRoot)
}

// checkRequirement applies the requirement and signer rules to req.
func checkRequirement(name string, req store.TrustRequirement, instances TrustInstances) error {
	if len(req.Signers) == 0 {
		return trustRefusal("%s names no signer", name)
	}
	for j, s := range req.Signers {
		if err := checkSigner(fmt.Sprintf("%s.signers[%d]", name, j), s, instances); err != nil {
			return err
		}
	}
	return nil
}

// checkSigner applies the signer rules. The issuer, identity, and binding
// rules are the verifier's load rules (sign.AttestationSigner.Validate), so
// the registry refuses at registration what the verifier would refuse at
// ingest.
func checkSigner(name string, s store.TrustSigner, instances TrustInstances) error {
	if err := VerifierSigner(s).Validate(); err != nil {
		return trustRefusal("%s: %s", name, signerRuleText(err))
	}
	// A signer that sets any_source or any_ref beside the member it waives
	// reads as bound while the verifier applies the member, so the policy
	// would say something other than what it does; it is refused as
	// ambiguous.
	if s.AnySource && sourceBound(s) {
		return trustRefusal("%s sets any_source and names a source binding member", name)
	}
	if s.AnyRef && s.SourceRepositoryRef != "" {
		return trustRefusal("%s sets any_ref and names source_repository_ref", name)
	}
	if !instances.known(s.Instance) {
		return trustRefusal("%s names the Sigstore instance %q, which this registry does not configure", name, s.Instance)
	}
	return nil
}

// checkTrustLayerRoot applies the trust_layer_root rules: no value on a git
// layer, which takes its layer subject from its root, and a value in the
// form a root takes after its slashes are trimmed, so the expected layer
// subject is one `podium attest` writes.
//
// Spec: §4.7.11 (Policy rules)
func checkTrustLayerRoot(sourceType, root string) error {
	if root == "" {
		return nil
	}
	if sourceType == "git" {
		return trustRefusal("trust_layer_root applies to a local layer; a git layer takes its layer subject from its root")
	}
	if strings.HasPrefix(root, "/") || strings.HasSuffix(root, "/") {
		return trustRefusal("trust_layer_root %q begins or ends with /", root)
	}
	for _, seg := range strings.Split(root, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return trustRefusal("trust_layer_root %q holds the segment %q", root, seg)
		}
	}
	if err := attest.CheckSubjectNameCharacters(root); err != nil {
		return trustRefusal("trust_layer_root %q holds a character a subject name excludes", root)
	}
	return nil
}

// signerRuleText returns the rule text of a sign.AttestationSigner.Validate
// refusal, which joins ErrAttestationSignerInvalid with the rule, without the
// sentinel's text.
func signerRuleText(err error) string {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range joined.Unwrap() {
			if !errors.Is(e, sign.ErrAttestationSignerInvalid) {
				return e.Error()
			}
		}
	}
	return err.Error()
}

// VerifierSigner converts a stored signer to the verifier's signer.
func VerifierSigner(s store.TrustSigner) sign.AttestationSigner {
	return sign.AttestationSigner{
		Issuer:                  s.Issuer,
		Identity:                s.Identity,
		SourceRepository:        s.SourceRepository,
		SourceRepositoryOwner:   s.SourceRepositoryOwner,
		SourceRepositoryID:      s.SourceRepositoryID,
		SourceRepositoryOwnerID: s.SourceRepositoryOwnerID,
		AnySource:               s.AnySource,
		SourceRepositoryRef:     s.SourceRepositoryRef,
		AnyRef:                  s.AnyRef,
	}
}

// sourceBound reports whether s names a source binding member.
func sourceBound(s store.TrustSigner) bool {
	return s.SourceRepository != "" || s.SourceRepositoryOwner != "" ||
		s.SourceRepositoryID != "" || s.SourceRepositoryOwnerID != ""
}

// DeriveSourceBinding returns reqs with the §4.7.11 derived source binding
// applied to each signer of a git layer whose repository is repo: a signer
// with a URI identity that names no source binding member and no any_source,
// and whose identity begins with the repository URL followed by
// /.github/workflows/, gets that URL as its source_repository, and also the
// identity's ref after its last @ when it names no ref binding. reqs is not
// modified. Any other source type, and a repository URL outside the derivable
// form, derives nothing.
//
// Spec: §4.7.11 (Derived source binding)
func DeriveSourceBinding(sourceType, repo string, reqs []store.TrustRequirement) []store.TrustRequirement {
	if len(reqs) == 0 {
		return reqs
	}
	out := make([]store.TrustRequirement, len(reqs))
	for i, r := range reqs {
		out[i] = store.TrustRequirement{PredicateType: r.PredicateType, Signers: slices.Clone(r.Signers)}
	}
	base, ok := derivableRepository(sourceType, repo)
	if !ok {
		return out
	}
	for i := range out {
		for j := range out[i].Signers {
			deriveSigner(&out[i].Signers[j], base)
		}
	}
	return out
}

// deriveSigner applies the derivation to one signer against the repository
// URL base.
func deriveSigner(s *store.TrustSigner, base string) {
	if sourceBound(*s) || s.AnySource || !strings.HasPrefix(s.Identity, base+workflowsPath) {
		return
	}
	s.SourceRepository = base
	if s.SourceRepositoryRef != "" || s.AnyRef {
		return
	}
	if at := strings.LastIndex(s.Identity, "@"); at >= 0 && at+1 < len(s.Identity) {
		s.SourceRepositoryRef = s.Identity[at+1:]
	}
}

// derivableRepository returns the source_repository value a git layer's
// repository URL derives: https://<host>/<owner>/<repo> without a trailing
// .git or /. A URL with user information, a port, a query, or a fragment, a
// scheme other than https, an SSH or scp-like address, and a path of another
// depth derive nothing. The host keeps the letter case it was registered
// with, because the certificate's value is compared byte for byte and a
// changed case fails closed.
//
// Spec: §4.7.11 (Derived source binding)
func derivableRepository(sourceType, repo string) (string, bool) {
	if sourceType != "git" || !strings.HasPrefix(repo, "https://") {
		return "", false
	}
	u, err := url.Parse(repo)
	if err != nil || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(repo, "#") || u.Opaque != "" || u.Host == "" || u.RawPath != "" {
		return "", false
	}
	path := u.Path
	if trimmed, ok := strings.CutSuffix(path, ".git"); ok {
		path = trimmed
	} else {
		path = strings.TrimSuffix(path, "/")
	}
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if !strings.HasPrefix(path, "/") || len(segs) != 2 || segs[0] == "" || segs[1] == "" {
		return "", false
	}
	return "https://" + u.Host + path, true
}

// TrustPolicyEqual reports whether a and b hold the same trust policy. A nil
// and an empty requirement list are equal, because both store no
// requirement.
//
// Spec: §7.3.1 (The layer lifecycle event follows what the write changed)
func TrustPolicyEqual(a, b store.LayerConfig) bool {
	if a.TrustMode != b.TrustMode || a.TrustLayerRoot != b.TrustLayerRoot ||
		len(a.TrustRequirements) != len(b.TrustRequirements) {
		return false
	}
	for i := range a.TrustRequirements {
		ra, rb := a.TrustRequirements[i], b.TrustRequirements[i]
		if ra.PredicateType != rb.PredicateType || !slices.Equal(ra.Signers, rb.Signers) {
			return false
		}
	}
	return true
}

// trustRefusal builds an error wrapping ErrTrustPolicy.
func trustRefusal(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrTrustPolicy, fmt.Sprintf(format, args...))
}
