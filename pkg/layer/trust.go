package layer

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/typeprovider"
)

// The §4.7.11 trust modes. The empty mode resolves to the stricter of the
// tenant floor's mode and the deployment default.
const (
	TrustModeNone    = "none"
	TrustModeWarn    = "warn"
	TrustModeRequire = "require"
)

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

// ResolveTrustMode returns mode, or for an empty mode the stricter of the
// tenant floor's mode floorMode and the trust default def, or none when
// neither sets a mode. The stricter of the two is taken rather than the
// floor's mode first, so a floor of none, which sets no minimum, never
// lowers the default.
//
// Spec: §4.7.11 (Layer trust policy, Tenant trust floor)
func ResolveTrustMode(mode, floorMode, def string) string {
	if mode != "" {
		return mode
	}
	if m := stricterMode(floorMode, def); m != "" {
		return m
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
// read against its source type. defaultMode is the mode an empty trust_mode
// resolves to for the no-requirement rule: the layer endpoints pass
// PODIUM_LAYER_TRUST_DEFAULT, and a declared layer passes "", because the
// default only makes a declared policy invalid per cycle. It returns nil or
// an error wrapping ErrTrustPolicy that names the member and the rule.
// Callers derive the source binding (DeriveSourceBinding) before they check,
// because the rules read the derived members.
//
// Spec: §4.7.11 (Policy rules)
func CheckTrustPolicy(cfg store.LayerConfig, instances TrustInstances, defaultMode string) error {
	if !ValidTrustMode(cfg.TrustMode) {
		return trustRefusal("trust_mode %q is not none, warn, or require", cfg.TrustMode)
	}
	if err := checkCovering(cfg.TrustRequirements); err != nil {
		return err
	}
	if mode := ResolveTrustMode(cfg.TrustMode, "", defaultMode); (mode == TrustModeWarn || mode == TrustModeRequire) && len(cfg.TrustRequirements) == 0 {
		if cfg.TrustMode == "" {
			return trustRefusal("trust_mode is empty and resolves to %s through PODIUM_LAYER_TRUST_DEFAULT, which needs a requirement in trust_requirements; add one or set trust_mode", mode)
		}
		return trustRefusal("trust_mode %s needs a requirement in trust_requirements", mode)
	}
	for i, req := range cfg.TrustRequirements {
		if err := checkRequirement(fmt.Sprintf("trust_requirements[%d]", i), req, instances); err != nil {
			return err
		}
	}
	if err := checkRequireTypes(cfg); err != nil {
		return err
	}
	return checkTrustLayerRoot(cfg.SourceType, cfg.TrustLayerRoot)
}

// CoveringRequirement returns the position of the covering requirement of
// reqs, whose signers attest the layer subject and every artifact: the only
// requirement of a policy that holds one, and the first that sets
// covers_layer in a policy that holds several. It returns -1 for no
// requirement and for several with none marked, which checkCovering
// refuses, so a caller fails closed on a policy no rule admitted.
//
// Spec: §4.7.11 (Layer trust policy)
func CoveringRequirement(reqs []store.TrustRequirement) int {
	if len(reqs) == 1 {
		return 0
	}
	for i, r := range reqs {
		if r.CoversLayer {
			return i
		}
	}
	return -1
}

// checkCovering applies the rules of the covering requirement: exactly one
// requirement of a policy with several sets covers_layer, and the covering
// requirement is not optional, because the layer step reads it for the
// layer subject and the completeness condition.
//
// Spec: §4.7.11 (Policy rules)
func checkCovering(reqs []store.TrustRequirement) error {
	marked := 0
	for _, r := range reqs {
		if r.CoversLayer {
			marked++
		}
	}
	if len(reqs) > 1 && marked != 1 {
		return trustRefusal("trust_requirements holds %d requirements and %d of them set covers_layer; a policy with several requirements marks exactly one covering requirement", len(reqs), marked)
	}
	if c := CoveringRequirement(reqs); c >= 0 && reqs[c].Optional {
		return trustRefusal("trust_requirements[%d] is the covering requirement and sets optional; the covering requirement cannot be optional", c)
	}
	return nil
}

// checkRequireTypes applies the trust_require_types rules: each entry names
// an artifact type the registry knows, no entry is listed twice, and a
// policy that lists a type has a requirement and a trust_mode other than
// none. A type the registry does not know, such as a misspelled hooks, is
// refused rather than stored as a list that enforces nothing.
//
// Spec: §4.7.11 (Policy rules)
func checkRequireTypes(cfg store.LayerConfig) error {
	if len(cfg.TrustRequireTypes) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for i, t := range cfg.TrustRequireTypes {
		if !KnownArtifactType(t) {
			return trustRefusal("trust_require_types[%d] %q is not an artifact type this registry knows", i, t)
		}
		if seen[t] {
			return trustRefusal("trust_require_types lists %q twice", t)
		}
		seen[t] = true
	}
	if len(cfg.TrustRequirements) == 0 {
		return trustRefusal("trust_require_types needs a requirement in trust_requirements")
	}
	if cfg.TrustMode == TrustModeNone {
		return trustRefusal("trust_require_types applies to a layer whose trust_mode is warn or require, and trust_mode is none")
	}
	return nil
}

// KnownArtifactType reports whether t is an artifact type the registry
// knows: a §4.1 first-class or built-in extension type, or a type that a
// registered type provider supplies.
//
// Spec: §4.7.11 (Policy rules), §4.1
func KnownArtifactType(t string) bool {
	if t == "" {
		return false
	}
	_, ok := typeprovider.Default.Get(manifest.ArtifactType(t))
	return ok
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
	if IsKeySigner(s) {
		// A key-based signer verifies under its key with no trusted root,
		// so an instance beside the key would state a binding nothing
		// applies.
		if s.Instance != "" {
			return trustRefusal("%s names public_key and the Sigstore instance %q; a key-based signer names no instance", name, s.Instance)
		}
		return nil
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

// IsKeySigner reports whether s is a §4.7.11 key-based signer, which names
// a public_key in place of an issuer and an identity.
//
// Spec: §4.7.11 (Key-based signers)
func IsKeySigner(s store.TrustSigner) bool { return s.PublicKey != "" }

// SignerInstance is the Sigstore instance whose trusted root verifies the
// bundles of s: the instance it names, public-good for none, and the empty
// name for a key-based signer, which names no instance.
//
// Spec: §4.7.11 (Sigstore instances, Key-based signers)
func SignerInstance(s store.TrustSigner) string {
	if IsKeySigner(s) {
		return ""
	}
	return instanceName(s.Instance)
}

// VerifierSigner converts a stored signer to the verifier's signer.
func VerifierSigner(s store.TrustSigner) sign.AttestationSigner {
	return sign.AttestationSigner{
		PublicKey:               s.PublicKey,
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
		out[i] = r
		out[i].Signers = slices.Clone(r.Signers)
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
	// A percent-escape, an empty port ("host:"), and a host with a trailing
	// dot each name the repository in a form the host's certificates do not
	// carry, so the derived value would differ from the certificate's by
	// more than the operator can see; they derive nothing.
	if strings.ContainsAny(repo, "%#") {
		return "", false
	}
	u, err := url.Parse(repo)
	if err != nil || u.User != nil || strings.Contains(u.Host, ":") || u.RawQuery != "" || u.ForceQuery ||
		u.Opaque != "" || u.Host == "" || strings.HasSuffix(u.Host, ".") {
		return "", false
	}
	if !derivableHost(u.Host) {
		return "", false
	}
	path := u.Path
	if trimmed, ok := strings.CutSuffix(path, ".git"); ok {
		path = trimmed
	} else {
		path = strings.TrimSuffix(path, "/")
	}
	if strings.HasSuffix(strings.ToLower(path), ".git") {
		// .git followed by /, and a .git suffix in another letter case, are
		// neither form; the rule stays a byte comparison.
		return "", false
	}
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if !strings.HasPrefix(path, "/") || len(segs) != 2 || !derivableSegment(segs[0]) || !derivableSegment(segs[1]) {
		return "", false
	}
	return "https://" + u.Host + path, true
}

// derivableHost reports whether host is a DNS name of the derivable form:
// dot-separated labels of letters, digits, and inner hyphens. An empty
// label, a label that begins or ends with a hyphen, and any other character
// derive nothing.
func derivableHost(host string) bool {
	for label := range strings.SplitSeq(host, ".") {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// derivableSegment reports whether seg is an owner or repository segment of
// the derivable form. The dot segments . and .. name a path a client
// resolves rather than a repository, so they derive nothing.
func derivableSegment(seg string) bool {
	return seg != "." && seg != ".." && segmentChars.MatchString(seg)
}

// hostLabel and segmentChars are the characters a derivable repository URL
// holds in a host label and in its owner and repository segments. A space or
// any other character derives nothing.
var (
	hostLabel    = regexp.MustCompile(`\A[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?\z`)
	segmentChars = regexp.MustCompile(`\A[A-Za-z0-9._-]+\z`)
)

// TrustPolicyEqual reports whether a and b hold the same trust policy. A nil
// and an empty requirement list are equal, because both store no
// requirement, and so are a nil and an empty type list.
//
// Spec: §7.3.1 (The layer lifecycle event follows what the write changed)
func TrustPolicyEqual(a, b store.LayerConfig) bool {
	if a.TrustMode != b.TrustMode || a.TrustLayerRoot != b.TrustLayerRoot ||
		len(a.TrustRequirements) != len(b.TrustRequirements) || !slices.Equal(a.TrustRequireTypes, b.TrustRequireTypes) {
		return false
	}
	for i := range a.TrustRequirements {
		ra, rb := a.TrustRequirements[i], b.TrustRequirements[i]
		if ra.PredicateType != rb.PredicateType || ra.CoversLayer != rb.CoversLayer || ra.Optional != rb.Optional ||
			!slices.EqualFunc(ra.Signers, rb.Signers, signerEqual) {
			return false
		}
	}
	return true
}

// signerEqual compares two signers with an empty instance read as
// public-good, which it names; the stored value is kept as given.
func signerEqual(a, b store.TrustSigner) bool {
	a.Instance, b.Instance = instanceName(a.Instance), instanceName(b.Instance)
	return a == b
}

// instanceName returns the instance a signer names, public-good for empty.
func instanceName(n string) string {
	if n == "" {
		return sign.PublicGoodInstance
	}
	return n
}

// TrustMemberNames are the layer-level trust policy members of a layer
// object, a layer request body, and a registry.yaml layer entry.
var TrustMemberNames = []string{"trust_mode", "trust_requirements", "trust_require_types", "trust_layer_root"}

// IsTrustLikeName reports whether name, with its letters folded to lower
// case and each - read as _, begins with "trust". A member of that form that
// is not a trust member is refused rather than ignored, so a misspelled
// policy member such as trust_mod or trust-mode is never dropped silently.
//
// Spec: §7.3.1 (Layer trust policy)
func IsTrustLikeName(name string) bool {
	return strings.HasPrefix(strings.ReplaceAll(strings.ToLower(name), "-", "_"), "trust")
}

// trustRefusal builds an error wrapping ErrTrustPolicy.
func trustRefusal(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrTrustPolicy, fmt.Sprintf(format, args...))
}
