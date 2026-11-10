package manifest

import (
	"errors"
	"testing"
)

// Spec: §4.3.4, §4.7.6 — the synthesized ARTIFACT.md is exactly the bytes
// §4.3.4 fixes, for an authored version and for a registry-assigned one.
func TestSynthesizedSkillDocument_GoldenBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		version string
		want    string
	}{
		{"1.0.0", "---\ntype: skill\nversion: 1.0.0\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"},
		{"0.0.0-auto.3", "---\ntype: skill\nversion: 0.0.0-auto.3\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			got, err := SynthesizedSkillDocument(tc.version)
			if err != nil {
				t.Fatalf("SynthesizedSkillDocument(%q): %v", tc.version, err)
			}
			if string(got) != tc.want {
				t.Errorf("SynthesizedSkillDocument(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}

// Spec: §4.3.4, §4.7.6 — synthesis refuses a version that is neither a
// major.minor.patch value nor the reserved form, so no value injects a key.
func TestSynthesizedSkillDocument_RefusesInvalidVersions(t *testing.T) {
	t.Parallel()
	for _, v := range []string{
		"",
		"not-a-version",
		"1.0.0\nsensitivity: low",
		"1.0",
		"0.0.0-auto.01",
		"1.0.0-auto.1",
		"0.0.0-auto.",
	} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			got, err := SynthesizedSkillDocument(v)
			if !errors.Is(err, ErrInvalidVersion) {
				t.Fatalf("SynthesizedSkillDocument(%q) err = %v, want ErrInvalidVersion", v, err)
			}
			if got != nil {
				t.Errorf("SynthesizedSkillDocument(%q) bytes = %q, want nil", v, got)
			}
		})
	}
}

// Spec: §4.3.4 — the writers recognize the synthesized document by byte
// equality, so any deviation from the exact bytes reads as authored.
func TestIsSynthesizedSkillDocument(t *testing.T) {
	t.Parallel()
	doc, err := SynthesizedSkillDocument("2.1.0")
	if err != nil {
		t.Fatalf("SynthesizedSkillDocument: %v", err)
	}
	auto, err := SynthesizedSkillDocument(AutoVersion(7))
	if err != nil {
		t.Fatalf("SynthesizedSkillDocument(auto): %v", err)
	}
	oneByteOff := append([]byte(nil), doc...)
	oneByteOff[len("---\ntype: skil")] = 'k'
	cases := []struct {
		name string
		doc  []byte
		want bool
	}{
		{"authored version", doc, true},
		{"assigned version", auto, true},
		{"one byte differs", oneByteOff, false},
		{"trailing byte added", append(append([]byte(nil), doc...), '\n'), false},
		{"comment missing", []byte("---\ntype: skill\nversion: 2.1.0\n---\n"), false},
		{"invalid version line", []byte("---\ntype: skill\nversion: 2.1\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"), false},
		{"reserved form with leading zero", []byte("---\ntype: skill\nversion: 0.0.0-auto.07\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"), false},
		{"injected key", []byte("---\ntype: skill\nversion: 2.1.0\nsensitivity: low\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"), false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsSynthesizedSkillDocument(tc.doc); got != tc.want {
				t.Errorf("IsSynthesizedSkillDocument(%q) = %v, want %v", tc.doc, got, tc.want)
			}
		})
	}
}

// Spec: §4.3.4 — the ARTIFACT.md version wins, a major.minor.patch
// metadata.version is the fallback, and any other metadata.version is
// ignored so the registry assigns one.
func TestResolveSkillVersion_Precedence(t *testing.T) {
	t.Parallel()
	skill := func(v string) *Skill { return &Skill{Metadata: map[string]string{"version": v}} }
	cases := []struct {
		name string
		a    *Artifact
		s    *Skill
		want string
	}{
		{"ARTIFACT.md only", &Artifact{Version: "1.2.3"}, &Skill{}, "1.2.3"},
		{"metadata.version only", &Artifact{}, skill("2.0.0"), "2.0.0"},
		{"metadata.version with nil artifact", nil, skill("2.0.0"), "2.0.0"},
		{"both equal", &Artifact{Version: "3.0.0"}, skill("3.0.0"), "3.0.0"},
		{"both differ", &Artifact{Version: "3.0.0"}, skill("4.0.0"), "3.0.0"},
		{"non-semver metadata.version", nil, skill("v1"), ""},
		{"reserved-form metadata.version", nil, skill("0.0.0-auto.2"), ""},
		{"no metadata", nil, &Skill{}, ""},
		{"ARTIFACT.md with nil skill", &Artifact{Version: "1.0.0"}, nil, "1.0.0"},
		{"nil inputs", nil, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveSkillVersion(tc.a, tc.s); got != tc.want {
				t.Errorf("ResolveSkillVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// Spec: §4.7.6 — the reserved form is exactly 0.0.0-auto.<N> with N a
// non-negative decimal integer without leading zeros that fits in an int.
func TestIsAutoVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		wantN  int
		wantOK bool
	}{
		{"0.0.0-auto.0", 0, true},
		{"0.0.0-auto.42", 42, true},
		{AutoVersion(9), 9, true},
		{"0.0.0-auto.01", 0, false},
		{"0.0.0-auto.-1", 0, false},
		{"0.0.0-auto.", 0, false},
		{"1.0.0-auto.1", 0, false},
		{"0.0.0-auto.1\n", 0, false},
		{"1.0.0", 0, false},
		{"0.0.0-auto.99999999999999999999999999", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			n, ok := IsAutoVersion(tc.in)
			if n != tc.wantN || ok != tc.wantOK {
				t.Errorf("IsAutoVersion(%q) = (%d, %v), want (%d, %v)", tc.in, n, ok, tc.wantN, tc.wantOK)
			}
		})
	}
}

// Spec: §4.7.6 — AutoVersion renders N in decimal after the reserved prefix.
func TestAutoVersion(t *testing.T) {
	t.Parallel()
	for n, want := range map[int]string{0: "0.0.0-auto.0", 15: "0.0.0-auto.15"} {
		if got := AutoVersion(n); got != want {
			t.Errorf("AutoVersion(%d) = %q, want %q", n, got, want)
		}
	}
}
