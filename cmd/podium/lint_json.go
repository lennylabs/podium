package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
)

// lintExitCode is the `podium lint` exit status for a completed run. It is 1
// when an artifact was refused, when a diagnostic is an error, or when a
// diagnostic is a warning and failOn is lint.SeverityWarning, and 0
// otherwise. An info diagnostic never fails the run. The rule lives apart
// from runLint because no built-in rule emits info, so only a direct test
// can pin that an info diagnostic stays below every --fail-on value.
//
// Spec: §7.3.1
func lintExitCode(diags []lint.Diagnostic, dropped bool, failOn lint.Severity) int {
	if dropped {
		return 1
	}
	for _, d := range diags {
		switch d.Severity {
		case lint.SeverityError:
			return 1
		case lint.SeverityWarning:
			if failOn == lint.SeverityWarning {
				return 1
			}
		}
	}
	return 0
}

// lintDiagnosticJSON is one entry of the envelope's diagnostics member.
type lintDiagnosticJSON struct {
	ArtifactID string `json:"artifact_id"`
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
}

// lintRejectedJSON is one entry of the envelope's rejected member. Details
// is omitted when the refusing rule defines none, as for a collision.
type lintRejectedJSON struct {
	ArtifactID string         `json:"artifact_id"`
	Code       string         `json:"code"`
	Reason     string         `json:"reason"`
	Details    map[string]any `json:"details,omitempty"`
}

// lintCountsJSON counts the diagnostics by severity. Every member is always
// present so a CI consumer reads a zero rather than a missing key.
type lintCountsJSON struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

// lintEnvelopeJSON is the single object `podium lint --json` writes.
type lintEnvelopeJSON struct {
	Registry    string               `json:"registry"`
	Diagnostics []lintDiagnosticJSON `json:"diagnostics"`
	Rejected    []lintRejectedJSON   `json:"rejected"`
	Counts      lintCountsJSON       `json:"counts"`
}

// writeLintEnvelope writes the `podium lint --json` envelope to w as
// indented JSON. diags is already in the Linter.Lint order (artifact ID,
// code, message); drops are sorted here by artifact ID. Both members render
// as [] when empty.
//
// Spec: §7.3.1
func writeLintEnvelope(w io.Writer, registry string, diags []lint.Diagnostic, drops []layer.Drop) error {
	env := lintEnvelopeJSON{
		Registry:    registry,
		Diagnostics: make([]lintDiagnosticJSON, 0, len(diags)),
		Rejected:    make([]lintRejectedJSON, 0, len(drops)),
	}
	for _, d := range diags {
		env.Diagnostics = append(env.Diagnostics, lintDiagnosticJSON{
			ArtifactID: d.ArtifactID,
			Code:       d.Code,
			Severity:   string(d.Severity),
			Message:    d.Message,
		})
		switch d.Severity {
		case lint.SeverityError:
			env.Counts.Error++
		case lint.SeverityWarning:
			env.Counts.Warning++
		case lint.SeverityInfo:
			env.Counts.Info++
		}
	}
	for _, d := range drops {
		env.Rejected = append(env.Rejected, lintRejectedJSON{
			ArtifactID: d.ArtifactID,
			Code:       d.Code,
			Reason:     d.Reason,
			Details:    d.Details,
		})
	}
	sort.SliceStable(env.Rejected, func(i, j int) bool {
		return env.Rejected[i].ArtifactID < env.Rejected[j].ArtifactID
	})
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("lint: encode json: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(b)); err != nil {
		return fmt.Errorf("lint: write json: %w", err)
	}
	return nil
}
