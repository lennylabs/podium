// Package ghactions holds the GitHub Actions signing recipe of author
// attestation: the repository record that `podium layer register
// --trust-preset github-actions` and `podium attest init` read from the
// GitHub API, the trust policy the preset resolves from it, and the
// workflow that `podium attest init` writes.
//
// Spec: §7.3.1 (Trust preset), §7.3.5 (Writing the signing workflow)
package ghactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// APIBase is the GitHub API the preset and `podium attest init` read. A
// production binary sends the request nowhere else, so the token in GH_TOKEN
// or GITHUB_TOKEN reaches no other host.
const APIBase = "https://api.github.com"

// Host is the one repository host the recipe covers.
const Host = "github.com"

// DefaultTimeout is the deadline of the one API request when
// PODIUM_GITHUB_API_TIMEOUT is unset.
const DefaultTimeout = 30 * time.Second

// TimeoutEnv names the setting that replaces DefaultTimeout.
const TimeoutEnv = "PODIUM_GITHUB_API_TIMEOUT"

// maxRecordBytes bounds the response body the command reads. A repository
// record is a few kilobytes; a larger body is no record.
const maxRecordBytes = 1 << 20

// ErrNotGitHub reports a repository URL that is not on github.com in a form
// the recipe reads.
var ErrNotGitHub = errors.New("not a repository on github.com")

// Repo names a repository on github.com by its owner and name, as a URL of
// the repository spells them.
type Repo struct {
	Owner string
	Name  string
}

// FullName returns "<owner>/<name>", the form of the API's full_name.
func (r Repo) FullName() string { return r.Owner + "/" + r.Name }

// URL returns "https://github.com/<owner>/<name>".
func (r Repo) URL() string { return "https://" + Host + "/" + r.FullName() }

// segment is the form of an owner or repository name: the characters the
// host allows in them. The dot segments name a path a client resolves.
var segment = regexp.MustCompile(`\A[A-Za-z0-9._-]+\z`)

// ParseRepoURL reads the owner and name of a repository on github.com from
// raw. It accepts https://github.com/<owner>/<repo>, optionally followed by
// ".git" or "/", with no user information, port, query, or fragment, and the
// SSH forms git@github.com:<owner>/<repo> and
// ssh://git@github.com/<owner>/<repo>, optionally followed by ".git". The
// host compares without regard to letter case. Any other value returns an
// error wrapping ErrNotGitHub.
//
// Spec: §7.3.1 (Trust preset)
func ParseRepoURL(raw string) (Repo, error) {
	path, ok := githubPath(raw)
	if !ok {
		return Repo{}, fmt.Errorf("%w: %q", ErrNotGitHub, raw)
	}
	if trimmed, cut := strings.CutSuffix(path, ".git"); cut {
		path = trimmed
	} else if strings.HasPrefix(raw, "https://") {
		path = strings.TrimSuffix(path, "/")
	}
	owner, name, found := strings.Cut(path, "/")
	// A name that still ends in .git, as after ".git/", is neither form.
	if !found || !validSegment(owner) || !validSegment(name) || strings.HasSuffix(strings.ToLower(name), ".git") {
		return Repo{}, fmt.Errorf("%w: %q does not name <owner>/<repo>", ErrNotGitHub, raw)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// githubPath returns the path of raw after the github.com host, without its
// leading "/", for the URL forms ParseRepoURL accepts.
func githubPath(raw string) (string, bool) {
	if rest, ok := strings.CutPrefix(raw, "git@"); ok {
		host, path, found := strings.Cut(rest, ":")
		return path, found && strings.EqualFold(host, Host)
	}
	if strings.ContainsAny(raw, "%#?") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Port() != "" || strings.HasSuffix(u.Host, ":") ||
		!strings.EqualFold(u.Hostname(), Host) || !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	switch u.Scheme {
	case "https":
		if u.User != nil {
			return "", false
		}
	case "ssh":
		if u.User == nil || u.User.Username() != "git" {
			return "", false
		}
		if _, hasPassword := u.User.Password(); hasPassword {
			return "", false
		}
	default:
		return "", false
	}
	return strings.TrimPrefix(u.Path, "/"), true
}

func validSegment(s string) bool {
	return s != "." && s != ".." && segment.MatchString(s)
}

// Record is the part of the GitHub API repository record the recipe reads.
type Record struct {
	// ID is the repository's numeric identifier.
	ID int64
	// OwnerID is the numeric identifier of the repository's owner.
	OwnerID int64
	// FullName is the repository's current "<owner>/<name>".
	FullName string
	// DefaultBranch is the repository's default branch name.
	DefaultBranch string
	// Visibility is "public", "private", or "internal".
	Visibility string
}

// Public reports whether the record is of a public repository.
func (r Record) Public() bool { return r.Visibility == "public" }

// Repo returns the repository the record's full name names.
func (r Record) Repo() Repo { return repoNamed(r.FullName) }

// repoNamed returns the repository that "<owner>/<name>" names.
func repoNamed(fullName string) Repo {
	owner, name, _ := strings.Cut(fullName, "/")
	return Repo{Owner: owner, Name: name}
}

// StatusError reports an API answer other than 200.
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("the GitHub API answered HTTP %d", e.Code)
}

// Fetcher requests repository records from a GitHub API.
type Fetcher struct {
	// Base is the API's base URL. A production binary uses APIBase.
	Base string
	// Token is sent as a bearer token when it is not empty.
	Token string
	// Timeout is the deadline of the one request.
	Timeout time.Duration
	// Transport carries the request; nil uses http.DefaultTransport.
	Transport http.RoundTripper
}

// Fetch requests the record of repo with one GET request under the
// Fetcher's deadline. It does not retry. It follows a redirect only to the
// scheme and host of Base, which the API answers for a renamed repository,
// so the token reaches no other host. An answer other than 200 returns a
// *StatusError, and a body that is no repository record returns an error.
//
// Spec: §7.3.1 (Trust preset)
func (f Fetcher) Fetch(ctx context.Context, repo Repo) (Record, error) {
	base, err := url.Parse(strings.TrimSuffix(f.Base, "/"))
	if err != nil || base.Host == "" {
		return Record{}, fmt.Errorf("ghactions: API base %q is not a URL", f.Base)
	}
	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	target := base.String() + "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Record{}, fmt.Errorf("ghactions: build the request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "podium-cli")
	if f.Token != "" {
		req.Header.Set("Authorization", "Bearer "+f.Token)
	}
	client := &http.Client{Transport: f.Transport, CheckRedirect: sameOrigin(base)}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Record{}, fmt.Errorf("ghactions: request %s: no answer within %s (%s): %w", repo.FullName(), f.Timeout, TimeoutEnv, err)
		}
		return Record{}, fmt.Errorf("ghactions: request %s: %w", repo.FullName(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Record{}, &StatusError{Code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRecordBytes+1))
	if err != nil {
		return Record{}, fmt.Errorf("ghactions: read the record of %s: %w", repo.FullName(), err)
	}
	if len(body) > maxRecordBytes {
		return Record{}, fmt.Errorf("ghactions: the record of %s exceeds %d bytes", repo.FullName(), maxRecordBytes)
	}
	return decodeRecord(body)
}

