package layer

import (
	"fmt"
	"strings"
)

// InvalidArtifactCode is the §6.10 code ingest returns for an artifact it
// cannot accept as authored, and the code a filesystem-source composition
// reports for an artifact it drops under the §4.6 executable-field rule, under
// the §4.6 sandbox-profile rule, or because its extends: reference no longer
// resolves.
//
// Spec: §4.6, §13.11.3
const InvalidArtifactCode = "ingest.invalid_artifact"

// ExecutableInheritanceConstraint is the details.constraint value of a §4.6
// executable-field rejection.
//
// Spec: §4.6, §7.3.1
const ExecutableInheritanceConstraint = "executable_inheritance"

// UnreadableExecutableFieldsReason is the reason both modes give a child
// whose §4.6 executable-field comparison returns an error, including a field
// path that would name a chain parent. It names no field and no parent,
// because a field path built from a parent-authored name can disclose an
// ancestor the read path withholds.
//
// Spec: §4.6
const UnreadableExecutableFieldsReason = "extends: executable fields could not be read"

// extendsReasonPrefix opens every extends: rejection reason both modes give.
const extendsReasonPrefix = "extends: "

// ExecutableInheritance describes a child that leaves executable fields to a
// parent in another layer. It is an error so a filesystem walk that has no
// drop callback can fail with it and a caller can match it with errors.As.
//
// Spec: §4.6
type ExecutableInheritance struct {
	// ArtifactID is the canonical ID of the child the rule refuses.
	ArtifactID string
	// Fields holds the sorted, de-duplicated field paths the parent sets and
	// the child does not restate.
	Fields []string
}

// Reason returns the human-readable rejection text. It lists the field paths
// and names neither the parent's ID nor any layer, so a child that overlays
// its own ID carries only that ID and a cross-layer parent stays withheld.
//
// Spec: §4.6
func (e *ExecutableInheritance) Reason() string {
	return fmt.Sprintf("%sa parent in another layer sets executable fields this artifact does not restate: %s; restate each one in this artifact",
		extendsReasonPrefix, strings.Join(e.Fields, ", "))
}

// Error returns Reason.
func (e *ExecutableInheritance) Error() string { return e.Reason() }

// Details returns the §7.3.1 details member a server rejection carries: the
// constraint name and the field paths. The filesystem path reports the code
// and the reason alone.
//
// Spec: §4.6, §7.3.1
func (e *ExecutableInheritance) Details() map[string]any {
	return map[string]any{
		"constraint": ExecutableInheritanceConstraint,
		"fields":     e.Fields,
	}
}

// UnresolvedParentReason is the reason both modes give an artifact whose
// extends: reference names an ID no stored or kept record provides. Server
// ingest builds its unresolved-pin error from the same text, so the two
// modes word the failure identically.
//
// Spec: §4.6, §13.11.3
func UnresolvedParentReason(parentID string) string {
	return fmt.Sprintf("%sno parent artifact %q ingested yet", extendsReasonPrefix, parentID)
}
