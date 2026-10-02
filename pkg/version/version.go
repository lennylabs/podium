// Package version implements semver pinning and content-hash derivation
// for spec §4.7.6 (Version Resolution and Consistency) and §4.7
// (immutability invariant), and the §4.7.10 delivery-hash derivation.
package version

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Errors returned by version functions.
var (
	// ErrInvalidPin signals an unparsable pinning string. Maps to
	// registry.invalid_argument when surfaced to clients.
	ErrInvalidPin = errors.New("version: invalid pin")
)

// PinKind enumerates the §4.7.6 pinning forms.
type PinKind int

// PinKind values.
const (
	PinExact PinKind = iota
	PinMinor
	PinMajor
	PinContentHash
	PinLatest
)

// Pin is a parsed reference. Major / Minor / Patch are populated when
// applicable; Hash is the sha256 hex content hash for PinContentHash.
type Pin struct {
	Kind  PinKind
	Major int
	Minor int
	Patch int
	Hash  string
}

// ParsePin parses one of the §4.7.6 forms:
//
//	""              -> PinLatest
//	"1.2.3"         -> PinExact
//	"1.2.x"         -> PinMinor
//	"1.x"           -> PinMajor
//	"sha256:<hex>"  -> PinContentHash
func ParsePin(s string) (Pin, error) {
	if s == "" {
		return Pin{Kind: PinLatest}, nil
	}
	if strings.HasPrefix(s, "sha256:") {
		hash := strings.TrimPrefix(s, "sha256:")
		if len(hash) != 64 {
			return Pin{}, fmt.Errorf("%w: sha256 must be 64 hex chars", ErrInvalidPin)
		}
		for i := 0; i < len(hash); i++ {
			c := hash[i]
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return Pin{}, fmt.Errorf("%w: sha256 must be hex; non-hex %q at offset %d",
					ErrInvalidPin, c, i)
			}
		}
		return Pin{Kind: PinContentHash, Hash: hash}, nil
	}
	parts := strings.Split(s, ".")
	switch len(parts) {
	case 2:
		// "1.x" form.
		if parts[1] != "x" {
			return Pin{}, fmt.Errorf("%w: %q", ErrInvalidPin, s)
		}
		major, err := strconv.Atoi(parts[0])
		if err != nil {
			return Pin{}, fmt.Errorf("%w: %v", ErrInvalidPin, err)
		}
		return Pin{Kind: PinMajor, Major: major}, nil
	case 3:
		major, err := strconv.Atoi(parts[0])
		if err != nil {
			return Pin{}, fmt.Errorf("%w: %v", ErrInvalidPin, err)
		}
		minor, err := strconv.Atoi(parts[1])
		if err != nil {
			return Pin{}, fmt.Errorf("%w: %v", ErrInvalidPin, err)
		}
		if parts[2] == "x" {
			return Pin{Kind: PinMinor, Major: major, Minor: minor}, nil
		}
		patch, err := strconv.Atoi(parts[2])
		if err != nil {
			return Pin{}, fmt.Errorf("%w: %v", ErrInvalidPin, err)
		}
		return Pin{Kind: PinExact, Major: major, Minor: minor, Patch: patch}, nil
	}
	return Pin{}, fmt.Errorf("%w: %q", ErrInvalidPin, s)
}

// StripPin returns the canonical ID portion of a §4.7.6 reference by
// dropping a trailing @<semver>, @<semver>.x, or @sha256:<hash> pin. It cuts
// at the first "@" because canonical-ID segments cannot contain "@"
// (filesystem.ValidateCanonicalID), so the pin always begins there.
//
// Spec: §4.7.6
func StripPin(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i]
	}
	return ref
}

// Resolve picks the highest version from candidates that satisfies pin.
// Candidates is a list of "major.minor.patch" strings; the resolved
// version is returned, or ErrInvalidPin when no match exists.
func Resolve(pin Pin, candidates []string) (string, error) {
	versions := make([]Pin, 0, len(candidates))
	for _, c := range candidates {
		v, err := ParsePin(c)
		if err != nil || v.Kind != PinExact {
			continue
		}
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool {
		return less(versions[j], versions[i]) // descending
	})
	for _, v := range versions {
		if matches(pin, v) {
			return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch), nil
		}
	}
	return "", fmt.Errorf("%w: no candidate matches", ErrInvalidPin)
}

