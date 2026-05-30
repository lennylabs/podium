// Package conformance holds the cross-cutting suites that any built-in
// or community implementation runs against. This suite verifies the
// governance documentation matches the project-model claims in spec §1.6:
// the RFC process terminology and the directory those docs point readers to.
package conformance

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the absolute path to the repository root, derived from
// this test file's location so the test runs from any working directory.
func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func readRepoFile(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// Spec: §1.6 Project Model — "Governance. Maintainer model + RFC process
// for spec changes; see GOVERNANCE.md." The governance docs the spec points
// to must describe an RFC process, matching the spec's terminology (F-1.6.1).
func TestGovernanceDocs_UseRFCProcessTerminology(t *testing.T) {
	t.Parallel()

	// Anchor the requirement: confirm the spec still states the RFC process.
	spec := readRepoFile(t, filepath.Join("spec", "01-overview.md"))
	if !strings.Contains(spec, "RFC process for spec changes; see `GOVERNANCE.md`") {
		t.Fatalf("spec §1.6 governance claim changed; update this conformance test to match")
	}

	governanceDocs := []string{
		"GOVERNANCE.md",
		filepath.Join("docs", "about", "governance.md"),
	}
	for _, rel := range governanceDocs {
		body := readRepoFile(t, rel)
		if !strings.Contains(body, "RFC process") {
			t.Errorf("%s does not describe an RFC process; spec §1.6 requires it", rel)
		}
		// The proposal mechanism must not point at the stale docs/adr/ path.
		if strings.Contains(body, "docs/adr/") {
			t.Errorf("%s still references docs/adr/; spec §1.6 RFC process lives in docs/rfc/ (F-1.6.2)", rel)
		}
		if strings.Contains(body, "draft ADRs") {
			t.Errorf("%s still files proposals as ADRs; spec §1.6 requires the RFC process (F-1.6.1)", rel)
		}
	}
}

// Spec: §1.6 Project Model — GOVERNANCE.md directs contributors to the RFC
// directory. The referenced location must exist with an index, so the link
// does not resolve to a missing directory (F-1.6.2).
func TestGovernanceDocs_RFCDirectoryExists(t *testing.T) {
	t.Parallel()

	governance := readRepoFile(t, "GOVERNANCE.md")
	if !strings.Contains(governance, "docs/rfc/") {
		t.Fatalf("GOVERNANCE.md no longer references docs/rfc/; spec §1.6 RFC process needs a location")
	}

	rfcDir := filepath.Join(repoRoot(t), "docs", "rfc")
	info, err := os.Stat(rfcDir)
	if err != nil {
		t.Fatalf("docs/rfc/ is referenced by GOVERNANCE.md but does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("docs/rfc exists but is not a directory")
	}

	// The directory must carry an index describing the RFC format and numbering.
	index := filepath.Join(rfcDir, "README.md")
	body, err := os.ReadFile(index)
	if err != nil {
		t.Fatalf("docs/rfc/README.md index missing: %v", err)
	}
	for _, want := range []string{"RFC", "Numbering", "Format"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("docs/rfc/README.md missing %q section; index should describe the RFC format and numbering", want)
		}
	}
}
