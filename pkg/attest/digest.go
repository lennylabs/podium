// Package attest holds the author-attestation surface that registry ingest,
// filesystem-source sync, `podium attest`, and the Go consumers share: the
// §4.7.6 package digest and layer digest, and the subject names that bind an
// artifact or a layer to one of them. The registry, the signing workflow, and
// a consumer each compute the digests from what they hold, so every
// serialization here is fixed once it ships (§4.7.6 *Format stability*).
package attest

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strconv"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/version"
)

// Serialization tags. A change to a serialization, to the resolution of the
// resolved fields, or to the discovery behind the artifact list takes a new
// tag version rather than an edit of these.
const (
	packageDigestTag = "podium/package-digest/1"
	layerDigestTag   = "podium/layer-digest/1"
)

// Resolution states framed after the package digest tag.
const (
	stateResolved   = "resolved"
	stateUnresolved = "unresolved"
)

// digestPrefix is the algorithm prefix of every digest this package writes.
const digestPrefix = "sha256:"

// Sum is the SHA-256 digest of one file's bytes.
type Sum = [sha256.Size]byte

// emptySum is the digest of zero bytes, which marks a zero-length document.
var emptySum = sha256.Sum256(nil)

// FileDigest returns the §4.7.6 file digest of b: the text sha256: followed
// by the lowercase hexadecimal SHA-256 digest of the bytes.
//
// Spec: §4.7.6
func FileDigest(b []byte) string {
	return formatSum(sha256.Sum256(b))
}

func formatSum(s Sum) string {
	return digestPrefix + hex.EncodeToString(s[:])
}

// Package is the input of PackageDigest. Fields are framed only when
// Resolved is true. Entries maps each entry path, slash-separated and
// relative to the package root, to the digest of the file's bytes; it holds
// the entries after the §4.7.6 entry rule, so a zero-length skill document is
// absent from it.
type Package struct {
	Resolved bool
	Fields   manifest.ResolvedFields
	Entries  map[string]Sum
}

// NewPackage returns the Package of a package read from a tree or a stored
// row. files maps every file of the package to its digest, the root
// ARTIFACT.md and SKILL.md included, under paths relative to the package
// root; res is the package's resolution. The root SKILL.md stays an entry
// whatever its role, except that a zero-length skill document is omitted, so
// an absent and an empty skill document give one digest. A zero-length
// SKILL.md that is a bundled resource stays an entry. files is not modified.
//
// Spec: §4.7.6
func NewPackage(res manifest.PackageResolution, files map[string]Sum) Package {
	entries := make(map[string]Sum, len(files))
	for p, sum := range files {
		if p == manifest.SkillFile && sum == emptySum && res.SkillDocument() {
			continue
		}
		entries[p] = sum
	}
	return Package{Resolved: res.Resolved, Fields: res.Fields, Entries: entries}
}

// PackageDigest returns the §4.7.6 package digest of p, written
// sha256:<hex>: the SHA-256 digest of the framed tag, the framed resolution
// state, the framed resolved fields of a resolved package in the order type,
// sensitivity, deprecated, and replaced_by, and each entry's framed path and
// framed file digest in ascending byte-wise path order. The state fixes how
// many field values follow it, so no field value reads as an entry path.
//
// Spec: §4.7.6
func PackageDigest(p Package) string {
	h := sha256.New()
	version.WriteFramed(h, []byte(packageDigestTag))
	if p.Resolved {
		version.WriteFramed(h, []byte(stateResolved))
		version.WriteFramed(h, []byte(p.Fields.Type))
		version.WriteFramed(h, []byte(p.Fields.Sensitivity))
		version.WriteFramed(h, []byte(strconv.FormatBool(p.Fields.Deprecated)))
		version.WriteFramed(h, []byte(p.Fields.ReplacedBy))
	} else {
		version.WriteFramed(h, []byte(stateUnresolved))
	}
	writeEntries(h, p.Entries)
	return digestPrefix + hex.EncodeToString(h.Sum(nil))
}

// Subject is one artifact of a layer's artifact list: its subject name and
// its package digest in the sha256:<hex> form.
type Subject struct {
	Name   string
	Digest string
}

// Layer is the input of LayerDigest: one subject per package that discovery
// finds under the layer root, and the digest of each layer file under its
// path relative to the layer root.
type Layer struct {
	Subjects []Subject
	Files    map[string]Sum
}

// LayerDigest returns the §4.7.6 layer digest of l, written sha256:<hex>:
// the SHA-256 digest of the framed tag, the framed decimal number of
// subjects, each subject's framed name and framed package digest in ascending
// byte-wise name order, and each layer file's framed path and framed file
// digest in ascending byte-wise path order. The number separates the two
// lists, so no path reads as a subject name. l is not modified.
//
// Spec: §4.7.6
func LayerDigest(l Layer) string {
	subjects := append([]Subject(nil), l.Subjects...)
	sort.Slice(subjects, func(i, j int) bool { return subjects[i].Name < subjects[j].Name })
	h := sha256.New()
	version.WriteFramed(h, []byte(layerDigestTag))
	version.WriteFramed(h, []byte(strconv.Itoa(len(subjects))))
	for _, s := range subjects {
		version.WriteFramed(h, []byte(s.Name))
		version.WriteFramed(h, []byte(s.Digest))
	}
	writeEntries(h, l.Files)
	return digestPrefix + hex.EncodeToString(h.Sum(nil))
}

// writeEntries frames each path and its file digest in ascending byte-wise
// path order. Go compares strings byte by byte, which is the UTF-8 order the
// spec fixes rather than the UTF-16 order a JavaScript sort gives.
func writeEntries(h io.Writer, entries map[string]Sum) {
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		version.WriteFramed(h, []byte(p))
		version.WriteFramed(h, []byte(formatSum(entries[p])))
	}
}
