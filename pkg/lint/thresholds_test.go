package lint

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec: §13.12, §4.1, §4.3.4, §4.5.5 — a partly filled Thresholds keeps
// the value it sets and the default for every other field. The defaults
// are asserted as literals, so a changed constant fails here. A
// whole-struct zero check fails this test, because PerFileSoftCapBytes
// is set and every other field would stay zero.
func TestThresholds_ResolvedFieldByField(t *testing.T) {
	t.Parallel()
	got := Thresholds{PerFileSoftCapBytes: 1024}.resolved()
	want := Thresholds{
		PerFileSoftCapBytes:    1024,
		PerPackageSoftCapBytes: 10485760,
		SkillBodyWarnTokens:    5000,
		SkillBodyWarnLines:     500,
		ManifestErrTokens:      20000,
		DomainBodyWarnTokens:   2000,
		MinDescriptionChars:    15,
		MinDescriptionWords:    3,
		DescriptionSimilarity:  0.7,
		ContentSimilarity:      0.8,
		URLConcurrency:         8,
	}
	if got != want {
		t.Errorf("resolved() = %+v, want %+v", got, want)
	}

	def := DefaultThresholds()
	if (Thresholds{}).resolved() != def {
		t.Errorf("zero Thresholds did not resolve to the defaults")
	}
	neg := Thresholds{
		PerFileSoftCapBytes: -1, PerPackageSoftCapBytes: -1, SkillBodyWarnTokens: -1,
		SkillBodyWarnLines: -1, ManifestErrTokens: -1, DomainBodyWarnTokens: -1,
		MinDescriptionChars: -1, MinDescriptionWords: -1,
	}
	if neg.resolved() != def {
		t.Errorf("negative integer fields did not resolve to the defaults: %+v", neg.resolved())
	}
	for _, s := range []float64{0.3, 1.2, 0.4999, -1, math.NaN()} {
		r := Thresholds{DescriptionSimilarity: s, ContentSimilarity: s}.resolved()
		if r.DescriptionSimilarity != 0.7 || r.ContentSimilarity != 0.8 {
			t.Errorf("similarity %v resolved to %v/%v, want 0.7/0.8", s, r.DescriptionSimilarity, r.ContentSimilarity)
		}
	}
	for _, s := range []float64{0.5, 0.55, 1.0} {
		r := Thresholds{DescriptionSimilarity: s, ContentSimilarity: s}.resolved()
		if r.DescriptionSimilarity != s || r.ContentSimilarity != s {
			t.Errorf("in-range similarity %v resolved to %v/%v", s, r.DescriptionSimilarity, r.ContentSimilarity)
		}
	}
}

// thresholdVar names one §13.12 variable, a valid value for it, and the
// Thresholds field it sets.
type thresholdVar struct {
	name  string
	valid string
	want  any
	field func(Thresholds) any
}

var thresholdVars = []thresholdVar{
	{"PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES", "2048", int64(2048), func(t Thresholds) any { return t.PerFileSoftCapBytes }},
	{"PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES", "4096", int64(4096), func(t Thresholds) any { return t.PerPackageSoftCapBytes }},
	{"PODIUM_LINT_SKILL_BODY_WARN_TOKENS", "100", 100, func(t Thresholds) any { return t.SkillBodyWarnTokens }},
	{"PODIUM_LINT_SKILL_BODY_WARN_LINES", "40", 40, func(t Thresholds) any { return t.SkillBodyWarnLines }},
	{"PODIUM_LINT_MANIFEST_ERROR_TOKENS", "900", 900, func(t Thresholds) any { return t.ManifestErrTokens }},
	{"PODIUM_LINT_DOMAIN_BODY_WARN_TOKENS", "300", 300, func(t Thresholds) any { return t.DomainBodyWarnTokens }},
	{"PODIUM_LINT_MIN_DESCRIPTION_CHARS", "20", 20, func(t Thresholds) any { return t.MinDescriptionChars }},
	{"PODIUM_LINT_MIN_DESCRIPTION_WORDS", "5", 5, func(t Thresholds) any { return t.MinDescriptionWords }},
	{"PODIUM_LINT_DESCRIPTION_SIMILARITY", "0.85", 0.85, func(t Thresholds) any { return t.DescriptionSimilarity }},
	{"PODIUM_LINT_CONTENT_SIMILARITY", "1.0", 1.0, func(t Thresholds) any { return t.ContentSimilarity }},
	{"PODIUM_LINT_URL_CONCURRENCY", "16", 16, func(t Thresholds) any { return t.URLConcurrency }},
}

// Spec: §13.12 — PODIUM_LINT_URL_CONCURRENCY accepts 1 to 64; a value above
// the range keeps the default and logs, and the bounds themselves apply.
func TestThresholdsFromEnv_URLConcurrencyRange(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]int{"1": 1, "64": 64, "65": DefaultURLConcurrency} {
		var logged int
		got := ThresholdsFromEnv(func(k string) string {
			if k == "PODIUM_LINT_URL_CONCURRENCY" {
				return value
			}
			return ""
		}, func(string, ...any) { logged++ })
		if got.URLConcurrency != want || (logged == 1) != (value == "65") {
			t.Errorf("%s: URLConcurrency=%d logged=%d, want %d", value, got.URLConcurrency, logged, want)
		}
	}
	if (Thresholds{URLConcurrency: 99}).resolved().URLConcurrency != DefaultURLConcurrency {
		t.Error("an out-of-range URLConcurrency did not resolve to the default")
	}
}

