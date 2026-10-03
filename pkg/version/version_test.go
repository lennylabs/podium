package version

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
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

// Spec: §4.7.6 — a resource path is framed as the bytes it was ingested as,
// with no Unicode normal-form conversion and no case folding, so the same
// name in NFC and in NFD is two resources and "A.md" and "a.md" are two
// resources. A normalization step added to the composer would move every
// stored hash that carries such a path, and this test is the one place that
// catches it: neither resource walk converts a path either.
func TestCanonicalContentHash_PathBytesAreNotNormalized(t *testing.T) {
	t.Parallel()
	const nfc = "caf\xc3\xa9.md"  // the UTF-8 of U+00E9
	const nfd = "cafe\xcc\x81.md" // "e" followed by the UTF-8 of U+0301
	body := []byte("resource body")
	cases := []struct {
		name string
		a, b string
	}{
		{
			name: "NFC/NFD",
			a:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{nfc: body}),
			b:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{nfd: body}),
		},
		{
			name: "letter case",
			a:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{"A.md": body}),
			b:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{"a.md": body}),
		},
	}
	for _, c := range cases {
		if c.a == c.b {
			t.Errorf("%s: two distinct paths share the digest %q", c.name, c.a)
		}
	}
}

// Spec: §4.7.6 — a part is framed as the bytes it was ingested as, with no
// line-ending conversion and no byte-order-mark stripping. The mark case sits
// on a resource body because manifest.SplitFrontmatter anchors the
// frontmatter at the first byte and refuses a manifest that opens with one,
// so no stored manifest carries it.
func TestCanonicalContentHash_BodyBytesAreNotNormalized(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b string
	}{
		{
			name: "line endings",
			a:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{"r.md": []byte("a\r\nb\n")}),
			b:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{"r.md": []byte("a\nb\n")}),
		},
		{
			name: "byte-order mark",
			a:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{"r.md": []byte("\xef\xbb\xbfx\n")}),
			b:    CanonicalContentHash([]byte("M"), nil, map[string][]byte{"r.md": []byte("x\n")}),
		},
	}
	for _, c := range cases {
		if c.a == c.b {
			t.Errorf("%s: two distinct bodies share the digest %q", c.name, c.a)
		}
	}
}

// deliveryStream frames the §4.7.10 fields in the order the spec text lists
// them, starting with the given leading values. The tests build expectations
// with it and never call DeliveryHash to derive one.
func deliveryStream(parts ...string) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, frame([]byte(p))...)
	}
	return out
}

// Spec: §4.7.10 — the delivery serialization is the framed tag
// "podium/delivery-record/2", the framed identity, version, type, content
// hash, sensitivity, and ingest time, the framed ARTIFACT.md document, manifest body, and
// SKILL.md, then each bundled resource's framed path and framed content hash in
// ascending byte-wise path order, served as sha256:<hex>. This vector is built
// from the spec text and not from DeliveryHash.
func TestDeliveryHash_MatchesTheSpecSerialization(t *testing.T) {
	t.Parallel()

	// Two resources, inserted in reverse of the order the stream frames them
	// in, so the ascending-path order is what the digest depends on.
	stream := deliveryStream(
		"podium/delivery-record/2",
		"team/review", "1.2.0", "skill", "sha256:abc", "internal",
		"2025-01-01T00:00:00.000000Z",
		"---\nname: review\n---\n", "body\n", "---\nname: review\n---\nskill\n",
		"a.md", "sha256:aaa",
		"ref.md", "sha256:rrr",
	)
	resources := map[string]string{}
	resources["ref.md"] = "sha256:rrr"
	resources["a.md"] = "sha256:aaa"
	got := DeliveryHash(DeliveryRecord{
		ID: "team/review", Version: "1.2.0", Type: "skill",
		ContentHash: "sha256:abc", Sensitivity: "internal",
		ArtifactRevision: "2025-01-01T00:00:00.000000Z",
		Frontmatter:      "---\nname: review\n---\n",
		ManifestBody:     "body\n",
		SkillRaw:         "---\nname: review\n---\nskill\n",
		Resources:        resources,
	})
	if want := "sha256:" + sha256Hex(stream); got != want {
		t.Errorf("DeliveryHash = %q, want %q", got, want)
	}

	// No SKILL.md and one resource: the SKILL.md slot frames a zero-length
	// value rather than being omitted.
	noSkill := deliveryStream(
		"podium/delivery-record/2",
		"team/rule", "0.1.0", "rule", "sha256:def", "public",
		"1970-01-01T00:00:00.000000Z",
		"---\nname: rule\n---\n", "", "",
		"data/a.txt", "sha256:aaa",
	)
	got = DeliveryHash(DeliveryRecord{
		ID: "team/rule", Version: "0.1.0", Type: "rule",
		ContentHash: "sha256:def", Sensitivity: "public",
		ArtifactRevision: "1970-01-01T00:00:00.000000Z",
		Frontmatter:      "---\nname: rule\n---\n",
		Resources:        map[string]string{"data/a.txt": "sha256:aaa"},
	})
	if want := "sha256:" + sha256Hex(noSkill); got != want {
		t.Errorf("DeliveryHash (no SKILL.md) = %q, want %q", got, want)
	}
}

