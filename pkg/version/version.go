// Package version implements semver pinning and content-hash derivation
// for spec §4.7.6 (Version Resolution and Consistency) and §4.7
// (immutability invariant).
package version

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// ContentHash returns the SHA-256 hex digest of the canonicalized bytes.
// Spec §4.7 invariant: ingest is keyed by this hash.
func ContentHash(bytes ...[]byte) string {
	h := sha256.New()
	for _, b := range bytes {
		_, _ = h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}
