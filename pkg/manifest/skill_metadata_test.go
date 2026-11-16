package manifest

import (
	"errors"
	"reflect"
	"testing"
)

// metaSkill builds a Skill whose metadata holds kv as key/value pairs.
func metaSkill(kv ...string) *Skill {
	m := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return &Skill{Metadata: m}
}

// Spec: §4.3.4 — each metadata-eligible key decodes by its encoding, a value
// that does not follow the encoding becomes an issue, and an empty value is
// absent. The tags cases fail against a decoder that splits on white space or
// keeps empty elements.
func TestDecodeSkillMetadata_Encodings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		key     string
		value   string
		want    Artifact
		decoded bool
	}{
		{"tags", "a, b", Artifact{Tags: []string{"a", "b"}}, true},
		{"tags", "month end", Artifact{Tags: []string{"month end"}}, true},
		{"tags", "a,,b", Artifact{}, false},
		{"tags", " , ", Artifact{}, false},
		{"tags", "a,", Artifact{}, false},
		{"tags", "a\nb", Artifact{}, false},
		{"when_to_use", "x\n\n y \n", Artifact{WhenToUse: []string{"x", "y"}}, true},
		{"when_to_use", "\n\n", Artifact{}, false},
		{"sensitivity", "medium", Artifact{Sensitivity: SensitivityMedium}, true},
		{"sensitivity", "low", Artifact{Sensitivity: SensitivityLow}, true},
		{"sensitivity", "high", Artifact{Sensitivity: SensitivityHigh}, true},
		{"sensitivity", "secret", Artifact{}, false},
		{"sensitivity", "Medium", Artifact{}, false},
		{"search_visibility", "direct-only", Artifact{SearchVisibility: SearchVisibilityDirectOnly}, true},
		{"search_visibility", "indexed", Artifact{SearchVisibility: SearchVisibilityIndexed}, true},
		{"search_visibility", "hidden", Artifact{}, false},
		{"deprecated", "true", Artifact{Deprecated: true}, true},
		{"deprecated", "false", Artifact{}, true},
		{"deprecated", "yes", Artifact{}, false},
		{"replaced_by", "finance/close-v2", Artifact{ReplacedBy: "finance/close-v2"}, true},
		{"replaced_by", "a\nb", Artifact{}, false},
		{"replaced_by", "a\rb", Artifact{}, false},
		{"release_notes", "line one\nline two\n", Artifact{ReleaseNotes: "line one\nline two\n"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Parallel()
			got := DecodeSkillMetadata(metaSkill(tc.key, tc.value))
			if !reflect.DeepEqual(got.Values, tc.want) {
				t.Errorf("Values = %+v, want %+v", got.Values, tc.want)
			}
			if got.Decoded[tc.key] != tc.decoded {
				t.Errorf("Decoded[%q] = %v, want %v", tc.key, got.Decoded[tc.key], tc.decoded)
			}
			if tc.decoded {
				if len(got.Issues) != 0 {
					t.Errorf("Issues = %+v, want none", got.Issues)
				}
				return
			}
			want := []SkillMetadataIssue{{Key: tc.key, Value: tc.value, Encoding: skillMetadataCodecs[tc.key].encoding}}
			if !reflect.DeepEqual(got.Issues, want) {
				t.Errorf("Issues = %+v, want %+v", got.Issues, want)
			}
		})
	}
	for _, key := range skillMetadataFields {
		got := DecodeSkillMetadata(metaSkill(key, ""))
		if got.Decoded[key] || len(got.Issues) != 0 || !reflect.DeepEqual(got.Values, Artifact{}) {
			t.Errorf("empty %s: got %+v, want absent with no issue", key, got)
		}
	}
}

// Spec: §4.3.4 — issues come out in the table order of the keys, so the lint
// warnings are deterministic whatever the map iteration order.
func TestDecodeSkillMetadata_IssuesInTableOrder(t *testing.T) {
	t.Parallel()
	got := DecodeSkillMetadata(metaSkill(
		"release_notes", "ok",
		"deprecated", "yes",
		"sensitivity", "secret",
		"tags", "a,,b",
		"when_to_use", " ",
		"owner", "finance",
	))
	var keys []string
	for _, is := range got.Issues {
		keys = append(keys, is.Key)
	}
	want := []string{"when_to_use", "tags", "sensitivity", "deprecated"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("issue keys = %v, want %v", keys, want)
	}
	if !got.Decoded["release_notes"] || got.Decoded["owner"] {
		t.Errorf("Decoded = %v, want release_notes only", got.Decoded)
	}
}