// Candidate is one ingested version with the timestamp the registry
// recorded at ingest. ResolveLatest orders by IngestedAt so `latest`
// follows §4.7.6 ("the most recently ingested non-deprecated version")
// rather than the highest semver.
type Candidate struct {
	Version    string
	IngestedAt time.Time
}

// ResolveLatest implements the §4.7.6 `latest` rule: it returns the
// version with the greatest IngestedAt among candidates, breaking ties
// by the higher semver. Callers pass only the versions that are
// visible and non-deprecated; this function does not filter. It exists
// as a distinct path from Resolve because range, exact, and hash pins
// order by semver while `latest` orders by ingest time. A backported
// fix published after a newer major line is therefore selected by
// `latest` even though its semver is lower.
func ResolveLatest(candidates []Candidate) (string, error) {
	best := ""
	var bestAt time.Time
	var bestPin Pin
	for _, c := range candidates {
		p, err := ParsePin(c.Version)
		if err != nil || p.Kind != PinExact {
			continue
		}
		if best == "" || c.IngestedAt.After(bestAt) ||
			(c.IngestedAt.Equal(bestAt) && less(bestPin, p)) {
			best = c.Version
			bestAt = c.IngestedAt
			bestPin = p
		}
	}
	if best == "" {
		return "", fmt.Errorf("%w: no candidate matches", ErrInvalidPin)
	}
	return best, nil
}

// parseCore parses the major.minor.patch core of an exact semver string,
// discarding any prerelease or build suffix (so a "-dev" or "-rc.1" binary
// version compares by its release core). It returns ErrInvalidPin when the
// core is not an exact major.minor.patch triple.
func parseCore(s string) (Pin, error) {
	core := s
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	p, err := ParsePin(core)
	if err != nil {
		return Pin{}, err
	}
	if p.Kind != PinExact {
		return Pin{}, fmt.Errorf("%w: %q is not an exact version", ErrInvalidPin, s)
	}
	return p, nil
}

