package lint

import (
	"context"
	"fmt"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// ruleHarnessCapability implements the §6.7.1 capability matrix lint and
// the §4.3.5 target_harnesses escape hatch. When an artifact declares
// target_harnesses, the rule checks every field it uses against each named
// harness via the adapter package's §6.7.1 matrix:
//
//   - a ✗ cell (the harness cannot translate the field) is an ingest error
//     ("field X is used but adapter cursor cannot translate it"), so the
//     author must drop the field or remove the harness from
//     target_harnesses;
//   - a ⚠ cell (the adapter falls back) is a warning.
//
// An artifact without target_harnesses declares no harness set, so it draws
// no capability diagnostic: it materializes best-effort on every harness,
// with the adapter dropping or falling back per field. This matches the
// §4.3.5 framing where target_harnesses is the surface that scopes the
// (✗) intersection, and the doc-derived conformance suite, which lints
// rule_mode / hook_event / delegates_to artifacts without target_harnesses
// cleanly.
type ruleHarnessCapability struct{}

func (ruleHarnessCapability) Code() string { return "lint.harness_capability" }

func (r ruleHarnessCapability) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		art := rec.Artifact
		if art == nil || len(art.TargetHarnesses) == 0 {
			continue
		}
		for _, m := range adapter.Evaluate(art, art.TargetHarnesses) {
			switch m.Support {
			case adapter.SupportUnsupported:
				out = append(out, errMsg(rec.ID, r, fmt.Sprintf(
					"field %s is used but adapter %q cannot translate it; remove %q from target_harnesses or drop the field",
					m.Capability, m.Harness, m.Harness)))
			case adapter.SupportFallback:
				out = append(out, warn(rec.ID, r.Code(), fmt.Sprintf(
					"field %s falls back on adapter %q; materialization is degraded for that harness",
					m.Capability, m.Harness)))
			}
		}
	}
	return out
}
