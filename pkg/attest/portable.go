package attest

import (
	"strings"
	"unicode/utf8"
)

// windowsDevices are the Windows device names a segment must not take, in
// upper case. COM and LPT followed by a superscript digit are devices as well
// and are checked apart, because their letter case folds only on the prefix.
var windowsDevices = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM0": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT0": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// IsPortableName reports whether seg, one segment of a canonical ID or of a
// bundled resource path, is a §4.2 portable name: a name that every file
// system a consumer can write to stores and opens as itself. Windows removes
// a trailing "." or space, refuses the characters < > : " | ? *, opens a
// device for a device name such as "CON .txt", and resolves an 8.3 short name
// such as GIT~1 to the long name it abbreviates, and HFS+ ignores a set of
// format code points, so <U+200C>.git names .git there. A segment that is not
// portable can therefore name another file than the one it spells.
//
// Spec: §4.2
func IsPortableName(seg string) bool {
	switch {
	case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
		return false
	case strings.ContainsAny(seg, `<>:"|?*`):
		return false
	case strings.ContainsFunc(seg, hfsIgnored):
		return false
	case isWindowsDevice(seg), isShortName(seg):
		return false
	}
	return true
}

// NonPortableSegment returns the first segment of the slash-separated path p
// that is not a portable name, and false when every segment is one.
//
// Spec: §4.2
func NonPortableSegment(p string) (string, bool) {
	for _, seg := range strings.Split(p, "/") {
		if !IsPortableName(seg) {
			return seg, true
		}
	}
	return "", false
}

// hfsIgnored reports whether HFS+ ignores r when it compares names.
func hfsIgnored(r rune) bool {
	return (r >= 0x200C && r <= 0x200F) || (r >= 0x202A && r <= 0x202E) ||
		(r >= 0x206A && r <= 0x206F) || r == 0xFEFF
}

// isWindowsDevice reports whether seg is a Windows device name in any letter
// case, alone or followed by zero or more spaces and then "." or ":" and any
// further text. Windows reads the name before that point and drops the
// spaces, so "CON .txt" and "nul:stream" open a device.
func isWindowsDevice(seg string) bool {
	base := seg
	if i := strings.IndexAny(seg, ".:"); i >= 0 {
		base = seg[:i]
	}
	base = strings.TrimRight(base, " ")
	upper := strings.ToUpper(base)
	if windowsDevices[upper] {
		return true
	}
	for _, prefix := range []string{"COM", "LPT"} {
		if rest, ok := strings.CutPrefix(upper, prefix); ok && (rest == "¹" || rest == "²" || rest == "³") {
			return true
		}
	}
	return false
}

// isShortName reports whether seg has the 8.3 short-name form: a part before
// the first "." of at most 8 code points that ends in "~" and one or more
// ASCII digits, and at most one "." in the segment with at most 3 code points
// after it. Windows assigns every short name in this form, so a segment
// outside it, such as release~2, abbreviates no other name.
func isShortName(seg string) bool {
	base, ext, hasDot := strings.Cut(seg, ".")
	if hasDot && (strings.Contains(ext, ".") || utf8.RuneCountInString(ext) > 3) {
		return false
	}
	if utf8.RuneCountInString(base) > 8 {
		return false
	}
	digits := strings.TrimRightFunc(base, func(r rune) bool { return r >= '0' && r <= '9' })
	return len(digits) < len(base) && strings.HasSuffix(digits, "~")
}
