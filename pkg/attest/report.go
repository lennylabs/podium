package attest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxReportedBytes is the length above which a reported string is cut.
const MaxReportedBytes = 256

// ReportString applies the §4.7.11 rule for reported strings to s, a value a
// cycle takes from a bundle, a certificate, a statement, or a path of the
// snapshot and places in an error envelope, an audit event, the attestation
// record, or a log line. A string longer than MaxReportedBytes is cut to its
// first MaxReportedBytes bytes at a character boundary, and the mark
// "...(<n> bytes, sha256:<hex>)" is appended, where n is the byte length of
// the full string and hex the first 16 hexadecimal digits of its SHA-256
// digest, so two strings that share their first bytes read differently.
// Each character of the Unicode categories Cc, Cf, Zl, and Zp, and each byte
// that is not valid UTF-8, is replaced with U+FFFD, in a cut string and in
// an uncut one, so no bidirectional control or line break reaches a reader.
// No comparison reads a reported string.
//
// Spec: §4.7.11 (Reported strings)
func ReportString(s string) string {
	kept, mark := s, ""
	if len(s) > MaxReportedBytes {
		cut := MaxReportedBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		sum := sha256.Sum256([]byte(s))
		kept = s[:cut]
		mark = fmt.Sprintf("...(%d bytes, sha256:%s)", len(s), hex.EncodeToString(sum[:8]))
	}
	return replaceUnreportable(kept) + mark
}

// replaceUnreportable replaces each character of the categories Cc, Cf, Zl,
// and Zp, and each invalid byte, with U+FFFD.
func replaceUnreportable(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		if r == utf8.RuneError {
			if _, size := utf8.DecodeRuneInString(s[i:]); size == 1 {
				b.WriteRune(utf8.RuneError)
				continue
			}
		}
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			b.WriteRune(utf8.RuneError)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ReportStrings applies ReportString to each of ss and returns at most limit
// of them, in order.
//
// Spec: §4.7.11 (Reported strings)
func ReportStrings(ss []string, limit int) []string {
	if len(ss) > limit {
		ss = ss[:limit]
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = ReportString(s)
	}
	return out
}
