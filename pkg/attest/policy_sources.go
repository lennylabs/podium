package attest

import (
	"fmt"
	"strconv"
	"strings"
)

// SourceClass is who can write a policy source (§4.7.11 Policy sources).
type SourceClass int

// The source classes.
const (
	// UserSource is a source the user of the consumer controls: the user's
	// own sync.yaml, the machine-wide sync.yaml, an SDK client argument,
	// and the environment of podium sync or an SDK process.
	UserSource SourceClass = iota + 1
	// RepositorySource is a source a repository can set: a workspace
	// sync.yaml or sync.local.yaml, and every flag, configuration file, and
	// environment variable of the MCP server.
	RepositorySource
)

// Source is one policy source: its class, a name for messages, the default
// mode it sets (unset when it sets none), and the policy files it names.
type Source struct {
	Class       SourceClass
	Name        string
	DefaultMode Mode
	Files       []*PolicyFile
}

// Policy is the combined consumer trust policy of every source (§4.7.11
// Policy sources). Every source contributes and none replaces another.
type Policy struct {
	files []policyFileRef
	// defaultMode is the strictest default any source sets, unset when none
	// does.
	defaultMode Mode
	// userDefault is the strictest default a user source sets.
	userDefault Mode
}

// policyFileRef is one applying file with the class of the source that
// names it.
type policyFileRef struct {
	file  *PolicyFile
	class SourceClass
}

// NewPolicy combines sources into one policy. A source of no known class, a
// default mode that is not none, warn, or require, and a nil file are
// refused with config.attestation_policy_unavailable.
//
// Spec: §4.7.11 (Policy sources)
func NewPolicy(sources ...Source) (*Policy, error) {
	p := &Policy{}
	for _, s := range sources {
		if s.Class != UserSource && s.Class != RepositorySource {
			return nil, policyUnavailable("policy source %q has no class", s.Name)
		}
		if _, err := ParseMode(string(s.DefaultMode)); err != nil {
			return nil, policyUnavailable("policy source %q: %v", s.Name, err)
		}
		p.defaultMode = p.defaultMode.Stricter(s.DefaultMode)
		if s.Class == UserSource {
			p.userDefault = p.userDefault.Stricter(s.DefaultMode)
		}
		for _, f := range s.Files {
			if f == nil || f.Document == nil {
				return nil, policyUnavailable("policy source %q names a file that did not load", s.Name)
			}
			p.files = append(p.files, policyFileRef{file: f, class: s.Class})
		}
	}
	return p, nil
}

// AboveNone reports whether the policy holds a rule or a default above
// none, which is when a consumer can run the attestation check at all.
//
// Spec: §4.7.11 (Policy sources)
func (p *Policy) AboveNone() bool {
	if p.defaultMode.AboveNone() {
		return true
	}
	for _, ref := range p.files {
		for _, r := range ref.file.Document.Rules {
			if r.Mode.AboveNone() {
				return true
			}
		}
	}
	return false
}

// RuleMatch is the rule that one applying file resolves an ID to.
type RuleMatch struct {
	// File is the policy file, and Index the rule's position in it.
	File  *PolicyFile
	Index int
	// Class is the class of the source that names the file.
	Class SourceClass
	// Mode is the rule's mode for the ID: its own mode or the default mode,
	// raised to the user default mode for a repository-source file.
	Mode Mode
	// userMode is the rule's mode in the user mode of the ID: its own mode
	// or the user default mode. It is set for a user-source file only.
	userMode Mode
}

// Rule returns the matched rule.
func (m RuleMatch) Rule() PolicyRule { return m.File.Document.Rules[m.Index] }

// Resolution is the policy of one requested ID.
type Resolution struct {
	// ID is the requested ID.
	ID string
	// Mode is the mode of the ID: the strictest of the modes of the rules
	// it matched, or the default mode when it matched none.
	Mode Mode
	// UserMode is the mode the user sources alone give the ID.
	UserMode Mode
	// Matches lists the rule each applying file resolves the ID to, in
	// source and file order; a file whose rules all miss is absent.
	Matches []RuleMatch
	// key identifies the set of matched rules.
	key string
}

// Resolve resolves a requested ID under the policy. The ID matched is the
// requested ID and never one a registry served.
//
// Spec: §4.7.11 (Consumer trust policy, Policy sources)
func (p *Policy) Resolve(id string) Resolution {
	res := Resolution{ID: id}
	var keys []string
	var userMatched bool
	for _, ref := range p.files {
		idx := firstMatch(ref.file.Document.Rules, id)
		if idx < 0 {
			keys = append(keys, "-")
			continue
		}
		keys = append(keys, strconv.Itoa(idx))
		own := ref.file.Document.Rules[idx].Mode
		m := RuleMatch{File: ref.file, Index: idx, Class: ref.class}
		if own != "" {
			m.Mode = own
		} else {
			m.Mode = p.defaultMode.orNone()
		}
		if ref.class == RepositorySource {
			m.Mode = m.Mode.Stricter(p.userDefault)
		} else {
			userMatched = true
			m.userMode = own
			if own == "" {
				m.userMode = p.userDefault.orNone()
			}
			res.UserMode = res.UserMode.Stricter(m.userMode)
		}
		res.Mode = res.Mode.Stricter(m.Mode)
		res.Matches = append(res.Matches, m)
	}
	if len(res.Matches) == 0 {
		res.Mode = p.defaultMode.orNone()
		res.key = "default"
	} else {
		res.key = strings.Join(keys, ",")
	}
	if !userMatched {
		res.UserMode = p.userDefault.orNone()
	}
	return res
}

// firstMatch returns the index of the first rule with a pattern that
// matches id, or -1.
func firstMatch(rules []PolicyRule, id string) int {
	for i, r := range rules {
		for _, pat := range r.IDs {
			if MatchPattern(pat, id) {
				return i
			}
		}
	}
	return -1
}

// RuleKey identifies the rule of the ID: the set of rules it matched, one
// in each file. Two IDs resolve to one rule exactly when their keys are
// equal, and the IDs that match no rule share the key of the default. The
// key is meaningful only between resolutions of one Policy.
//
// Spec: §4.7.11 (Policy sources)
func (r Resolution) RuleKey() string { return r.key }

// metRules returns the matched rules whose mode equals mode, which are the
// rules the record meets under require and whose failures a consumer
// reports under warn.
func (r Resolution) metRules(mode Mode) []RuleMatch {
	var out []RuleMatch
	for _, m := range r.Matches {
		if m.Mode == mode {
			out = append(out, m)
		}
	}
	return out
}

// userRuleMet reports whether a matched rule of a user-source file has the
// user mode of the ID and holds a requirement. A user mode above none needs
// one: a requirement of a repository-source file tightens a user's mode and
// never meets it alone.
func (r Resolution) userRuleMet() bool {
	for _, m := range r.Matches {
		if m.Class == UserSource && m.userMode == r.UserMode && len(m.Rule().Requirements) > 0 {
			return true
		}
	}
	return false
}

// String renders the resolution for a log line.
func (r Resolution) String() string {
	return fmt.Sprintf("%s: mode %s, user mode %s, %d matched rule(s)", r.ID, r.Mode, r.UserMode, len(r.Matches))
}
