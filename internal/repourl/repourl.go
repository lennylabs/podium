// Package repourl classifies a layer's repo value: which part of it is a URL
// credential, and whether any part of it may be reported. The store and the
// git source share it so the class the store splits is the class a response
// strips.
//
// Spec: §7.3.1 (Repository credentials)
package repourl

import (
	"net/url"
	"regexp"
	"strings"
)

// Redacted is what a response reports for a repo whose credential-bearing
// part cannot be identified.
const Redacted = "[redacted]"

// schemeSeparator ends the scheme of a URL that carries an authority.
const schemeSeparator = "://"

// schemeRE restates go-git's scheme test, which lives in an internal package.
// Testing it before url.Parse keeps scp-like remotes out of the parse-failure arm.
var schemeRE = regexp.MustCompile(`^[^:]+://`)

// Parts is a repo in the split class, taken apart.
type Parts struct {
	// Clean is the value a response reports: Redact of the registered value.
	Clean string
	// Bare is the registered bytes with the raw userinfo and its '@' removed.
	Bare string
	// RawUserinfo is the registered userinfo substring, unescaped and
	// possibly empty.
	RawUserinfo string
}

// Redact returns the form of a layer's repo that a response or a log line may
// carry.
//
// A value with no URL scheme, such as an scp-like remote or a filesystem path,
// is returned as registered. A value in a fail-closed class is returned as
// Redacted. A URL with no userinfo is returned byte-identical, and any other
// URL is returned with its whole userinfo removed. The username is removed
// with the password because a token is commonly the username of an https
// remote, which is why (*url.URL).Redacted does not fit.
//
// Redact is defined on its own and is not derived from Split. A value such as
// //tok@host/x://y parses with userinfo and an empty scheme, so it is outside
// the split class, and Redact still removes its userinfo.
func Redact(repo string) string {
	u, unsafe := parseRepo(repo)
	if unsafe {
		return Redacted
	}
	if u == nil || u.User == nil {
		return repo
	}
	u.User = nil
	return u.String()
}

// Unsafe reports whether repo is a URL whose credential-bearing part cannot be
// identified, so that no part of it may be reported.
func Unsafe(repo string) bool {
	_, unsafe := parseRepo(repo)
	return unsafe
}

// Split takes apart a repo in the split class: a value that parses as a URL
// with a non-empty scheme and with userinfo, and that is outside the
// fail-closed classes. It returns false for every other value.
//
// RawUserinfo and Bare are cut from the registered bytes, so Join of the two
// returns the registered value byte for byte. The parsed URL cannot serve for
// that, because url.URL.String re-escapes the userinfo and the path.
func Split(repo string) (Parts, bool) {
	u, unsafe := parseRepo(repo)
	if unsafe || u == nil || u.User == nil || u.Scheme == "" {
		return Parts{}, false
	}
	// url.Parse lowercases the scheme, so the registered prefix is compared
	// without regard to case and kept as registered.
	prefixLen := len(u.Scheme) + len(schemeSeparator)
	if len(repo) < prefixLen || !strings.EqualFold(repo[:prefixLen], u.Scheme+schemeSeparator) {
		return Parts{}, false
	}
	rest := repo[prefixLen:]
	authority := rest
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority = rest[:i]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		// This branch cannot run. url.Parse reads the authority as the same
		// span, the text after "//" up to the first '/', '?', or '#', and it
		// sets User only when that span contains '@'.
		return Parts{}, false
	}
	// Clean repeats the last arm of Redact on the URL already parsed, which
	// saves a second parse and yields the same bytes.
	u.User = nil
	return Parts{
		Clean:       u.String(),
		Bare:        repo[:prefixLen] + rest[at+1:],
		RawUserinfo: authority[:at],
	}, true
}

// Join is the inverse of Split. It returns bare with rawUserinfo and '@'
// inserted after the first "://", and false when bare has no "://".
func Join(bare, rawUserinfo string) (string, bool) {
	i := strings.Index(bare, schemeSeparator)
	if i < 0 {
		return "", false
	}
	head := i + len(schemeSeparator)
	return bare[:head] + rawUserinfo + "@" + bare[head:], true
}

// parseRepo classifies repo once for Redact, Unsafe, and Split. It returns a
// nil URL and false for a value with no scheme, a nil URL and true for a value
// in a fail-closed class, and the parsed URL and false otherwise.
//
// The fail-closed classes are a value with a scheme that url.Parse rejects,
// and an http or https value with '@' after its authority. An unescaped '/' in
// a credential ends the authority early, so the credential lands in the path
// and the URL parses with no userinfo or with the wrong one. The '@' check
// therefore runs whether or not the parsed URL carries userinfo.
func parseRepo(repo string) (u *url.URL, unsafe bool) {
	if !schemeRE.MatchString(repo) {
		return nil, false
	}
	u, err := url.Parse(repo)
	if err != nil {
		return nil, true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return u, false
	}
	_, rest, _ := strings.Cut(repo, "://")
	if i := strings.IndexAny(rest, "/?#"); i >= 0 && strings.Contains(rest[i:], "@") {
		return nil, true
	}
	return u, false
}
