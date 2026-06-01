package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNamingConvention_NoDocIDsOrDocFilenames enforces the e2e suite naming
// convention: test functions and files are named for the feature/behavior
// under test, with the spec/doc reference carried in a comment, not the name.
//
//   - No test function name embeds a numeric doc-test ID
//     (Test<Feature>_<n>..., e.g. the former TestDocCLI_69_DomainShowRoot).
//   - No test file is named after its source documentation page
//     (docs_<page>_test.go).
//
// A failure here means a new test reintroduced the doc-derived naming. Rename
// the function to Test<Feature>_<Behavior> and the file to <feature>_test.go,
// and move the doc/spec reference into a // Covers: comment.
func TestNamingConvention_NoDocIDsOrDocFilenames(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read e2e dir: %v", err)
	}
	// Test<Word>_<digit> — the doc-test-ID prefix the rename removed.
	docID := regexp.MustCompile(`func (Test[A-Za-z]+_[0-9][0-9a-z]*_[A-Za-z0-9_]+)\(`)

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		if strings.HasPrefix(name, "docs_") {
			t.Errorf("%s: test files are named for the feature under test, not the doc page; rename docs_<page>_test.go to <feature>_test.go", name)
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range docID.FindAllStringSubmatch(string(src), -1) {
			t.Errorf("%s: %q embeds a numeric doc-test ID; name the function for the behavior (Test<Feature>_<Behavior>) and cite the doc/spec in a comment", name, m[1])
		}
	}
	// Defensive: this file itself lives at a known path.
	if _, err := os.Stat(filepath.Join(".", "helpers_test.go")); err != nil {
		t.Errorf("expected shared helpers_test.go in the e2e package: %v", err)
	}
}
