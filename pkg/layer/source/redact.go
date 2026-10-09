package source

import (
	"net/url"
	"regexp"
	"strings"
)

// RedactedRepo is what a response reports for a repo whose credential-bearing
// part cannot be identified.
//
// Spec: §7.3.1 (Repository credentials)
const RedactedRepo = "[redacted]"

// cloneErrorWithheld replaces the upstream clone error for a repo that
// RedactRepo reports as RedactedRepo. The upstream text quotes such a repo in
// a form the userinfo pattern cannot match.
const cloneErrorWithheld = "clone failed; the error text is withheld because the repository URL cannot be reported"

// schemeRE restates go-git's scheme test, which lives in an internal package.
// Testing it before url.Parse keeps scp-like remotes out of the parse-failure arm.
var schemeRE = regexp.MustCompile(`^[^:]+://`)

// urlUserinfoRE matches the userinfo of a URL inside error text. url.URL.String
// percent-escapes '/', '@', ':', '"', and whitespace in userinfo. go-git's
// Endpoint.String uses url.PathEscape, which leaves '@' and ':' raw, so the
// match is greedy to the last '@' before a '/', whitespace, or a quote.
var urlUserinfoRE = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/\s"]*@`)

// RedactRepo returns the form of a layer's repo that a response or a log line
// may carry. The stored value is never passed through it on the way to a clone.
//
// A value with no URL scheme, such as an scp-like remote or a filesystem path,
// is returned as stored. A value in a fail-closed class is returned as
// RedactedRepo. A URL with no userinfo is returned byte-identical, and any
// other URL is returned with its whole userinfo removed. The username is
// removed with the password because a token is commonly the username of an
// https remote, which is why (*url.URL).Redacted does not fit.
//
// Spec: §7.3.1 (Repository credentials)
func RedactRepo(repo string) string {
	u, unsafe := parseRepo(repo)
	if unsafe {
		return RedactedRepo
	}
	if u == nil || u.User == nil {
		return repo
	}
	u.User = nil
	return u.String()
}

// RedactCloneError returns the text of a failed clone's error with no URL
// userinfo. The caller passes a non-nil err. For a repo that RedactRepo reports
// as RedactedRepo, the upstream text is replaced with a fixed phrase. For every
// other repo, an scp-like value included, the userinfo of each URL in the text
// is removed by pattern, because go-git and net/http render the request URL in
// a form that differs from the stored string.
//
// Spec: §7.3.1 (Repository credentials)
func RedactCloneError(repo string, err error) string {
	if repoUnsafeToEcho(repo) {
		return cloneErrorWithheld
	}
	return urlUserinfoRE.ReplaceAllString(err.Error(), "$1")
}

// repoUnsafeToEcho reports whether repo is a URL whose credential-bearing part
// cannot be identified, so that no part of it may be reported.
//
// Spec: §7.3.1 (Repository credentials)
func repoUnsafeToEcho(repo string) bool {
	_, unsafe := parseRepo(repo)
	return unsafe
}

// parseRepo classifies repo once for RedactRepo and repoUnsafeToEcho. It
// returns a nil URL and false for a value with no scheme, a nil URL and true
// for a value in a fail-closed class, and the parsed URL and false otherwise.
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
