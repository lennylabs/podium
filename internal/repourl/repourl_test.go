package repourl_test

import (
	"net/url"
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
