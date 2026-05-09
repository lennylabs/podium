package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Common parser errors. Tests assert against these sentinels via errors.Is.
var (
	// ErrNoFrontmatter signals that the input did not begin with a YAML
	// frontmatter block. Per §4.3, every manifest must start with one.
	ErrNoFrontmatter = errors.New("manifest: missing YAML frontmatter")
	// ErrEmptyBodyForNonSkill signals that a non-skill ARTIFACT.md has no
	// prose body. Skills are allowed an empty (or HTML-comment-only) body
	// per §4.3.4; other types must have one.
	ErrEmptyBodyForNonSkill = errors.New("manifest: ARTIFACT.md prose body is empty for non-skill type")
	// ErrInvalidYAML wraps the underlying YAML decode error.
	ErrInvalidYAML = errors.New("manifest: invalid YAML frontmatter")
	// ErrInvalidName signals that the agentskills.io name constraints
	// (§4.3.4) are not satisfied.
	ErrInvalidName = errors.New("manifest: invalid name")
	// ErrInvalidVersion signals a non-semver version field (§4.7.6).
	ErrInvalidVersion = errors.New("manifest: invalid semver version")
	// ErrUnknownType signals a type field not registered with any
	// TypeProvider (first-class or extension) (§4.1).
	ErrUnknownType = errors.New("manifest: unknown artifact type")
)

// frontmatterRegex matches a leading YAML frontmatter block delimited by
// "---" lines, capturing the YAML body and the prose body that follows.
var frontmatterRegex = regexp.MustCompile(`(?ms)\A---\r?\n(.*?)\r?\n---\r?\n?(.*)\z`)

// SplitFrontmatter splits a markdown source into its YAML frontmatter and
// prose body. The first line must be exactly "---" (per §4.3); the closing
// "---" terminates the frontmatter and everything after is the body.
func SplitFrontmatter(src []byte) (frontmatter []byte, body []byte, err error) {
	m := frontmatterRegex.FindSubmatch(src)
	if m == nil {
		return nil, nil, ErrNoFrontmatter
	}
	return m[1], bytes.TrimLeft(m[2], "\r\n"), nil
}

// ParseArtifact decodes an ARTIFACT.md source into an Artifact. The
// frontmatter populates the typed fields; the markdown after the
// frontmatter populates Artifact.Body.
//
// ParseArtifact handles syntactic decoding only. Semantic lint rules (cross
// reference resolution, sensitivity / SBOM enforcement, name / version
// validation against the spec's per-type rules) live in pkg/lint and run on
// top of the parsed value.
func ParseArtifact(src []byte) (*Artifact, error) {
	fm, body, err := SplitFrontmatter(src)
	if err != nil {
		return nil, err
	}
	a := &Artifact{}
	if err := yaml.Unmarshal(fm, a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	a.Body = string(body)
	return a, nil
}

// ParseSkill decodes a SKILL.md source per §4.3.4. The frontmatter populates
// Skill fields; the prose body populates Skill.Body. Name validation and
// the agentskills.io spec checks happen in pkg/lint at ingest time.
func ParseSkill(src []byte) (*Skill, error) {
	fm, body, err := SplitFrontmatter(src)
	if err != nil {
		return nil, err
	}
	s := &Skill{}
	if err := yaml.Unmarshal(fm, s); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	s.Body = string(body)
	return s, nil
}

// ParseDomain decodes a DOMAIN.md source per §4.5.1. Glob validation,
// import resolution, and per-domain discovery overrides happen later.
func ParseDomain(src []byte) (*Domain, error) {
	fm, body, err := SplitFrontmatter(src)
	if err != nil {
		return nil, err
	}
	d := &Domain{}
	if err := yaml.Unmarshal(fm, d); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	d.Body = string(body)
	return d, nil
}

// ValidateName returns nil when name satisfies the agentskills.io
// constraints (§4.3.4), and ErrInvalidName otherwise. Constraints:
// 1–64 characters, lowercase alphanumeric and hyphens, no leading or
// trailing hyphen, no consecutive hyphens. Implemented procedurally
// because Go's RE2 regex syntax does not support negative lookahead.
func ValidateName(name string) error {
	if len(name) < 1 || len(name) > 64 {
		return fmt.Errorf("%w: length %d outside [1,64]", ErrInvalidName, len(name))
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return fmt.Errorf("%w: %q has leading or trailing hyphen", ErrInvalidName, name)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
			if i+1 < len(name) && name[i+1] == '-' {
				return fmt.Errorf("%w: %q has consecutive hyphens", ErrInvalidName, name)
			}
		default:
			return fmt.Errorf("%w: %q contains invalid character %q", ErrInvalidName, name, c)
		}
	}
	return nil
}

// semverRegex matches the canonical major.minor.patch form. Pre-release
// and build metadata land with full semver support in phase 1.
var semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// ValidateVersion returns nil when v is a recognized semver-major.minor.patch
// string, and ErrInvalidVersion otherwise.
func ValidateVersion(v string) error {
	if !semverRegex.MatchString(v) {
		return fmt.Errorf("%w: %q", ErrInvalidVersion, v)
	}
	return nil
}

// IsFirstClassType reports whether the given type is one of the seven
// first-class types listed in §4.1.
func IsFirstClassType(t ArtifactType) bool {
	switch t {
	case TypeSkill, TypeAgent, TypeContext, TypeCommand,
		TypeRule, TypeHook, TypeMCPServer:
		return true
	}
	return false
}

// CanonicalArtifactPathSeparator is the path separator used in canonical
// artifact IDs (§4.2). Always "/", regardless of the host OS.
const CanonicalArtifactPathSeparator = "/"

// JoinCanonicalPath joins path segments with the canonical separator.
func JoinCanonicalPath(parts ...string) string {
	return strings.Join(parts, CanonicalArtifactPathSeparator)
}
