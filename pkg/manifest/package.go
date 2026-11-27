package manifest

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// ErrSkillDocumentMissing reports a package whose ARTIFACT.md declares
// type: skill and whose root holds no SKILL.md. The package is unresolved and
// refused per artifact under the §4.1 unparseable-manifest rule, so the
// message keeps the "missing SKILL.md" wording the walk errors used before
// the refusal moved per artifact.
var ErrSkillDocumentMissing = errors.New("manifest: type: skill missing SKILL.md")

// ErrInvalidUTF8 reports a resolved field value that is not valid UTF-8. The
// YAML decoder yields no such value; the check keeps the §4.7.6 resolution
// total, so a value the package digest frames is always UTF-8.
var ErrInvalidUTF8 = errors.New("manifest: resolved value is not valid UTF-8")

// Root document names of an artifact package.
const (
	ArtifactFile = "ARTIFACT.md"
	SkillFile    = "SKILL.md"
)

// PackageDocuments holds the documents at the root of an artifact package.
// HasArtifactMD and HasSkillMD report that the file exists, which is
// distinct from a zero-length document: a present zero-length ARTIFACT.md
// does not parse, while an absent one makes the package a skill with no
// ARTIFACT.md.
type PackageDocuments struct {
	ArtifactMD    []byte
	HasArtifactMD bool
	SkillMD       []byte
	HasSkillMD    bool
}

// StoredDocuments returns the documents of a stored row or a served record,
// whose empty manifest stands for an absent ARTIFACT.md and whose empty
// SKILL.md stands for an absent one. Ingest never stores a zero-length
// document, because none parses, so the reading loses no stored package.
func StoredDocuments(artifactMD, skillMD []byte) PackageDocuments {
	return PackageDocuments{
		ArtifactMD:    artifactMD,
		HasArtifactMD: len(artifactMD) != 0,
		SkillMD:       skillMD,
		HasSkillMD:    len(skillMD) != 0,
	}
}

// ResolvedFields are the §4.7.6 resolved fields: the values a load_artifact
// response serves in the type, sensitivity, deprecated, and replaced_by
// members and that the documents of a package determine.
type ResolvedFields struct {
	Type        ArtifactType
	Sensitivity Sensitivity
	Deprecated  bool
	ReplacedBy  string
}

// PackageResolution is the outcome of ResolvePackage. A resolved package
// carries the resolved artifact, its parsed SKILL.md for a skill, the
// metadata keys §4.3.4 filled, and its resolved fields. An unresolved package
// carries the root document that failed and the cause.
type PackageResolution struct {
	Resolved bool
	Artifact *Artifact
	Skill    *Skill
	Filled   []string
	Fields   ResolvedFields
	// FailedFile is ArtifactFile or SkillFile for an unresolved package, and
	// Err the parse error or ErrSkillDocumentMissing.
	FailedFile string
	Err        error
	// skillDocument reports that the root SKILL.md is the package's skill
	// document rather than a bundled resource.
	skillDocument bool
}

// SkillDocument reports whether the root SKILL.md of the package is its
// skill document: the package has no ARTIFACT.md, or its ARTIFACT.md parses
// and declares type: skill. In every other package, an unparseable
// ARTIFACT.md included, a root SKILL.md is a bundled resource. The §4.7.6
// package digest omits a zero-length skill document and lists a zero-length
// resource, so it reads this role.
//
// Spec: §4.7.6
func (r PackageResolution) SkillDocument() bool { return r.skillDocument }

// ResolvePackage resolves the §4.7.6 resolved fields of a package from its
// root documents. It is the one resolution that registry ingest, the
// filesystem-source walk, and §13.4 stored-row admission apply, extracted
// from the walks so the three cannot resolve one package differently. A
// package with an ARTIFACT.md resolves from that document, and a type: skill
// takes its SKILL.md metadata by the §4.3.4 precedence. A package with a
// SKILL.md and no ARTIFACT.md is a skill whose fields come from its metadata
// with no key stated. Every other outcome is unresolved: an ARTIFACT.md that
// does not parse, a type: skill whose SKILL.md is absent or does not parse, a
// plain skill whose SKILL.md does not parse, and a resolved value that is not
// valid UTF-8. A zero-length document does not parse, because it has no
// frontmatter.
//
// The resolution is part of the package digest format, so a change to it,
// including a YAML library update that changes a decoded value, is a change
// of format (§4.7.6 *Format stability*).
//
// Spec: §4.1, §4.3.4, §4.7.6
func ResolvePackage(docs PackageDocuments) PackageResolution {
	if !docs.HasArtifactMD {
		return resolvePlainSkill(docs)
	}
	a, err := ParseArtifact(docs.ArtifactMD)
	if err != nil {
		return unresolved(ArtifactFile, err, false)
	}
	if a.Type != TypeSkill {
		return resolved(a, nil, nil, false)
	}
	if !docs.HasSkillMD {
		return unresolved(SkillFile, ErrSkillDocumentMissing, true)
	}
	s, err := ParseSkill(docs.SkillMD)
	if err != nil {
		return unresolved(SkillFile, err, true)
	}
	stated, err := StatedKeys(docs.ArtifactMD)
	if err != nil {
		// ParseArtifact decoded the same frontmatter, so this branch runs
		// only if the two decoders ever disagree. Resolving with no stated
		// keys would let metadata override ARTIFACT.md, so the package is
		// unresolved instead.
		return unresolved(ArtifactFile, err, true)
	}
	filled := ResolveSkillFields(a, stated, s)
	return resolved(a, s, filled, true)
}

// resolvePlainSkill resolves a package with no ARTIFACT.md. Its base artifact
// is a skill carrying the SKILL.md description, which is how the registry
// reads a plain-skill row (§13.4).
func resolvePlainSkill(docs PackageDocuments) PackageResolution {
	if !docs.HasSkillMD {
		return unresolved(SkillFile, ErrSkillDocumentMissing, true)
	}
	s, err := ParseSkill(docs.SkillMD)
	if err != nil {
		return unresolved(SkillFile, err, true)
	}
	a := &Artifact{Type: TypeSkill, Description: s.Description}
	filled := ResolveSkillFields(a, nil, s)
	return resolved(a, s, filled, true)
}

// resolved builds the resolution of a package whose documents parsed. A
// resolved value that is not valid UTF-8 leaves the package unresolved.
func resolved(a *Artifact, s *Skill, filled []string, skillDocument bool) PackageResolution {
	fields := ResolvedFields{
		Type:        a.Type,
		Sensitivity: a.Sensitivity,
		Deprecated:  a.Deprecated,
		ReplacedBy:  a.ReplacedBy,
	}
	for _, v := range []string{string(fields.Type), string(fields.Sensitivity), fields.ReplacedBy} {
		if !utf8.ValidString(v) {
			return unresolved(ArtifactFile, fmt.Errorf("%w: %q", ErrInvalidUTF8, v), skillDocument)
		}
	}
	return PackageResolution{
		Resolved:      true,
		Artifact:      a,
		Skill:         s,
		Filled:        filled,
		Fields:        fields,
		skillDocument: skillDocument,
	}
}

// unresolved builds the resolution of a package whose file did not resolve.
func unresolved(file string, err error, skillDocument bool) PackageResolution {
	return PackageResolution{FailedFile: file, Err: err, skillDocument: skillDocument}
}
