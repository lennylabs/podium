// The §7.5.3 aliasing rule for .git and .podium.
//
// A filesystem can resolve a name whose bytes differ from .git to the .git
// directory: HFS+ ignores some code points and folds case, and NTFS strips
// trailing spaces and periods, opens an alternate data stream at a colon, and
// gives .git the 8.3 short name GIT~1. A tree written on one platform is
// checked out on another, so the rule applies on every platform. It follows
// Git's core.protectHFS and core.protectNTFS checks (is_hfs_dotgit in utf8.c
// and is_ntfs_dotgit in path.c) and matches materialize.AliasesDotGit and
// materialize.AliasesDotPodium in the Go module case for case;
// test/vectors/reserved-names.json pins the two.
//
// Names are JavaScript strings. A lone surrogate, which is what a malformed
// sequence becomes when it survives into a string, reads as the end of the
// name, as Git reads the malformed sequence.

// isHFSIgnored reports whether HFS+ drops the code point cp from a name when
// it compares names; the set Git's next_hfs_char skips.
function isHFSIgnored(cp: number): boolean {
  return (
    (cp >= 0x200c && cp <= 0x200f) ||
    (cp >= 0x202a && cp <= 0x202e) ||
    (cp >= 0x206a && cp <= 0x206f) ||
    cp === 0xfeff
  );
}

// nextHFSChar returns the next code point HFS+ keeps at or after index i, and
// the index after it. -1 marks the end of the name: the end of seg, U+0000, a
// lone surrogate, U+FFFE, or U+FFFF. Git's pick_one_utf8_char refuses the
// sequences the last three stand for, and next_hfs_char reads that as the end.
function nextHFSChar(seg: string, i: number): [number, number] {
  while (i < seg.length) {
    const cp = seg.codePointAt(i) as number;
    if (cp === 0 || (cp >= 0xd800 && cp <= 0xdfff) || cp === 0xfffe || cp === 0xffff) return [-1, seg.length];
    i += cp > 0xffff ? 2 : 1;
    if (!isHFSIgnored(cp)) return [cp, i];
  }
  return [-1, i];
}

// lowerASCII folds an ASCII upper-case letter to lower case and returns any
// other code point unchanged.
function lowerASCII(cp: number): number {
  return cp >= 0x41 && cp <= 0x5a ? cp + 0x20 : cp;
}

// isHFSDotName reports whether seg reads as "." + name once the code points
// HFS+ ignores are removed. ASCII letters compare in either case, and a
// non-ASCII code point in place of a name character never matches, as in Git.
function isHFSDotName(seg: string, name: string): boolean {
  let [cp, i] = nextHFSChar(seg, 0);
  if (cp !== 0x2e) return false;
  for (let k = 0; k < name.length; k++) {
    [cp, i] = nextHFSChar(seg, i);
    if (lowerASCII(cp) !== name.charCodeAt(k)) return false;
  }
  [cp] = nextHFSChar(seg, i);
  return cp === -1 || cp === 0x2f;
}

// equalFoldASCII compares s with the lower-case ASCII string lower, ASCII
// letters in either case.
function equalFoldASCII(s: string, lower: string): boolean {
  if (s.length !== lower.length) return false;
  for (let k = 0; k < s.length; k++) {
    if (lowerASCII(s.charCodeAt(k)) !== lower.charCodeAt(k)) return false;
  }
  return true;
}

// ntfsTrailer reports whether rest, the part of a segment after a reserved
// name, leaves the name unchanged on NTFS: only spaces and periods up to its
// end, a colon that opens an alternate data stream, or a directory separator.
function ntfsTrailer(rest: string): boolean {
  for (const ch of rest) {
    if (ch === ":" || ch === "/" || ch === "\\") return true;
    if (ch !== " " && ch !== ".") return false;
  }
  return true;
}

// isNTFSDotGit reports whether NTFS resolves seg to .git (Git's
// is_ntfs_dotgit).
function isNTFSDotGit(seg: string): boolean {
  if (seg.length >= 4 && seg[0] === "." && equalFoldASCII(seg.slice(1, 4), "git")) return ntfsTrailer(seg.slice(4));
  if (seg.length >= 5 && equalFoldASCII(seg.slice(0, 5), "git~1")) return ntfsTrailer(seg.slice(5));
  return false;
}

const isDigit = (ch: string): boolean => ch >= "0" && ch <= "9";
const isHex = (ch: string): boolean => /^[0-9a-fA-F]$/.test(ch);

// isNTFSFallbackShortName reports whether the first eight characters of seg
// are an NTFS fallback short name for a long name that begins with prefix:
// prefix, hexadecimal digits, "~", and decimal digits whose first is not 0,
// eight characters in all, followed by a trailer ntfsTrailer accepts.
function isNTFSFallbackShortName(seg: string, prefix: string): boolean {
  if (seg.length < 8 || !equalFoldASCII(seg.slice(0, 2), prefix)) return false;
  let tilde = -1;
  for (let i = 2; i < 8 && tilde < 0; i++) {
    if (seg[i] === "~") tilde = i;
    else if (i >= 6 || !isHex(seg[i])) return false;
  }
  if (tilde < 0 || seg[tilde + 1] < "1" || seg[tilde + 1] > "9") return false;
  for (let i = tilde + 2; i < 8; i++) if (!isDigit(seg[i])) return false;
  return ntfsTrailer(seg.slice(8));
}

// isNTFSDotPodium reports whether NTFS resolves seg to .podium or one of its
// short names.
function isNTFSDotPodium(seg: string): boolean {
  if (seg.length >= 7 && seg[0] === "." && equalFoldASCII(seg.slice(1, 7), "podium")) return ntfsTrailer(seg.slice(7));
  if (seg.length >= 8 && equalFoldASCII(seg.slice(0, 7), "podium~") && seg[7] >= "1" && seg[7] <= "4") {
    return ntfsTrailer(seg.slice(8));
  }
  return isNTFSFallbackShortName(seg, "po");
}

// namesDotGit reports whether a filesystem can resolve the path segment seg
// to .git. GIT~2 and later short names are not aliases: Git creates .git first
// in its directory, where it takes GIT~1.
//
// Spec: §7.5.3
export function namesDotGit(seg: string): boolean {
  return isHFSDotName(seg, "git") || isNTFSDotGit(seg);
}

// namesDotPodium reports whether a filesystem can resolve the path segment seg
// to .podium. .podium has no fixed place in its directory, so every NTFS short
// name Windows can assign it is an alias: PODIUM~1 through PODIUM~4 and the
// fallback form of PO, a hexadecimal checksum, "~", and a digit. The checksum
// is not computed, so any hexadecimal value matches.
//
// Spec: §7.5.3
export function namesDotPodium(seg: string): boolean {
  return isHFSDotName(seg, "podium") || isNTFSDotPodium(seg);
}
