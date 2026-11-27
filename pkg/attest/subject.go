package attest

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/manifest"
)

// Subject name prefixes. Every name under reservedPrefix that is no layer
// subject name is reserved, and a statement that lists a name under
// signedAtPrefix is not accepted.
const (
	reservedPrefix  = ".podium/"
	layerSubject    = ".podium/layer"
	signedAtPrefix  = ".podium/signed-at/"
	versionSplitter = "@"
)

var (
	// ErrNoSubjectName reports a canonical ID or a layer root that holds a
	// character the §4.7.6 subject name rule excludes, or that is not valid
	// UTF-8. Such a package or layer has no subject name and cannot be
	// attested.
	ErrNoSubjectName = errors.New("attest: no subject name")
	// ErrReservedName reports a canonical ID under the reserved .podium/
	// prefix. Package discovery skips dot-prefixed directories, so no walk
	// yields one; the check keeps SubjectName from writing a reserved name
	// for any input.
	ErrReservedName = errors.New("attest: reserved subject name")
	// ErrSignedAtSubject reports a statement that lists a subject under
	// .podium/signed-at/, which no verifier accepts. The names are kept free
	// so that a signed time, should one be defined, cannot be carried by a
	// statement signed before that definition.
	ErrSignedAtSubject = errors.New("attest: statement lists a subject under .podium/signed-at/")
)

// CheckSubjectNameCharacters returns nil when s is valid UTF-8 and holds no
// character the §4.7.6 subject name rule excludes, and an error wrapping
// ErrNoSubjectName that names the first offending character otherwise. The
// excluded characters are the control characters, the backslash, and the
// characters that ECMAScript treats as white space or a line terminator,
// which is the set a JavaScript reader of a checksums file splits and trims
// on. A name holding one of them would read differently in the tools that
// write and read the one-subject-per-line checksums format, and a line feed
// would add a subject line of its own.
//
// Spec: §4.7.6
func CheckSubjectNameCharacters(s string) error {
	for i, r := range s {
		if r == utf8.RuneError {
			if _, size := utf8.DecodeRuneInString(s[i:]); size == 1 {
				return fmt.Errorf("%w: %q is not valid UTF-8 at byte %d", ErrNoSubjectName, s, i)
			}
		}
		if excludedRune(r) {
			return fmt.Errorf("%w: %q holds U+%04X at byte %d", ErrNoSubjectName, s, r, i)
		}
	}
	return nil
}

// CheckEntryPath returns an error wrapping ErrNoSubjectName when p, the path
// of a file relative to its package root, is not valid UTF-8. The package
// digest frames such a path as raw bytes, which a consumer that holds paths
// as Unicode strings, as every JSON reader does, cannot reproduce, so the
// package it belongs to has no subject name.
//
// Spec: §4.7.6
func CheckEntryPath(p string) error {
	if !utf8.ValidString(p) {
		return fmt.Errorf("%w: the file path %q is not valid UTF-8", ErrNoSubjectName, p)
	}
	return nil
}

// excludedRune reports whether r is a character the subject name rule
// excludes.
func excludedRune(r rune) bool {
	switch {
	case r <= 0x1F, r >= 0x7F && r <= 0x9F:
		return true
	case r >= 0x2000 && r <= 0x200A:
		return true
	}
	switch r {
	case ' ', '\\', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return false
}

// SubjectName returns the §4.7.6 subject name of the package with canonical
// ID id whose authored version is authoredVersion: <id>@<version> when the
// version is in major.minor.patch form, and the bare ID otherwise. A caller
// passes an empty authoredVersion for an unresolved package and for one that
// declares no version, so its name is the bare ID. An ID that breaks the
// character rule returns an error wrapping ErrNoSubjectName, and one under
// the reserved .podium/ prefix an error wrapping ErrReservedName.
//
// Spec: §4.7.6
func SubjectName(id, authoredVersion string) (string, error) {
	if err := CheckSubjectNameCharacters(id); err != nil {
		return "", err
	}
	if strings.HasPrefix(id, reservedPrefix) {
		return "", fmt.Errorf("%w: %q", ErrReservedName, id)
	}
	if manifest.ValidateVersion(authoredVersion) != nil {
		return id, nil
	}
	return id + versionSplitter + authoredVersion, nil
}

// ParseSubjectName splits an artifact subject name at its last @ into the
// canonical ID and the version, which is empty for a bare-ID name. A
// canonical ID holds no @ (§4.2), so the split is unambiguous.
//
// Spec: §4.7.6
func ParseSubjectName(name string) (id, version string) {
	i := strings.LastIndex(name, versionSplitter)
	if i < 0 {
		return name, ""
	}
	return name[:i], name[i+1:]
}

// LayerSubjectName returns the name of the layer subject of a layer whose
// registered root is root: .podium/layer for a layer at the root of its
// source, and .podium/layer/<root> otherwise, where <root> has its leading
// and trailing / characters removed. No other normalization applies, so a
// root with an empty, ".", or ".." segment yields a name that `podium attest`
// never writes and that therefore matches no layer subject. A root that
// breaks the character rule returns an error wrapping ErrNoSubjectName.
//
// Spec: §4.7.6
func LayerSubjectName(root string) (string, error) {
	trimmed := strings.Trim(root, "/")
	if trimmed == "" {
		return layerSubject, nil
	}
	if err := CheckSubjectNameCharacters(trimmed); err != nil {
		return "", err
	}
	return layerSubject + "/" + trimmed, nil
}

// IsLayerSubjectName reports whether name is in the form of a layer subject
// name: .podium/layer, or .podium/layer/ followed by a non-empty root.
//
// Spec: §4.7.6
func IsLayerSubjectName(name string) bool {
	if name == layerSubject {
		return true
	}
	rest, ok := strings.CutPrefix(name, layerSubject+"/")
	return ok && rest != ""
}

// IsReservedName reports whether name begins with .podium/ and is no layer
// subject name. A reserved name matches no artifact and no layer.
//
// Spec: §4.7.6
func IsReservedName(name string) bool {
	return strings.HasPrefix(name, reservedPrefix) && !IsLayerSubjectName(name)
}

// CheckStatementSubjects returns an error wrapping ErrSignedAtSubject when
// names, the subject names of one statement, include a name under
// .podium/signed-at/. A bundle whose statement fails the check is not
// accepted in any mode.
//
// Spec: §4.7.6
func CheckStatementSubjects(names []string) error {
	for _, n := range names {
		if strings.HasPrefix(n, signedAtPrefix) {
			return fmt.Errorf("%w: %q", ErrSignedAtSubject, n)
		}
	}
	return nil
}
