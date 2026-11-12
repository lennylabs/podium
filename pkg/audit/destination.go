package audit

import (
	"fmt"
	"net/url"
	"strings"
)

// destKind is the kind of an audit destination (§8.3). The values are also
// the destination label values of the audit metrics (§13.8).
type destKind string

const (
	kindFile   destKind = "file"
	kindStdout destKind = "stdout"
	kindHTTP   destKind = "http"
	kindOTLP   destKind = "otlp"
)

// The list variables a destination list comes from (§6.2, §13.12).
const (
	envLocalSink    = "PODIUM_AUDIT_SINK"
	envRegistryPath = "PODIUM_AUDIT_LOG_PATH"
	envTracing      = "PODIUM_TRACING"
)

// tokenStdout names the stdout destination, and tokenDefault names the
// default file in the local stream's list (§6.2, §8.3).
const (
	tokenStdout  = "stdout"
	tokenDefault = "default"
)

// defaultFileName is how an error names the default file destination, which
// NewFileSink resolves from an empty path.
const defaultFileName = "~/.podium/audit.log"

// destSpec is one parsed element of a destination list. A file destination
// carries its path, empty for the default file. A network destination
// carries its parsed URL.
type destSpec struct {
	kind destKind
	path string
	url  *url.URL
}

// name is how a log line or an error names the destination: the file path,
// "stdout", or redactedURL, never the raw element (§8.3 Transport).
func (d destSpec) name() string {
	switch d.kind {
	case kindFile:
		if d.path == "" {
			return defaultFileName
		}
		return d.path
	case kindStdout:
		return tokenStdout
	default:
		return redactedURL(d.url)
	}
}

// listVariable names the variable a list came from, which every refusal
// names (§13.12).
func listVariable(opts OpenOptions) string {
	if opts.Local {
		return envLocalSink
	}
	return envRegistryPath
}

// parseDestinations parses a §8.3 destination list. An empty value is the
// default file. Every refusal wraps ErrConfig, names the list variable, and
// names a URL element by redactedURL or by its scheme alone, because any
// such element can carry userinfo or a query that holds a credential.
//
// Spec: §8.3, §13.12
func parseDestinations(value string, opts OpenOptions) ([]destSpec, error) {
	variable := listVariable(opts)
	if value == "" {
		return []destSpec{{kind: kindFile}}, nil
	}
	var specs []destSpec
	seen := map[string]bool{}
	files := 0
	for i, raw := range strings.Split(value, ",") {
		elem := strings.TrimSpace(raw)
		spec, err := parseElement(elem, i, opts)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrConfig, variable, err)
		}
		if seen[elem] {
			return nil, fmt.Errorf("%w: %s: destination %s is listed twice", ErrConfig, variable, spec.name())
		}
		seen[elem] = true
		if spec.kind == kindFile {
			files++
			if files > 1 {
				return nil, fmt.Errorf("%w: %s: more than one file destination (second: %s)", ErrConfig, variable, spec.name())
			}
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// parseElement parses one trimmed list element at index i.
func parseElement(elem string, i int, opts OpenOptions) (destSpec, error) {
	switch {
	case elem == "":
		return destSpec{}, fmt.Errorf("element %d is empty", i+1)
	case strings.Contains(elem, "://"):
		return parseURLElement(elem)
	case elem == tokenStdout:
		return stdoutElement(opts)
	case elem == tokenDefault && opts.Local:
		return destSpec{kind: kindFile}, nil
	default:
		return destSpec{kind: kindFile, path: elem}, nil
	}
}

// stdoutElement accepts the stdout token only in the registry and only when
// tracing does not also write to standard output (§8.3, §13.12).
func stdoutElement(opts OpenOptions) (destSpec, error) {
	if opts.Local {
		return destSpec{}, fmt.Errorf("the stdout destination is registry-only, because the MCP server's standard output carries the MCP protocol")
	}
	if opts.TracingStdout {
		return destSpec{}, fmt.Errorf("the stdout destination cannot be listed while %s=stdout writes spans to standard output", envTracing)
	}
	return destSpec{kind: kindStdout}, nil
}

// parseURLElement parses an element that contains "://".
func parseURLElement(elem string) (destSpec, error) {
	scheme := elem[:strings.Index(elem, "://")]
	u, err := url.Parse(elem)
	if err != nil {
		return destSpec{}, fmt.Errorf("an element with scheme %s does not parse as a URL", schemeName(scheme))
	}
	var kind destKind
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		kind = kindHTTP
	case "otlp+http", "otlp+https":
		kind = kindOTLP
	default:
		return destSpec{}, fmt.Errorf("destination %s has an unrecognized scheme", redactedURL(u))
	}
	if u.User != nil {
		return destSpec{}, fmt.Errorf("destination %s carries userinfo; supply credentials through PODIUM_AUDIT_HEADERS_FILE", redactedURL(u))
	}
	return destSpec{kind: kind, url: u}, nil
}

// schemeName returns scheme when it is a well-formed URL scheme, and a
// placeholder otherwise, because the text before "://" of an element that
// does not parse can hold anything, a credential included.
func schemeName(scheme string) string {
	if scheme == "" {
		return "scheme-less"
	}
	for i, r := range scheme {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		other := (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.'
		if !letter && (i == 0 || !other) {
			return "malformed-scheme"
		}
	}
	return scheme
}

// redactedURL returns scheme://host/path, with no userinfo, query, or
// fragment, which is how every log line and error names a URL destination.
//
// Spec: §8.3
func redactedURL(u *url.URL) string {
	return u.Scheme + "://" + u.Host + u.Path
}