// Spec: §4.3.4 — Decoded records a decoded false, so a deprecated "false" is
// distinguishable from an absent key, and a nil skill decodes to an empty,
// non-nil Decoded.
func TestDecodeSkillMetadata_DecodedKeysRecordsFalse(t *testing.T) {
	t.Parallel()
	got := DecodeSkillMetadata(metaSkill("deprecated", "false"))
	if !got.Decoded["deprecated"] || got.Values.Deprecated {
		t.Errorf("deprecated \"false\": Decoded=%v Deprecated=%v, want true/false", got.Decoded, got.Values.Deprecated)
	}
	empty := DecodeSkillMetadata(nil)
	if empty.Decoded == nil || len(empty.Decoded) != 0 {
		t.Errorf("nil skill Decoded = %#v, want empty non-nil map", empty.Decoded)
	}
	if len(empty.Issues) != 0 || !reflect.DeepEqual(empty.Values, Artifact{}) {
		t.Errorf("nil skill = %+v, want empty", empty)
	}
}

// Spec: §4.3.4 — the metadata-eligible set is exactly the encoding table, and
// every key in it has a codec.
func TestIsSkillMetadataField(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"when_to_use", "tags", "sensitivity", "search_visibility", "deprecated", "replaced_by", "release_notes"} {
		if !IsSkillMetadataField(key) {
			t.Errorf("IsSkillMetadataField(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"version", "sandbox_profile", "effort_hint", "model_class_hint", "source", "audit_redact", "lint_suppress", "type", "owner", ""} {
		if IsSkillMetadataField(key) {
			t.Errorf("IsSkillMetadataField(%q) = true, want false", key)
		}
	}
	if len(skillMetadataFields) != len(skillMetadataCodecs) {
		t.Errorf("skillMetadataFields has %d keys, codecs %d", len(skillMetadataFields), len(skillMetadataCodecs))
	}
}

// Spec: §4.3.4 — StatedKeys reads key presence from the raw frontmatter,
// whatever the value, and empty input states nothing.
func TestStatedKeys(t *testing.T) {
	t.Parallel()
	got, err := StatedKeys([]byte("---\ntype: skill\ndeprecated: false\ntags: []\nsensitivity: \"\"\n---\n"))
	if err != nil {
		t.Fatalf("StatedKeys: %v", err)
	}
	want := map[string]bool{"type": true, "deprecated": true, "tags": true, "sensitivity": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StatedKeys = %v, want %v", got, want)
	}
	for _, in := range [][]byte{nil, {}} {
		got, err := StatedKeys(in)
		if got != nil || err != nil {
			t.Errorf("StatedKeys(%q) = %v, %v; want nil, nil", in, got, err)
		}
	}
	if _, err := StatedKeys([]byte("no frontmatter")); !errors.Is(err, ErrNoFrontmatter) {
		t.Errorf("no frontmatter: err = %v, want ErrNoFrontmatter", err)
	}
	if _, err := StatedKeys([]byte("---\ntags: [a\n---\n")); !errors.Is(err, ErrInvalidYAML) {
		t.Errorf("bad YAML: err = %v, want ErrInvalidYAML", err)
	}
}

// Spec: §4.3.4 — precedence is by key presence: a stated key keeps its
// ARTIFACT.md value even when that value is empty or false, an unstated key is
// filled from metadata, and the result lists the filled keys. It fails against
// a zero-value precedence rule.
func TestResolveSkillFields_PrecedenceByKeyPresence(t *testing.T) {
	t.Parallel()
	s := metaSkill(
		"version", "2.0.0",
		"when_to_use", "closing the books",
		"tags", "finance, close",
		"sensitivity", "high",
		"search_visibility", "direct-only",
		"deprecated", "true",
		"replaced_by", "finance/close-v2",
		"release_notes", "notes",
	)

	t.Run("stated empty and false values win", func(t *testing.T) {
		t.Parallel()
		a := &Artifact{Type: TypeSkill}
		stated := map[string]bool{"deprecated": true, "sensitivity": true, "tags": true}
		filled := ResolveSkillFields(a, stated, s)
		if a.Deprecated || a.Sensitivity != "" || a.Tags != nil {
			t.Errorf("stated keys overridden: %+v", a)
		}
		want := []string{"when_to_use", "search_visibility", "replaced_by", "release_notes"}
		if !reflect.DeepEqual(filled, want) {
			t.Errorf("filled = %v, want %v", filled, want)
		}
		if a.SearchVisibility != SearchVisibilityDirectOnly || a.ReplacedBy != "finance/close-v2" ||
			a.ReleaseNotes != "notes" || !reflect.DeepEqual(a.WhenToUse, []string{"closing the books"}) {
			t.Errorf("unstated keys not filled: %+v", a)
		}
		if a.Version != "2.0.0" {
			t.Errorf("Version = %q, want 2.0.0", a.Version)
		}
	})

	t.Run("nothing stated fills every decoded key", func(t *testing.T) {
		t.Parallel()
		a := &Artifact{Type: TypeSkill}
		filled := ResolveSkillFields(a, nil, s)
		if !reflect.DeepEqual(filled, skillMetadataFields) {
			t.Errorf("filled = %v, want %v", filled, skillMetadataFields)
		}
		if !a.Deprecated || a.Sensitivity != SensitivityHigh || !reflect.DeepEqual(a.Tags, []string{"finance", "close"}) {
			t.Errorf("resolved = %+v", a)
		}
	})

	t.Run("undecodable metadata fills nothing", func(t *testing.T) {
		t.Parallel()
		a := &Artifact{Type: TypeSkill, Sensitivity: SensitivityLow}
		filled := ResolveSkillFields(a, nil, metaSkill("sensitivity", "secret", "tags", ""))
		if filled != nil || a.Sensitivity != SensitivityLow || a.Tags != nil {
			t.Errorf("filled = %v, a = %+v; want nothing filled", filled, a)
		}
	})

	t.Run("version rule matches ResolveSkillVersion", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			a    Artifact
			s    *Skill
			want string
		}{
			{Artifact{Version: "1.2.3"}, &Skill{}, "1.2.3"},
			{Artifact{}, metaSkill("version", "2.0.0"), "2.0.0"},
			{Artifact{Version: "3.0.0"}, metaSkill("version", "4.0.0"), "3.0.0"},
			{Artifact{}, metaSkill("version", "v1"), ""},
			{Artifact{}, metaSkill("version", "0.0.0-auto.2"), ""},
			{Artifact{Version: "1.0.0"}, nil, "1.0.0"},
			{Artifact{}, nil, ""},
		} {
			a := tc.a
			ResolveSkillFields(&a, nil, tc.s)
			if a.Version != tc.want {
				t.Errorf("ResolveSkillFields(%+v, %+v) version = %q, want %q", tc.a, tc.s, a.Version, tc.want)
			}
		}
	})
}

