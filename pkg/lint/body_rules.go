package lint

import (
	"bytes"
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

func (ruleArtifactBodyForSkill) Code() string        { return "lint.skill_artifact_body" }

func (r ruleArtifactBodyForSkill) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
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

func (ruleProseReferenceResolution) Code() string        { return "lint.prose_reference" }

// proseLinkPattern matches Markdown links of the form [text](href)
// without escaped backslashes. The HTTP / HTTPS branch matches
// absolute URLs; everything else is treated as a relative bundled
// path.
var proseLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

func (r ruleProseReferenceResolution) Check(_ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	for _, rec := range records {
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
				if d := r.checkURL(rec.ID, href); d != nil {
					out = append(out, *d)
				}
				continue
			}
			if strings.HasPrefix(href, "#") {
				// Pure anchor — references the same document.
				continue
			}
			if d := r.checkBundled(rec, href); d != nil {
				out = append(out, *d)
			}
		}
	}
	return out
}

func (r ruleProseReferenceResolution) relevantBody(rec filesystem.ArtifactRecord) []byte {
	if rec.Artifact != nil && rec.Artifact.Type == manifest.TypeSkill && len(rec.SkillBytes) > 0 {
		return bodyAfterFrontmatter(rec.SkillBytes)
	}
	return bodyAfterFrontmatter(rec.ArtifactBytes)
}

func (r ruleProseReferenceResolution) checkBundled(rec filesystem.ArtifactRecord, href string) *Diagnostic {
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
	if _, ok := rec.Resources[clean]; ok {
		return nil
	}
	// Tolerate references to manifest files themselves.
	switch clean {
	case "ARTIFACT.md", "SKILL.md":
		return nil
	}
	return &Diagnostic{
		ArtifactID: rec.ID,
		Code:       "lint.prose_reference",
		Severity:   SeverityError,
		Message:    fmt.Sprintf("prose reference %q does not match any bundled file", href),
	}
}

func (r ruleProseReferenceResolution) checkURL(artifactID, href string) *Diagnostic {
	if r.HTTPClient == nil {
		return nil
	}
	ctx, cancel := contextWithTimeout(5 * time.Second)
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
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
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