// sameOrigin refuses a redirect to another scheme or host than base's, so
// the Authorization header goes nowhere else.
func sameOrigin(base *url.URL) func(*http.Request, []*http.Request) error {
	return func(next *http.Request, via []*http.Request) error {
		if !strings.EqualFold(next.URL.Scheme, base.Scheme) || !strings.EqualFold(next.URL.Host, base.Host) {
			return fmt.Errorf("ghactions: refused a redirect to %s://%s", next.URL.Scheme, next.URL.Host)
		}
		if len(via) >= 5 {
			return errors.New("ghactions: more than 5 redirects")
		}
		return nil
	}
}

// decodeRecord reads the members the recipe needs and refuses a record that
// lacks one, because each of them becomes part of a trust policy.
func decodeRecord(body []byte) (Record, error) {
	var raw struct {
		ID            int64  `json:"id"`
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Visibility    string `json:"visibility"`
		Private       *bool  `json:"private"`
		Owner         struct {
			ID int64 `json:"id"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Record{}, fmt.Errorf("ghactions: the answer is no repository record: %w", err)
	}
	rec := Record{ID: raw.ID, OwnerID: raw.Owner.ID, FullName: raw.FullName, DefaultBranch: raw.DefaultBranch, Visibility: raw.Visibility}
	// An older API version reports only "private"; a record that reports
	// neither is read as not public, which refuses the preset.
	if rec.Visibility == "" && raw.Private != nil && !*raw.Private {
		rec.Visibility = "public"
	}
	owner, name, found := strings.Cut(rec.FullName, "/")
	switch {
	case rec.ID <= 0 || rec.OwnerID <= 0:
		return Record{}, errors.New("ghactions: the repository record carries no repository or owner identifier")
	case !found || !validSegment(owner) || !validSegment(name):
		return Record{}, fmt.Errorf("ghactions: the repository record's full_name %q is not <owner>/<repo>", rec.FullName)
	case !validBranch(rec.DefaultBranch):
		return Record{}, fmt.Errorf("ghactions: the repository record's default_branch %q is no branch name", rec.DefaultBranch)
	}
	return rec, nil
}

// validBranch reports whether b is a branch name the recipe writes into a
// ref and a certificate identity: not empty, and free of white space,
// control characters, and the characters Git refuses in a ref.
func validBranch(b string) bool {
	if b == "" || strings.HasPrefix(b, "-") || strings.Contains(b, "..") {
		return false
	}
	for _, r := range b {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`~^:?*[\`, r) {
			return false
		}
	}
	return true
}

// TokenFromEnv returns the token in GH_TOKEN, or in GITHUB_TOKEN when
// GH_TOKEN is empty, as the gh tool reads them.
func TokenFromEnv(getenv func(string) string) string {
	if t := getenv("GH_TOKEN"); t != "" {
		return t
	}
	return getenv("GITHUB_TOKEN")
}

// TimeoutFromEnv reads PODIUM_GITHUB_API_TIMEOUT: a Go duration above zero.
// Surrounding white space is removed and an empty value counts as unset,
// which gives DefaultTimeout.
//
// Spec: §13.12 (PODIUM_GITHUB_API_TIMEOUT)
func TimeoutFromEnv(getenv func(string) string) (time.Duration, error) {
	v := strings.TrimSpace(getenv(TimeoutEnv))
	if v == "" {
		return DefaultTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s %q is not a duration above zero", TimeoutEnv, v)
	}
	return d, nil
}

// formatID writes a numeric identifier as the certificate field holds it.
func formatID(id int64) string { return strconv.FormatInt(id, 10) }