// Spec: §4.3.4 — a nil artifact is a no-op and never panics.
func TestResolveSkillFields_NilArtifact(t *testing.T) {
	t.Parallel()
	if got := ResolveSkillFields(nil, nil, metaSkill("tags", "a")); got != nil {
		t.Errorf("ResolveSkillFields(nil) = %v, want nil", got)
	}
}

// Spec: §4.3.4, §13.4 — ResolveStoredSkill resolves a plain-skill row and a
// both-files row from their stored bytes and refuses every other input.
func TestResolveStoredSkill_Forms(t *testing.T) {
	t.Parallel()
	const skillMD = "---\nname: close\ndescription: Close the books.\nmetadata:\n  version: \"1.0.0\"\n  tags: \"finance, close\"\n  sensitivity: high\n---\nBody.\n"

	t.Run("plain skill", func(t *testing.T) {
		t.Parallel()
		a, s, filled, ok := ResolveStoredSkill(nil, []byte(skillMD))
		if !ok || s == nil || s.Name != "close" {
			t.Fatalf("ok = %v, s = %+v", ok, s)
		}
		want := &Artifact{
			Type:        TypeSkill,
			Version:     "1.0.0",
			Description: "Close the books.",
			Tags:        []string{"finance", "close"},
			Sensitivity: SensitivityHigh,
		}
		if !reflect.DeepEqual(a, want) {
			t.Errorf("a = %+v, want %+v", a, want)
		}
		if !reflect.DeepEqual(filled, []string{"tags", "sensitivity"}) {
			t.Errorf("filled = %v", filled)
		}
	})

	t.Run("both files keep stated values", func(t *testing.T) {
		t.Parallel()
		artifactMD := []byte("---\ntype: skill\nversion: 1.0.0\nsensitivity: low\n---\n<!-- Skill body lives in SKILL.md. -->\n")
		a, _, filled, ok := ResolveStoredSkill(artifactMD, []byte(skillMD))
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if a.Sensitivity != SensitivityLow || !reflect.DeepEqual(a.Tags, []string{"finance", "close"}) {
			t.Errorf("a = %+v, want sensitivity low and metadata tags", a)
		}
		if a.Description != "" {
			t.Errorf("Description = %q, want the ARTIFACT.md value", a.Description)
		}
		if !reflect.DeepEqual(filled, []string{"tags"}) {
			t.Errorf("filled = %v, want [tags]", filled)
		}
	})

	refused := []struct {
		name       string
		artifactMD string
		skillMD    string
	}{
		{"non-skill manifest", "---\ntype: context\nversion: 1.0.0\n---\nBody.\n", skillMD},
		{"unparsable SKILL.md", "", "no frontmatter"},
		{"empty SKILL.md", "", ""},
		{"unparsable ARTIFACT.md", "---\ntype: [skill\n---\n", skillMD},
		{"list metadata value", "", "---\nname: x\nmetadata:\n  tags: [a, b]\n---\n"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, s, filled, ok := ResolveStoredSkill([]byte(tc.artifactMD), []byte(tc.skillMD))
			if ok || a != nil || s != nil || filled != nil {
				t.Errorf("got (%v, %v, %v, %v), want refused", a, s, filled, ok)
			}
		})
	}
}
