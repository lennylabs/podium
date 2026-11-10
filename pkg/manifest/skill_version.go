package manifest

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// autoVersionRegex matches the registry-reserved version form 0.0.0-auto.<N>,
// where N is a non-negative decimal integer with no leading zeros.
var autoVersionRegex = regexp.MustCompile(`^0\.0\.0-auto\.(0|[1-9]\d*)$`)

// synthesizedSkillPrefix and synthesizedSkillSuffix frame the version in the
// synthesized ARTIFACT.md of a skill that has none. The bytes are fixed by
// §4.3.4, so every consumer that compares or produces the document builds it
// from these two constants.
const (
	synthesizedSkillPrefix = "---\ntype: skill\nversion: "
	synthesizedSkillSuffix = "\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"
)

// AutoVersion returns the registry-assigned version 0.0.0-auto.<n>. The
// caller passes a non-negative n.
//
// Spec: §4.7.6
func AutoVersion(n int) string {
	return fmt.Sprintf("0.0.0-auto.%d", n)
}

// IsAutoVersion reports whether s is in the registry-reserved form
// 0.0.0-auto.<N> and returns N. An N that overflows int returns false, so a
// caller never receives a truncated or wrapped value.
//
// Spec: §4.7.6
func IsAutoVersion(s string) (n int, ok bool) {
	m := autoVersionRegex.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// ResolveSkillVersion returns a skill's authored version: the ARTIFACT.md
// version when a is non-nil and sets one, then the SKILL.md metadata.version
// when s is non-nil and the value is a major.minor.patch version, and
// otherwise "". A metadata.version in any other form, the reserved
// 0.0.0-auto.<N> form included, is ignored because metadata is free-form in
// the Agent Skills standard. Callers treat "" as a version the registry
// assigns.
//
// Spec: §4.3.4
func ResolveSkillVersion(a *Artifact, s *Skill) string {
	if a != nil && a.Version != "" {
		return a.Version
	}
	if s == nil {
		return ""
	}
	if v := s.Metadata["version"]; ValidateVersion(v) == nil {
		return v
	}
	return ""
}

// SynthesizedSkillDocument returns the ARTIFACT.md bytes the registry serves
// for a skill with no ARTIFACT.md at version v. It refuses a v that is
// neither a major.minor.patch version nor the reserved 0.0.0-auto.<N> form
// with an error wrapping ErrInvalidVersion, so no value can inject a YAML key
// or a line into the document.
//
// Spec: §4.3.4
func SynthesizedSkillDocument(v string) ([]byte, error) {
	if !synthesizableVersion(v) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidVersion, v)
	}
	return []byte(synthesizedSkillPrefix + v + synthesizedSkillSuffix), nil
}

// IsSynthesizedSkillDocument reports whether doc is exactly the synthesized
// ARTIFACT.md for the version it names. It compares bytes and does no YAML
// parse, so an authored document that differs in any byte, such as a
// reordered key or a missing comment, reports false.
//
// Spec: §4.3.4
func IsSynthesizedSkillDocument(doc []byte) bool {
	rest, ok := strings.CutPrefix(string(doc), synthesizedSkillPrefix)
	if !ok {
		return false
	}
	v, ok := strings.CutSuffix(rest, synthesizedSkillSuffix)
	if !ok {
		return false
	}
	want, err := SynthesizedSkillDocument(v)
	return err == nil && bytes.Equal(want, doc)
}

// synthesizableVersion reports whether v is an authored major.minor.patch
// version or a registry-assigned 0.0.0-auto.<N> version.
func synthesizableVersion(v string) bool {
	if ValidateVersion(v) == nil {
		return true
	}
	_, ok := IsAutoVersion(v)
	return ok
}
