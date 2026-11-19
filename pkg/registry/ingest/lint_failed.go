package ingest

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lennylabs/podium/pkg/lint"
)

// lintFailedNamedMax is the most artifacts an ingest.lint_failed message
// names (§7.3.1). A layer with many failing artifacts would otherwise put an
// unbounded list into one error envelope and one log line.
const lintFailedNamedMax = 10

// lintFailedError is the ingest.lint_failed refusal of a cycle that stored
// nothing. Its message states the number of diagnostics and names the
// artifacts they came from in identifier order, each with its lint codes, so
// the envelope's suggested action, which tells the caller to correct the
// artifact the message names, has an artifact to point at. A diagnostic that
// names no artifact counts toward the number and names nothing.
//
// Spec: §7.3.1 (Ingest outcome), §6.10
func lintFailedError(diags []lint.Diagnostic) error {
	codesByID := map[string][]string{}
	for _, d := range diags {
		if d.ArtifactID == "" {
			continue
		}
		if !slices.Contains(codesByID[d.ArtifactID], d.Code) {
			codesByID[d.ArtifactID] = append(codesByID[d.ArtifactID], d.Code)
		}
	}
	noun := "diagnostics"
	if len(diags) == 1 {
		noun = "diagnostic"
	}
	msg := fmt.Sprintf("%d lint %s", len(diags), noun)
	ids := slices.Sorted(maps.Keys(codesByID))
	if len(ids) == 0 {
		return fmt.Errorf("%w: %s", ErrLintFailed, msg)
	}
	named := make([]string, 0, min(len(ids), lintFailedNamedMax))
	for _, id := range ids[:cap(named)] {
		codes := codesByID[id]
		slices.Sort(codes)
		named = append(named, fmt.Sprintf("%s (%s)", id, strings.Join(codes, ", ")))
	}
	msg += " in " + strings.Join(named, ", ")
	if rest := len(ids) - len(named); rest > 0 {
		msg += fmt.Sprintf(", and %d more artifacts", rest)
	}
	return fmt.Errorf("%w: %s", ErrLintFailed, msg)
}
