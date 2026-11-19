package lint

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/version"
)

// ruleArtifactBodyForSkill enforces §4.3.4: a skill's ARTIFACT.md body
// must be empty or a single HTML comment; anything else warns.
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
// whitespace) per §4.3.4.
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
	return !bytes.Contains(inner, []byte("-->"))
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

// ruleProseReferenceResolution implements §4.4: prose references in the
// manifest body must resolve to bundled files (existence check), URLs (HTTP
// HEAD returning 200/3xx), or other artifacts (registry-side resolution). A
// prose reference is the destination of a Markdown inline link or image as
// CommonMark parses the body (proseLinkTargets). The default linter checks
// bundled files; URL HEAD checks are gated behind the rule's HTTPClient field
// for tests + offline ingest.
type ruleProseReferenceResolution struct {
	// HTTPClient drives URL HEAD checks. When nil, URL references
	// are not validated; offline ingest sees a single notice.
	HTTPClient *http.Client
	// Concurrency is the most probes the run holds in flight (§13.12
	// PODIUM_LINT_URL_CONCURRENCY). A value outside 1 to MaxURLConcurrency
	// takes the default.
	Concurrency int
	// Logf receives the progress lines of a long run. Nil logs nothing.
	Logf func(format string, args ...any)
	// now reads the clock that paces the progress lines; nil is time.Now.
	// Tests substitute it.
	now func() time.Time
	// timeout overrides the §4.4 probe deadline for tests; zero is
	// urlProbeTimeout.
	timeout time.Duration
}

// urlProgressThreshold is the distinct-URL count above which a run logs its
// progress, and urlProgressInterval the least time between two progress
// lines (§4.4).
const (
	urlProgressThreshold = 20
	urlProgressInterval  = 10 * time.Second
	urlProbeTimeout      = 5 * time.Second
)

// NewProseReferenceRule returns a Rule that validates prose
// references against bundled files (always) and against URL HEAD
// checks (when client is non-nil). Callers that ingest offline
// pass nil to skip the network probe.
func NewProseReferenceRule(client *http.Client) Rule {
	return ruleProseReferenceResolution{HTTPClient: client, Concurrency: DefaultURLConcurrency}
}

func (ruleProseReferenceResolution) Code() string { return "lint.prose_reference" }

func (r ruleProseReferenceResolution) Check(ctx context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	var out []Diagnostic
	// Spec: §4.4 — a prose reference may resolve to another
	// artifact, so build the visible catalog once up front and resolve
	// non-bundled references against it. The registry argument is nil at
	// ingest (pkg/registry/ingest/ingest.go), so the catalog is the linted
	// record set: the full registry walk for `podium lint`, the layer's own
	// records at single-layer ingest.
	catalog := artifactCatalog(records)
	// Spec: §4.4 — URL references are gathered first and probed once per
	// distinct URL after the scan, concurrently, so each reference takes its
	// URL's outcome in its own position.
	type urlRef struct {
		artifactID, href string
		at               int
	}
	var refs []urlRef
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
		for _, href := range proseLinkTargets(body) {
			if scheme, ok := urlScheme(href); ok {
				// Spec: §4.4 — an http or https target is a URL reference. A
				// target with any other scheme, such as mailto:, is not checked.
				if (scheme == "http" || scheme == "https") && r.HTTPClient != nil {
					refs = append(refs, urlRef{artifactID: rec.ID, href: href, at: len(out)})
				}
				continue
			}
			if strings.HasPrefix(href, "#") {
				// Spec: §4.4 — a target that is only a fragment refers to
				// the same document and is not checked.
				continue
			}
			if strings.HasPrefix(href, "/") {
				// Spec: §4.4 — a root-relative target names a path on a
				// website, such as /features/analytics. Bundled-file and
				// artifact references are relative to the package, so it
				// names neither and is not checked.
				continue
			}
			if d := r.checkReference(rec, href, catalog); d != nil {
				out = append(out, *d)
			}
		}
	}
	if len(refs) == 0 {
		return out
	}
	hrefs := make([]string, 0, len(refs))
	for _, ref := range refs {
		hrefs = append(hrefs, ref.href)
	}
	failures := r.probeURLs(ctx, hrefs)
	// Each failed reference is inserted at the position its scan reached, so
	// the diagnostics keep the order of a serial check.
	merged := make([]Diagnostic, 0, len(out)+len(refs))
	next := 0
	for _, ref := range refs {
		merged = append(merged, out[next:ref.at]...)
		next = ref.at
		if msg, failed := failures[ref.href]; failed {
			merged = append(merged, Diagnostic{
				ArtifactID: ref.artifactID,
				Code:       "lint.prose_reference",
				Severity:   SeverityError,
				Message:    msg,
			})
		}
	}
	return append(merged, out[next:]...)
}

