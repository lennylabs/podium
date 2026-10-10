package source_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/source"
)

// withheldCloneError restates the unexported fixed phrase that replaces the
// upstream clone error for a repo reported as source.RedactedRepo.
const withheldCloneError = "clone failed; the error text is withheld because the repository URL cannot be reported"

// credentialParts are fragments of the distinctive credential fixtures. No
// redacted output may contain any of them.
var credentialParts = []string{"alice-user", "s3cr3t", "s3@cr3t", "ghp_tok", "bob@"}

// unsafeRepos are the repo values that RedactRepo reports as
// source.RedactedRepo.
var unsafeRepos = []string{
	"https://alice-user:s3cr3t\x7fpw@host/x",
	"https://alice-user:s3cr3t/pw@host/x",
	"https://ghp_tok/3n@host/x.git",
	"https://alice-user:12/s3cr3tpw@host/x",
	"https://bob@ghp_tok/3n@host/x.git",
	"https://git.acme.com/acme/x.git?ref=a@b",
}

func assertNoCredential(t *testing.T, got string) {
	t.Helper()
	for _, part := range credentialParts {
		if strings.Contains(got, part) {
			t.Errorf("output %q contains credential part %q", got, part)
		}
	}
}

// Spec: §7.3.1 (Repository credentials)
func TestRedactRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"user and password", "https://alice-user:s3cr3tpw@git.acme.com/acme/private.git", "https://git.acme.com/acme/private.git"},
		{"username-only token", "https://ghp_tok3n@git.acme.com/acme/private.git", "https://git.acme.com/acme/private.git"},
		{"http with port", "http://alice-user:s3cr3tpw@git.acme.com:8080/acme/x.git", "http://git.acme.com:8080/acme/x.git"},
		{"uppercase scheme with userinfo", "HTTPS://alice-user:s3cr3tpw@git.acme.com/acme/x.git", "https://git.acme.com/acme/x.git"},
		{"ssh user and password", "ssh://git:s3cr3tpw@git.acme.com/acme/x.git", "ssh://git.acme.com/acme/x.git"},
		{"ssh username", "ssh://git@git.acme.com/acme/x.git", "ssh://git.acme.com/acme/x.git"},
		{"no userinfo", "https://git.acme.com/acme/x.git", "https://git.acme.com/acme/x.git"},
		// u.String() would escape the space.
		{"no userinfo, space in path", "https://git.acme.com/acme/my repo.git", "https://git.acme.com/acme/my repo.git"},
		// u.String() would lowercase the scheme.
		{"no userinfo, uppercase scheme", "HTTPS://git.acme.com/acme/x.git", "HTTPS://git.acme.com/acme/x.git"},
		{"scp-like remote", "git@github.com:acme/x.git", "git@github.com:acme/x.git"},
		{"filesystem path", "/srv/git/acme.git", "/srv/git/acme.git"},
		{"file URL with @ in path", "file:///srv/git/a@b.git", "file:///srv/git/a@b.git"},
		{"empty", "", ""},
		{"network-path reference", "//tok@host/x://y", "//host/x://y"},
		{"control character fails to parse", unsafeRepos[0], source.RedactedRepo},
		{"slash in password fails to parse", unsafeRepos[1], source.RedactedRepo},
		{"slash in token parses with no userinfo", unsafeRepos[2], source.RedactedRepo},
		{"slash after a valid port parses with no userinfo", unsafeRepos[3], source.RedactedRepo},
		{"slash in token behind parsed userinfo", unsafeRepos[4], source.RedactedRepo},
		{"@ in query", unsafeRepos[5], source.RedactedRepo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := source.RedactRepo(tc.in)
			if got != tc.want {
				t.Errorf("RedactRepo(%q) = %q, want %q", tc.in, got, tc.want)
			}
			assertNoCredential(t, got)
		})
	}
}

// Spec: §7.3.1 (Repository credentials)
func TestRedactCloneError(t *testing.T) {
	t.Parallel()
	const repo = "https://alice-user:s3cr3tpw@host/x.git"
	cases := []struct {
		name string
		repo string
		text string
		want string
	}{
		{
			name: "go-git status form",
			repo: repo,
			text: `unexpected requesting "http://alice-user:s3cr3tpw@127.0.0.1:9/acme/private.git/info/refs?service=git-upload-pack" status code: 500`,
			want: `unexpected requesting "http://127.0.0.1:9/acme/private.git/info/refs?service=git-upload-pack" status code: 500`,
		},
		{
			name: "net/http username-only form",
			repo: "http://ghp_tok3n@127.0.0.1:9/x",
			text: `Get "http://ghp_tok3n@127.0.0.1:9/x": dial tcp 127.0.0.1:9: connect: connection refused`,
			want: `Get "http://127.0.0.1:9/x": dial tcp 127.0.0.1:9: connect: connection refused`,
		},
		{
			name: "net/http masked password form",
			repo: repo,
			text: `Get "http://alice-user:***@127.0.0.1:9/x": EOF`,
			want: `Get "http://127.0.0.1:9/x": EOF`,
		},
		{
			// The match is greedy to the last '@' before the path.
			name: "raw @ and : in userinfo",
			repo: repo,
			text: `Get "http://alice-user:s3@cr3t:pw@host/x": EOF`,
			want: `Get "http://host/x": EOF`,
		},
		{
			name: "no URL",
			repo: repo,
			text: "repository not found",
			want: "repository not found",
		},
		{
			name: "two URLs",
			repo: repo,
			text: `Get "https://alice-user:s3cr3tpw@host/x.git": redirected to "https://ghp_tok3n@mirror.acme.com/x.git"`,
			want: `Get "https://host/x.git": redirected to "https://mirror.acme.com/x.git"`,
		},
		{
			// The pattern runs whatever the repo holds.
			name: "scp-like repo",
			repo: "git@github.com:acme/x.git",
			text: `Get "https://ghp_tok3n@host/x.git": EOF`,
			want: `Get "https://host/x.git": EOF`,
		},
	}
	// The withheld phrase replaces the upstream text whatever it holds: a text
	// that quotes the repo, and a text with no URL in it.
	upstream := map[string]func(repo string) string{
		"quoting the repo": func(repo string) string {
			return `unexpected requesting "` + repo + `/info/refs?service=git-upload-pack" status code: 500`
		},
		"with no URL": func(string) string { return "repository not found" },
	}
	for _, unsafe := range unsafeRepos {
		for label, text := range upstream {
			cases = append(cases, struct {
				name string
				repo string
				text string
				want string
			}{
				name: "withheld for " + unsafe + " " + label,
				repo: unsafe,
				text: text(unsafe),
				want: withheldCloneError,
			})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := source.RedactCloneError(tc.repo, errors.New(tc.text))
			if got != tc.want {
				t.Errorf("RedactCloneError(%q, %q) = %q, want %q", tc.repo, tc.text, got, tc.want)
			}
			assertNoCredential(t, got)
		})
	}
}
