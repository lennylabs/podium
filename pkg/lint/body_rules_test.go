package lint_test

import (
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
			diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
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
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
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
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
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
	diags := (&lint.Linter{}).Lint(nil, []filesystem.ArtifactRecord{rec})
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
	diags := (&lint.Linter{Rules: []lint.Rule{rule}}).Lint(nil, []filesystem.ArtifactRecord{rec})
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