// probeURLs probes each distinct URL of hrefs once, at most r.Concurrency at
// a time, and returns the failure message of each URL that failed. A run
// above urlProgressThreshold distinct URLs logs its start, its progress at
// most every urlProgressInterval, and its closing counts through r.Logf.
// Cancellation stops the workers before their next probe, and a URL left
// unprobed reports no failure.
//
// Spec: §4.4
func (r ruleProseReferenceResolution) probeURLs(ctx context.Context, hrefs []string) map[string]string {
	var distinct []string
	seen := map[string]bool{}
	for _, h := range hrefs {
		if !seen[h] {
			seen[h] = true
			distinct = append(distinct, h)
		}
	}
	workers := r.Concurrency
	if workers < 1 || workers > MaxURLConcurrency {
		workers = DefaultURLConcurrency
	}
	workers = min(workers, len(distinct))
	now := r.now
	if now == nil {
		now = time.Now
	}
	logf := func(format string, args ...any) {}
	if r.Logf != nil && len(distinct) > urlProgressThreshold {
		logf = r.Logf
	}
	start := now()
	logf("lint.prose_reference: checking %d URLs, %d at a time", len(distinct), workers)

	var (
		mu       sync.Mutex
		failures = map[string]string{}
		done     int
		lastLog  = start
	)
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for href := range jobs {
				msg, failed := r.probeURL(ctx, href)
				mu.Lock()
				done++
				if failed {
					failures[href] = msg
				}
				if t := now(); t.Sub(lastLog) >= urlProgressInterval && done < len(distinct) {
					lastLog = t
					logf("lint.prose_reference: checked %d of %d URLs", done, len(distinct))
				}
				mu.Unlock()
			}
		}()
	}
	for _, href := range distinct {
		if ctx.Err() != nil {
			break
		}
		jobs <- href
	}
	close(jobs)
	wg.Wait()
	logf("lint.prose_reference: checked %d URLs in %s, %d failed", done, now().Sub(start).Round(time.Millisecond), len(failures))
	return failures
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

func (r ruleProseReferenceResolution) relevantBody(rec filesystem.ArtifactRecord) []byte {
	if rec.Artifact != nil && rec.Artifact.Type == manifest.TypeSkill && len(rec.SkillBytes) > 0 {
		return bodyAfterFrontmatter(rec.SkillBytes)
	}
	return bodyAfterFrontmatter(rec.ArtifactBytes)
}

