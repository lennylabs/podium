package lint

import (
	"net/url"
	"regexp"
	"strings"
)

// proseLinkPattern matches a Markdown inline link of the form [text](target).
// The capture holds everything between the parentheses, which
// linkDestination reduces to the target. It runs only over text that
// stripCodeSpans has already cleared of inline code.
var proseLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

// proseLinkTargets returns the target of each Markdown inline link in body,
// in document order, skipping link syntax inside fenced code blocks and inline
// code spans. Link syntax inside code is example text, such as a template
// line or a `[text](url)` span that shows the syntax itself, so the §4.4
// check never sees it.
//
// The body is read line by line. A fenced code block opens on a line of three
// or more backticks or tildes, indented by at most three spaces, and closes on
// a line of the same character at least as long with nothing after it but
// white space. A fence that never closes runs to the end of the body, as
// CommonMark specifies, so the rest of the body is treated as code. Inline code
// spans are matched within one paragraph, a run of consecutive non-blank lines
// outside a fence, because CommonMark does not let a span cross a blank line.
// Indented code blocks are not recognized; §4.4 names only inline code spans
// and fenced code blocks.
//
// Spec: §4.4
func proseLinkTargets(body []byte) []string {
	var out []string
	var para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		text := stripCodeSpans(strings.Join(para, "\n"))
		for _, m := range proseLinkPattern.FindAllStringSubmatch(text, -1) {
			if target := linkDestination(m[1]); target != "" {
				out = append(out, target)
			}
		}
		para = para[:0]
	}
	var fence codeFence
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if fence.char != 0 {
			if fence.closedBy(line) {
				fence = codeFence{}
			}
			continue
		}
		if f, ok := openingFence(line); ok {
			flush()
			fence = f
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		para = append(para, line)
	}
	flush()
	return out
}

// codeFence is an open fenced code block: the fence character and the length
// of the opening run. The zero value means no fence is open.
type codeFence struct {
	char byte
	n    int
}

// fenceIndent returns line without the at most three leading spaces a fence
// line may carry, and false when the line is indented further, which makes it
// an indented line rather than a fence.
func fenceIndent(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	return trimmed, len(line)-len(trimmed) <= 3
}

// openingFence reports whether line opens a fenced code block. A backtick
// fence whose info string holds a backtick is an inline code span rather
// than a fence, as CommonMark specifies.
func openingFence(line string) (codeFence, bool) {
	s, ok := fenceIndent(line)
	if !ok || len(s) == 0 || (s[0] != '`' && s[0] != '~') {
		return codeFence{}, false
	}
	n := runLength(s, 0)
	if n < 3 {
		return codeFence{}, false
	}
	if s[0] == '`' && strings.IndexByte(s[n:], '`') >= 0 {
		return codeFence{}, false
	}
	return codeFence{char: s[0], n: n}, true
}

// closedBy reports whether line closes the fence f.
func (f codeFence) closedBy(line string) bool {
	s, ok := fenceIndent(line)
	if !ok || len(s) == 0 || s[0] != f.char {
		return false
	}
	n := runLength(s, 0)
	return n >= f.n && strings.TrimSpace(s[n:]) == ""
}

// runLength returns the length of the run of the byte at s[i] starting at i.
func runLength(s string, i int) int {
	j := i
	for j < len(s) && s[j] == s[i] {
		j++
	}
	return j - i
}

// stripCodeSpans replaces each inline code span in text with a space. A span
// opens on a run of one or more backticks and closes on the next run of the
// same length. A run with no closing run of its length is literal text, and a
// backslash-escaped backtick opens no span.
func stripCodeSpans(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		switch c := text[i]; {
		case c == '\\' && i+1 < len(text):
			b.WriteString(text[i : i+2])
			i += 2
		case c == '`':
			n := runLength(text, i)
			if end := closingRun(text, i+n, n); end >= 0 {
				b.WriteByte(' ')
				i = end
				continue
			}
			b.WriteString(text[i : i+n])
			i += n
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// closingRun returns the index just past the first run of exactly n
// backticks at or after from, or -1 when there is none.
func closingRun(text string, from, n int) int {
	for j := from; j < len(text); {
		if text[j] != '`' {
			j++
			continue
		}
		m := runLength(text, j)
		if m == n {
			return j + m
		}
		j += m
	}
	return -1
}

// linkDestination returns the target of a link from the text between its
// parentheses. A target in angle brackets runs to the closing bracket;
// otherwise it runs to the first white space, which separates it from an
// optional link title such as `"Title"`.
func linkDestination(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "<") {
		if end := strings.IndexByte(raw, '>'); end > 0 {
			return strings.TrimSpace(raw[1:end])
		}
		return raw
	}
	if i := strings.IndexAny(raw, " \t\n"); i >= 0 {
		return raw[:i]
	}
	return raw
}

// referenceLookupPath returns the path the bundled-file and artifact lookups
// read for a relative prose reference: the target without its fragment (from
// `#`) and its query (from `?`), percent-decoded, so `references/api.md#errors`
// looks up `references/api.md`. A target whose percent-encoding does not
// decode, such as `notes%zz.md`, is looked up as written, without its fragment
// and query, and the lookup then reports it like any other unresolved target.
//
// Spec: §4.4
func referenceLookupPath(target string) string {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	if i := strings.IndexByte(target, '?'); i >= 0 {
		target = target[:i]
	}
	if decoded, err := url.PathUnescape(target); err == nil {
		return decoded
	}
	return target
}
