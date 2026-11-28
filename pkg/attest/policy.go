package attest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/sign"
)

// Mode is an attestation mode of the consumer trust policy (§4.7.11): none,
// warn, or require. The empty Mode is unset: a rule that omits mode, or a
// source that sets no default.
type Mode string

// The attestation modes, from the least strict.
const (
	ModeNone    Mode = "none"
	ModeWarn    Mode = "warn"
	ModeRequire Mode = "require"
)

// ParseMode reads a mode value. The empty string is unset; any value other
// than none, warn, and require is an error.
//
// Spec: §4.7.11 (Consumer trust policy)
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case "", ModeNone, ModeWarn, ModeRequire:
		return m, nil
	}
	return "", fmt.Errorf("attestation mode %q is not none, warn, or require", s)
}

// rank orders the modes from the least strict; an unset mode ranks below
// none.
func (m Mode) rank() int {
	switch m {
	case ModeNone:
		return 1
	case ModeWarn:
		return 2
	case ModeRequire:
		return 3
	}
	return 0
}

// AboveNone reports whether m is warn or require.
func (m Mode) AboveNone() bool { return m.rank() > ModeNone.rank() }

// Stricter returns the stricter of m and o; an unset mode yields the other.
func (m Mode) Stricter(o Mode) Mode {
	if o.rank() > m.rank() {
		return o
	}
	return m
}

// orNone returns m, or none when m is unset.
func (m Mode) orNone() Mode {
	if m == "" {
		return ModeNone
	}
	return m
}

// PolicyDocument is a parsed consumer policy file (§4.7.11): its instances
// and its rules in file order.
type PolicyDocument struct {
	Instances []PolicyInstance
	Rules     []PolicyRule
}

// PolicyInstance is an instance object of a policy file: a Sigstore
// instance whose trusted root is a file. TrustedRootFile is as written; a
// relative path resolves against the directory of the policy file.
type PolicyInstance struct {
	Name                    string
	TrustedRootFile         string
	TransparencyLog         bool
	CertificateTransparency bool
}

// PolicyRule is a rule object of a policy file. Mode is unset when the
// rule omits it, and the rule then takes the default mode of its sources.
type PolicyRule struct {
	IDs          []string
	Mode         Mode
	Requirements []PolicyRequirement
}

// PolicyRequirement is a requirement of a rule: the signers whose accepted
// bundles meet it and the predicate type their statements carry, when it
// names one. A consumer does not read covers_layer or optional.
type PolicyRequirement struct {
	Signers       []PolicySigner
	PredicateType string
}

// PolicySigner is a signer of a requirement with the instance it names. A
// certificate signer's Instance is public-good when the file names none,
// and a key-based signer's Instance is empty.
type PolicySigner struct {
	Signer   sign.AttestationSigner
	Instance string
}

// policyFileVersion is the one version of the policy file format.
const policyFileVersion = "1"

// Log requirement values of an instance object.
const (
	logRequired = "required"
	logNone     = "none"
)

// policyUnavailable builds the config.attestation_policy_unavailable
// Failure of a policy file that does not load.
func policyUnavailable(format string, args ...any) *Failure {
	return fail(CodePolicyUnavailable, "", fmt.Sprintf(format, args...), nil)
}