// checkReference resolves a non-URL prose reference against the three §4.4
// targets in order: bundled files (existence check), the artifact's own
// manifest files, and other artifacts (registry-side resolution against the
// current visible catalog). The lookups read the target without its fragment
// and query and with its percent-encoding decoded (referenceLookupPath). It
// returns a warning when the reference leaves the package and an error when
// it matches none of the three; the message quotes the target as written.
//
// Spec: §4.4
func (r ruleProseReferenceResolution) checkReference(rec filesystem.ArtifactRecord, href string, catalog map[string]bool) *Diagnostic {
	clean := path.Clean(strings.TrimPrefix(referenceLookupPath(href), "./"))
	if clean == "." || clean == "" {
		return nil
	}
	if strings.HasPrefix(clean, "../") || clean == ".." {
		// A target outside the package names no bundled file. It resolves
		// only while the artifacts sit side by side in one source tree, which
		// a consumer that installs one of them does not reproduce, so it is a
		// warning that points at the canonical-ID form rather than an error.
		return &Diagnostic{
			ArtifactID: rec.ID,
			Code:       "lint.prose_reference",
			Severity:   SeverityWarning,
			Message: fmt.Sprintf("prose reference %q leaves the artifact package; it resolves only while the artifacts sit side by side in one source tree. "+
				"Name the other artifact by its canonical ID instead", href),
		}
	}
	// §4.4: bundled files (existence check).
	if _, ok := rec.Resources[clean]; ok {
		return nil
	}
	// §4.4: a directory of the package resolves when a bundled file lies
	// below it, so `[templates](assets/templates/)` names the bundled
	// templates. path.Clean already dropped the trailing slash.
	if bundledDirectory(rec.Resources, clean) {
		return nil
	}
	// Tolerate references to the artifact's own manifest files.
	switch clean {
	case "ARTIFACT.md", "SKILL.md":
		return nil
	}
	// §4.4: other artifacts (registry-side resolution against the current
	// visible catalog). A reference that names another artifact by its
	// canonical ID resolves; an unknown ID is the ingest error §4.4 mandates.
	// Strip any §4.7.6 version pin before the lookup.
	if catalog[version.StripPin(clean)] {
		return nil
	}
	return &Diagnostic{
		ArtifactID: rec.ID,
		Code:       "lint.prose_reference",
		Severity:   SeverityError,
		Message:    fmt.Sprintf("prose reference %q does not resolve to a bundled file or a known artifact", href),
	}
}

// bundledDirectory reports whether dir names a directory of the package that
// holds at least one bundled file.
func bundledDirectory(resources map[string][]byte, dir string) bool {
	prefix := dir + "/"
	for name := range resources {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// probeURL sends one HEAD for href under the §4.4 probe deadline and returns
// the failure message and true when the reference does not resolve.
func (r ruleProseReferenceResolution) probeURL(ctx context.Context, href string) (string, bool) {
	deadline := urlProbeTimeout
	if r.timeout > 0 {
		deadline = r.timeout
	}
	ctx, cancel := contextWithTimeout(ctx, deadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, href, nil)
	if err != nil {
		return fmt.Sprintf("prose URL %q cannot be requested: %v", href, err), true
	}
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return fmt.Sprintf("prose URL %q HEAD failed: %v", href, err), true
	}
	defer func() { _ = resp.Body.Close() }()
	// Spec: §4.4 — a URL reference is valid when HEAD returns 200
	// or any 3xx redirect. Other 2xx codes (201, 204, 206) do not confirm
	// the named resource is served, so they are rejected.
	if resp.StatusCode == http.StatusOK || (resp.StatusCode >= 300 && resp.StatusCode < 400) {
		return "", false
	}
	return fmt.Sprintf("prose URL %q HEAD returned HTTP %d", href, resp.StatusCode), true
}

// urlScheme returns the lowercased URL scheme of href and true when href
// begins with one: the RFC 3986 production, a letter followed by letters,
// digits, "+", "-", or ".", and then ":". A relative path such as
// `references/a:b.md` carries no scheme, because its "/" comes before the
// ":". The web viewer applies the same production when it decides that a
// reference addresses something outside the registry.
func urlScheme(href string) (string, bool) {
	i := strings.IndexByte(href, ':')
	if i < 1 || !isASCIILetter(href[0]) {
		return "", false
	}
	for _, c := range []byte(href[1:i]) {
		if !isSchemeByte(c) {
			return "", false
		}
	}
	return strings.ToLower(href[:i]), true
}

func isASCIILetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// isSchemeByte reports whether c can follow the first letter of a scheme.
func isSchemeByte(c byte) bool {
	return isASCIILetter(c) || ('0' <= c && c <= '9') || c == '+' || c == '-' || c == '.'
}
