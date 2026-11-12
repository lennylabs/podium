// Package repourl classifies a layer's repo value and builds the userinfo of a
// repository credential taken from separate members. It decides which part of
// a repo value is a URL credential, whether any part of it may be reported,
// which repo takes a credential, and which repo is refused for carrying one.
// The store, the git source, the registry layer handler, and the declared-layer
// seed share it so they apply one class rule: the class the store splits is
// the class a response strips, and the class a registration refuses.
//
// Spec: §7.3.1 (Repository credentials)
package repourl

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Redacted is what a response reports for a repo whose credential-bearing
// part cannot be identified.
const Redacted = "[redacted]"

// The errors WithCredential returns. Their messages name the failed condition
// and carry no part of the remote, the username, or the token, so a caller may
// wrap or log them.
var (
	// ErrCredentialRemote reports a remote that takes no credential from
	// separate members: one that is not an http or https URL, one in the
	// [redacted] class, or one that carries userinfo of its own.
	ErrCredentialRemote = errors.New("repourl: remote does not take a credential")
	// ErrCredentialUsername reports a username that contains a byte the
	// credential rule refuses.
	ErrCredentialUsername = errors.New("repourl: username contains a refused character")
	// ErrCredentialToken reports a token that is empty or contains a control
	// character.
	ErrCredentialToken = errors.New("repourl: token is empty or contains a control character")
)

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

// TakesCredential reports whether repo is a remote that takes a credential
// from separate members: an http or https URL outside the [redacted] class.
// url.Parse lowercases the scheme, so HTTPS:// qualifies. The value's own
// userinfo does not affect the answer, so a caller may test a stored
// registered value that already carries a credential.
//
// Spec: §7.3.1 (Repository credentials)
func TakesCredential(repo string) bool {
	u, unsafe := parseRepo(repo)
	return !unsafe && isHTTP(u)
}

// CarriesURLCredential reports whether repo carries, or may carry, a
// credential in its URL: an http or https URL with userinfo, an empty
// userinfo such as https://@host/x included, or a value in the [redacted]
// class.
//
// The class is the whole [redacted] class whatever the scheme, so it includes
// a schemed value of any scheme that url.Parse rejects, such as
// ssh://git@host:org/x.git. A caller that needs to tell that sub-case apart
// tests Unsafe and the scheme.
//
// Spec: §7.3.1 (Repository credentials)
func CarriesURLCredential(repo string) bool {
	u, unsafe := parseRepo(repo)
	return unsafe || (isHTTP(u) && u.User != nil)
}

// WithoutCredential returns repo with its userinfo and the '@' that ends it
// removed, and repo unchanged when it is outside the split class. Its one
// caller is the update handler's rotation and removal path in
// pkg/registry/server/layers.go, which rebuilds the stored remote from it.
//
// Spec: §7.3.1 (Setting, replacing, and removing a repository credential)
func WithoutCredential(repo string) string {
	if parts, ok := Split(repo); ok {
		return parts.Bare
	}
	return repo
}

// CredentialUsername returns the username of the credential in a registered
// repo value, and the empty string when the value holds none. A username
// counts only when the userinfo also carries a password: a legacy
// https://<token>@host value holds a token in the username position, so it
// holds no username.
//
// Spec: §7.3.1 (Setting, replacing, and removing a repository credential)
func CredentialUsername(registered string) string {
	u, _ := parseRepo(registered)
	if u == nil || u.User == nil {
		return ""
	}
	if _, ok := u.User.Password(); !ok {
		return ""
	}
	return u.User.Username()
}

// WithCredential returns bare with the credential built from username and
// token as its userinfo. It returns ErrCredentialRemote when bare takes no
// credential or carries userinfo of its own, ErrCredentialUsername when the
// username contains a refused byte, and ErrCredentialToken when the token is
// empty or contains a control character. An empty username yields a
// password-only userinfo.
//
// Spec: §7.3.1 (Setting, replacing, and removing a repository credential)
func WithCredential(bare, username, token string) (string, error) {
	u, unsafe := parseRepo(bare)
	if unsafe || !isHTTP(u) || u.User != nil {
		return "", ErrCredentialRemote
	}
	if !ValidUsername(username) {
		return "", ErrCredentialUsername
	}
	if !ValidToken(token) {
		return "", ErrCredentialToken
	}
	joined, ok := Join(bare, url.UserPassword(username, token).String())
	if !ok {
		// This branch cannot run. TakesCredential admitted bare, so it
		// matched schemeRE and contains "://".
		return "", ErrCredentialRemote
	}
	// This check cannot fail. url.Userinfo.String escapes '@', '/', '?', '#',
	// ':', '%', and the space inside the username and the password, and Join
	// inserts after the first "://", which schemeRE guarantees is the scheme
	// separator, so Split recovers bare exactly. It stays as a self-check on
	// the precedent of the store's split-and-join check.
	if parts, ok := Split(joined); !ok || parts.Bare != bare {
		return "", ErrCredentialRemote
	}
	return joined, nil
}

// ValidUsername reports whether username is free of the bytes the credential
// rule refuses: ':', '/', '"', the space, and control characters.
//
// ':' ends the username in userinfo. '/', '"', and the space are refused
// because a failed clone's transport error renders the URL with the raw
// username before ":***@", and RedactCloneError in pkg/layer/source removes
// userinfo with a pattern that stops at '/', whitespace, and '"'. A username
// holding one of them would leave part of itself in the
// ingest.source_unreachable message.
//
// Spec: §7.3.1 (Setting, replacing, and removing a repository credential)
func ValidUsername(username string) bool {
	return !strings.ContainsAny(username, ":/\" ") && !hasControlByte(username)
}

// ValidToken reports whether token is non-empty and free of control
// characters.
//
// Spec: §7.3.1 (Setting, replacing, and removing a repository credential)
func ValidToken(token string) bool {
	return token != "" && !hasControlByte(token)
}

// hasControlByte reports whether s contains a byte below 0x20 or equal to
// 0x7f.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// isHTTP reports whether u is a parsed http or https URL.
func isHTTP(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https")
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
