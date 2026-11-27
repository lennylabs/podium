package store

import (
	"strings"
	"testing"
)

// Spec: §4.7.11 (Reported strings) — the details column keeps <, >, and & as
// written, so a bounded details object is not lengthened by HTML escaping,
// and it decodes back to the same values.
func TestAttestationDetailsColumn_NoHTMLEscaping(t *testing.T) {
	t.Parallel()
	col, err := attestationDetailsColumn(map[string]any{"file": "<a&b>"})
	if err != nil || col != `{"file":"<a&b>"}` || strings.Contains(col, `\u003c`) {
		t.Fatalf("column %q, err %v", col, err)
	}
	if d, err := parseAttestationDetails(col); err != nil || d["file"] != "<a&b>" {
		t.Fatalf("decoded %v, err %v", d, err)
	}
}
