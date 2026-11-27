package attest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// stream builds a §4.7.6 serialization by hand, independently of
// version.WriteFramed, so a test compares the digest functions with the
// framing the spec states rather than with themselves.
type stream struct{ bytes.Buffer }

func (s *stream) frame(v string) *stream {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(v)))
	s.Write(n[:])
	s.WriteString(v)
	return s
}

func (s *stream) digest() string {
	sum := sha256.Sum256(s.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hexSum(b string) string { return FileDigest([]byte(b)) }

// Spec: §4.7.6 — the package digest frames the tag, the state, the resolved
// fields of a resolved package in the order type, sensitivity, deprecated,
// and replaced_by, and each entry's path and file digest in ascending
// byte-wise UTF-8 order. The paths U+FF61 and U+1F600 sort the other way in
// UTF-16, so a JavaScript sort of the same entries gives another digest.
func TestPackageDigest_SerializationMatchesTheSpec(t *testing.T) {
	t.Parallel()
	entries := map[string]Sum{
		"\U0001F600.txt": sha256.Sum256([]byte("b")),
		"｡.txt":          sha256.Sum256([]byte("a")),
		"SKILL.md":       sha256.Sum256([]byte("s")),
	}
	resolved := Package{
		Resolved: true,
		Fields:   manifest.ResolvedFields{Type: manifest.TypeSkill, Sensitivity: manifest.SensitivityHigh, Deprecated: true, ReplacedBy: "a/b"},
		Entries:  entries,
	}
	want := new(stream).frame("podium/package-digest/1").frame("resolved").
		frame("skill").frame("high").frame("true").frame("a/b").
		frame("SKILL.md").frame(hexSum("s")).
		frame("｡.txt").frame(hexSum("a")).
		frame("\U0001F600.txt").frame(hexSum("b")).digest()
	if got := PackageDigest(resolved); got != want {
		t.Errorf("resolved digest = %s, want %s", got, want)
	}

	absent := Package{Resolved: true, Fields: manifest.ResolvedFields{Type: manifest.TypeRule}, Entries: map[string]Sum{}}
	want = new(stream).frame("podium/package-digest/1").frame("resolved").
		frame("rule").frame("").frame("false").frame("").digest()
	if got := PackageDigest(absent); got != want {
		t.Errorf("digest with absent fields = %s, want %s", got, want)
	}

	unresolved := Package{Fields: resolved.Fields, Entries: map[string]Sum{"ARTIFACT.md": sha256.Sum256(nil)}}
	want = new(stream).frame("podium/package-digest/1").frame("unresolved").
		frame("ARTIFACT.md").frame(hexSum("")).digest()
	if got := PackageDigest(unresolved); got != want {
		t.Errorf("unresolved digest = %s, want %s; an unresolved package frames no field", got, want)
	}
}

// Spec: §4.7.6 — the pinned digest of a fixed package fails the build when
// the serialization changes.
func TestPackageDigest_Pinned(t *testing.T) {
	t.Parallel()
	p := Package{
		Resolved: true,
		Fields:   manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityMedium},
		Entries: map[string]Sum{
			"ARTIFACT.md":       sha256.Sum256([]byte("---\ntype: agent\nsensitivity: medium\n---\nBody.\n")),
			"schemas/in.json":   sha256.Sum256([]byte("{}\n")),
			".hidden/notes.txt": sha256.Sum256([]byte("notes\n")),
		},
	}
	const want = "sha256:065443622a904e7206b20764c861d19fe95be135668bdd474c3ffa54c0a414f3"
	if got := PackageDigest(p); got != want {
		t.Errorf("PackageDigest = %s, want the pinned %s", got, want)
	}
}

// Spec: §4.7.6 — the root SKILL.md is one entry whatever its role, a
// zero-length skill document is omitted, and a zero-length SKILL.md that is a
// bundled resource, including one beside an ARTIFACT.md that does not parse,
// is listed under the digest of zero bytes.
func TestNewPackage_EntryRule(t *testing.T) {
	t.Parallel()
	empty := sha256.Sum256(nil)
	body := sha256.Sum256([]byte("x"))
	skillDoc := manifest.ResolvePackage(manifest.PackageDocuments{SkillMD: []byte{}, HasSkillMD: true})
	rule := manifest.ResolvePackage(manifest.PackageDocuments{ArtifactMD: []byte("---\ntype: rule\n---\nBody.\n"), HasArtifactMD: true})
	broken := manifest.ResolvePackage(manifest.PackageDocuments{ArtifactMD: []byte("no frontmatter"), HasArtifactMD: true})
	cases := []struct {
		name   string
		res    manifest.PackageResolution
		files  map[string]Sum
		listed bool
	}{
		{"zero-length skill document", skillDoc, map[string]Sum{"SKILL.md": empty}, false},
		{"skill document with bytes", skillDoc, map[string]Sum{"SKILL.md": body}, true},
		{"zero-length resource of a rule", rule, map[string]Sum{"ARTIFACT.md": body, "SKILL.md": empty}, true},
		{"zero-length resource beside an unparseable ARTIFACT.md", broken, map[string]Sum{"ARTIFACT.md": body, "SKILL.md": empty}, true},
	}
	for _, tc := range cases {
		p := NewPackage(tc.res, tc.files)
		if _, ok := p.Entries["SKILL.md"]; ok != tc.listed {
			t.Errorf("%s: SKILL.md listed = %v, want %v", tc.name, ok, tc.listed)
		}
		if _, ok := tc.files["SKILL.md"]; !ok {
			t.Errorf("%s: NewPackage modified its input", tc.name)
		}
		if p.Resolved != tc.res.Resolved || p.Fields != tc.res.Fields {
			t.Errorf("%s: Package = %+v, want the resolution's state and fields", tc.name, p)
		}
	}
	nested := NewPackage(skillDoc, map[string]Sum{"refs/SKILL.md": empty})
	if _, ok := nested.Entries["refs/SKILL.md"]; !ok {
		t.Errorf("a nested zero-length SKILL.md is a resource and stays listed")
	}
}

// Spec: §4.7.6 — the layer digest frames the tag, the decimal number of
// subjects, each subject's name and package digest in ascending byte-wise
// name order, and each layer file's path and file digest in path order.
func TestLayerDigest_SerializationMatchesTheSpec(t *testing.T) {
	t.Parallel()
	l := Layer{
		Subjects: []Subject{
			{Name: "z/last", Digest: "sha256:22"},
			{Name: "a/first@1.0.0", Digest: "sha256:11"},
		},
		Files: map[string]Sum{
			"finance/DOMAIN.md": sha256.Sum256([]byte("f")),
			".layer-config":     sha256.Sum256([]byte("c")),
			"DOMAIN.md":         sha256.Sum256([]byte("r")),
		},
	}
	want := new(stream).frame("podium/layer-digest/1").frame("2").
		frame("a/first@1.0.0").frame("sha256:11").
		frame("z/last").frame("sha256:22").
		frame(".layer-config").frame(hexSum("c")).
		frame("DOMAIN.md").frame(hexSum("r")).
		frame("finance/DOMAIN.md").frame(hexSum("f")).digest()
	if got := LayerDigest(l); got != want {
		t.Errorf("LayerDigest = %s, want %s", got, want)
	}
	if l.Subjects[0].Name != "z/last" {
		t.Errorf("LayerDigest reordered its input")
	}

	// The number separates the lists: a subject moved into the file list
	// under the same bytes gives another digest.
	moved := Layer{Files: map[string]Sum{"a/first@1.0.0": {}}}
	if LayerDigest(moved) == LayerDigest(Layer{Subjects: []Subject{{Name: "a/first@1.0.0", Digest: formatSum(Sum{})}}}) {
		t.Errorf("a subject and a layer file of the same bytes give one digest")
	}

	emptyWant := new(stream).frame("podium/layer-digest/1").frame("0").digest()
	if got := LayerDigest(Layer{}); got != emptyWant {
		t.Errorf("empty layer digest = %s, want %s", got, emptyWant)
	}
}