// ParsePolicyDocument parses and validates a consumer policy file (§4.7.11)
// with no file access: the text is valid UTF-8 with no byte order mark, the
// top-level object holds version 1, an optional instances array, and a
// rules array, every object holds only its own members and none twice,
// with names compared byte for byte, each rule has patterns of the
// §4.7.11 form and a mode of none, warn, or require, a rule whose mode is
// warn or require holds a requirement, each requirement holds a signer, and
// each signer meets sign.AttestationSigner.Validate and names an instance
// of the file or public-good. A refusal is a
// config.attestation_policy_unavailable Failure.
//
// Spec: §4.7.11 (Consumer trust policy)
func ParsePolicyDocument(raw []byte) (*PolicyDocument, error) {
	if !utf8.Valid(raw) {
		return nil, policyUnavailable("the policy file is not valid UTF-8")
	}
	if bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		return nil, policyUnavailable("the policy file begins with a byte order mark")
	}
	top, err := strictObject(raw, "the policy file", "version", "instances", "rules")
	if err != nil {
		return nil, err
	}
	if v, ok := top["version"]; !ok || string(bytes.TrimSpace(v)) != policyFileVersion {
		return nil, policyUnavailable("the policy file's version is not 1")
	}
	doc := &PolicyDocument{}
	names := map[string]bool{}
	if raw, ok := top["instances"]; ok {
		elems, err := strictArray(raw, "instances")
		if err != nil {
			return nil, err
		}
		for i, e := range elems {
			inst, err := parseInstance(e, fmt.Sprintf("instances[%d]", i))
			if err != nil {
				return nil, err
			}
			if names[inst.Name] {
				return nil, policyUnavailable("instances[%d]: instance %q is defined twice", i, inst.Name)
			}
			names[inst.Name] = true
			doc.Instances = append(doc.Instances, inst)
		}
	}
	rawRules, ok := top["rules"]
	if !ok {
		return nil, policyUnavailable("the policy file has no rules member")
	}
	rules, err := strictArray(rawRules, "rules")
	if err != nil {
		return nil, err
	}
	for i, r := range rules {
		rule, err := parseRule(r, fmt.Sprintf("rules[%d]", i), names)
		if err != nil {
			return nil, err
		}
		doc.Rules = append(doc.Rules, rule)
	}
	return doc, nil
}

// parseInstance reads one instance object.
func parseInstance(raw json.RawMessage, where string) (PolicyInstance, error) {
	m, err := strictObject(raw, where, "name", "trusted_root_file", "transparency_log", "certificate_transparency")
	if err != nil {
		return PolicyInstance{}, err
	}
	var inst PolicyInstance
	if inst.Name, err = requiredString(m, "name", where); err != nil {
		return PolicyInstance{}, err
	}
	switch {
	case inst.Name == sign.PublicGoodInstance:
		return PolicyInstance{}, policyUnavailable("%s: the public-good instance is built in, and no file defines it", where)
	case !sign.ValidInstanceName(inst.Name):
		return PolicyInstance{}, policyUnavailable("%s: instance name %q breaks the §13.12 instance name rule", where, inst.Name)
	}
	if inst.TrustedRootFile, err = requiredString(m, "trusted_root_file", where); err != nil {
		return PolicyInstance{}, err
	}
	tlog, tlogSet, err := logValue(m, "transparency_log", where)
	if err != nil {
		return PolicyInstance{}, err
	}
	ct, _, err := logValue(m, "certificate_transparency", where)
	if err != nil {
		return PolicyInstance{}, err
	}
	if inst.Name == sign.GitHubInstance {
		if tlog || ct {
			return PolicyInstance{}, policyUnavailable("%s: the github instance has no transparency log and no certificate transparency logs", where)
		}
		return inst, nil
	}
	if !tlogSet {
		return PolicyInstance{}, policyUnavailable("%s: instance %q states no transparency_log", where, inst.Name)
	}
	inst.TransparencyLog, inst.CertificateTransparency = tlog, ct
	return inst, nil
}

// logValue reads a log member, required or none, and reports whether it is
// present.
func logValue(m map[string]json.RawMessage, name, where string) (required, present bool, err error) {
	raw, ok := m[name]
	if !ok {
		return false, false, nil
	}
	s, err := strictString(raw, where+"."+name)
	if err != nil {
		return false, true, err
	}
	switch s {
	case logRequired:
		return true, true, nil
	case logNone:
		return false, true, nil
	}
	return false, true, policyUnavailable("%s.%s is %q, not required or none", where, name, s)
}

