package version

import (
	"errors"
	"testing"
)

// Spec: §4.7.6 — empty string resolves to PinLatest.
func TestParsePin_LatestForEmpty(t *testing.T) {
	t.Parallel()
	p, err := ParsePin("")
	if err != nil {
		t.Fatalf("ParsePin: %v", err)
	}
	if p.Kind != PinLatest {
		t.Errorf("Kind = %v, want PinLatest", p.Kind)
	}
}

// Spec: §4.7.6 — exact, minor (1.2.x), major (1.x), and content-hash
// pins each parse into the expected PinKind.
func TestParsePin_AllForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		kind PinKind
	}{
		{"1.2.3", PinExact},
		{"1.2.x", PinMinor},
		{"1.x", PinMajor},
		{"sha256:" + repeat("a", 64), PinContentHash},
	}
	for _, c := range cases {
		p, err := ParsePin(c.in)
		if err != nil {
			t.Errorf("ParsePin(%q): %v", c.in, err)
			continue
		}
		if p.Kind != c.kind {
			t.Errorf("ParsePin(%q).Kind = %v, want %v", c.in, p.Kind, c.kind)
		}
	}
}

// Spec: §4.7.6 — invalid pin strings return ErrInvalidPin.
func TestParsePin_RejectsInvalid(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"v1.0.0",
		"1.2",
		"1.2.3.4",
		"sha256:tooshort",
		"abc",
	} {
		_, err := ParsePin(in)
		if !errors.Is(err, ErrInvalidPin) {
			t.Errorf("ParsePin(%q) = %v, want ErrInvalidPin", in, err)
		}
	}
}

// Spec: §4.7.6 — Resolve picks the highest version satisfying pin.
func TestResolve_HighestMatching(t *testing.T) {
	t.Parallel()
	candidates := []string{"1.0.0", "1.2.0", "1.2.5", "2.0.0", "2.1.3"}

	cases := []struct {
		pin, want string
	}{
		{"", "2.1.3"},      // latest
		{"1.x", "1.2.5"},   // major
		{"1.2.x", "1.2.5"}, // minor
		{"1.2.0", "1.2.0"}, // exact
		{"2.x", "2.1.3"},
	}
	for _, c := range cases {
		p, _ := ParsePin(c.pin)
		got, err := Resolve(p, candidates)
		if err != nil {
			t.Errorf("Resolve(%q): %v", c.pin, err)
			continue
		}
		if got != c.want {
			t.Errorf("Resolve(%q) = %q, want %q", c.pin, got, c.want)
		}
	}
}

// Spec: §4.7 — ContentHash is deterministic across identical inputs.
func TestContentHash_Deterministic(t *testing.T) {
	t.Parallel()
	a := ContentHash([]byte("frontmatter"), []byte("body"))
	b := ContentHash([]byte("frontmatter"), []byte("body"))
	if a != b {
		t.Errorf("got %q != %q", a, b)
	}
	c := ContentHash([]byte("different"))
	if a == c {
		t.Errorf("different inputs produced the same hash")
	}
	if len(a) != 64 {
		t.Errorf("hash length = %d, want 64 (sha256 hex)", len(a))
	}
}

func repeat(s string, n int) string {
	out := make([]byte, n*len(s))
	for i := 0; i < n; i++ {
		copy(out[i*len(s):], s)
	}
	return string(out)
}
