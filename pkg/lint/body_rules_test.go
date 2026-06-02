package lint_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec: §4.3.4 line 286 — a skill's ARTIFACT.md body is empty
// or a single HTML comment; anything else warns.
func TestRuleArtifactBodyForSkill_AcceptsEmptyOrSingleComment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		warn bool
	}{
		{"empty body", "---\ntype: skill\n---\n", false},
		{"single comment", "---\ntype: skill\n---\n<!-- pointer to SKILL.md -->\n", false},
		{"prose body", "---\ntype: skill\n---\nThis is body content.\n", true},
		{"two comments", "---\ntype: skill\n---\n<!-- one --> <!-- two -->\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := filesystem.ArtifactRecord{
				ID:            "skills/example",
				Artifact:      &manifest.Artifact{Type: manifest.TypeSkill},
				ArtifactBytes: []byte(tc.body),
			}
			diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
			gotWarn := false
			for _, d := range diags {
				if d.Code == "lint.skill_artifact_body" {
					gotWarn = true
				}
			}
			if gotWarn != tc.warn {
				t.Errorf("warn = %v, want %v (body %q): %+v", gotWarn, tc.warn, tc.body, diags)
			}
		})
	}
}

// Spec: §4.4 lines 336-342 — bundled-file references must
// resolve. References to missing files are an ingest error.
func TestRuleProseReference_BundledFileResolves(t *testing.T) {
	t.Parallel()
	body := "---\ntype: context\n---\nSee [the script](./scripts/run.py)."
	rec := filesystem.ArtifactRecord{
		ID:            "team/example",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte(body),
		Resources: map[string][]byte{
			"scripts/run.py": []byte("print('run')\n"),
		},
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	for _, d := range diags {
		if d.Code == "lint.prose_reference" {
			t.Errorf("unexpected diagnostic for resolvable reference: %s", d.Message)
		}
	}
}

func TestRuleProseReference_MissingBundledFileErrors(t *testing.T) {
	t.Parallel()
	body := "---\ntype: context\n---\nSee [the script](./scripts/missing.py)."
	rec := filesystem.ArtifactRecord{
		ID:            "team/example",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte(body),
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	gotErr := false
	for _, d := range diags {
		if d.Code == "lint.prose_reference" && d.Severity == lint.SeverityError {
			gotErr = true
			if !strings.Contains(d.Message, "missing.py") {
				t.Errorf("Message missing path: %s", d.Message)
			}
		}
	}
	if !gotErr {
		t.Errorf("missing diagnostic for unresolvable reference: %+v", diags)
	}
}

func TestRuleProseReference_RejectsPathEscape(t *testing.T) {
	t.Parallel()
	body := "---\ntype: context\n---\nSee [escape](../../etc/passwd)."
	rec := filesystem.ArtifactRecord{
		ID:            "team/example",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte(body),
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	gotErr := false
	for _, d := range diags {
		if d.Code == "lint.prose_reference" && strings.Contains(d.Message, "escapes") {
			gotErr = true
		}
	}
	if !gotErr {
		t.Errorf("missing escape error: %+v", diags)
	}
}

// Spec: §4.4 line 348 (F-4.4.1) — a prose reference that names another
// artifact resolves against the current visible catalog rather than being
// reported as a missing bundled file. The catalog is the linted record set.
func TestRuleProseReference_ResolvesAgainstCatalog(t *testing.T) {
	t.Parallel()
	referrer := filesystem.ArtifactRecord{
		ID:            "finance/ap/pay-invoice",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte("---\ntype: context\n---\nSee [the reconciler](finance/ap/reconcile)."),
	}
	target := filesystem.ArtifactRecord{
		ID:            "finance/ap/reconcile",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte("---\ntype: context\n---\nNo references here."),
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil,
		[]filesystem.ArtifactRecord{referrer, target})
	for _, d := range diags {
		if d.Code == "lint.prose_reference" {
			t.Errorf("reference to a catalog artifact should resolve: %s", d.Message)
		}
	}
}

// Spec: §4.4 line 348 (F-4.4.1) — an artifact reference carrying a §4.7.6
// version pin resolves against the catalog after the pin is stripped.
func TestRuleProseReference_ResolvesPinnedArtifact(t *testing.T) {
	t.Parallel()
	referrer := filesystem.ArtifactRecord{
		ID:            "finance/ap/pay-invoice",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte("---\ntype: context\n---\nPinned [reconcile](finance/ap/reconcile@1.2.0)."),
	}
	target := filesystem.ArtifactRecord{
		ID:            "finance/ap/reconcile",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte("---\ntype: context\n---\nNo references here."),
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil,
		[]filesystem.ArtifactRecord{referrer, target})
	for _, d := range diags {
		if d.Code == "lint.prose_reference" {
			t.Errorf("pinned reference to a catalog artifact should resolve: %s", d.Message)
		}
	}
}

// Spec: §4.4 lines 348-350 (F-4.4.1) — a reference that matches neither a
// bundled file nor any artifact in the catalog is an ingest error.
func TestRuleProseReference_UnknownArtifactErrors(t *testing.T) {
	t.Parallel()
	rec := filesystem.ArtifactRecord{
		ID:            "finance/ap/pay-invoice",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte("---\ntype: context\n---\nSee [ghost](finance/ap/does-not-exist)."),
	}
	diags := (&lint.Linter{}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	gotErr := false
	for _, d := range diags {
		if d.Code == "lint.prose_reference" && d.Severity == lint.SeverityError {
			gotErr = true
			if !strings.Contains(d.Message, "finance/ap/does-not-exist") {
				t.Errorf("Message should name the unresolved reference: %s", d.Message)
			}
		}
	}
	if !gotErr {
		t.Errorf("missing diagnostic for an unknown artifact reference: %+v", diags)
	}
}

// Spec: §4.4 line 347 (F-4.4.3) — a URL reference is valid only when HEAD
// returns 200 or a 3xx redirect. Other 2xx codes (201/204/206) do not
// confirm the named resource and are rejected.
func TestRuleProseReference_URLStatusRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status   int
		wantPass bool
	}{
		{http.StatusOK, true},                   // 200
		{http.StatusMovedPermanently, true},     // 301
		{http.StatusFound, true},                // 302
		{http.StatusTemporaryRedirect, true},    // 307
		{http.StatusPermanentRedirect, true},    // 308
		{http.StatusCreated, false},             // 201
		{http.StatusNoContent, false},           // 204
		{http.StatusPartialContent, false},      // 206
		{http.StatusNotFound, false},            // 404
		{http.StatusInternalServerError, false}, // 500
	}
	for _, tc := range cases {
		tc := tc
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Set Location so the client does not follow redirects to a
				// missing target; HEAD with no redirect-following returns the
				// 3xx status verbatim.
				if tc.status >= 300 && tc.status < 400 {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(tc.status)
			}))
			defer ts.Close()

			rec := filesystem.ArtifactRecord{
				ID:            "team/example",
				Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
				ArtifactBytes: []byte("---\ntype: context\n---\nLink [home](" + ts.URL + "/r)."),
			}
			rule := lint.NewProseReferenceRule(noRedirectClient())
			diags := (&lint.Linter{Rules: []lint.Rule{rule}}).Lint(
				context.Background(), nil, []filesystem.ArtifactRecord{rec})
			gotErr := false
			for _, d := range diags {
				if d.Code == "lint.prose_reference" {
					gotErr = true
				}
			}
			if tc.wantPass && gotErr {
				t.Errorf("status %d should pass the URL check, got diagnostic: %+v", tc.status, diags)
			}
			if !tc.wantPass && !gotErr {
				t.Errorf("status %d should fail the URL check, got none: %+v", tc.status, diags)
			}
		})
	}
}