// Spec: §13.12 — each variable applies a valid value. Unset and empty
// keep the default without a log line. A value that does not parse or
// falls outside its range keeps the default and logs one line naming the
// variable and no other variable's value. A white-space-only or padded
// value is set and does not parse, so it logs as invalid.
func TestThresholdsFromEnv(t *testing.T) {
	t.Parallel()
	def := DefaultThresholds()
	for _, v := range thresholdVars {
		cases := []struct {
			value   string
			set     bool
			want    any
			wantLog bool
		}{
			{value: v.valid, set: true, want: v.want},
			{set: false, want: v.field(def)},
			{value: "", set: true, want: v.field(def)},
		}
		for _, bad := range []string{"abc", "0", "-5", "0.3", "1.5", "  ", "\t", " " + v.valid + " "} {
			cases = append(cases, struct {
				value   string
				set     bool
				want    any
				wantLog bool
			}{value: bad, set: true, want: v.field(def), wantLog: true})
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s=%q/set=%v", v.name, c.value, c.set), func(t *testing.T) {
				t.Parallel()
				env := map[string]string{"PODIUM_LINT_UNRELATED_SECRET": "s3cret"}
				if c.set {
					env[v.name] = c.value
				}
				var lines []string
				logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
				got := ThresholdsFromEnv(func(k string) string { return env[k] }, logf)
				if v.field(got) != c.want {
					t.Errorf("%s = %v, want %v", v.name, v.field(got), c.want)
				}
				// Every other field keeps its default.
				for _, o := range thresholdVars {
					if o.name != v.name && o.field(got) != o.field(def) {
						t.Errorf("%s changed to %v while setting %s", o.name, o.field(got), v.name)
					}
				}
				if !c.wantLog {
					if len(lines) != 0 {
						t.Errorf("unexpected log lines: %q", lines)
					}
					return
				}
				if len(lines) != 1 {
					t.Fatalf("log lines = %q, want exactly one", lines)
				}
				want := fmt.Sprintf("lint: %s=%q is invalid", v.name, c.value)
				if !strings.HasPrefix(lines[0], want) || !strings.Contains(lines[0], fmt.Sprintf("using the default %v", v.field(def))) {
					t.Errorf("log line %q does not name the variable, value, and default", lines[0])
				}
				if strings.Contains(lines[0], "s3cret") {
					t.Errorf("log line leaks another variable's value: %q", lines[0])
				}
			})
		}
	}
}

// Spec: §13.12, §4.3.4, §7.3.1 — a zero Linter, and one that sets a single
// threshold, apply the default size thresholds: a 5,001-token SKILL.md
// body warns and a 5,000-token body does not. Their AdvisoryRules keep the
// thin-description default, so a 14-code-point description warns.
func TestLinter_ZeroValueUsesDefaults(t *testing.T) {
	t.Parallel()
	skill := func(tokens int) []filesystem.ArtifactRecord {
		return []filesystem.ArtifactRecord{{
			ID:       "skills/big",
			Artifact: &manifest.Artifact{Type: manifest.TypeSkill},
			Skill:    &manifest.Skill{Body: strings.Repeat("a", tokens*4)},
		}}
	}
	tokenWarn := func(diags []Diagnostic) bool {
		for _, d := range diags {
			if d.Code == "lint.manifest_size" && d.Severity == SeverityWarning && strings.Contains(d.Message, "tokens") {
				return true
			}
		}
		return false
	}
	for name, l := range map[string]*Linter{
		"zero":    {},
		"partial": {Thresholds: Thresholds{PerFileSoftCapBytes: 1024}},
	} {
		if !tokenWarn(l.Lint(context.Background(), nil, skill(5001))) {
			t.Errorf("%s linter: a 5,001-token body did not warn", name)
		}
		if tokenWarn(l.Lint(context.Background(), nil, skill(5000))) {
			t.Errorf("%s linter: a 5,000-token body warned", name)
		}
		// Spec: §7.3.1 — the advisories keep the 15-code-point default.
		thin := []filesystem.ArtifactRecord{{
			ID:       "x",
			Artifact: &manifest.Artifact{Type: manifest.TypeAgent, Description: "abcd efgh ijkl"},
		}}
		if !hasCode((&Linter{Rules: l.AdvisoryRules()}).Lint(context.Background(), nil, thin), "lint.thin_description") {
			t.Errorf("%s linter: a 14-code-point description did not warn", name)
		}
	}
	var nilLinter *Linter
	if got := len(nilLinter.AdvisoryRules()); got != 4 {
		t.Errorf("nil linter: %d advisory rules, want 4", got)
	}
}
