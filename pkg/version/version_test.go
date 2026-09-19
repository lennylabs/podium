package version

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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

// frame returns v's length as an unsigned 64-bit big-endian integer followed
// by v, which is what §4.7.6 calls a framed value. The tests below build the
// canonical stream out of the spec text with it; they never call
// CanonicalContentHash to derive an expectation.
func frame(v []byte) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(v)))
	return append(n[:], v...)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Spec: §4.7.6 — the canonical serialization is the framed manifest, the framed
// SKILL.md, then each resource's framed path and framed body in ascending path
// order. A framed value is its length as an unsigned 64-bit big-endian integer
// followed by its bytes. This vector is built from the spec text and not from
// CanonicalContentHash; a change to either the literal stream or the recorded
// digest is a change to §4.7.6 and needs a spec edit first.
//
// An absent and an empty SKILL.md frame alike, which is deliberate: the wire's
// skill_raw is omitempty, so ingest's nil slot and the consumer's empty slice
// describe the same artifact, and a manifest with no frontmatter is refused at
// parse time, so no stored artifact carries a zero-byte SKILL.md.
func TestCanonicalContentHash_MatchesTheSpecSerialization(t *testing.T) {
	t.Parallel()

	// A package carrying a manifest, a SKILL.md, and two bundled resources.
	// The resources are supplied in reverse of the order the stream frames
	// them in, so the ascending-path order is what the digest depends on.
	var stream []byte
	stream = append(stream, frame([]byte("artifact"))...)
	stream = append(stream, frame([]byte("skill"))...)
	stream = append(stream, frame([]byte("a.md"))...)
	stream = append(stream, frame([]byte("A"))...)
	stream = append(stream, frame([]byte("ref.md"))...)
	stream = append(stream, frame([]byte("R"))...)
	resources := map[string][]byte{}
	resources["ref.md"] = []byte("R")
	resources["a.md"] = []byte("A")
	// 2c176d6fac7fba0805b19f1a412df9ea3d685419f30f33da29dd8e47e82259fd
	if got, want := CanonicalContentHash([]byte("artifact"), []byte("skill"), resources), sha256Hex(stream); got != want {
		t.Errorf("CanonicalContentHash = %q, want %q", got, want)
	}

	// A package that declares no SKILL.md and carries one bundled resource.
	// The SKILL.md slot is framed as a zero-length value rather than omitted,
	// which is the boundary an implementation that frames only the parts it
	// has would otherwise get wrong.
	var noSkill []byte
	noSkill = append(noSkill, frame([]byte("artifact"))...)
	noSkill = append(noSkill, frame(nil)...)
	noSkill = append(noSkill, frame([]byte("data/a.txt"))...)
	noSkill = append(noSkill, frame([]byte("A"))...)
	// 3969c4804f97491fbcc06dd87341246b7d9d37cf602b880de126e9662237d61f
	got := CanonicalContentHash([]byte("artifact"), nil, map[string][]byte{"data/a.txt": []byte("A")})
	if want := sha256Hex(noSkill); got != want {
		t.Errorf("CanonicalContentHash (no SKILL.md) = %q, want %q", got, want)
	}
}

// Spec: §4.7.6 — the framing makes the serialization injective, so moving a
// byte across a part boundary changes the digest. Before the framing each of
// these pairs collided.
func TestCanonicalContentHash_RepartitioningChangesTheDigest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b string
	}{
		{
			name: "manifest/SKILL.md",
			a:    CanonicalContentHash([]byte("AB"), []byte("C"), nil),
			b:    CanonicalContentHash([]byte("A"), []byte("BC"), nil),
		},
		{
			name: "SKILL.md/first resource path",
			a: CanonicalContentHash([]byte("M"), []byte("SA"),
				map[string][]byte{"b.md": []byte("R")}),
			b: CanonicalContentHash([]byte("M"), []byte("S"),
				map[string][]byte{"Ab.md": []byte("R")}),
		},
		{
			name: "resource path/body",
			a:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{"ab": []byte("c")}),
			b:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{"a": []byte("bc")}),
		},
		{
			name: "adjacent resources",
			a: CanonicalContentHash([]byte("M"), nil,
				map[string][]byte{"a": []byte("xb"), "c": []byte("y")}),
			b: CanonicalContentHash([]byte("M"), nil,
				map[string][]byte{"a": []byte("x"), "bc": []byte("y")}),
		},
	}
	for _, c := range cases {
		if c.a == c.b {
			t.Errorf("%s: repartitioned packages share the digest %q", c.name, c.a)
		}
	}
}

// Spec: §4.7.6 — resources are framed in ascending path order, so the digest
// does not depend on the order the caller's map was built in.
func TestCanonicalContentHash_IgnoresMapInsertionOrder(t *testing.T) {
	t.Parallel()
	first := map[string][]byte{}
	first["a.md"] = []byte("A")
	first["ref.md"] = []byte("R")
	second := map[string][]byte{}
	second["ref.md"] = []byte("R")
	second["a.md"] = []byte("A")

	a := CanonicalContentHash([]byte("artifact"), []byte("skill"), first)
	b := CanonicalContentHash([]byte("artifact"), []byte("skill"), second)
	if a != b {
		t.Errorf("insertion order changed the digest: %q != %q", a, b)
	}
}