// Spec: §4.7.10 — the framing makes the delivery serialization injective, so
// moving a byte across a field boundary changes the digest.
func TestDeliveryHash_RepartitioningChangesTheDigest(t *testing.T) {
	t.Parallel()
	base := DeliveryRecord{ID: "a/b", Version: "1.0.0", Type: "skill", Frontmatter: "FM", ManifestBody: "BODY"}
	with := func(f func(*DeliveryRecord)) string {
		r := base
		f(&r)
		return DeliveryHash(r)
	}
	// The tag/ID boundary: a stream whose tag absorbed the ID's first byte
	// would frame "podium/delivery-record/2a" then "/b". Hash that stream
	// directly, since the tag is not a field a caller sets.
	shifted := "sha256:" + sha256Hex(deliveryStream(
		"podium/delivery-record/2a", "/b", "1.0.0", "skill", "", "", "", "FM", "BODY", ""))
	cases := []struct {
		name string
		a, b string
	}{
		{name: "tag/ID", a: DeliveryHash(base), b: shifted},
		{
			name: "ID/version",
			a:    with(func(r *DeliveryRecord) { r.ID, r.Version = "a/b1", ".0.0" }),
			b:    DeliveryHash(base),
		},
		{
			name: "sensitivity/ingest time",
			a:    with(func(r *DeliveryRecord) { r.Sensitivity, r.ArtifactRevision = "internal", "1970" }),
			b:    with(func(r *DeliveryRecord) { r.Sensitivity, r.ArtifactRevision = "internal1970", "" }),
		},
		{
			name: "frontmatter/body",
			a:    with(func(r *DeliveryRecord) { r.Frontmatter, r.ManifestBody = "FMB", "ODY" }),
			b:    DeliveryHash(base),
		},
		{
			name: "resource path/resource hash",
			a:    with(func(r *DeliveryRecord) { r.Resources = map[string]string{"ab": "c"} }),
			b:    with(func(r *DeliveryRecord) { r.Resources = map[string]string{"a": "bc"} }),
		},
	}
	for _, c := range cases {
		if c.a == c.b {
			t.Errorf("%s: repartitioned records share the digest %q", c.name, c.a)
		}
	}
}

// Spec: §4.7.10 — the ingest time is a framed field, so two records that
// differ only in ArtifactRevision carry different delivery digests.
func TestDeliveryHash_ArtifactRevisionChangesTheDigest(t *testing.T) {
	t.Parallel()
	base := DeliveryRecord{
		ID: "a/b", Version: "1.0.0", Type: "skill", ContentHash: "sha256:c",
		Sensitivity: "internal", ArtifactRevision: "2025-01-01T00:00:00.000000Z",
	}
	later := base
	later.ArtifactRevision = "2025-01-01T00:00:00.000001Z"
	if a, b := DeliveryHash(base), DeliveryHash(later); a == b {
		t.Errorf("records differing only in ArtifactRevision share the digest %q", a)
	}
}

// Spec: §4.7.10 — resources are framed in ascending path order, so the digest
// does not depend on the order the caller's map was built in.
func TestDeliveryHash_IgnoresMapInsertionOrder(t *testing.T) {
	t.Parallel()
	first := map[string]string{}
	first["a.md"] = "sha256:aaa"
	first["ref.md"] = "sha256:rrr"
	second := map[string]string{}
	second["ref.md"] = "sha256:rrr"
	second["a.md"] = "sha256:aaa"

	a := DeliveryHash(DeliveryRecord{ID: "x/y", Resources: first})
	b := DeliveryHash(DeliveryRecord{ID: "x/y", Resources: second})
	if a != b {
		t.Errorf("insertion order changed the digest: %q != %q", a, b)
	}
}

