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

// ManifestBodyOf derives the manifest body of a manifest document by the
// §4.7.10 delimiter rule: the bytes after the closing "---", with leading CR
// and LF characters removed. A document with no frontmatter block has an empty
// body. It parses no YAML, because the delivery hash frames bytes and a
// document whose frontmatter does not parse still has a body to frame.
// Spec: §4.7.10
func ManifestBodyOf(doc []byte) (string, error) {
	_, body, err := SplitFrontmatter(doc)
	if errors.Is(err, ErrNoFrontmatter) {
		return "", nil
	}
	if err != nil {
		// SplitFrontmatter returns no other error today. The branch keeps a
		// future failure from reading as an empty body.
		return "", fmt.Errorf("manifest: derive body: %w", err)
	}
	return string(body), nil
}

// FrontmatterFields extracts the named top-level frontmatter fields from a
// manifest source, returning a name->value map for the names present as YAML
// scalars. It is the value source for the §8.2 manifest-declared redaction
// surface: the registry surfaces the author-named sensitive fields (for
// example bank_account or ssn) into the audit context so an audit_redact
// directive has a concrete value to mask. Names absent from the frontmatter,
// and non-scalar values (mappings or sequences), are skipped; a source
// without parseable frontmatter yields nil. The returned values are raw and
// must pass through redaction before reaching an audit sink.
func FrontmatterFields(src []byte, names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	fm, _, err := SplitFrontmatter(src)
	if err != nil {
		return nil
	}
	var all map[string]yaml.Node
	if err := yaml.Unmarshal(fm, &all); err != nil {
		return nil
	}
	out := make(map[string]string, len(names))
	for _, n := range names {
		node, ok := all[n]
		if !ok || node.Kind != yaml.ScalarNode {
			continue
		}
		out[n] = node.Value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseArtifact decodes an ARTIFACT.md source into an Artifact. The
// frontmatter populates the typed fields; the markdown after the
// frontmatter populates Artifact.Body.
//
// ParseArtifact handles syntactic decoding only. Semantic lint rules (cross
// reference resolution, name / version validation against the spec's
// per-type rules) live in pkg/lint and run on top of the parsed value.
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
		return nil, skillDecodeError(fm, err)
	}
	s.Body = string(body)
	return s, nil
}

// skillDecodeError wraps a SKILL.md frontmatter decode failure. yaml.v3
// reports a sequence or mapping under metadata as "cannot unmarshal !!seq
// into string" with no key name, so when the metadata mapping holds a value
// that is not a scalar, the error names the first such key. The YAML error
// stays in the message, so a second error in the same frontmatter is still
// reported.
//
// Spec: §4.3.4
func skillDecodeError(fm []byte, err error) error {
	key, ok := nonStringMetadataKey(fm)
	if !ok {
		return fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	hint := ""
	if key == "tags" {
		hint = "; tags is comma-separated"
	}
	return fmt.Errorf("%w: metadata.%s is not a string (metadata values are strings%s): %v", ErrInvalidYAML, key, hint, err)
}

// nonStringMetadataKey returns the first key of the frontmatter's metadata
// mapping whose value, after alias resolution, is not a scalar.
func nonStringMetadataKey(fm []byte) (string, bool) {
	var doc yaml.Node
	if yaml.Unmarshal(fm, &doc) != nil || len(doc.Content) == 0 {
		return "", false
	}
	md := mappingValue(doc.Content[0], "metadata")
	if md == nil || md.Kind != yaml.MappingNode {
		return "", false
	}
	w := metadataWalker{seen: map[string]bool{}, visited: map[*yaml.Node]bool{}}
	return w.firstNonScalarKey(md)
}

// metadataWalker holds the state of one walk over a metadata mapping and the
// mappings merged into it. seen holds the keys already checked. visited holds
// the mapping nodes already walked: yaml.v3 registers an anchor before it
// parses the anchored node's children, so `metadata: &m {<<: *m}` merges a
// mapping into itself, and without visited that cycle recurses until the
// runtime aborts with a stack overflow that no caller can recover from.
type metadataWalker struct {
	seen    map[string]bool
	visited map[*yaml.Node]bool
}

// firstNonScalarKey returns the first key of the mapping node m whose
// alias-resolved value is not a scalar, skipping the keys in seen and adding
// each key it checks. Keys are alias-resolved, because an alias key's own
// Value is the anchor name rather than the key yaml.v3 decodes. A merge key's
// value, a mapping or a sequence of mappings, is descended into, so a sequence
// that reaches metadata through `<<` is named by its merged key. The direct
// keys are checked before the merged ones and seen skips a key already
// checked, because yaml.v3 decodes a direct key in place of a merged one and
// an earlier merged mapping in place of a later one. A mapping already walked
// is skipped, so a cyclic merge ends the walk and the caller keeps the
// unchanged decode error.
func (w metadataWalker) firstNonScalarKey(m *yaml.Node) (string, bool) {
	w.visited[m] = true
	var merges []*yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		kn := resolveAlias(m.Content[i])
		vn := resolveAlias(m.Content[i+1])
		if kn.ShortTag() == "!!merge" {
			merges = append(merges, vn)
			continue
		}
		if w.seen[kn.Value] {
			continue
		}
		w.seen[kn.Value] = true
		if vn.Kind != yaml.ScalarNode {
			return kn.Value, true
		}
	}
	for _, mn := range merges {
		sources := []*yaml.Node{mn}
		if mn.Kind == yaml.SequenceNode {
			sources = mn.Content
		}
		for _, src := range sources {
			if src = resolveAlias(src); src.Kind != yaml.MappingNode || w.visited[src] {
				continue
			}
			if key, ok := w.firstNonScalarKey(src); ok {
				return key, true
			}
		}
	}
	return "", false
}

