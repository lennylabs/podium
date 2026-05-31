package audit

import (
	"fmt"
	"regexp"
	"strings"
)

// PIIRedactionConfig is the §8.2 query-text scrub configuration surface.
// It selects whether scrubbing runs (default-on) and lets a deployment
// extend the default pattern set without recompiling. The registry and
// the MCP server both build a PIIScrubber from this config and apply it
// to free-text search queries before they reach an audit sink.
//
// spec: §8.2 — "Patterns configurable via PIIRedactionConfig. Default-on."
type PIIRedactionConfig struct {
	// Enabled is tri-state: nil (absent) means default-on, an explicit
	// false disables query-text scrubbing. A deployment turns the scrub
	// off only deliberately.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Patterns are extra regexes appended to the spec's default set
	// (SSN, credit-card, email, phone). Each compiles to a custom rule.
	Patterns []PIIPattern `yaml:"patterns,omitempty"`
}

// PIIPattern is one deployment-supplied scrub rule: a name, a regular
// expression, and the replacement string written in place of a match.
type PIIPattern struct {
	Name        string `yaml:"name,omitempty"`
	Regex       string `yaml:"regex"`
	Replacement string `yaml:"replacement,omitempty"`
}

// Active reports whether query-text scrubbing should run. Absent config
// (the zero value) is default-on per §8.2.
func (c PIIRedactionConfig) Active() bool { return c.Enabled == nil || *c.Enabled }

// BuildScrubber returns a scrubber pre-loaded with the spec's default
// patterns plus any configured custom patterns. It returns nil when the
// config disables scrubbing, so callers treat nil as "scrub off." An
// invalid custom regex is a configuration error.
func (c PIIRedactionConfig) BuildScrubber() (*PIIScrubber, error) {
	if !c.Active() {
		return nil, nil
	}
	s := NewPIIScrubber()
	for _, p := range c.Patterns {
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			return nil, fmt.Errorf("audit: pii_redaction pattern %q: %w", p.Name, err)
		}
		repl := p.Replacement
		if repl == "" {
			repl = "[redacted]"
		}
		name := p.Name
		if name == "" {
			name = "custom"
		}
		s.Add(name, re, repl)
	}
	return s, nil
}

// PIIScrubber rewrites query text and user-supplied strings to hide
// common PII before they land in audit logs. Default-on per §8.2.
//
// The patterns below catch the spec's named cases: SSN, credit-card,
// email, phone. Custom patterns can be added via Add.
type PIIScrubber struct {
	patterns []namedPattern
}

type namedPattern struct {
	name string
	re   *regexp.Regexp
	repl string
}

// NewPIIScrubber returns a scrubber pre-loaded with the default
// patterns the spec lists.
func NewPIIScrubber() *PIIScrubber {
	s := &PIIScrubber{}
	s.Add("ssn", regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), "[ssn-redacted]")
	s.Add("credit-card", regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`), "[cc-redacted]")
	s.Add("email", regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`), "[email-redacted]")
	s.Add("phone-us", regexp.MustCompile(`(?:\+?1[-. ]?)?\(?\d{3}\)?[-. ]?\d{3}[-. ]?\d{4}`), "[phone-redacted]")
	return s
}

// Add registers a custom pattern + replacement. Useful for
// org-specific PII shapes that the defaults miss.
func (s *PIIScrubber) Add(name string, re *regexp.Regexp, replacement string) {
	s.patterns = append(s.patterns, namedPattern{name: name, re: re, repl: replacement})
}

// Scrub returns input with every configured pattern's matches
// replaced by their replacement string. Non-matching text passes
// through unchanged.
func (s *PIIScrubber) Scrub(input string) string {
	out := input
	for _, p := range s.patterns {
		out = p.re.ReplaceAllString(out, p.repl)
	}
	return out
}

// ScrubEvent applies query-text scrubbing to a search event before it is
// written to a sink (§8.2 "Query text ... before being written to
// audit"). It scrubs the free-text "query" context value on
// artifacts.searched and domains.searched events and leaves every other
// event untouched. A nil scrubber (scrubbing disabled) returns the event
// unchanged. The event's context map is copied before mutation so a
// caller's shared map is never altered in place.
func (s *PIIScrubber) ScrubEvent(e Event) Event {
	if s == nil {
		return e
	}
	switch e.Type {
	case EventArtifactsSearched, EventDomainsSearched:
	default:
		return e
	}
	q, ok := e.Context["query"]
	if !ok || q == "" {
		return e
	}
	scrubbed := s.Scrub(q)
	if scrubbed == q {
		return e
	}
	c := make(map[string]string, len(e.Context))
	for k, v := range e.Context {
		c[k] = v
	}
	c["query"] = scrubbed
	e.Context = c
	return e
}

// RedactFields produces a copy of fields with the named keys' values
// replaced by "[redacted]". Used to honor manifest-declared
// redaction directives (§8.2 first surface): the artifact author
// names which fields the registry must redact in audit.
func RedactFields(fields map[string]string, redactKeys []string) map[string]string {
	if len(fields) == 0 {
		return fields
	}
	keys := map[string]bool{}
	for _, k := range redactKeys {
		keys[strings.ToLower(k)] = true
	}
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		if keys[strings.ToLower(k)] {
			out[k] = "[redacted]"
		} else {
			out[k] = v
		}
	}
	return out
}
