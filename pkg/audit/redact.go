package audit

import (
	"regexp"
	"strings"
)

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
	s.Add("phone-us", regexp.MustCompile(`\b(?:\+?1[-. ]?)?(?:\(?\d{3}\)?[-. ]?)\d{3}[-. ]?\d{4}\b`), "[phone-redacted]")
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
