package vuln

import (
	"errors"
	"strings"
)

// PURL is a parsed Package URL per the spec at
// https://github.com/package-url/purl-spec.
//
// pkg:type/namespace/name@version?qualifiers#subpath
//
// Phase 17 ships parsing and equality. Range matching ("affected
// versions": "<4.17.21") lands when CVE feed ingestion stabilizes.
type PURL struct {
	Type      string
	Namespace string
	Name      string
	Version   string
}

// ErrInvalidPURL signals a malformed package URL.
var ErrInvalidPURL = errors.New("vuln: invalid PURL")

// ParsePURL decomposes a Package URL string. The grammar is the
// official PURL spec subset Podium consumes from SBOMs:
//
//	pkg:<type>/<namespace>/<name>@<version>
//	pkg:<type>/<name>@<version>     (no namespace)
func ParsePURL(s string) (PURL, error) {
	const prefix = "pkg:"
	if !strings.HasPrefix(s, prefix) {
		return PURL{}, ErrInvalidPURL
	}
	rest := s[len(prefix):]
	// Strip qualifiers and subpath; not consumed today.
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	atIdx := strings.LastIndex(rest, "@")
	var version string
	body := rest
	if atIdx >= 0 {
		body = rest[:atIdx]
		version = rest[atIdx+1:]
	}
	parts := strings.Split(body, "/")
	if len(parts) < 2 {
		return PURL{}, ErrInvalidPURL
	}
	p := PURL{Type: parts[0], Version: version}
	if p.Type == "" {
		return PURL{}, ErrInvalidPURL
	}
	if len(parts) == 2 {
		p.Name = parts[1]
	} else {
		p.Namespace = strings.Join(parts[1:len(parts)-1], "/")
		p.Name = parts[len(parts)-1]
	}
	if p.Name == "" {
		return PURL{}, ErrInvalidPURL
	}
	return p, nil
}

// String renders the PURL in canonical form.
func (p PURL) String() string {
	out := "pkg:" + p.Type + "/"
	if p.Namespace != "" {
		out += p.Namespace + "/"
	}
	out += p.Name
	if p.Version != "" {
		out += "@" + p.Version
	}
	return out
}

// SamePackage reports whether two PURLs refer to the same package
// regardless of version (type + namespace + name match).
func (p PURL) SamePackage(other PURL) bool {
	return p.Type == other.Type &&
		p.Namespace == other.Namespace &&
		p.Name == other.Name
}
