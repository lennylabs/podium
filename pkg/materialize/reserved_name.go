package materialize

import (
	"unicode/utf8"
)

// This file holds the §7.5.3 aliasing rule: the test for a path segment that
// a filesystem resolves to .git or to .podium although its bytes differ. A
// workspace written on one machine is committed and checked out on another,
// so the rule applies on every platform rather than only on the platform
// whose filesystem performs the aliasing. The comparisons follow Git's
// core.protectHFS and core.protectNTFS checks (is_hfs_dotgit in utf8.c and
// is_ntfs_dotgit in path.c), which Git applies for the same reason.

// AliasesDotGit reports whether a filesystem can resolve the path segment
// seg to .git. It covers .git in any letter case, the HFS+ forms that carry
// code points HFS+ ignores, and the NTFS forms: trailing spaces and periods,
// an alternate data stream after a colon, and the 8.3 short name GIT~1.
// Git places .git first in a repository directory, so its short name is
// always GIT~1, and GIT~2 and later short names are not aliases, as in Git.
//
// Spec: §7.5.3
func AliasesDotGit(seg string) bool {
	return isHFSDotName(seg, "git") || isNTFSDotGit(seg)
}

// AliasesDotPodium reports whether a filesystem can resolve the path segment
// seg to .podium, under the same HFS+ and NTFS rules as AliasesDotGit. Unlike
// .git, .podium has no guaranteed place in its directory, so every NTFS short
// name Windows can assign it is an alias: PODIUM~1 through PODIUM~4, and the
// fallback form Windows uses once those are taken, PO followed by a
// hexadecimal checksum, a tilde, and a digit. The checksum is a function of
// the long name that this package does not compute, so any hexadecimal value
// matches, which errs toward refusing.
//
// Spec: §7.5.3
func AliasesDotPodium(seg string) bool {
	return isHFSDotName(seg, "podium") || isNTFSDotPodium(seg)
}

// hfsIgnored reports whether HFS+ drops r from a file name when it compares
// names. The set is the one Git's next_hfs_char skips.
func hfsIgnored(r rune) bool {
	switch {
	case r >= 0x200c && r <= 0x200f, // zero width non-joiner and joiner, direction marks
		r >= 0x202a && r <= 0x202e, // directional embeddings and overrides
		r >= 0x206a && r <= 0x206f, // deprecated format characters
		r == 0xfeff:                // zero width no-break space
		return true
	}
	return false
}

// isHFSDotName reports whether seg reads as "." followed by name once the
// code points HFS+ ignores are removed, with ASCII letters compared in either
// case. name is lower-case ASCII. A non-ASCII code point in place of a name
// character never matches, which is how Git clamps the comparison. HFS+
// stores a malformed UTF-8 sequence as a percent escape, so a segment that
// carries one names neither .git nor .podium there.
func isHFSDotName(seg, name string) bool {
	if !utf8.ValidString(seg) {
		return false
	}
	kept := make([]byte, 0, len(seg))
	for _, r := range seg {
		if !hfsIgnored(r) {
			kept = utf8.AppendRune(kept, r)
		}
	}
	return len(kept) == len(name)+1 && kept[0] == '.' && equalFoldASCII(string(kept[1:]), name)
}

// isNTFSDotGit reports whether NTFS resolves seg to .git: ".git" or "git~1"
// in any letter case, followed only by spaces and periods, which NTFS strips,
// up to the end of the segment, a colon that opens an alternate data stream,
// or a directory separator. It mirrors Git's is_ntfs_dotgit.
func isNTFSDotGit(seg string) bool {
	switch {
	case len(seg) >= 4 && seg[0] == '.' && equalFoldASCII(seg[1:4], "git"):
		return ntfsTrailer(seg[4:])
	case len(seg) >= 5 && equalFoldASCII(seg[:5], "git~1"):
		return ntfsTrailer(seg[5:])
	}
	return false
}

// isNTFSDotPodium reports whether NTFS resolves seg to .podium: ".podium" or
// one of its short names, followed by a trailer ntfsTrailer accepts. It
// follows Git's is_ntfs_dot_generic with the checksum characters of the
// fallback short name left open (see AliasesDotPodium).
func isNTFSDotPodium(seg string) bool {
	switch {
	case len(seg) >= 7 && seg[0] == '.' && equalFoldASCII(seg[1:7], "podium"):
		return ntfsTrailer(seg[7:])
	case len(seg) >= 8 && equalFoldASCII(seg[:7], "podium~") && seg[7] >= '1' && seg[7] <= '4':
		return ntfsTrailer(seg[8:])
	}
	return isNTFSFallbackShortName(seg, "po")
}

// isNTFSFallbackShortName reports whether the first eight bytes of seg form
// an NTFS fallback short name for a long name that begins with prefix (two
// lower-case ASCII letters): prefix, then hexadecimal digits, then "~" and a
// run of decimal digits whose first is not 0, eight characters in all, and a
// trailer ntfsTrailer accepts after them.
func isNTFSFallbackShortName(seg, prefix string) bool {
	if len(seg) < 8 || !equalFoldASCII(seg[:2], prefix) {
		return false
	}
	tilde := -1
	for i := 2; i < 8 && tilde < 0; i++ {
		switch c := seg[i]; {
		case c == '~':
			tilde = i
		case i >= 6 || !isHexDigit(c):
			return false
		}
	}
	if tilde < 0 || seg[tilde+1] < '1' || seg[tilde+1] > '9' {
		return false
	}
	for i := tilde + 2; i < 8; i++ {
		if seg[i] < '0' || seg[i] > '9' {
			return false
		}
	}
	return ntfsTrailer(seg[8:])
}

// ntfsTrailer reports whether rest, the part of a segment after a reserved
// name, leaves the name unchanged on NTFS: it holds only spaces and periods
// up to its end, a colon that opens an alternate data stream, or a directory
// separator.
func ntfsTrailer(rest string) bool {
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case ':', '/', '\\':
			return true
		case ' ', '.':
		default:
			return false
		}
	}
	return true
}

// equalFoldASCII reports whether s equals the lower-case ASCII string lower
// with ASCII letters compared case-insensitively. A non-ASCII byte in s never
// matches.
func equalFoldASCII(s, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if lowerASCII(rune(s[i])) != rune(lower[i]) {
			return false
		}
	}
	return true
}

// lowerASCII folds an ASCII upper-case letter to lower case and returns any
// other code point unchanged.
func lowerASCII(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// isHexDigit reports whether c is an ASCII hexadecimal digit in either case.
func isHexDigit(c byte) bool {
	l := byte(lowerASCII(rune(c)))
	return (l >= '0' && l <= '9') || (l >= 'a' && l <= 'f')
}
