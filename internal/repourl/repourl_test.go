package repourl_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/repourl"
)

// Spec: §7.3.1 (Repository credentials)
//
// Every expected Clean is a literal. Computing it through Redact would make
// the Redact(input) == Clean assertion compare the function with itself.
func TestSplit_SplitClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		in          string
		clean       string
		rawUserinfo string
	}{
		{"user and password", "https://alice-user:s3cr3tpw@git.acme.com/acme/x.git", "https://git.acme.com/acme/x.git", "alice-user:s3cr3tpw"},
		{"username-only token", "https://ghp_tok3n@git.acme.com/acme/x.git", "https://git.acme.com/acme/x.git", "ghp_tok3n"},
		{"password only", "https://:s3cr3tpw@git.acme.com/acme/x.git", "https://git.acme.com/acme/x.git", ":s3cr3tpw"},
		{"uppercase scheme", "HTTPS://t@host/a.git", "https://host/a.git", "t"},
		{"escaped userinfo and space in path", "https://u:p%2fq@host/a b.git", "https://host/a%20b.git", "u:p%2fq"},
		// The cut is at the last '@' of the authority.
		{"@ inside userinfo", "https://a@b:c@host/x.git", "https://host/x.git", "a@b:c"},
		{"no path", "https://tok@host", "https://host", "tok"},
		{"empty userinfo", "https://@host/x.git", "https://host/x.git", ""},
		{"empty user and password", "https://:@host/x.git", "https://host/x.git", ":"},
		{"empty fragment", "https://tok@host/x.git#", "https://host/x.git", "tok"},
		{"empty query", "https://tok@host/x.git?", "https://host/x.git?", "tok"},
		{"ssh username", "ssh://git@host/x.git", "ssh://host/x.git", "git"},
		{"file URL with empty host", "file://tok@/srv/x", "file:///srv/x", "tok"},
		// The next two reported forms lose their "//", so go-git resolves them
		// to a different transport than the registered value.
		{"file URL with query and no host", "file://tok@?/srv/x", "file:?/srv/x", "tok"},
		{"https with userinfo only", "https://tok@", "https:", "tok"},
		{"escaped @ and space in path", "https://ghp_tok3n@git.acme.com/acme/a%40b c.git", "https://git.acme.com/acme/a@b%20c.git", "ghp_tok3n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parts, ok := repourl.Split(tc.in)
			if !ok {
				t.Fatalf("Split(%q) = false, want true", tc.in)
			}
			if parts.Clean != tc.clean {
				t.Errorf("Split(%q).Clean = %q, want %q", tc.in, parts.Clean, tc.clean)
			}
			if parts.RawUserinfo != tc.rawUserinfo {
				t.Errorf("Split(%q).RawUserinfo = %q, want %q", tc.in, parts.RawUserinfo, tc.rawUserinfo)
			}
			joined, ok := repourl.Join(parts.Bare, parts.RawUserinfo)
			if !ok || joined != tc.in {
				t.Errorf("Join(%q, %q) = %q, %v, want %q, true", parts.Bare, parts.RawUserinfo, joined, ok, tc.in)
			}
			bare, err := url.Parse(parts.Bare)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", parts.Bare, err)
			}
			if bare.User != nil {
				t.Errorf("url.Parse(%q).User = %q, want nil", parts.Bare, bare.User)
			}
			if got := repourl.Redact(tc.in); got != tc.clean {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.clean)
			}
			if repourl.Unsafe(tc.in) {
				t.Errorf("Unsafe(%q) = true, want false", tc.in)
			}
		})
	}
}

// Spec: §7.3.1 (Repository credentials)
//
// Redact is not idempotent. The Clean of the last split-class row carries an
// unescaped '@' in its path, which puts it in a fail-closed class. This is why
// no caller redacts a Clean value. The assertion records the 0.5.2 behavior
// and is not a requirement on it.
func TestRedact_CleanValueIsNotAFixedPoint(t *testing.T) {
	t.Parallel()
	const clean = "https://git.acme.com/acme/a@b%20c.git"
	if got := repourl.Redact(clean); got != repourl.Redacted {
		t.Errorf("Redact(%q) = %q, want %q", clean, got, repourl.Redacted)
	}
}

