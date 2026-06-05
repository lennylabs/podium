package lint

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// ruleArtifactBodyForSkill enforces §4.3.4 line 286: a skill's
// ARTIFACT.md body must be empty or a single HTML comment;
// anything else warns.
type ruleArtifactBodyForSkill struct{}

func (ruleArtifactBodyForSkill) Code() string { return "lint.skill_artifact_body" }

func (r ruleArtifactBodyForSkill) Check(_ context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
		if rec.Artifact == nil || rec.Artifact.Type != manifest.TypeSkill {
			continue
		}
		body := bodyAfterFrontmatter(rec.ArtifactBytes)
		if !skillArtifactBodyIsAllowed(body) {
			out = append(out, Diagnostic{
				ArtifactID: rec.ID,
				Code:       r.Code(),
				Severity:   SeverityWarning,
				Message:    "ARTIFACT.md body for a skill must be empty or a single HTML comment",
			})
		}
	}
	return out
}

// skillArtifactBodyIsAllowed returns true when body is whitespace
// only or a single HTML-comment block (with optional surrounding
// whitespace) per §4.3.4 line 286.
func skillArtifactBodyIsAllowed(body []byte) bool {
	stripped := bytes.TrimSpace(body)
	if len(stripped) == 0 {
		return true
	}
	if !bytes.HasPrefix(stripped, []byte("<!--")) || !bytes.HasSuffix(stripped, []byte("-->")) {
		return false
	}
	// Reject embedded comment delimiters that would mean two
	// comments back to back.
	inner := stripped[len("<!--") : len(stripped)-len("-->")]
	if bytes.Contains(inner, []byte("-->")) {
		return false
	}
	return true
}

// bodyAfterFrontmatter returns everything after the closing `---`
// of the YAML frontmatter, or the full input when no frontmatter
// is present.
func bodyAfterFrontmatter(src []byte) []byte {
	if !bytes.HasPrefix(src, []byte("---")) {
		return src
	}
	rest := src[3:]
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return rest
	}
	body := rest[idx+len("\n---"):]
	// Skip the newline trailing the closing fence.
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	return body
}

// ruleProseReferenceResolution implements §4.4 lines 336-342:
// prose references in the manifest body must resolve to bundled
// files (existence check), URLs (HTTP HEAD returning 200/3xx),
// or other artifacts (registry-side resolution). The default
// linter checks bundled files; URL HEAD checks are gated behind
// the rule's HTTPClient field for tests + offline ingest.
type ruleProseReferenceResolution struct {
	// HTTPClient drives URL HEAD checks. When nil, URL references
	// are not validated; offline ingest sees a single notice.
	HTTPClient *http.Client
}

// NewProseReferenceRule returns a Rule that validates prose
// references against bundled files (always) and against URL HEAD
// checks (when client is non-nil). Callers that ingest offline
// pass nil to skip the network probe.
func NewProseReferenceRule(client *http.Client) Rule {
	return ruleProseReferenceResolution{HTTPClient: client}
}

func (ruleProseReferenceResolution) Code() string { return "lint.prose_reference" }

// proseLinkPattern matches Markdown links of the form [text](href)
// without escaped backslashes. The HTTP / HTTPS branch matches
// absolute URLs; everything else is treated as a relative bundled
// path.
var proseLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

func (r ruleProseReferenceResolution) Check(ctx context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	// spec: §4.4 line 348 — a prose reference may resolve to another
	// artifact, so build the visible catalog once up front and resolve
	// non-bundled references against it. The registry argument is nil at
	// ingest (pkg/registry/ingest/ingest.go), so the catalog is the linted
	// record set: the full registry walk for `podium lint`, the layer's own
	// records at single-layer ingest.
	catalog := artifactCatalog(records)
	// spec: §9.3 — this rule scans every record's body and may issue a URL
	// HEAD per reference, so it is the long-running work the constraint
	// names; it checks for cancellation before each record.
	for _, rec := range records {
		if ctx.Err() != nil {
			break
		}
		body := r.relevantBody(rec)
		if len(body) == 0 {
			continue
		}
		for _, match := range proseLinkPattern.FindAllSubmatch(body, -1) {
			href := strings.TrimSpace(string(match[1]))
			if href == "" {
				continue
			}
			if isAbsoluteURL(href) {
				if d := r.checkURL(ctx, rec.ID, href); d != nil {
					out = append(out, *d)
				}
				continue
			}
			if strings.HasPrefix(href, "#") {
				// Pure anchor — references the same document.
				continue
			}
			if d := r.checkReference(rec, href, catalog); d != nil {
				out = append(out, *d)
			}
		}
	}
	return out
}