// Spec: §4.7.10, §4.7.6 — the delivery digest stays separate from every
// stored content hash. Equal framed streams frame the same number of values,
// and the untagged delivery stream frames an odd count while a §4.7.6 stream
// frames an even one, so only the tagged stream can coincide with a content
// hash. The record below makes it coincide with the content hash of a package
// whose ARTIFACT.md bytes are the tag: the tag fills the ARTIFACT.md slot, the
// ID fills the SKILL.md slot, and the remaining fields pair up as ascending
// resource paths and bodies. The separation therefore rests on ingest refusing
// that package, which it does because the tag carries no frontmatter.
func TestDeliveryHash_DiffersFromContentHashForTheSameBytes(t *testing.T) {
	t.Parallel()
	const tag = "podium/delivery-record/2"
	rec := DeliveryRecord{
		ID: "s", Version: "p1",
		Type: "v1", ContentHash: "p2",
		Sensitivity: "v2", ArtifactRevision: "p3",
		Frontmatter: "v3", ManifestBody: "p4",
		SkillRaw:  "v4",
		Resources: map[string]string{"p5": "v5"},
	}
	content := CanonicalContentHash([]byte(tag), []byte("s"), map[string][]byte{
		"p1": []byte("v1"), "p2": []byte("v2"), "p3": []byte("v3"),
		"p4": []byte("v4"), "p5": []byte("v5"),
	})
	tagged := sha256Hex(deliveryStream(tag, "s", "p1", "v1", "p2", "v2", "p3", "v3", "p4", "v4", "p5", "v5"))
	if tagged != content {
		t.Fatalf("fixture does not reproduce the §4.7.6 stream: %q != %q", tagged, content)
	}
	if got, want := DeliveryHash(rec), "sha256:"+content; got != want {
		t.Errorf("DeliveryHash = %q, want the coinciding content hash %q", got, want)
	}
	if _, err := manifest.ParseArtifact([]byte(tag)); !errors.Is(err, manifest.ErrNoFrontmatter) {
		t.Errorf("ParseArtifact(tag) error = %v, want manifest.ErrNoFrontmatter", err)
	}
}

// Spec: §4.7.10, §7.2.1 — the ingest time is written in UTC with exactly six
// fractional digits and a literal Z, and a zero or pre-1970 ingest time is
// written as the epoch.
func TestFormatArtifactRevision(t *testing.T) {
	t.Parallel()
	east := time.FixedZone("east", 5*3600)
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{name: "zero", in: time.Time{}, want: "1970-01-01T00:00:00.000000Z"},
		{name: "pre-1970", in: time.Date(1969, 12, 31, 23, 59, 59, 999_999_000, time.UTC), want: "1970-01-01T00:00:00.000000Z"},
		{name: "epoch", in: time.Unix(0, 0), want: "1970-01-01T00:00:00.000000Z"},
		{name: "whole second", in: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), want: "2025-01-01T00:00:00.000000Z"},
		{name: "sub-microsecond truncated", in: time.Date(2025, 6, 2, 3, 4, 5, 123_456_789, time.UTC), want: "2025-06-02T03:04:05.123456Z"},
		{name: "non-UTC zone", in: time.Date(2025, 6, 2, 8, 4, 5, 1_000, east), want: "2025-06-02T03:04:05.000001Z"},
	}
	for _, c := range cases {
		if got := FormatArtifactRevision(c.in); got != c.want {
			t.Errorf("%s: FormatArtifactRevision = %q, want %q", c.name, got, c.want)
		}
	}
}

// Spec: §4.7.6 — StripPin drops every pin form and returns the canonical ID
// unchanged when the reference carries no pin.
func TestStripPin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"foo/bar", "foo/bar"},
		{"foo/bar@1.0.0", "foo/bar"},
		{"foo/bar@1.2.x", "foo/bar"},
		{"foo/bar@sha256:abc", "foo/bar"},
		{"", ""},
	}
	for _, c := range cases {
		if got := StripPin(c.in); got != c.want {
			t.Errorf("StripPin(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