// noRedirectClient returns an HTTP client that does not follow redirects, so a
// 3xx response surfaces verbatim to the linter rather than being chased to its
// (possibly missing) target.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Spec: §4.4 — URL references with HEAD 200 pass; a URL HEAD
// that returns 404 surfaces as an ingest error. The fixture
// drives the rule's HTTPClient field directly.
func TestRuleProseReference_URLHEAD(t *testing.T) {
	t.Parallel()
	hits := atomic.Int64{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodHead {
			t.Errorf("method = %q, want HEAD", r.Method)
		}
		if strings.HasSuffix(r.URL.Path, "/missing") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	body := "---\ntype: context\n---\nLive at [home](" + ts.URL + "/ok). Dead at [home](" + ts.URL + "/missing)."
	rec := filesystem.ArtifactRecord{
		ID:            "team/example",
		Artifact:      &manifest.Artifact{Type: manifest.TypeContext},
		ArtifactBytes: []byte(body),
	}
	rule := lint.NewProseReferenceRule(ts.Client())
	diags := (&lint.Linter{Rules: []lint.Rule{rule}}).Lint(context.Background(), nil, []filesystem.ArtifactRecord{rec})
	gotMissing := false
	for _, d := range diags {
		if d.Code == "lint.prose_reference" && strings.Contains(d.Message, "/missing") {
			gotMissing = true
		}
	}
	if !gotMissing {
		t.Errorf("missing /missing URL diagnostic: %+v", diags)
	}
	if hits.Load() < 2 {
		t.Errorf("HEAD hits = %d, want at least 2", hits.Load())
	}
}
