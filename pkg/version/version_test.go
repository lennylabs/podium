package version

import (
	"errors"
	"testing"
	"time"
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

// Spec: §4.7.6 — `latest` is the most recently ingested version, not
// the highest semver. ResolveLatest orders by IngestedAt.
func TestResolveLatest_NewestByIngest(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cands := []Candidate{
		{Version: "1.0.0", IngestedAt: base},
		{Version: "2.0.0", IngestedAt: base.Add(1 * time.Hour)},
		{Version: "2.1.0", IngestedAt: base.Add(2 * time.Hour)},
	}
	got, err := ResolveLatest(cands)
	if err != nil {
		t.Fatalf("ResolveLatest: %v", err)
	}
	if got != "2.1.0" {
		t.Errorf("ResolveLatest = %q, want 2.1.0", got)
	}
}

// Spec: §4.7.6 — the backport case. A lower-semver line (1.2.4)
// ingested AFTER a newer major line (2.0.0) is the most recently
// ingested version and must win, even though 2.0.0 has the higher
// semver. This is the case that distinguishes ingest-time ordering
// from semver ordering.
func TestResolveLatest_BackportWinsOverHigherSemver(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cands := []Candidate{
		{Version: "1.0.0", IngestedAt: base},
		{Version: "2.0.0", IngestedAt: base.Add(1 * time.Hour)},
		{Version: "1.2.4", IngestedAt: base.Add(2 * time.Hour)}, // backport, newest
	}
	got, err := ResolveLatest(cands)
	if err != nil {
		t.Fatalf("ResolveLatest: %v", err)
	}
	if got != "1.2.4" {
		t.Errorf("ResolveLatest = %q, want 1.2.4 (backport ingested last)", got)
	}
}

// Spec: §4.7.6 — ties on ingest time are broken by the higher semver
// so resolution is deterministic when two versions share a timestamp.
func TestResolveLatest_TieBrokenByHigherSemver(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cands := []Candidate{
		{Version: "1.4.0", IngestedAt: at},
		{Version: "1.5.0", IngestedAt: at},
	}
	got, err := ResolveLatest(cands)
	if err != nil {
		t.Fatalf("ResolveLatest: %v", err)
	}
	if got != "1.5.0" {
		t.Errorf("ResolveLatest = %q, want 1.5.0 (tie broken by semver)", got)
	}
}

// Spec: §4.7.6 — an empty candidate set has no latest.
func TestResolveLatest_EmptyIsError(t *testing.T) {
	t.Parallel()
	if _, err := ResolveLatest(nil); !errors.Is(err, ErrInvalidPin) {
		t.Errorf("ResolveLatest(nil) = %v, want ErrInvalidPin", err)
	}
}

// Spec: §6.7 "Versioning" — Compare orders two exact versions by their
// major.minor.patch core, ignoring any -prerelease or +build suffix.
func TestCompare_Ordering(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"0.1.2", "0.2.0", -1},
		// Pre-release and build suffixes compare by the release core.
		{"0.0.0-dev", "0.0.0", 0},
		{"1.2.0-rc.1", "1.2.0", 0},
		{"1.2.3+build.5", "1.2.3", 0},
	}
	for _, c := range cases {
		got, err := Compare(c.a, c.b)
		if err != nil {
			t.Fatalf("Compare(%q,%q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// Spec: §6.7 "Versioning" — Compare rejects a non-exact version string.
func TestCompare_InvalidIsError(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"1.x", "1.2.x", "", "not-a-version", "1.2"} {
		if _, err := Compare(s, "1.0.0"); !errors.Is(err, ErrInvalidPin) {
			t.Errorf("Compare(%q,...) = %v, want ErrInvalidPin", s, err)
		}
	}
}

// Spec: §6.7 "Versioning" — AtLeast reports whether a binary version meets a
// pinned minimum.
func TestAtLeast(t *testing.T) {
	t.Parallel()
	cases := []struct {
		version, min string
		want         bool
	}{
		{"1.2.3", "1.2.3", true},
		{"1.2.4", "1.2.3", true},
		{"1.2.2", "1.2.3", false},
		{"0.1.2", "0.2.0", false},
		{"2.0.0", "1.9.9", true},
		{"0.0.0-dev", "0.0.0", true},
	}
	for _, c := range cases {
		got, err := AtLeast(c.version, c.min)
		if err != nil {
			t.Fatalf("AtLeast(%q,%q): %v", c.version, c.min, err)
		}
		if got != c.want {
			t.Errorf("AtLeast(%q,%q) = %v, want %v", c.version, c.min, got, c.want)
		}
	}
}

func repeat(s string, n int) string {
	out := make([]byte, n*len(s))
	for i := 0; i < n; i++ {
		copy(out[i*len(s):], s)
	}
	return string(out)
}
