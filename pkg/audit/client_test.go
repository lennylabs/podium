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
		Name: ClientNameCLI, Version: "0.7.0",
		Host: "Claude Code", HostVersion: "2.1.0", Harness: "claude-code", Purpose: PurposeSync,
	}
	got := ParseClientHeader(want.Header())
	if got == nil || *got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if h := (CallerClient{Name: ClientNameCLI}).Header(); h != "name=cli" {
		t.Errorf("Header() = %q, want empty members omitted", h)
	}
}

// Spec: §7.2.2, §8.1 — the registry ignores an empty, oversized, or
// unparsable value, keeps the first occurrence of a repeated member, ignores
// unknown members (the removed kind among them), sanitizes and truncates each
// member, keeps a provided name as provided, and drops a purpose other than
// sync before deciding whether any member is left.
func TestParseClientHeader(t *testing.T) {
	t.Parallel()
	atLimit := "name=" + strings.Repeat("a", maxClientHeaderBytes-len("name="))
	cases := []struct {
		name string
		in   string
		want *CallerClient
	}{
		{"empty", "", nil},
		{"at the 1024-byte limit", atLimit, &CallerClient{Name: strings.Repeat("a", maxClientMemberBytes)}},
		{"over the limit", atLimit + "a", nil},
		{"unparsable", "%zz", nil},
		{"first occurrence wins", "name=cli&name=mcp", &CallerClient{Name: "cli"}},
		{"unknown member ignored", "name=sdk&color=blue", &CallerClient{Name: "sdk"}},
		{"kind is an unknown member", "kind=mcp&name=acme-evals", &CallerClient{Name: "acme-evals"}},
		{"control bytes sanitized", "name=mcp&host=%00%1b[31m%0a@x", &CallerClient{Name: "mcp", Host: "___31m__x"}},
		{"200-byte member truncated", "name=sdk&version=" + strings.Repeat("9", 200), &CallerClient{Name: "sdk", Version: strings.Repeat("9", maxClientMemberBytes)}},
		{"custom name kept", "name=Acme%20Evals", &CallerClient{Name: "Acme Evals"}},
		{"sync purpose kept", "name=cli&purpose=sync&harness=codex", &CallerClient{Name: "cli", Harness: "codex", Purpose: PurposeSync}},
		{"other purpose dropped", "name=cli&purpose=install", &CallerClient{Name: "cli"}},
		{"only an unknown purpose", "purpose=install", nil},
		{"absent name", "harness=cursor", &CallerClient{Harness: "cursor"}},
		{"only empty members", "name=&harness=", nil},
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
	got := NormalizeClient(CallerClient{Name: strings.Repeat("n", 200), Harness: "a=b", Purpose: PurposeSync})
	if got == nil || len(got.Name) != maxClientMemberBytes || got.Harness != "a_b" || got.Purpose != PurposeSync {
		t.Fatalf("NormalizeClient = %+v, want a 64-byte name, a sanitized harness, and the sync purpose", got)
	}
	if again := NormalizeClient(*got); again == nil || *again != *got {
		t.Errorf("NormalizeClient is not idempotent: %+v then %+v", got, again)
	}
}