// mappingValue returns the alias-resolved value of key in the mapping node m,
// or nil when m is not a mapping or does not hold key.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	m = resolveAlias(m)
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return resolveAlias(m.Content[i+1])
		}
	}
	return nil
}

// resolveAlias follows an alias node to its anchored target.
func resolveAlias(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// UnmarshalYAML decodes a SchemaRef from either the documented mapping
// form (input: { $ref: ./schemas/input.json }, §4.3 type-specific fields)
// or a bare scalar path (input: ./schemas/input.json). Both populate Ref.
// Without this, the spec's { $ref: ... } mapping fails to decode into the
// field and ParseArtifact rejects the manifest with ErrInvalidYAML.
func (s *SchemaRef) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		s.Ref = node.Value
		return nil
	case yaml.MappingNode:
		var aux struct {
			Ref string `yaml:"$ref"`
		}
		if err := node.Decode(&aux); err != nil {
			return err
		}
		s.Ref = aux.Ref
		return nil
	default:
		return fmt.Errorf("schema ref must be a scalar path or a { $ref: ... } mapping, got YAML kind %d", node.Kind)
	}
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

// semverRegex matches an authored major.minor.patch version. The
// registry-reserved 0.0.0-auto.<N> form (§4.7.6) is validated separately by
// IsAutoVersion and is never accepted from an author.
var semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// ValidateVersion returns nil when v is a recognized semver-major.minor.patch
// string, and ErrInvalidVersion otherwise.
func ValidateVersion(v string) error {
	if !semverRegex.MatchString(v) {
		return fmt.Errorf("%w: %q", ErrInvalidVersion, v)
	}
	return nil
}

// firstClassTypes and builtinExtensionTypes encode the §4.1 type
// taxonomy. They are the single source of truth for the IsFirstClassType
// / IsBuiltinExtensionType predicates and for the default TypeProvider
// registry seeded in pkg/typeprovider.
var (
	firstClassTypes       = []ArtifactType{TypeSkill, TypeAgent, TypeContext, TypeCommand, TypeRule, TypeHook}
	builtinExtensionTypes = []ArtifactType{TypeMCPServer}
)

// FirstClassTypes returns the first-class artifact types listed in §4.1
// (skill, agent, context, command, rule, hook). These types carry full
// lint coverage and conformance-suite participation. The returned slice
// is a copy the caller may modify.
func FirstClassTypes() []ArtifactType {
	return append([]ArtifactType(nil), firstClassTypes...)
}

// BuiltinExtensionTypes returns the built-in extension types listed in
// §4.1. Podium ships schemas and lint rules for these but makes no
// conformance commitment beyond the type owner's. mcp-server is the only
// built-in extension type. The returned slice is a copy the caller may
// modify.
func BuiltinExtensionTypes() []ArtifactType {
	return append([]ArtifactType(nil), builtinExtensionTypes...)
}

// IsFirstClassType reports whether t is one of the first-class types
// listed in §4.1 (skill, agent, context, command, rule, hook). Per §4.1
// mcp-server is a built-in extension type, not first-class; use
// IsBuiltinExtensionType for it.
func IsFirstClassType(t ArtifactType) bool {
	for _, ft := range firstClassTypes {
		if t == ft {
			return true
		}
	}
	return false
}

// IsBuiltinExtensionType reports whether t is one of the built-in
// extension types listed in §4.1. mcp-server is currently the only one.
func IsBuiltinExtensionType(t ArtifactType) bool {
	for _, et := range builtinExtensionTypes {
		if t == et {
			return true
		}
	}
	return false
}

// TargetsHarness reports whether an artifact whose target_harnesses field
// is targets should materialize for harnessID. Per §4.3 the field is an
// opt-out of cross-harness materialization: an empty (or absent) list
// targets every harness, and a non-empty list restricts materialization
// to the harnesses it names. Matching is exact on the adapter ID, which
// is the PODIUM_HARNESS value (§6.7).
func TargetsHarness(targets []string, harnessID string) bool {
	if len(targets) == 0 {
		return true
	}
	for _, h := range targets {
		if h == harnessID {
			return true
		}
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