// Compare orders two exact semver strings by their major.minor.patch core,
// ignoring any prerelease or build suffix. It returns -1, 0, or 1 when a is
// below, equal to, or above b, and ErrInvalidPin when either string is not
// an exact version.
func Compare(a, b string) (int, error) {
	pa, err := parseCore(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseCore(b)
	if err != nil {
		return 0, err
	}
	switch {
	case less(pa, pb):
		return -1, nil
	case less(pb, pa):
		return 1, nil
	default:
		return 0, nil
	}
}

// AtLeast reports whether version is greater than or equal to minimum,
// comparing the major.minor.patch core and ignoring any prerelease or
// build suffix. It returns ErrInvalidPin when either string is not an
// exact version. Used by the §6.7 adapter-versioning pin to decide whether
// a binary satisfies a declared minimum MCP server version.
func AtLeast(version, minimum string) (bool, error) {
	c, err := Compare(version, minimum)
	if err != nil {
		return false, err
	}
	return c >= 0, nil
}

func matches(pin, candidate Pin) bool {
	switch pin.Kind {
	case PinLatest:
		return true
	case PinExact:
		return pin.Major == candidate.Major &&
			pin.Minor == candidate.Minor &&
			pin.Patch == candidate.Patch
	case PinMinor:
		return pin.Major == candidate.Major && pin.Minor == candidate.Minor
	case PinMajor:
		return pin.Major == candidate.Major
	}
	return false
}

func less(a, b Pin) bool {
	if a.Major != b.Major {
		return a.Major < b.Major
	}
	if a.Minor != b.Minor {
		return a.Minor < b.Minor
	}
	return a.Patch < b.Patch
}

// CanonicalContentHash returns the §4.7.6 content hash of one artifact: the
// SHA-256 digest over the framed manifest bytes, the framed SKILL.md bytes, and
// each bundled resource's framed path and framed body in ascending path order.
// A framed value is its length as an unsigned 64-bit big-endian integer
// followed by its bytes. The digest carries no "sha256:" prefix; a caller that
// stores or serves the value adds one.
//
// The framing is what makes the serialization injective. Without it the digest
// is a function of the concatenation of the parts, so a byte moved across a
// part boundary leaves it unchanged and a resource named "ab" with body "c"
// hashes the same as one named "a" with body "bc". Every place the digest
// stands in for the artifact under an independent attestation then stops
// binding the bytes: the §4.7.9 signature envelope is produced over the hash at
// ingest and verified over the served hash, so a re-partitioned delivery would
// carry a valid signature from the legitimate signer; an @sha256: pin resolves
// against the stored value; and the §7.5.3 lock records it. The §6.6 step-2
// gate is where a consumer recomputes this value from the served bytes.
//
// It also exists so the registry and the filesystem consumer cannot compute the
// hash differently. They did: the filesystem path hashed SKILL.md instead of
// the manifest for a skill, so a frontmatter-only edit left the lock's
// content_hash unmoved while the materialized output changed.
//
// An absent SKILL.md frames a zero-length value, so an absent and an empty
// SKILL.md hash alike. That is deliberate: ingest passes a nil SKILL.md slot
// while the consumer passes the wire's omitempty skill_raw as a non-nil empty
// slice for the same artifact, and manifest parsing refuses input with no
// frontmatter, so no stored artifact carries a zero-byte SKILL.md.
func CanonicalContentHash(artifactBytes, skillBytes []byte, resources map[string][]byte) string {
	h := sha256.New()
	WriteFramed(h, artifactBytes)
	WriteFramed(h, skillBytes)
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		WriteFramed(h, []byte(k))
		WriteFramed(h, resources[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// WriteFramed writes v's length as an unsigned 64-bit big-endian integer
// followed by v itself, the §4.7.6 length framing. Every digest the registry
// frames uses it, so a domain-tagged digest outside this package frames its
// values the same way. A hash never fails a write.
// Spec: §4.7.6
func WriteFramed(w io.Writer, v []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(v)))
	_, _ = w.Write(n[:])
	_, _ = w.Write(v)
}

// deliveryRecordTag is the framed leading value of every §4.7.10 delivery
// stream.
const deliveryRecordTag = "podium/delivery-record/1"

// DeliveryRecord is the §4.7.10 delivery record: the values one load response
// serves, as the registry composes them and as the consumer receives them.
// Every field is the served value, so for a child declaring extends: the
// Frontmatter is the merged ARTIFACT.md document, whatever the type.
type DeliveryRecord struct {
	// ID is the canonical artifact ID the response carries.
	ID string
	// Version is the resolved semver the response carries.
	Version string
	// Type is the served artifact type.
	Type string
	// ContentHash is the §4.7.6 content hash of the stored artifact the
	// response was composed from, as served.
	ContentHash string
	// Sensitivity is the served sensitivity value.
	Sensitivity string
	// Frontmatter is the served ARTIFACT.md document.
	Frontmatter string
	// ManifestBody is the served manifest body.
	ManifestBody string
	// SkillRaw is the served SKILL.md, empty when the artifact has none.
	SkillRaw string
	// Resources maps each bundled resource's path to its content hash.
	Resources map[string]string
}

// DeliveryHash returns the §4.7.10 delivery hash of rec as "sha256:<hex>":
// the SHA-256 digest over the framed tag "podium/delivery-record/1", the
// framed ID, version, type, content hash, and sensitivity, the framed
// ARTIFACT.md document, manifest body, and SKILL.md, and then each bundled
// resource's framed path and framed content hash in ascending path order. An
// absent SKILL.md frames a zero-length value.
//
// The leading tag separates this digest's domain from the §4.7.6 content
// hash. Both use the same framing, the same hex encoding, and are signed under
// the same registry-managed key, so without the tag a stream that parses as
// both a delivery record and a stored package would give one digest two
// meanings, and a signature over either would be accepted as a signature over
// the other.
//
// Resources contribute their content hash rather than their body so that
// composing the digest reads nothing from object storage on each load. The
// consumer checks every resource body it fetches or decodes against the hash
// the verified record carries, which binds the body to the attestation.
//
// The per-caller extends_pin and the lifecycle fields are absent from the
// record: the digest must not depend on who reads the record or when, so the
// single-load path, the batch path, and every consumer agree on it.
//
// Spec: §4.7.10
func DeliveryHash(rec DeliveryRecord) string {
	h := sha256.New()
	for _, v := range []string{
		deliveryRecordTag,
		rec.ID, rec.Version, rec.Type, rec.ContentHash, rec.Sensitivity,
		rec.Frontmatter, rec.ManifestBody, rec.SkillRaw,
	} {
		WriteFramed(h, []byte(v))
	}
	paths := make([]string, 0, len(rec.Resources))
	for p := range rec.Resources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		WriteFramed(h, []byte(p))
		WriteFramed(h, []byte(rec.Resources[p]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
