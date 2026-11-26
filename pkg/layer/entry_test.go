package layer

import (
	"strings"
	"testing"
)

// Spec: §4.6 — a groups or users entry is a non-empty string with no
// control character (Unicode general category Cc). White space around an
// entry is part of it, so a padded entry and one carrying a space are
// admitted, and the refusal names the entry by its position.
func TestCheckEntries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		values []string
		want   string // substring of the refusal, or "" when admitted
	}{
		{"no list", nil, ""},
		{"plain entries", []string{"alice@acme.com", "bob@acme.com"}, ""},
		{"white space kept", []string{" alice@acme.com ", "Finance Team"}, ""},
		{"non-ASCII letters", []string{"zoë@acme.com"}, ""},
		{"empty entry", []string{"alice@acme.com", ""}, "users[1] is empty"},
		{"line feed inside", []string{"alice@acme.com\nbob@acme.com"}, "users[0] contains a control character"},
		{"trailing line feed", []string{"alice@acme.com\n"}, "users[0] contains a control character"},
		{"carriage return", []string{"alice@acme.com", "bob\r"}, "users[1] contains a control character"},
		{"tab", []string{"\t"}, "users[0] contains a control character"},
		{"NUL", []string{"alice\x00"}, "users[0] contains a control character"},
		{"C1 control", []string{"alice\u0085"}, "users[0] contains a control character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CheckEntries("users", tc.values)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("CheckEntries(%q) = %v, want nil", tc.values, err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("CheckEntries(%q) = %v, want an error containing %q", tc.values, err, tc.want)
			}
		})
	}
}
