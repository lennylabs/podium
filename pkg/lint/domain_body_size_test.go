package lint_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// domainBodySizeDiags lints a tree whose finance/DOMAIN.md carries body as
// its prose body under l and returns only the §4.5.5 body-size warnings.
func domainBodySizeDiags(t *testing.T, l *lint.Linter, body string) []lint.Diagnostic {
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
	all := l.Lint(context.Background(), reg, records)
	var out []lint.Diagnostic
	for _, d := range all {
		if d.Code == "lint.domain_body_size" {
			out = append(out, d)
		}
	}
	return out
}

// Spec: §4.5.5 — "By default lint warns above 2,000 estimated tokens."
// A DOMAIN.md body over the default warns.
func TestRuleDomainBodySize_WarnsAboveCap(t *testing.T) {
	t.Parallel()
	// ~4 bytes/token approximation; (4*2000)+8 bytes => > 2000 tokens.
	body := strings.Repeat("a", lint.DomainBodyWarnTokens*4+8)
	diags := domainBodySizeDiags(t, &lint.Linter{}, body)
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
			if diags := domainBodySizeDiags(t, &lint.Linter{}, body); len(diags) != 0 {
				t.Errorf("false positive for %s: %+v", name, diags)
			}
		})
	}
}

// Spec: §4.5.5, §13.12 — the DOMAIN.md body threshold is configurable: a
// body at the configured value draws nothing and one token above it warns,
// naming the configured value.
func TestDomainBodySize_ConfiguredThreshold(t *testing.T) {
	t.Parallel()
	l := &lint.Linter{Thresholds: lint.Thresholds{DomainBodyWarnTokens: 50}}
	if diags := domainBodySizeDiags(t, l, strings.Repeat("a", 50*4)); len(diags) != 0 {
		t.Errorf("body at the configured threshold warned: %+v", diags)
	}
	diags := domainBodySizeDiags(t, l, strings.Repeat("a", 50*4+1))
	if len(diags) != 1 {
		t.Fatalf("want 1 body-size warning one token above the threshold, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "above 50") {
		t.Errorf("message %q does not name the configured threshold", diags[0].Message)
	}
}
