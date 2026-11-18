package manifest

import (
	"reflect"
	"testing"
)

// Spec: §4.3.4 — ParseSkill reads Claude Code's top-level when_to_use: a
// scalar lands as a string, a null or an absent key leaves nothing, and a
// sequence or a mapping is kept aside so the SKILL.md still parses. An alias
// to a scalar decodes like the scalar.
func TestParseSkill_TopLevelWhenToUse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, fm, want, nonStringKind string
	}{
		{"plain scalar", "when_to_use: When the user says hi.\n", "When the user says hi.", ""},
		{"literal block", "when_to_use: |\n  At session start.\n  On a greeting.\n", "At session start.\nOn a greeting.", ""},
		{"null", "when_to_use: ~\n", "", ""},
		{"absent", "", "", ""},
		{"alias", "x: &a hello\nwhen_to_use: *a\n", "hello", ""},
		{"sequence", "when_to_use:\n  - a\n  - b\n", "", "sequence"},
		{"mapping", "when_to_use:\n  a: b\n", "", "mapping"},
	}
	for _, tc := range cases {
		s, err := ParseSkill([]byte("---\nname: hello\ndescription: d\n" + tc.fm + "---\n\nbody\n"))
		if err != nil {
			t.Fatalf("%s: ParseSkill: %v", tc.name, err)
		}
		if s.WhenToUse != tc.want {
			t.Errorf("%s: WhenToUse = %q, want %q", tc.name, s.WhenToUse, tc.want)
		}
		gotKind := ""
		if s.WhenToUseNonString != nil {
			gotKind = s.WhenToUseNonString.Kind
			if s.WhenToUseNonString.Key != "when_to_use" {
				t.Errorf("%s: WhenToUseNonString.Key = %q", tc.name, s.WhenToUseNonString.Key)
			}
		}
		if gotKind != tc.nonStringKind {
			t.Errorf("%s: non-string kind = %q, want %q", tc.name, gotKind, tc.nonStringKind)
		}
	}
}

// Spec: §4.3.4 — the top-level value decodes by the metadata.when_to_use
// encoding: lines are trimmed and empty ones dropped; a value of white space
// alone, a sequence, and a mapping are issues marked top-level; an empty
// value is absent with no issue.
func TestDecodeSkillTopLevelWhenToUse(t *testing.T) {
	t.Parallel()
	lines, issue := DecodeSkillTopLevelWhenToUse(&Skill{WhenToUse: "  At session start. \n\n On a greeting.\r\n"})
	if issue != nil || !reflect.DeepEqual(lines, []string{"At session start.", "On a greeting."}) {
		t.Errorf("lines = %q issue = %+v, want two trimmed lines and no issue", lines, issue)
	}
	if lines, issue := DecodeSkillTopLevelWhenToUse(&Skill{}); lines != nil || issue != nil {
		t.Errorf("empty value: lines = %q issue = %+v, want nothing", lines, issue)
	}
	if lines, issue := DecodeSkillTopLevelWhenToUse(nil); lines != nil || issue != nil {
		t.Errorf("nil skill: lines = %q issue = %+v, want nothing", lines, issue)
	}
	_, issue = DecodeSkillTopLevelWhenToUse(&Skill{WhenToUse: " \n "})
	if issue == nil || !issue.TopLevel || issue.Kind != "" || issue.Value != " \n " {
		t.Errorf("white space value: issue = %+v, want a top-level string issue", issue)
	}
	_, issue = DecodeSkillTopLevelWhenToUse(&Skill{WhenToUseNonString: &NonStringMetadata{Key: "when_to_use", Kind: "sequence", Value: "[a]"}})
	if issue == nil || !issue.TopLevel || issue.Kind != "sequence" || issue.Value != "[a]" || issue.Encoding == "" {
		t.Errorf("sequence value: issue = %+v, want a top-level sequence issue with the encoding", issue)
	}
}

// Spec: §4.3.4 — when_to_use takes the ARTIFACT.md value when ARTIFACT.md
// states the key, otherwise the top-level value when it follows the
// encoding, and otherwise the metadata value. A top-level value that does not
// follow the encoding falls through to metadata.
func TestResolveSkillFields_TopLevelWhenToUsePrecedence(t *testing.T) {
	t.Parallel()
	both := &Skill{WhenToUse: "top", Metadata: map[string]string{"when_to_use": "meta"}}
	cases := []struct {
		name   string
		stated map[string]bool
		start  []string
		s      *Skill
		want   []string
		filled bool
	}{
		{"ARTIFACT.md states the key", map[string]bool{"when_to_use": true}, []string{"artifact"}, both, []string{"artifact"}, false},
		{"top-level over metadata", nil, nil, both, []string{"top"}, true},
		{"top-level alone", nil, nil, &Skill{WhenToUse: "top"}, []string{"top"}, true},
		{"invalid top-level falls to metadata", nil, nil,
			&Skill{WhenToUse: "  ", Metadata: map[string]string{"when_to_use": "meta"}}, []string{"meta"}, true},
		{"non-string top-level falls to metadata", nil, nil,
			&Skill{WhenToUseNonString: &NonStringMetadata{Key: "when_to_use", Kind: "sequence"}, Metadata: map[string]string{"when_to_use": "meta"}},
			[]string{"meta"}, true},
	}
	for _, tc := range cases {
		a := &Artifact{Type: TypeSkill, WhenToUse: tc.start}
		filled := ResolveSkillFields(a, tc.stated, tc.s)
		if !reflect.DeepEqual(a.WhenToUse, tc.want) {
			t.Errorf("%s: WhenToUse = %q, want %q", tc.name, a.WhenToUse, tc.want)
		}
		got := false
		for _, k := range filled {
			got = got || k == "when_to_use"
		}
		if got != tc.filled {
			t.Errorf("%s: filled = %v, want when_to_use filled %v", tc.name, filled, tc.filled)
		}
	}
}

// Spec: §4.3.4, §13.4 — a stored plain-skill row resolves the top-level
// when_to_use, so admission and the adapters read the same value as ingest.
func TestResolveStoredSkill_TopLevelWhenToUse(t *testing.T) {
	t.Parallel()
	a, _, _, ok := ResolveStoredSkill(nil, []byte("---\nname: hello\ndescription: d\nwhen_to_use: On a greeting.\n---\n\nbody\n"))
	if !ok || !reflect.DeepEqual(a.WhenToUse, []string{"On a greeting."}) {
		t.Errorf("ok = %v WhenToUse = %q, want the top-level line", ok, a.WhenToUse)
	}
}
