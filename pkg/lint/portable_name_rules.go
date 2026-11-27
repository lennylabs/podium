package lint

import (
	"context"
	"fmt"
	"sort"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// NonPortableNameCode is the code of the §4.2 portable-name warning.
const NonPortableNameCode = "lint.non_portable_name"

// ruleNonPortableName warns on an artifact whose canonical ID or one of whose
// bundled resource paths holds a segment that is not a §4.2 portable name. A
// consumer writes the ID and each resource path as a path in a workspace, and
// on Windows or HFS+ such a segment can name another file than the one it
// spells, such as a device for "CON .txt" or the .git directory for GIT~1.
// Ingest stores the artifact, so the warning is how its author learns of it.
// It reports one diagnostic per artifact, naming the first offending segment:
// the ID before the resource paths, and the paths in ascending byte order. It
// reads the ID alone of a record that carries no parsed fields, so
// `podium attest` can run it over every package a walk finds. The warning
// honors no lint_suppress entry.
//
// Spec: §4.2
type ruleNonPortableName struct{}

// NonPortableNameRule returns the §4.2 portable-name rule, which `podium
// attest` runs over the packages it writes subjects for.
func NonPortableNameRule() Rule { return ruleNonPortableName{} }

func (ruleNonPortableName) Code() string { return NonPortableNameCode }

func (r ruleNonPortableName) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		msg, ok := nonPortableMessage(rec)
		if !ok {
			continue
		}
		out = append(out, Diagnostic{ArtifactID: rec.ID, Code: r.Code(), Severity: SeverityWarning, Message: msg})
	}
	return out
}

// nonPortableMessage describes the first segment of rec's ID or resource
// paths that is not a portable name, and reports whether there is one.
func nonPortableMessage(rec filesystem.ArtifactRecord) (string, bool) {
	if seg, ok := attest.NonPortableSegment(rec.ID); ok {
		return fmt.Sprintf("the canonical ID has the segment %q, which is not a portable name on every file system; rename the directory", seg), true
	}
	paths := make([]string, 0, len(rec.Resources))
	for p := range rec.Resources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if seg, ok := attest.NonPortableSegment(p); ok {
			return fmt.Sprintf("bundled resource %q has the segment %q, which is not a portable name on every file system; rename it", p, seg), true
		}
	}
	return "", false
}
