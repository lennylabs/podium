package lint

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/materialize"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// ruleResourcePathReserved refuses a bundled resource whose path has a
// segment that names .git, the literal name or a form a filesystem resolves
// to it under the §7.5.3 aliasing rule. Every consumer writes a resource
// below the artifact's directory, so such a resource writes into a .git
// directory there; a .git/config can set a core.fsmonitor command that Git
// runs. The §6.6 writer refuses the path at write time, and this rule closes
// it for every consumer, the language SDKs included, by refusing the artifact
// at ingest. The error honors no lint_suppress entry.
//
// Spec: §4.4, §7.5.3
type ruleResourcePathReserved struct{}

func (ruleResourcePathReserved) Code() string { return "lint.resource_path_reserved" }

func (r ruleResourcePathReserved) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		paths := make([]string, 0, len(rec.Resources))
		for p := range rec.Resources {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			seg, ok := dotGitSegment(p)
			if !ok {
				continue
			}
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       r.Code(),
				Severity:   SeverityError,
				Message:    fmt.Sprintf("bundled resource %q has the segment %q, which names .git; rename or remove it", p, seg),
			})
		}
	}
	return out
}

// dotGitSegment returns the first segment of the slash-separated resource
// path p that names .git under the aliasing rule.
func dotGitSegment(p string) (string, bool) {
	for _, seg := range strings.Split(p, "/") {
		if materialize.AliasesDotGit(seg) {
			return seg, true
		}
	}
	return "", false
}
