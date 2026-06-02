package lint_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// domainBodySizeDiags lints a tree whose finance/DOMAIN.md carries body as
// its prose body and returns only the §4.5.5 body-size warnings.
func domainBodySizeDiags(t *testing.T, body string) []lint.Diagnostic {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"finance/ap/run/ARTIFACT.md": liveArtifact,
		"finance/DOMAIN.md":          "---\nname: finance\ndescription: Finance\n---\n\n" + body,
	})
	reg, err := filesystem.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	records, err := reg.Walk(filesystem.WalkOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	all := (&lint.Linter{}).Lint(context.Background(), reg, records)
	var out []lint.Diagnostic
	for _, d := range all {
		if d.Code == "lint.domain_body_size" {
			out = append(out, d)
		}
	}
	return out
}

// Spec: §4.5.5 — "Body length is recommended <= 2000 tokens; lint warns
// above." A DOMAIN.md body over the 2000-token approximation warns.
func TestRuleDomainBodySize_WarnsAboveCap(t *testing.T) {
	t.Parallel()
	// ~4 bytes/token approximation; (4*2000)+8 bytes => > 2000 tokens.
	body := strings.Repeat("a", lint.DomainBodyWarnTokens*4+8)
	diags := domainBodySizeDiags(t, body)
	if len(diags) != 1 {
		t.Fatalf("want 1 body-size warning, got %d: %+v", len(diags), diags)
	}
	if diags[0].Severity != lint.SeverityWarning {
		t.Errorf("severity = %q, want warning", diags[0].Severity)
	}
	if diags[0].ArtifactID != "finance" {
		t.Errorf("artifact id = %q, want finance", diags[0].ArtifactID)
	}
	if !strings.Contains(diags[0].Message, "tokens") {
		t.Errorf("message missing token count: %s", diags[0].Message)
	}
}

// Spec: §4.5.5 — a DOMAIN.md body at or below the recommendation draws no
// warning (boundary and empty-body cases).
func TestRuleDomainBodySize_NoWarnAtOrBelowCap(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"empty body":     "",
		"short body":     "# Finance\n\nAccounts payable and receivable.\n",
		"exactly at cap": strings.Repeat("a", lint.DomainBodyWarnTokens*4),
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diags := domainBodySizeDiags(t, body); len(diags) != 0 {
				t.Errorf("false positive for %s: %+v", name, diags)
			}
		})
	}
}
