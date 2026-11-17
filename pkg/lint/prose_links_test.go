package lint

import (
	"slices"
	"testing"
)

// Spec: §4.4 — a prose reference is the target of a Markdown inline link
// outside inline code spans and fenced code blocks; link syntax inside code is
// example text and is not checked.
func TestProseLinkTargets_SkipsCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"plain link", "See [a](a.md).", []string{"a.md"}},
		{"inline code span", "Write `[text](url)` for a link, then see [b](b.md).", []string{"b.md"}},
		{"double backtick span holding a backtick", "Use ``[x](`y`) [z](z.md)`` here.", nil},
		{"span spans lines in one paragraph", "Start `[a](a.md)\n[b](b.md)` end.", nil},
		{"unmatched backtick is literal", "A ` tick and [c](c.md).", []string{"c.md"}},
		{"span lengths must match", "``[a](a.md)` [b](b.md)", []string{"a.md", "b.md"}},
		{"escaped backtick opens no span", "\\`[a](a.md)` and `[b](b.md)`", []string{"a.md", "b.md"}},
		{"span does not cross a blank line", "A `tick\n\n[b](b.md) `", []string{"b.md"}},
		{"backtick fence", "```\n[a](a.md)\n```\n[b](b.md)", []string{"b.md"}},
		{"tilde fence", "~~~markdown\n[a](a.md)\n~~~\n[b](b.md)", []string{"b.md"}},
		{"fence with info string", "```md\n- [a](a.md)\n```\n", nil},
		{"indented fence", "   ```\n[a](a.md)\n   ```\n[b](b.md)", []string{"b.md"}},
		{"four-space indent is no fence", "    ```\n[a](a.md)", []string{"a.md"}},
		{"shorter closing run does not close", "````\n```\n[a](a.md)\n````\n[b](b.md)", []string{"b.md"}},
		{"longer closing run closes", "```\n[a](a.md)\n`````\n[b](b.md)", []string{"b.md"}},
		{"other fence character does not close", "~~~\n```\n[a](a.md)\n~~~\n[b](b.md)", []string{"b.md"}},
		{"closing fence with trailing text does not close", "```\n``` x\n[a](a.md)\n```\n[b](b.md)", []string{"b.md"}},
		{"unterminated fence runs to the end", "[a](a.md)\n```\n[b](b.md)\n[c](c.md)", []string{"a.md"}},
		{"backtick in info string is no fence", "```x`\n[a](a.md)", []string{"a.md"}},
		{"two-character run is no fence", "``\n[a](a.md)", []string{"a.md"}},
		{"CRLF line endings", "```\r\n[a](a.md)\r\n```\r\n[b](b.md)\r\n", []string{"b.md"}},
		{"title after the target", `See [a](a.md "The A file").`, []string{"a.md"}},
		{"angle-bracket target", "See [a](<my file.md>).", []string{"my file.md"}},
		{"unclosed angle bracket", "See [a](<a.md).", []string{"<a.md"}},
		{"blank target", "See [a]( ).", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := proseLinkTargets([]byte(tc.body)); !slices.Equal(got, tc.want) {
				t.Errorf("proseLinkTargets(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// Spec: §4.4 — before the bundled-file and artifact lookups, the target's
// fragment and query are removed and its percent-encoding decoded. A target
// whose percent-encoding does not decode is looked up as written.
func TestReferenceLookupPath(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"references/api.md":             "references/api.md",
		"references/api.md#errors":      "references/api.md",
		"references/api.md?plain=1":     "references/api.md",
		"references/api.md?plain=1#L10": "references/api.md",
		"references/my%20notes.md":      "references/my notes.md",
		"references/a%23b.md#top":       "references/a#b.md",
		"references/notes%zz.md#top":    "references/notes%zz.md",
		"#only-a-fragment":              "",
		"?only-a-query":                 "",
	}
	for in, want := range cases {
		if got := referenceLookupPath(in); got != want {
			t.Errorf("referenceLookupPath(%q) = %q, want %q", in, got, want)
		}
	}
}