// artifactCatalog returns the set of canonical artifact IDs in the linted
// record set, the §4.4 "current visible catalog" a cross-artifact prose
// reference resolves against. For `podium lint` this is the full registry
// walk; at single-layer ingest it is the layer's own records.
func artifactCatalog(records []filesystem.ArtifactRecord) map[string]bool {
	m := make(map[string]bool, len(records))
	for _, rec := range records {
		m[rec.ID] = true
	}
	return m
}

// stripVersionPin removes a trailing §4.7.6 pin (@<semver>, @<semver>.x, or
// @sha256:<hash>) from an artifact reference so the bare ID matches a
// catalog entry.
func stripVersionPin(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i]
	}
	return ref
}

func (r ruleProseReferenceResolution) relevantBody(rec filesystem.ArtifactRecord) []byte {
	if rec.Artifact != nil && rec.Artifact.Type == manifest.TypeSkill && len(rec.SkillBytes) > 0 {
		return bodyAfterFrontmatter(rec.SkillBytes)
	}
	return bodyAfterFrontmatter(rec.ArtifactBytes)
}

// checkReference resolves a non-URL prose reference against the three §4.4
// targets in order: bundled files (existence check), the artifact's own
// manifest files, and other artifacts (registry-side resolution against the
// current visible catalog). It returns a diagnostic only when the reference
// escapes the package or matches none of the three.
func (r ruleProseReferenceResolution) checkReference(rec filesystem.ArtifactRecord, href string, catalog map[string]bool) *Diagnostic {
	clean := path.Clean(strings.TrimPrefix(href, "./"))
	if clean == "." || clean == "" {
		return nil
	}
	if strings.HasPrefix(clean, "../") || clean == ".." {
		return &Diagnostic{
			ArtifactID: rec.ID,
			Code:       "lint.prose_reference",
			Severity:   SeverityError,
			Message:    fmt.Sprintf("prose reference %q escapes the artifact package", href),
		}
	}
	// §4.4 line 346: bundled files (existence check).
	if _, ok := rec.Resources[clean]; ok {
		return nil
	}
	// Tolerate references to the artifact's own manifest files.
	switch clean {
	case "ARTIFACT.md", "SKILL.md":
		return nil
	}
	// §4.4 line 348: other artifacts (registry-side resolution against the
	// current visible catalog). A reference that names another artifact by
	// its canonical ID resolves; an unknown ID is the ingest error §4.4
	// line 350 mandates. Strip any §4.7.6 version pin before the lookup.
	if catalog[stripVersionPin(clean)] {
		return nil
	}
	return &Diagnostic{
		ArtifactID: rec.ID,
		Code:       "lint.prose_reference",
		Severity:   SeverityError,
		Message:    fmt.Sprintf("prose reference %q does not resolve to a bundled file or a known artifact", href),
	}
}

func (r ruleProseReferenceResolution) checkURL(ctx context.Context, artifactID, href string) *Diagnostic {
	if r.HTTPClient == nil {
		return nil
	}
	ctx, cancel := contextWithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, href, nil)
	if err != nil {
		return &Diagnostic{
			ArtifactID: artifactID,
			Code:       "lint.prose_reference",
			Severity:   SeverityError,
			Message:    fmt.Sprintf("prose URL %q cannot be requested: %v", href, err),
		}
	}
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return &Diagnostic{
			ArtifactID: artifactID,
			Code:       "lint.prose_reference",
			Severity:   SeverityError,
			Message:    fmt.Sprintf("prose URL %q HEAD failed: %v", href, err),
		}
	}
	defer resp.Body.Close()
	// spec: §4.4 line 347 — a URL reference is valid when HEAD returns 200
	// or any 3xx redirect. Other 2xx codes (201, 204, 206) do not confirm
	// the named resource is served, so they are rejected.
	if resp.StatusCode == http.StatusOK || (resp.StatusCode >= 300 && resp.StatusCode < 400) {
		return nil
	}
	return &Diagnostic{
		ArtifactID: artifactID,
		Code:       "lint.prose_reference",
		Severity:   SeverityError,
		Message:    fmt.Sprintf("prose URL %q HEAD returned HTTP %d", href, resp.StatusCode),
	}
}

func isAbsoluteURL(href string) bool {
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return true
	}
	return false
}