// parseRule reads one rule object. instances holds the names of the file's
// instances.
func parseRule(raw json.RawMessage, where string, instances map[string]bool) (PolicyRule, error) {
	m, err := strictObject(raw, where, "ids", "mode", "requirements")
	if err != nil {
		return PolicyRule{}, err
	}
	var rule PolicyRule
	rawIDs, ok := m["ids"]
	if !ok {
		return PolicyRule{}, policyUnavailable("%s has no ids", where)
	}
	ids, err := strictArray(rawIDs, where+".ids")
	if err != nil {
		return PolicyRule{}, err
	}
	if len(ids) == 0 {
		return PolicyRule{}, policyUnavailable("%s.ids is empty", where)
	}
	for i, r := range ids {
		p, err := strictString(r, fmt.Sprintf("%s.ids[%d]", where, i))
		if err != nil {
			return PolicyRule{}, err
		}
		if err := checkPattern(p); err != nil {
			return PolicyRule{}, policyUnavailable("%s.ids[%d]: %v", where, i, err)
		}
		rule.IDs = append(rule.IDs, p)
	}
	if raw, ok := m["mode"]; ok {
		s, err := strictString(raw, where+".mode")
		if err != nil {
			return PolicyRule{}, err
		}
		if rule.Mode, err = ParseMode(s); err != nil || rule.Mode == "" {
			return PolicyRule{}, policyUnavailable("%s.mode %q is not none, warn, or require", where, s)
		}
	}
	if raw, ok := m["requirements"]; ok {
		reqs, err := strictArray(raw, where+".requirements")
		if err != nil {
			return PolicyRule{}, err
		}
		for i, r := range reqs {
			req, err := parseRequirement(r, fmt.Sprintf("%s.requirements[%d]", where, i), instances)
			if err != nil {
				return PolicyRule{}, err
			}
			rule.Requirements = append(rule.Requirements, req)
		}
	}
	if rule.Mode.AboveNone() && len(rule.Requirements) == 0 {
		return PolicyRule{}, policyUnavailable("%s has mode %s and no requirement", where, rule.Mode)
	}
	return rule, nil
}

// parseRequirement reads one requirement object. covers_layer and optional
// are admitted, so a layer's requirements file can be copied in, and not
// read.
func parseRequirement(raw json.RawMessage, where string, instances map[string]bool) (PolicyRequirement, error) {
	m, err := strictObject(raw, where, "signers", "predicate_type", "covers_layer", "optional")
	if err != nil {
		return PolicyRequirement{}, err
	}
	var req PolicyRequirement
	for _, name := range []string{"covers_layer", "optional"} {
		if raw, ok := m[name]; ok {
			if err := strictBool(raw, where+"."+name, new(bool)); err != nil {
				return PolicyRequirement{}, err
			}
		}
	}
	if raw, ok := m["predicate_type"]; ok {
		if req.PredicateType, err = strictString(raw, where+".predicate_type"); err != nil {
			return PolicyRequirement{}, err
		}
	}
	rawSigners, ok := m["signers"]
	if !ok {
		return PolicyRequirement{}, policyUnavailable("%s has no signers", where)
	}
	signers, err := strictArray(rawSigners, where+".signers")
	if err != nil {
		return PolicyRequirement{}, err
	}
	if len(signers) == 0 {
		return PolicyRequirement{}, policyUnavailable("%s has no signer", where)
	}
	for i, r := range signers {
		s, err := parseSigner(r, fmt.Sprintf("%s.signers[%d]", where, i), instances)
		if err != nil {
			return PolicyRequirement{}, err
		}
		req.Signers = append(req.Signers, s)
	}
	return req, nil
}

// signerStrings lists the string members of a signer object with the field
// each fills.
func signerStrings(s *sign.AttestationSigner, instance *string) map[string]*string {
	return map[string]*string{
		"issuer":                     &s.Issuer,
		"identity":                   &s.Identity,
		"source_repository":          &s.SourceRepository,
		"source_repository_owner":    &s.SourceRepositoryOwner,
		"source_repository_id":       &s.SourceRepositoryID,
		"source_repository_owner_id": &s.SourceRepositoryOwnerID,
		"source_repository_ref":      &s.SourceRepositoryRef,
		"public_key":                 &s.PublicKey,
		"instance":                   instance,
	}
}

