package audit

import (
	"strings"
	"testing"
)

// Spec: §7.2.2, §8.1 — Header encodes every non-empty member and
// ParseClientHeader reads it back unchanged.
func TestCallerClient_HeaderRoundTrip(t *testing.T) {
	t.Parallel()
	want := CallerClient{
		Kind: "mcp", Name: "podium-mcp", Version: "0.6.0",
		Host: "Claude Code", HostVersion: "2.1.0", Harness: "claude-code",
	}
	got := ParseClientHeader(want.Header())
	if got == nil || *got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if h := (CallerClient{Kind: "cli"}).Header(); h != "kind=cli" {
		t.Errorf("Header() = %q, want empty members omitted", h)
	}
}

// Spec: §7.2.2, §8.1 — the registry ignores an empty, oversized, or
// unparsable value, keeps the first occurrence of a repeated member, ignores
// unknown members, sanitizes and truncates each member, and maps an absent or
// unknown kind to other only when a member is left non-empty.
func TestParseClientHeader(t *testing.T) {
	t.Parallel()
	atLimit := "kind=cli&name=" + strings.Repeat("a", maxClientHeaderBytes-len("kind=cli&name="))
	cases := []struct {
		name string
		in   string
		want *CallerClient
	}{
		{"empty", "", nil},
		{"at the 1024-byte limit", atLimit, &CallerClient{Kind: "cli", Name: strings.Repeat("a", maxClientMemberBytes)}},
		{"over the limit", atLimit + "a", nil},
		{"unparsable", "%zz", nil},
		{"first occurrence wins", "kind=sync&kind=cli&name=podium", &CallerClient{Kind: "sync", Name: "podium"}},
		{"unknown member ignored", "kind=sdk&color=blue", &CallerClient{Kind: "sdk"}},
		{"control bytes sanitized", "kind=mcp&host=%00%1b[31m%0a@x", &CallerClient{Kind: "mcp", Host: "___31m__x"}},
		{"200-byte member truncated", "kind=sdk&version=" + strings.Repeat("9", 200), &CallerClient{Kind: "sdk", Version: strings.Repeat("9", maxClientMemberBytes)}},
		{"uppercase kind", "kind=MCP", &CallerClient{Kind: "other"}},
		{"unknown kind", "kind=agent", &CallerClient{Kind: "other"}},
		{"absent kind", "host=cursor", &CallerClient{Kind: "other", Host: "cursor"}},
		{"only empty members", "kind=&name=", nil},
		{"only unknown members", "color=blue", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseClientHeader(tc.in)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("ParseClientHeader(%q) = %+v, want nil", tc.in, got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Fatalf("ParseClientHeader(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
			if got != nil {
				assertClientCharset(t, *got)
			}
		})
	}
}

// assertClientCharset fails when a normalized member holds a byte below 0x20
// or one of the separators the §7.2.2 character set excludes.
func assertClientCharset(t *testing.T, c CallerClient) {
	t.Helper()
	for _, m := range c.members() {
		if strings.ContainsAny(*m.val, "@=[&%") {
			t.Errorf("member %s = %q holds an excluded separator", m.key, *m.val)
		}
		for i := 0; i < len(*m.val); i++ {
			if (*m.val)[i] < 0x20 {
				t.Errorf("member %s = %q holds control byte 0x%02x", m.key, *m.val, (*m.val)[i])
			}
		}
	}
}

// Spec: §7.2.2, §8.1 — NormalizeClient returns nil for the zero value,
// truncates a 200-byte member to 64 bytes, and is idempotent.
func TestNormalizeClient(t *testing.T) {
	t.Parallel()
	if got := NormalizeClient(CallerClient{}); got != nil {
		t.Errorf("NormalizeClient(zero) = %+v, want nil", got)
	}
	got := NormalizeClient(CallerClient{Kind: "sdk", Name: strings.Repeat("n", 200), Harness: "a=b"})
	if got == nil || len(got.Name) != maxClientMemberBytes || got.Harness != "a_b" {
		t.Fatalf("NormalizeClient = %+v, want a 64-byte name and a sanitized harness", got)
	}
	if again := NormalizeClient(*got); again == nil || *again != *got {
		t.Errorf("NormalizeClient is not idempotent: %+v then %+v", got, again)
	}
}
