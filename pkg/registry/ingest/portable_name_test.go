package ingest_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
)

// Spec: §4.2, §7.3.1 — an artifact whose canonical ID or bundled resource
// path holds a segment that is not a portable name is stored, and the cycle
// reports lint.non_portable_name among its advisories with the ID and the
// segment. An artifact whose names are portable draws no such advisory.
func TestIngest_NonPortableNameAdvisory(t *testing.T) {
	t.Parallel()
	res := ingestLayer(t, map[string]string{
		"team/CON .txt/ARTIFACT.md":  ctxArtifact("1.0.0", "Reconcile vendor invoices at month-end close."),
		"team/notes/ARTIFACT.md":     ctxArtifact("1.0.0", "Summarize the weekly status notes for the team."),
		"team/notes/refs/aux.md":     "Reference text.\n",
		"team/portable/ARTIFACT.md":  ctxArtifact("1.0.0", "Draft the quarterly planning document for review."),
		"team/portable/release~2.md": "Release notes.\n",
	})
	if res.Accepted != 3 || len(res.Rejected) != 0 || len(res.LintFailures) != 0 {
		t.Fatalf("accepted=%d rejected=%+v lint=%+v, want all three stored", res.Accepted, res.Rejected, res.LintFailures)
	}
	want := map[string]string{"team/CON .txt": `"CON .txt"`, "team/notes": `"aux.md"`}
	for _, d := range res.Advisories {
		if d.Code != lint.NonPortableNameCode {
			continue
		}
		seg, ok := want[d.ArtifactID]
		if !ok || d.Severity != lint.SeverityWarning || !strings.Contains(d.Message, seg) {
			t.Errorf("unexpected advisory %+v", d)
		}
		delete(want, d.ArtifactID)
	}
	if len(want) != 0 {
		t.Errorf("no lint.non_portable_name advisory for %v; advisories %+v", want, res.Advisories)
	}
}