// parseSigner reads one signer object and applies the signer rules of
// §4.7.11 Policy rules, other than the derivation, which a consumer does
// not apply.
func parseSigner(raw json.RawMessage, where string, instances map[string]bool) (PolicySigner, error) {
	var ps PolicySigner
	strs := signerStrings(&ps.Signer, &ps.Instance)
	allowed := []string{"any_source", "any_ref"}
	for name := range strs {
		allowed = append(allowed, name)
	}
	m, err := strictObject(raw, where, allowed...)
	if err != nil {
		return PolicySigner{}, err
	}
	for name, dst := range strs {
		if raw, ok := m[name]; ok {
			if *dst, err = strictString(raw, where+"."+name); err != nil {
				return PolicySigner{}, err
			}
		}
	}
	for name, dst := range map[string]*bool{"any_source": &ps.Signer.AnySource, "any_ref": &ps.Signer.AnyRef} {
		if raw, ok := m[name]; ok {
			if err := strictBool(raw, where+"."+name, dst); err != nil {
				return PolicySigner{}, err
			}
		}
	}
	if err := checkSigner(ps, instances); err != nil {
		return PolicySigner{}, policyUnavailable("%s: %v", where, err)
	}
	if ps.Signer.PublicKey == "" && ps.Instance == "" {
		ps.Instance = sign.PublicGoodInstance
	}
	return ps, nil
}

// checkSigner applies the signer rules: sign's load rules, a signer that
// sets any_source or any_ref names no member it would replace, a key-based
// signer names no instance, and a certificate signer's instance is one of
// the file or public-good.
func checkSigner(ps PolicySigner, instances map[string]bool) error {
	s := ps.Signer
	if err := s.Validate(); err != nil {
		return err
	}
	bound := s.SourceRepository != "" || s.SourceRepositoryOwner != "" || s.SourceRepositoryID != "" || s.SourceRepositoryOwnerID != ""
	switch {
	case s.AnySource && bound:
		return fmt.Errorf("a signer that sets any_source names no source binding member")
	case s.AnyRef && s.SourceRepositoryRef != "":
		return fmt.Errorf("a signer that sets any_ref names no source_repository_ref")
	case s.PublicKey != "" && ps.Instance != "":
		return fmt.Errorf("a key-based signer names no instance")
	case ps.Instance != "" && ps.Instance != sign.PublicGoodInstance && !instances[ps.Instance]:
		return fmt.Errorf("instance %q is neither an instance of the file nor public-good", ps.Instance)
	}
	return nil
}

// checkPattern applies the §4.7.11 pattern rule: a non-empty sequence of
// non-empty segments separated by /, where a segment that holds * is * or
// ** alone.
func checkPattern(p string) error {
	if p == "" {
		return fmt.Errorf("the pattern is empty")
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "":
			return fmt.Errorf("pattern %q holds an empty segment", p)
		case seg == "*" || seg == "**":
		case strings.Contains(seg, "*"):
			return fmt.Errorf("pattern %q holds the segment %q; * stands alone in a segment as * or **", p, seg)
		default:
			// A literal segment that no well-formed requested ID holds
			// would match nothing, which reads as a rule that protects an
			// ID and never applies.
			if why := segmentViolation(seg, true); why != "" {
				return fmt.Errorf("pattern %q: the segment %q cannot occur in a well-formed requested ID, because such an ID %s, so the pattern matches no ID", p, seg, why)
			}
		}
	}
	return nil
}

// MatchPattern reports whether the §4.7.11 ID pattern p matches id: the
// segment * matches one ID segment, ** matches zero or more ID segments,
// and every other segment matches the ID segment that equals it byte for
// byte. It runs in time proportional to the product of the segment counts,
// because a pattern can come from a file a repository commits.
//
// Spec: §4.7.11 (Consumer trust policy)
func MatchPattern(p, id string) bool {
	return matchSegments(collapseStars(strings.Split(p, "/")), strings.Split(id, "/"))
}