// Spec: §7.3.1 (Repository credentials)
func TestSplit_OutsideSplitClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		in     string
		unsafe bool
		redact string
	}{
		{"scp-like remote", "git@github.com:acme/x.git", false, "git@github.com:acme/x.git"},
		{"filesystem path", "/srv/git/acme.git", false, "/srv/git/acme.git"},
		{"empty", "", false, ""},
		{"URL with no userinfo", "https://git.acme.com/acme/x.git", false, "https://git.acme.com/acme/x.git"},
		// The value matches the scheme pattern and parses with userinfo and an
		// empty scheme. Redact removes the userinfo although Split refuses it.
		{"network-path reference", "//tok@host/x://y", false, "//host/x://y"},
		{"control character fails to parse", "https://alice-user:s3cr3t\x7fpw@host/x", true, repourl.Redacted},
		{"slash in token parses with no userinfo", "https://ghp_tok/3n@host/x.git", true, repourl.Redacted},
		{"@ in query", "https://git.acme.com/acme/x.git?ref=a@b", true, repourl.Redacted},
		// The value parses with a non-nil User, so a Split that omitted the
		// fail-closed check would accept it.
		{"slash in token behind parsed userinfo", "https://bob@ghp_tok/3n@host/x.git", true, repourl.Redacted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if parts, ok := repourl.Split(tc.in); ok {
				t.Errorf("Split(%q) = %+v, true, want false", tc.in, parts)
			}
			if got := repourl.Unsafe(tc.in); got != tc.unsafe {
				t.Errorf("Unsafe(%q) = %v, want %v", tc.in, got, tc.unsafe)
			}
			if got := repourl.Redact(tc.in); got != tc.redact {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.redact)
			}
		})
	}
}

// Spec: §7.3.1 (Repository credentials)
func TestJoin_NoSchemeSeparator(t *testing.T) {
	t.Parallel()
	for _, bare := range []string{"git@github.com:acme/x.git", "/srv/git/acme.git", ""} {
		if got, ok := repourl.Join(bare, "ghp_tok3n"); ok || got != "" {
			t.Errorf("Join(%q, ...) = %q, %v, want \"\", false", bare, got, ok)
		}
	}
}

// Spec: §7.3.1 (Repository credentials)
//
// Every expected joined value and userinfo is a literal, so no assertion
// compares WithCredential, Split, or Redact with itself.
func TestWithCredential_RoundTrip(t *testing.T) {
	t.Parallel()
	const bare = "https://git.acme.com/acme/x.git"
	cases := []struct {
		name     string
		username string
		token    string
		userinfo string
	}{
		{"token username", "x-access-token", "ghp_x", "x-access-token:ghp_x"},
		{"empty username", "", "azpat", ":azpat"},
		{"reserved characters", "alice@acme.com", "p@ss/w:rd?#% ", "alice%40acme.com:p%40ss%2Fw%3Ard%3F%23%25%20"},
		{"bitbucket token username", "x-token-auth", "t", "x-token-auth:t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := "https://" + tc.userinfo + "@git.acme.com/acme/x.git"
			got, err := repourl.WithCredential(bare, tc.username, tc.token)
			if err != nil {
				t.Fatalf("WithCredential: %v", err)
			}
			if got != want {
				t.Fatalf("WithCredential = %q, want %q", got, want)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", got, err)
			}
			password, ok := u.User.Password()
			if u.User.Username() != tc.username || !ok || password != tc.token {
				t.Errorf("url.Parse(%q).User = %q, %q, %v, want %q, %q, true", got, u.User.Username(), password, ok, tc.username, tc.token)
			}
			parts, ok := repourl.Split(got)
			if !ok {
				t.Fatalf("Split(%q) = false, want true", got)
			}
			if parts.Bare != bare {
				t.Errorf("Split(%q).Bare = %q, want %q", got, parts.Bare, bare)
			}
			if parts.RawUserinfo != tc.userinfo {
				t.Errorf("Split(%q).RawUserinfo = %q, want %q", got, parts.RawUserinfo, tc.userinfo)
			}
			if parts.Clean != bare {
				t.Errorf("Split(%q).Clean = %q, want %q", got, parts.Clean, bare)
			}
			if r := repourl.Redact(got); r != bare {
				t.Errorf("Redact(%q) = %q, want %q", got, r, bare)
			}
		})
	}
}

