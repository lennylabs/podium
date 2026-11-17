package layer

import (
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// InvalidArtifactCode is the §6.10 code ingest returns for an artifact it
// cannot accept as authored (including the §7.3.1 registry-assigned-version
// rule), and the code both modes report for an artifact the §4.1
// unparseable-manifest rule, the §4.6 no-inheritance rule, or the §4.1
// removed-type rule refuses.
//
// Spec: §4.1, §4.6, §7.3.1, §13.11.3
const InvalidArtifactCode = "ingest.invalid_artifact"

// ManifestUnparseableConstraint is the details.constraint value of a §4.1
// unparseable-manifest rejection: an ARTIFACT.md or SKILL.md whose
// frontmatter does not parse.
//
// Spec: §4.1, §7.3.1, §13.11.3
const ManifestUnparseableConstraint = "manifest_unparseable"

// UnparseableReason is the reason both modes give an artifact whose manifest
// does not parse. file is the manifest's slash path under the layer root and
// err is the parse error, so the reason names the file and the error and
// names no layer. The YAML decoder reports a type error with one indented
// line per failure, and the CLI prints each refusal as one standard-error
// line, so the error's lines are joined with single spaces.
//
// Spec: §4.1, §7.3.1, §13.11.3
func UnparseableReason(file string, err error) string {
	lines := strings.Split(err.Error(), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return file + ": " + strings.Join(lines, " ")
}

// ExtendsRemovedConstraint is the details.constraint value of a §4.6
// no-inheritance rejection.
//
// Spec: §4.6, §13.11.3
const ExtendsRemovedConstraint = "extends_removed"

// ExtendsRemovedReason is the reason both modes give an ARTIFACT.md that
// declares extends:. It names no layer.
//
// Spec: §4.6, §13.11.3
const ExtendsRemovedReason = "extends: is not supported; remove the extends: key and declare every field this artifact needs in its own frontmatter"

// TypeRemovedConstraint is the details.constraint value of a §4.1
// removed-type rejection.
//
// Spec: §4.1, §13.11.3
const TypeRemovedConstraint = "type_removed"

// TypeRemovedReason is the reason both modes give an ARTIFACT.md whose type
// §4.1 removed. It names the replacement and the upgrade notes that describe
// the hand conversion, and it names no layer and no command.
//
// Spec: §4.1, §13.11.3
func TypeRemovedReason(t string) string {
	return "type: " + t + " is not supported; " +
		manifest.RemovedTypeReplacement(manifest.ArtifactType(t)) +
		"; the 0.7.0 upgrade notes describe how to convert an existing package"
}

// RemovedFeature classifies an authored ARTIFACT.md against the removed
// features. It reports the constraint and reason of the first rule that
// refuses it, or ok false when none does. The §4.6 extends: check runs before
// the §4.1 removed-type check, so an artifact that trips both reports
// extends_removed. Ingest and the filesystem walk both call it, so the two
// deployment modes refuse the same artifacts with the same details.
//
// Spec: §4.1, §4.6, §13.11.3
func RemovedFeature(authored []byte, t manifest.ArtifactType) (constraint, reason string, ok bool) {
	if manifest.DeclaresExtends(authored) {
		return ExtendsRemovedConstraint, ExtendsRemovedReason, true
	}
	if manifest.IsRemovedType(t) {
		return TypeRemovedConstraint, TypeRemovedReason(string(t)), true
	}
	return "", "", false
}

// RemovedFeatureDrop returns the drop a filesystem-source composition
// reports for an artifact a pre-lint rule refuses: the §4.1
// unparseable-manifest rule or a rule RemovedFeature applies. Its details
// carry the same constraint ingest reports for the same artifact.
//
// Spec: §4.1, §4.6, §13.11.3
func RemovedFeatureDrop(id, constraint, reason string) Drop {
	return Drop{
		ArtifactID: id,
		Code:       InvalidArtifactCode,
		Reason:     reason,
		Details:    map[string]any{"constraint": constraint},
	}
}
