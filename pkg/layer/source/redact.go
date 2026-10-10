package source

import (
	"regexp"

	"github.com/lennylabs/podium/internal/repourl"
)

// RedactedRepo is what a response reports for a repo whose credential-bearing
// part cannot be identified.
//
// Spec: §7.3.1 (Repository credentials)
const RedactedRepo = repourl.Redacted

// cloneErrorWithheld replaces the upstream clone error for a repo that
// RedactRepo reports as RedactedRepo. The upstream text quotes such a repo in
// a form the userinfo pattern cannot match.
const cloneErrorWithheld = "clone failed; the error text is withheld because the repository URL cannot be reported"

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
	return repourl.Redact(repo)
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
	if repourl.Unsafe(repo) {
		return cloneErrorWithheld
	}
	return urlUserinfoRE.ReplaceAllString(err.Error(), "$1")
}