// Spec: §7.3.1 (Repository credentials)
func TestWithCredential_Refusals(t *testing.T) {
	t.Parallel()
	const (
		bare     = "https://git.acme.com/acme/x.git"
		username = "alice-user"
		token    = "s3cr3tpw"
	)
	cases := []struct {
		name     string
		bare     string
		username string
		token    string
		want     error
	}{
		{"colon in username", bare, "alice:x", token, repourl.ErrCredentialUsername},
		{"newline in username", bare, "a\nb", token, repourl.ErrCredentialUsername},
		{"DEL in username", bare, "a\x7fb", token, repourl.ErrCredentialUsername},
		{"space in username", bare, "acme ci", token, repourl.ErrCredentialUsername},
		{"slash in username", bare, "acme/ci", token, repourl.ErrCredentialUsername},
		{"quote in username", bare, "a\"b", token, repourl.ErrCredentialUsername},
		{"empty token", bare, username, "", repourl.ErrCredentialToken},
		{"carriage return in token", bare, username, "t\r", repourl.ErrCredentialToken},
		{"NUL in token", bare, username, "t\x00", repourl.ErrCredentialToken},
		{"ssh URL", "ssh://h/x.git", username, token, repourl.ErrCredentialRemote},
		{"scp-like remote", "git@h:x.git", username, token, repourl.ErrCredentialRemote},
		{"filesystem path", "/srv/x", username, token, repourl.ErrCredentialRemote},
		{"remote with userinfo", "https://u:t@h/x.git", username, token, repourl.ErrCredentialRemote},
		{"redacted class", "https://h/x@y", username, token, repourl.ErrCredentialRemote},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := repourl.WithCredential(tc.bare, tc.username, tc.token)
			if !errors.Is(err, tc.want) {
				t.Fatalf("WithCredential(%q) = %q, %v, want %v", tc.bare, got, err, tc.want)
			}
			if got != "" {
				t.Errorf("WithCredential(%q) = %q, want \"\"", tc.bare, got)
			}
			for _, secret := range []string{tc.username, tc.token} {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Errorf("error %q contains %q", err, secret)
				}
			}
		})
	}
}

// credentialClassCases lists each repo value with its literal
// CarriesURLCredential and TakesCredential answers.
var credentialClassCases = []struct {
	in      string
	carries bool
	takes   bool
}{
	{"https://u:t@h/x", true, true},
	{"https://@h/x", true, true},
	{"HTTPS://h/x", false, true},
	{"http://h/x", false, true},
	{"https://h/x@y", true, false},
	{"ssh://git@host:org/x.git", true, false},
	{"ssh://git@h/x", false, false},
	{"git@h:x", false, false},
	{"/srv/x", false, false},
	{repourl.Redacted, false, false},
}

// Spec: §7.3.1 (Repository credentials)
func TestCarriesURLCredential(t *testing.T) {
	t.Parallel()
	for _, tc := range credentialClassCases {
		if got := repourl.CarriesURLCredential(tc.in); got != tc.carries {
			t.Errorf("CarriesURLCredential(%q) = %v, want %v", tc.in, got, tc.carries)
		}
	}
}

// Spec: §7.3.1 (Repository credentials)
func TestTakesCredential(t *testing.T) {
	t.Parallel()
	for _, tc := range credentialClassCases {
		if got := repourl.TakesCredential(tc.in); got != tc.takes {
			t.Errorf("TakesCredential(%q) = %v, want %v", tc.in, got, tc.takes)
		}
	}
}

// Spec: §7.3.1 (Repository credentials)
func TestWithoutCredential(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"https://u:t@H/a%2Fb.git", "https://H/a%2Fb.git"},
		{"https://@h/x", "https://h/x"},
		{"https://h/x", "https://h/x"},
		// A [redacted]-class value is returned unchanged.
		{"https://h/x@y", "https://h/x@y"},
	}
	for _, tc := range cases {
		if got := repourl.WithoutCredential(tc.in); got != tc.want {
			t.Errorf("WithoutCredential(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Spec: §7.3.1 (Repository credentials)
func TestCredentialUsername(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"https://alice%40acme.com:t@h/x", "alice@acme.com"},
		{"https://:t@h/x", ""},
		// A legacy token in the username position is not a username.
		{"https://tok@h/x", ""},
		{"https://h/x", ""},
	}
	for _, tc := range cases {
		if got := repourl.CredentialUsername(tc.in); got != tc.want {
			t.Errorf("CredentialUsername(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
