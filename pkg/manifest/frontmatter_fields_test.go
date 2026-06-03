package manifest

import "testing"

// spec: §8.2 — FrontmatterFields is the value source for manifest-declared
// audit redaction. It surfaces the author-named sensitive frontmatter fields
// (the spec's bank_account / ssn example) so the audit_redact directive has a
// concrete target the registry can mask. F-8.2.1.
func TestFrontmatterFields(t *testing.T) {
	t.Parallel()
	src := []byte("---\n" +
		"type: context\n" +
		"version: 1.0.0\n" +
		"bank_account: \"1234-5678\"\n" +
		"ssn: 000-11-2222\n" +
		"tags:\n  - a\n  - b\n" +
		"audit_redact:\n  - bank_account\n" +
		"---\n\nbody\n")

	got := FrontmatterFields(src, []string{"bank_account", "ssn", "missing", "tags"})
	if got["bank_account"] != "1234-5678" {
		t.Errorf("bank_account = %q, want 1234-5678", got["bank_account"])
	}
	if got["ssn"] != "000-11-2222" {
		t.Errorf("ssn = %q, want 000-11-2222", got["ssn"])
	}
	if _, ok := got["missing"]; ok {
		t.Errorf("absent field surfaced: %v", got)
	}
	// A sequence is non-scalar and must not be surfaced as a redaction target.
	if _, ok := got["tags"]; ok {
		t.Errorf("non-scalar (sequence) field surfaced: %v", got)
	}
}

// spec: §8.2 — corner cases: no names, no frontmatter, and all-absent names
// each yield nil so callers add nothing to the audit context. F-8.2.1.
func TestFrontmatterFields_Empty(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: context\nversion: 1.0.0\n---\n\nbody\n"
	if got := FrontmatterFields([]byte(fm), nil); got != nil {
		t.Errorf("nil names: got %v, want nil", got)
	}
	if got := FrontmatterFields([]byte("not a manifest"), []string{"x"}); got != nil {
		t.Errorf("no frontmatter: got %v, want nil", got)
	}
	if got := FrontmatterFields([]byte(fm), []string{"absent"}); got != nil {
		t.Errorf("all-absent names: got %v, want nil", got)
	}
}
