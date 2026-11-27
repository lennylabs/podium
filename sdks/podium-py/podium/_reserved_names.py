"""The §7.5.3 aliasing rule for ``.git`` and ``.podium``.

A filesystem can resolve a name whose bytes differ from ``.git`` to the
``.git`` directory: HFS+ ignores some code points and folds case, and NTFS
strips trailing spaces and periods, opens an alternate data stream at a
colon, and gives ``.git`` the 8.3 short name ``GIT~1``. A tree written on one
platform is checked out on another, so the rule applies on every platform. It
follows Git's ``core.protectHFS`` and ``core.protectNTFS`` checks
(``is_hfs_dotgit`` in utf8.c and ``is_ntfs_dotgit`` in path.c) and matches
``materialize.AliasesDotGit`` and ``materialize.AliasesDotPodium`` in the Go
module case for case; ``test/vectors/reserved-names.json`` pins the two.

Names are ``str``. A byte sequence that is not valid UTF-8 reaches Python as
lone surrogates under ``surrogateescape``, as ``os.fsdecode`` produces it, and
a lone surrogate reads as the end of the name, as Git reads the malformed
sequence it stands for.
"""

from __future__ import annotations

# Code points HFS+ drops from a name when it compares names; the set Git's
# next_hfs_char skips.
_HFS_IGNORED = frozenset(
    [*range(0x200C, 0x2010), *range(0x202A, 0x202F), *range(0x206A, 0x2070), 0xFEFF]
)

_NTFS_TRAILER_END = frozenset(":/\\")
_HEX = frozenset("0123456789abcdef")


def names_dot_git(seg: str) -> bool:
    """Report whether a filesystem can resolve the path segment ``seg`` to ``.git``.

    ``GIT~2`` and later short names are not aliases: Git creates ``.git``
    first in its directory, where it takes ``GIT~1``.

    Spec: §7.5.3
    """
    return _is_hfs_dot_name(seg, "git") or _is_ntfs_dot_git(seg)


def names_dot_podium(seg: str) -> bool:
    """Report whether a filesystem can resolve the path segment ``seg`` to ``.podium``.

    ``.podium`` has no fixed place in its directory, so every NTFS short name
    Windows can assign it is an alias: ``PODIUM~1`` through ``PODIUM~4`` and the
    fallback form of ``PO``, a hexadecimal checksum, ``~``, and a digit. The
    checksum is not computed, so any hexadecimal value matches.

    Spec: §7.5.3
    """
    return _is_hfs_dot_name(seg, "podium") or _is_ntfs_dot_podium(seg)


def _next_hfs_char(seg: str, i: int) -> tuple[str, int]:
    """Return the next character HFS+ keeps at or after index ``i``.

    The empty string marks the end of the name: the end of ``seg``, U+0000,
    a lone surrogate, U+FFFE, or U+FFFF. Git's pick_one_utf8_char refuses the
    sequences the last three stand for, and next_hfs_char reads that as the
    end of the name.
    """
    while i < len(seg):
        ch = seg[i]
        cp = ord(ch)
        if cp == 0 or 0xD800 <= cp <= 0xDFFF or cp in (0xFFFE, 0xFFFF):
            return "", len(seg)
        i += 1
        if cp not in _HFS_IGNORED:
            return ch, i
    return "", i


def _is_hfs_dot_name(seg: str, name: str) -> bool:
    """Report whether ``seg`` reads as ``.`` + ``name`` once HFS+ ignorables go.

    ASCII letters compare in either case, and a non-ASCII character in place
    of a name character never matches, as in Git.
    """
    ch, i = _next_hfs_char(seg, 0)
    if ch != ".":
        return False
    for want in name:
        ch, i = _next_hfs_char(seg, i)
        if _lower_ascii(ch) != want:
            return False
    ch, _ = _next_hfs_char(seg, i)
    return ch in ("", "/")


def _is_ntfs_dot_git(seg: str) -> bool:
    """Report whether NTFS resolves ``seg`` to ``.git`` (Git's is_ntfs_dotgit)."""
    if len(seg) >= 4 and seg[0] == "." and _equal_fold_ascii(seg[1:4], "git"):
        return _ntfs_trailer(seg[4:])
    if len(seg) >= 5 and _equal_fold_ascii(seg[:5], "git~1"):
        return _ntfs_trailer(seg[5:])
    return False


def _is_ntfs_dot_podium(seg: str) -> bool:
    """Report whether NTFS resolves ``seg`` to ``.podium`` or one of its short names."""
    if len(seg) >= 7 and seg[0] == "." and _equal_fold_ascii(seg[1:7], "podium"):
        return _ntfs_trailer(seg[7:])
    if len(seg) >= 8 and _equal_fold_ascii(seg[:7], "podium~") and "1" <= seg[7] <= "4":
        return _ntfs_trailer(seg[8:])
    return _is_ntfs_fallback_short_name(seg, "po")


def _is_ntfs_fallback_short_name(seg: str, prefix: str) -> bool:
    """Report whether the first eight characters of ``seg`` are a fallback short name.

    The form is ``prefix``, hexadecimal digits, ``~``, and decimal digits whose
    first is not ``0``, eight characters in all, followed by a trailer
    ``_ntfs_trailer`` accepts.
    """
    if len(seg) < 8 or not _equal_fold_ascii(seg[:2], prefix):
        return False
    tilde = -1
    for i in range(2, 8):
        ch = seg[i]
        if ch == "~":
            tilde = i
            break
        if i >= 6 or _lower_ascii(ch) not in _HEX:
            return False
    if tilde < 0 or not "1" <= seg[tilde + 1] <= "9":
        return False
    if any(not "0" <= ch <= "9" for ch in seg[tilde + 2 : 8]):
        return False
    return _ntfs_trailer(seg[8:])


def _ntfs_trailer(rest: str) -> bool:
    """Report whether ``rest`` leaves a reserved name unchanged on NTFS."""
    for ch in rest:
        if ch in _NTFS_TRAILER_END:
            return True
        if ch not in " .":
            return False
    return True


def _equal_fold_ascii(s: str, lower: str) -> bool:
    """Compare ``s`` with lower-case ASCII ``lower``, ASCII letters in either case."""
    return len(s) == len(lower) and all(_lower_ascii(a) == b for a, b in zip(s, lower))


def _lower_ascii(ch: str) -> str:
    """Fold an ASCII upper-case letter to lower case; return any other character unchanged."""
    return ch.lower() if "A" <= ch <= "Z" else ch