// collapseStars replaces each run of ** segments with one, which matches
// the same IDs.
func collapseStars(p []string) []string {
	out := make([]string, 0, len(p))
	for _, seg := range p {
		if seg == "**" && len(out) > 0 && out[len(out)-1] == "**" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// matchSegments matches pattern segments against ID segments with a table:
// m[i][j] reports whether p[i:] matches id[j:].
func matchSegments(p, id []string) bool {
	m := make([][]bool, len(p)+1)
	for i := range m {
		m[i] = make([]bool, len(id)+1)
	}
	m[len(p)][len(id)] = true
	for i := len(p) - 1; i >= 0; i-- {
		for j := len(id); j >= 0; j-- {
			switch {
			case p[i] == "**":
				m[i][j] = m[i+1][j] || (j < len(id) && m[i][j+1])
			case j == len(id):
				m[i][j] = false
			case p[i] == "*" || p[i] == id[j]:
				m[i][j] = m[i+1][j+1]
			}
		}
	}
	return m[0][0]
}

// strictObject decodes raw as one JSON object and returns its members by
// exact name. A member twice, a member whose name is not in allowed, and a
// value of another JSON type are refused.
func strictObject(raw []byte, where string, allowed ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, policyUnavailable("%s is not a JSON object", where)
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, policyUnavailable("%s does not parse: %v", where, err)
		}
		name, _ := tok.(string)
		if _, dup := out[name]; dup {
			return nil, policyUnavailable("%s holds the member %q twice", where, name)
		}
		if !contains(allowed, name) {
			return nil, policyUnavailable("%s holds the member %q, which it does not admit", where, name)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, policyUnavailable("%s does not parse: %v", where, err)
		}
		out[name] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, policyUnavailable("%s does not parse: %v", where, err)
	}
	if _, err := dec.Token(); err == nil {
		return nil, policyUnavailable("%s holds text after the object", where)
	}
	return out, nil
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// strictArray decodes raw as one JSON array.
func strictArray(raw json.RawMessage, where string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '[' || json.Unmarshal(t, &out) != nil {
		return nil, policyUnavailable("%s is not a JSON array", where)
	}
	return out, nil
}

// strictString decodes raw as one JSON string.
func strictString(raw json.RawMessage, where string) (string, error) {
	var s string
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '"' || json.Unmarshal(t, &s) != nil {
		return "", policyUnavailable("%s is not a JSON string", where)
	}
	return s, nil
}

// strictBool decodes raw as one JSON boolean into dst.
func strictBool(raw json.RawMessage, where string, dst *bool) error {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		*dst = true
	case "false":
		*dst = false
	default:
		return policyUnavailable("%s is not a JSON boolean", where)
	}
	return nil
}

// requiredString reads a member that must be a non-empty string.
func requiredString(m map[string]json.RawMessage, name, where string) (string, error) {
	raw, ok := m[name]
	if !ok {
		return "", policyUnavailable("%s has no %s", where, name)
	}
	s, err := strictString(raw, where+"."+name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s) == "" {
		return "", policyUnavailable("%s.%s is empty", where, name)
	}
	return s, nil
}

// PolicyFile is a loaded policy file: its path, its document, and the
// instances its signers verify under, which are the instances it lists and
// the public-good instance the caller gave it.
type PolicyFile struct {
	// Path is the file as a source named it.
	Path string
	// Document is the parsed file.
	Document *PolicyDocument
	// instances maps an instance name to the instance, public-good
	// included when the caller gave one.
	instances map[string]sign.AttestationInstance
}

// LoadPolicyFile reads, parses, and validates the policy file at path, and
// loads the trusted root of each instance it lists, under the §4.7.11
// trusted root file rules. A relative trusted_root_file resolves against
// the directory of path. publicGood is the public-good instance the rules
// of this file verify under, which the caller chooses by the source that
// names the file: a user source's root for a user-source file, and the
// consumer's own public-good settings for a repository-source file. A
// signer that names public-good while publicGood has no root is refused.
// now is the read time of each root file. Every refusal is a
// config.attestation_policy_unavailable Failure naming the file.
//
// Spec: §4.7.11 (Consumer trust policy, Trusted roots of a consumer, Policy load)
func LoadPolicyFile(path string, publicGood sign.AttestationInstance, now time.Time) (*PolicyFile, error) {
	return LoadPolicyFileWith(path, func() (sign.AttestationInstance, error) { return publicGood, nil }, now)
}

// LoadPolicyFileWith is LoadPolicyFile with the public-good instance built
// by publicGood, which it calls only when a signer of the file names that
// instance. A consumer builds a refreshed public-good root that way only
// for a policy that verifies under it, so a policy of file instances and
// keys alone makes no TUF request. An error of publicGood refuses the file.
//
// Spec: §4.7.11 (Consumer trust policy, Trusted roots of a consumer, Policy load)
func LoadPolicyFileWith(path string, publicGood func() (sign.AttestationInstance, error), now time.Time) (*PolicyFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, wrapPolicyFile(path, policyUnavailable("cannot read the file: %v", err))
	}
	doc, err := ParsePolicyDocument(raw)
	if err != nil {
		return nil, wrapPolicyFile(path, err)
	}
	var pg sign.AttestationInstance
	if doc.NamesPublicGood() {
		if pg, err = publicGood(); err != nil {
			return nil, wrapPolicyFile(path, policyUnavailable("the public-good root: %v", err))
		}
	}
	f, err := NewPolicyFile(path, doc, pg, func(file string) (*sign.AttestationRoot, error) {
		if !filepath.IsAbs(file) {
			file = filepath.Join(filepath.Dir(path), file)
		}
		return sign.LoadAttestationRootFile(file)
	}, now)
	if err != nil {
		return nil, wrapPolicyFile(path, err)
	}
	return f, nil
}

// NewPolicyFile builds a PolicyFile from a parsed document, loading each
// instance's trusted root through loadRoot, which receives the
// trusted_root_file as written. It checks that a signer naming public-good
// has a public-good root, and that an instance that requires a log of a
// kind holds a root that lists one, because such an instance would accept
// no bundle.
//
// Spec: §4.7.11 (Consumer trust policy, Trusted roots of a consumer)
func NewPolicyFile(path string, doc *PolicyDocument, publicGood sign.AttestationInstance,
	loadRoot func(file string) (*sign.AttestationRoot, error), now time.Time) (*PolicyFile, error) {
	f := &PolicyFile{Path: path, Document: doc, instances: map[string]sign.AttestationInstance{}}
	if publicGood.Root != nil {
		publicGood.Name = sign.PublicGoodInstance
		f.instances[sign.PublicGoodInstance] = publicGood
	}
	for _, pi := range doc.Instances {
		root, err := loadRoot(pi.TrustedRootFile)
		if err != nil {
			return nil, policyUnavailable("instance %q: trusted root %s: %v", pi.Name, pi.TrustedRootFile, err)
		}
		tlog, ct := root.ListsLogs()
		switch {
		case pi.TransparencyLog && !tlog:
			return nil, policyUnavailable("instance %q requires a transparency log, and its trusted root lists none", pi.Name)
		case pi.CertificateTransparency && !ct:
			return nil, policyUnavailable("instance %q requires certificate transparency, and its trusted root lists no log", pi.Name)
		}
		f.instances[pi.Name] = sign.AttestationInstance{
			Name: pi.Name, Root: sign.FileAttestationRoot{Root: root, ReadAt: now},
			TransparencyLog: pi.TransparencyLog, CertificateTransparency: pi.CertificateTransparency,
		}
	}
	for _, r := range doc.Rules {
		for _, req := range r.Requirements {
			for _, s := range req.Signers {
				if s.Instance == sign.PublicGoodInstance && publicGood.Root == nil {
					return nil, policyUnavailable("a signer names the public-good instance, and no public-good root is available")
				}
			}
		}
	}
	return f, nil
}

// NamesPublicGood reports whether a signer of the document names the
// public-good instance.
func (d *PolicyDocument) NamesPublicGood() bool {
	for _, r := range d.Rules {
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

// wrapPolicyFile prefixes a load refusal with the file's path.
func wrapPolicyFile(path string, err error) error {
	f, ok := FailureOf(err)
	if !ok {
		return policyUnavailable("policy file %s: %v", path, err)
	}
	return &Failure{Code: f.Code, Constraint: f.Constraint, detail: "policy file " + path + ": " + f.detail, cause: f.cause}
}

// Instance returns the instance of the file that a signer names.
func (f *PolicyFile) Instance(name string) (sign.AttestationInstance, bool) {
	inst, ok := f.instances[name]
	return inst, ok
}
