"""Workspace local overlay merge for the SDK (spec §6.4, §6.4.1).

The overlay is the highest-precedence layer in the caller's effective
view. Every consumer (MCP server, ``podium sync``, the SDKs) merges it
client-side. For the SDK this means ``search_artifacts`` fans out to both
the registry and a local BM25 index over overlay manifests and fuses the
two via reciprocal rank fusion, and ``load_artifact`` resolves an overlay
artifact ahead of the registry.

The implementation keeps no third-party dependency: the BM25 ranker and
the RRF fusion are small and self-contained.
"""

from __future__ import annotations

import math
import os
import re
from dataclasses import dataclass, field

from . import _reserved_names, _skill_document

_OVERLAY_SUBDIR = os.path.join(".podium", "overlay")
_WORD_RE = re.compile(r"[a-z0-9]+")


def resolve_overlay_path(explicit: str | None, env: str | None, cwd: str) -> str | None:
    """Apply the §6.4 lookup order for the overlay path.

    1. ``explicit`` (``Client(overlay_path=...)``) when set.
    2. ``PODIUM_OVERLAY_PATH`` (``env``) when set.
    3. ``<CWD>/.podium/overlay/`` when that directory exists.
    4. Otherwise ``None`` (the overlay layer is disabled).
    """
    if explicit:
        return explicit
    if env:
        return env
    candidate = os.path.join(cwd or ".", _OVERLAY_SUBDIR)
    if os.path.isdir(candidate):
        return candidate
    return None


def _tokenize(text: str) -> list[str]:
    return _WORD_RE.findall(text.lower())


def _parse_frontmatter(text: str) -> tuple[dict[str, object], str]:
    """Return (fields, body) for an ARTIFACT.md or a SKILL.md (§4.3, §4.3.4).

    Only the top-level fields the SDK ranks and reports are parsed:
    ``type``, ``version``, ``description``, and ``tags``. ``tags`` accepts
    both the inline ``[a, b]`` form and the block ``- a`` form. A SKILL.md
    ``metadata`` value is read by ``_metadata_version`` and ``_metadata_tags``
    instead, which take a value only in a form whose line reading equals the
    YAML decoding.
    """
    fields: dict[str, object] = {}
    if not text.startswith("---"):
        return fields, text
    lines = text.splitlines()
    end = None
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            end = i
            break
    if end is None:
        return fields, text
    body = "\n".join(lines[end + 1 :])
    pending_tags = False
    tags: list[str] = []
    for raw in lines[1:end]:
        if pending_tags and raw.lstrip().startswith("- "):
            tags.append(raw.lstrip()[2:].strip().strip("\"'"))
            continue
        pending_tags = False
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        if raw.startswith(" "):
            continue
        key, sep, val = raw.partition(":")
        if not sep:
            continue
        key = key.strip()
        val = val.strip()
        if key == "tags":
            if val.startswith("[") and val.endswith("]"):
                tags = [t.strip().strip("\"'") for t in val[1:-1].split(",") if t.strip()]
            elif val:
                tags = [t.strip().strip("\"'") for t in val.split(",") if t.strip()]
            else:
                pending_tags = True
        elif key in ("type", "version", "description"):
            fields[key] = _scalar_value(val)
    fields["tags"] = [t for t in tags if t]
    return fields, body


# The plain scalars yaml.v3 decodes to null, which a string field reads as
# empty, and the empty value, which is null too.
_YAML_NULLS = frozenset({"", "~", "null", "Null", "NULL"})


def _scalar_value(val: str) -> str:
    """Return the string a single-line top-level YAML scalar decodes to.

    Spec: §4.3, §4.3.4. A quoted value ends at its closing quote, so a
    trailing comment after it is dropped. A plain value ends before a ``#``
    that begins the value or follows white space, which starts a comment, and
    a plain null (``~``, ``null``, or an empty value) reads as empty, as
    Go's decode reads it into a string field. ``version: ~`` therefore
    declares no version, and ``type: skill # c`` declares a skill. Escapes
    inside a quoted value are kept as written. An unclosed quote keeps the
    earlier reading, with the surrounding quotes removed.
    """
    quote = val[:1]
    if quote in ("\"", "'"):
        i = 1
        while i < len(val):
            ch = val[i]
            if quote == '"' and ch == "\\":
                i += 2
                continue
            if ch == quote:
                if quote == "'" and val[i + 1 : i + 2] == "'":
                    i += 2
                    continue
                return val[1:i]
            i += 1
        return val.strip("\"'")
    comment = re.search(r"(?:^|[ \t])#", val)
    plain = (val[: comment.start()] if comment else val).rstrip(_WHITE)
    return "" if plain in _YAML_NULLS else plain


# The SKILL.md metadata.tags readable form. A line reader cannot reproduce
# YAML, so _metadata_tags reads metadata.tags only from one restricted
# single-line form, in a frontmatter laid out so that the line reading equals
# the yaml.v3 decoding Go applies, and returns [] for every other input.
# test/vectors/skill-metadata-tags.json, generated from
# manifest.ResolveStoredSkill, pins the form to Go. The frontmatter is
# delimited as Go's SplitFrontmatter delimits it: the text begins with "---"
# and a line end, lines split at LF with a CR before an LF removed, and the
# frontmatter ends at the first later line that begins with "---". A value is
# in the form when every numbered condition holds:
#   1. Exactly one frontmatter line begins with "metadata:", and nothing but
#      white space follows it.
#   2. The block runs to the next column-0 line that is not a comment. The
#      child indent is the space indentation of its first line that is neither
#      blank nor a comment.
#   3. Exactly one block line at the child indent has the key "tags" (the text
#      before the first ":", trailing spaces and one pair of quotes removed).
#   4. That line is the child indent, "tags:", one or more spaces, the value,
#      and optional trailing spaces, with no tab.
#   5. The value is plain (_TAGS_PLAIN_RE and not a null word), or double- or
#      single-quoted printable ASCII with no escape and no inner quote.
#   6. Every later line is blank up to the next block line at or above the
#      child indent, and no later block line holds a "*" that starts an alias
#      or a "!" that starts a tag: yaml.v3 lets an alias key that resolves to
#      "tags", or a tagged key that decodes to it, replace the value with no
#      error.
#   7. Splitting the content on "," and trimming spaces leaves no empty
#      element. The elements, in order, are the tags.
#   8. The frontmatter holds no CR, U+0085, U+2028, U+2029, or U+FEFF, which
#      yaml.v3 reads as, or skips at, a line start the line reader cannot see.
#   9. The first frontmatter line that is neither blank nor a comment begins
#      at column 0, since yaml.v3 decodes only the first document.
#  10. No line before the tags line begins with "..." or "%", holds "[" or
#      "{", or holds a quote at a token start, because each can end the
#      document or open a scalar that swallows later lines. A key followed by
#      a quoted value of the condition 5 form is exempt.
# White space here is the space and the tab only, so no check uses strip() or
# splitlines(), which also treat characters yaml.v3 does not as white space or
# as line breaks.
_TAGS_PLAIN_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9 ,._/-]*")
_TAGS_DOUBLE_RE = re.compile(r'"([\x20\x21\x23-\x5b\x5d-\x7e]*)"')
_TAGS_SINGLE_RE = re.compile(r"'([\x20-\x26\x28-\x7e]*)'")
_TAGS_NULLS = frozenset({"null", "Null", "NULL"})
# A key and a quoted scalar with no escape and no inner quote, which leaves no
# quoted scalar open; condition 10 exempts such a line before the tags line.
_QUOTED_PAIR_LINE_RE = re.compile(
    r" *[A-Za-z][A-Za-z0-9_-]*: +"
    r"(\"[\x20\x21\x23-\x5b\x5d-\x7e]*\"|'[\x20-\x26\x28-\x7e]*') *"
)
# yaml.v3 reads the first four as line breaks and skips a U+FEFF at the start
# of a line, so each can start a line the line reader does not see.
_HIDDEN_BREAK_RE = re.compile("[\r\u0085\u2028\u2029\ufeff]")
_WHITE = " \t"


def _is_blank_line(line: str) -> bool:
    return all(ch in _WHITE for ch in line)


def _indent_of(line: str) -> str:
    return line[: len(line) - len(line.lstrip(_WHITE))]


def _is_comment_line(line: str) -> bool:
    return line[len(_indent_of(line)) :].startswith("#")


def _trim_spaces(s: str) -> str:
    return s.strip(" ")


def _frontmatter_lines(text: str) -> list[str] | None:
    """Delimit the frontmatter as Go's SplitFrontmatter does.

    The text begins with ``---`` and an LF or a CR LF, and the frontmatter
    ends at the first later line that begins with ``---``. Lines split at each
    LF, with a CR directly before the LF removed. Returns ``None`` when there
    is no frontmatter.
    """
    if not (text.startswith("---\n") or text.startswith("---\r\n")):
        return None
    parts = text.split("\n")
    lines = [p[:-1] if i < len(parts) - 1 and p.endswith("\r") else p for i, p in enumerate(parts)]
    for end in range(1, len(lines)):
        if lines[end].startswith("---"):
            return lines[1:end]
    return None


def _line_key(rest: str) -> str | None:
    """Return the text before the first ``:``, unquoted once, or ``None``."""
    key, sep, _ = rest.partition(":")
    if not sep:
        return None
    key = key.rstrip(" ")
    if len(key) >= 2 and key[0] in "\"'" and key[-1] == key[0]:
        return key[1:-1]
    return key


def _tags_content(line: str, indent: str) -> str | None:
    """Return the tags line's value content (conditions 4 and 5), or ``None``."""
    if "\t" in line or not line.startswith(indent + "tags:"):
        return None
    rest = line[len(indent) + len("tags:") :]
    if not rest.startswith(" "):
        return None
    value = _trim_spaces(rest)
    if _TAGS_PLAIN_RE.fullmatch(value):
        return None if value in _TAGS_NULLS else value
    quoted = _TAGS_DOUBLE_RE.fullmatch(value) or _TAGS_SINGLE_RE.fullmatch(value)
    return quoted.group(1) if quoted else None


def _blank_until_sibling(lines: list[str], indent_len: int) -> bool:
    """Report whether no continuation line extends the tags value (condition 6).

    Every line up to the next line indented no deeper than ``indent_len``
    that is neither blank nor a comment must be blank.
    """
    for line in lines:
        if _is_blank_line(line):
            continue
        return not _is_comment_line(line) and len(_indent_of(line)) <= indent_len
    return True


def _holds_alias_or_tag(line: str) -> bool:
    """Report whether a line holds an alias or a tag at a token start (condition 6).

    yaml.v3 compares mapping keys for duplicates only between nodes of one
    kind and tag, so an alias key resolving to ``tags``, or a tagged key such
    as ``!!binary dGFncw==`` that decodes to it, replaces the earlier value
    with no error. A token starts at the line start, after white space, or
    after the ``[``, ``{``, or ``,`` of a flow collection, which needs no
    space before the next entry. A ``*`` starts an alias when a non-white
    character follows it, and a ``!`` always starts a tag. A ``*`` or a ``!``
    at a token start inside a scalar counts too, so the check errs toward
    reporting no value.
    """
    for i, ch in enumerate(line):
        if ch not in "*!" or (i > 0 and line[i - 1] not in _WHITE + "[{,"):
            continue
        if ch == "!" or (i + 1 < len(line) and line[i + 1] not in _WHITE):
            return True
    return False


def _opens_scalar_or_document_end(line: str) -> bool:
    """Report whether a line before the tags line breaks the reading (condition 10).

    Such a line can end the YAML document, or open a quoted scalar or a flow
    collection that continues across later lines and holds the metadata or
    tags line as text.
    """
    if line.startswith("...") or line.startswith("%"):
        return True
    if _QUOTED_PAIR_LINE_RE.fullmatch(line):
        return False
    if "[" in line or "{" in line:
        return True
    return any(ch in "\"'" and (i == 0 or line[i - 1] in _WHITE) for i, ch in enumerate(line))


def _metadata_tags(skill_md: str) -> list[str]:
    """Return the tags of a SKILL.md in the readable form, and [] otherwise.

    Spec: §4.3.4, §6.4. The caller passes the file's text with its line ends
    unchanged. The overlay runs no lint, so a dropped value raises nothing;
    ``podium lint`` reports it under the Go rules.
    """
    fm = _frontmatter_lines(skill_md)
    if fm is None or any(_HIDDEN_BREAK_RE.search(line) for line in fm):
        return []
    first = next((line for line in fm if not _is_blank_line(line) and not _is_comment_line(line)), None)
    if first is None or first[0] in _WHITE:
        return []
    meta_at = [i for i, line in enumerate(fm) if line.startswith("metadata:")]
    if len(meta_at) != 1 or not _is_blank_line(fm[meta_at[0]][len("metadata:") :]):
        return []
    start = meta_at[0] + 1
    stop = start
    while stop < len(fm) and not (fm[stop] and fm[stop][0] not in _WHITE and fm[stop][0] != "#"):
        stop += 1
    child = next(
        (line for line in fm[start:stop] if not _is_blank_line(line) and not _is_comment_line(line)),
        None,
    )
    if child is None:
        return []
    indent = _indent_of(child)
    if indent.strip(" "):
        return []
    tags_at = [
        i
        for i in range(start, stop)
        if _indent_of(fm[i]) == indent and _line_key(fm[i][len(indent) :]) == "tags"
    ]
    if len(tags_at) != 1:
        return []
    at = tags_at[0]
    content = _tags_content(fm[at], indent)
    if content is None:
        return []
    after = fm[at + 1 : stop]
    if not _blank_until_sibling(after, len(indent)) or any(_holds_alias_or_tag(line) for line in after):
        return []
    if any(_opens_scalar_or_document_end(line) for line in fm[:at]):
        return []
    tags = [_trim_spaces(t) for t in content.split(",")]
    return [] if "" in tags else tags


# The SKILL.md metadata.version readable form. _metadata_version reads the
# value only where the line reading equals the yaml.v3 decoding Go applies,
# and returns "" for every other input, so the overlay reports the decoded
# version or none (§4.3.4). test/vectors/skill-metadata-version.json,
# generated from manifest.ResolveSkillVersion, pins the form to Go. The
# frontmatter is delimited as for _metadata_tags, and conditions 8 and 9 of
# the tags form apply. A value is in the form when, in addition:
#   1. Exactly one frontmatter line begins with "metadata:". What follows it
#      is white space, a comment, or a flow mapping that closes on the line.
#   2. In the block form, the block and its child indent are as in condition 2
#      of the tags form, exactly one block line at the child indent has the
#      key "version", and that line is the child indent, "version:", one or
#      more spaces, the value, and optional trailing spaces. Every later line
#      is blank up to the next block line at or above the child indent.
#   3. In the flow form, the mapping holds no comment, exactly one of its
#      top-level entries has the key "version", once unquoted, and the value
#      is that entry's value.
#   4. The value is major.minor.patch, plain or in double or single quotes.
#   5. No line before the version line, or before the metadata line in the
#      flow form, begins with "..." or "%" or leaves a quoted scalar or a flow
#      collection open, because each can end the document or swallow the
#      version line into another value: a multi-line flow mapping such as
#      "openclaw: {" or a quoted scalar continued on the next line.
#   6. No later frontmatter line, and no part of a flow-form mapping, holds a
#      "*" that starts an alias or a "!" that starts a tag, which yaml.v3 lets
#      replace the metadata mapping or its version key with no error.
_VERSION_VALUE_RE = re.compile(r"([0-9]+\.[0-9]+\.[0-9]+)|\"([0-9]+\.[0-9]+\.[0-9]+)\"|'([0-9]+\.[0-9]+\.[0-9]+)'")


def _version_value(value: str) -> str:
    """Return the version a value in the condition 4 form holds, or ``""``."""
    m = _VERSION_VALUE_RE.fullmatch(_trim_spaces(value))
    return next((g for g in m.groups() if g), "") if m else ""


def _leaves_open(line: str) -> bool:
    """Report whether a line can end the document or leave a value open (condition 5).

    A quote at a token start opens a quoted scalar, and a bracket or a brace
    at a token start opens a flow collection; the line leaves it open when it
    does not close before the line ends. A ``#`` after white space outside a
    quoted scalar starts a comment, which ends the scan. A quote inside a
    plain scalar, such as ``say "hi``, counts as an opener too, so the check
    errs toward reporting no value.
    """
    if line.startswith("...") or line.startswith("%"):
        return True
    depth = 0
    quote = ""
    i = 0
    while i < len(line):
        ch = line[i]
        prev = line[i - 1] if i > 0 else " "
        if quote:
            if quote == '"' and ch == "\\":
                i += 1
            elif ch == quote and quote == "'" and line[i + 1 : i + 2] == "'":
                i += 1
            elif ch == quote:
                quote = ""
        elif ch == "#" and prev in _WHITE:
            break
        elif ch in "\"'" and (prev in _WHITE or (depth > 0 and prev in "[{,:")):
            quote = ch
        elif ch in "[{" and (prev in _WHITE or depth > 0):
            depth += 1
        elif ch in "]}" and depth > 0:
            depth -= 1
        i += 1
    return bool(quote) or depth > 0


def _flow_mapping_entries(val: str) -> list[str] | None:
    """Split a single-line YAML flow mapping into its top-level entries.

    Commas inside a nested mapping, a sequence, or a quoted scalar stay part
    of their entry, so a nested ``version`` key never reads as the mapping's
    own. Returns ``None`` for a value that is not a flow mapping, whose braces
    do not close on the line, that holds a comment, which would continue the
    mapping on a later line, or that carries anything but a comment after the
    closing brace.
    """
    if not val.startswith("{"):
        return None
    entries: list[str] = []
    depth = 0
    quote = ""
    start = 1
    i = 0
    while i < len(val):
        ch = val[i]
        if quote:
            if quote == '"' and ch == "\\":
                i += 1
            elif ch == quote:
                quote = ""
        elif ch in "\"'":
            quote = ch
        elif ch == "#" and val[i - 1] in _WHITE:
            return None
        elif ch in "{[":
            depth += 1
        elif ch == "," and depth == 1:
            entries.append(val[start:i])
            start = i + 1
        elif ch in "}]":
            depth -= 1
            if depth == 0:
                entries.append(val[start:i])
                rest = val[i + 1 :].strip(_WHITE)
                return None if rest and not rest.startswith("#") else entries
        i += 1
    return None


def _flow_metadata_version(val: str) -> str:
    """Return the version of a flow-form metadata value (condition 3), or ``""``."""
    entries = _flow_mapping_entries(val)
    if entries is None or _holds_alias_or_tag(val):
        return ""
    found = []
    for entry in entries:
        key, sep, value = entry.strip(_WHITE).partition(":")
        if not sep or (value and value[0] not in _WHITE):
            continue
        key = key.rstrip(_WHITE)
        if len(key) >= 2 and key[0] in "\"'" and key[-1] == key[0]:
            key = key[1:-1]
        if key == "version":
            found.append(value)
    return _version_value(found[0]) if len(found) == 1 else ""


def _block_metadata_version(fm: list[str], start: int) -> tuple[str, int]:
    """Return the version of a block-form metadata value and its line (condition 2).

    ``start`` is the index of the line after ``metadata:``. Returns
    ``("", -1)`` when the block holds no value in the form.
    """
    stop = start
    while stop < len(fm) and not (fm[stop] and fm[stop][0] not in _WHITE and fm[stop][0] != "#"):
        stop += 1
    child = next(
        (line for line in fm[start:stop] if not _is_blank_line(line) and not _is_comment_line(line)),
        None,
    )
    if child is None:
        return "", -1
    indent = _indent_of(child)
    if indent.strip(" "):
        return "", -1
    at = [i for i in range(start, stop) if _indent_of(fm[i]) == indent and _line_key(fm[i][len(indent) :]) == "version"]
    if len(at) != 1 or "\t" in fm[at[0]] or not fm[at[0]].startswith(indent + "version: "):
        return "", -1
    if not _blank_until_sibling(fm[at[0] + 1 : stop], len(indent)):
        return "", -1
    return _version_value(fm[at[0]][len(indent) + len("version:") :]), at[0]


def _metadata_version(skill_md: str) -> str:
    """Return the metadata.version of a SKILL.md in the readable form, and "" otherwise.

    Spec: §4.3.4, §6.4. The caller passes the file's text. A ``version:``
    line nested in another key's block, or inside a multi-line flow
    collection or quoted scalar, is never read, so the overlay reports no
    version the YAML decoding does not produce.
    """
    fm = _frontmatter_lines(skill_md)
    if fm is None or any(_HIDDEN_BREAK_RE.search(line) for line in fm):
        return ""
    first = next((line for line in fm if not _is_blank_line(line) and not _is_comment_line(line)), None)
    if first is None or first[0] in _WHITE:
        return ""
    meta_at = [i for i, line in enumerate(fm) if line.startswith("metadata:")]
    if len(meta_at) != 1:
        return ""
    rest = fm[meta_at[0]][len("metadata:") :]
    if _is_blank_line(rest) or rest.lstrip(_WHITE).startswith("#"):
        if rest and rest[0] not in _WHITE:
            return ""
        version, at = _block_metadata_version(fm, meta_at[0] + 1)
    elif rest.startswith(" "):
        version, at = _flow_metadata_version(rest.lstrip(" ")), meta_at[0]
    else:
        return ""
    if not version or any(_leaves_open(line) for line in fm[:at]):
        return ""
    if any(_holds_alias_or_tag(line) for line in fm[at + 1 :]):
        return ""
    return version


def _parse_domain(text: str) -> "OverlayDomain | None":
    """Parse one DOMAIN.md document into an OverlayDomain (§4.5.4).

    Mirrors :func:`_parse_frontmatter` for ARTIFACT.md: the frontmatter is
    hand-parsed with no third-party YAML dependency. The recognized fields
    are the top-level ``description``, ``unlisted``, ``include``, and
    ``exclude``, plus the nested ``discovery:`` block's ``keywords``,
    ``featured``, and ``notable_count``. Everything after the closing
    ``---`` is the prose body. A malformed document returns ``None`` so the
    caller skips it without crashing.
    """
    if not text.startswith("---"):
        return None
    lines = text.splitlines()
    end = None
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            end = i
            break
    if end is None:
        return None
    body = "\n".join(lines[end + 1 :])

    description = ""
    unlisted = False
    include: list[str] = []
    exclude: list[str] = []
    keywords: list[str] = []
    featured: list[str] = []
    notable_count = 0

    # The active block-list target: a mutable list a `- item` line appends to,
    # or None when the previous key did not open a block list. discovery_indent
    # tracks the column at which the discovery: child keys sit (greater than the
    # discovery key's own indent) so a top-level key closes the block.
    pending: list[str] | None = None
    in_discovery = False
    discovery_indent = -1

    def _inline_list(val: str) -> list[str]:
        inner = val[1:-1]
        return [t.strip().strip("\"'") for t in inner.split(",") if t.strip()]

    for raw in lines[1:end]:
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        stripped = raw.lstrip()
        indent = len(raw) - len(stripped)

        # A block-list item continues the most recent list key.
        if pending is not None and stripped.startswith("- "):
            pending.append(stripped[2:].strip().strip("\"'"))
            continue
        pending = None

        # Leaving the discovery block when a key dedents to its level or above.
        if in_discovery and indent <= discovery_indent:
            in_discovery = False

        key, sep, val = stripped.partition(":")
        if not sep:
            continue
        key = key.strip()
        val = val.strip()

        if in_discovery:
            if key == "keywords":
                if val.startswith("[") and val.endswith("]"):
                    keywords = _inline_list(val)
                elif val:
                    keywords = [t.strip().strip("\"'") for t in val.split(",") if t.strip()]
                else:
                    keywords = []
                    pending = keywords
            elif key == "featured":
                if val.startswith("[") and val.endswith("]"):
                    featured = _inline_list(val)
                elif val:
                    featured = [t.strip().strip("\"'") for t in val.split(",") if t.strip()]
                else:
                    featured = []
                    pending = featured
            elif key == "notable_count":
                try:
                    notable_count = int(val)
                except ValueError:
                    notable_count = 0
            continue

        # Top-level keys.
        if key == "description":
            description = val.strip("\"'")
        elif key == "unlisted":
            unlisted = val.lower() in ("true", "yes", "1")
        elif key == "include":
            if val.startswith("[") and val.endswith("]"):
                include = _inline_list(val)
            elif val:
                include = [t.strip().strip("\"'") for t in val.split(",") if t.strip()]
            else:
                include = []
                pending = include
        elif key == "exclude":
            if val.startswith("[") and val.endswith("]"):
                exclude = _inline_list(val)
            elif val:
                exclude = [t.strip().strip("\"'") for t in val.split(",") if t.strip()]
            else:
                exclude = []
                pending = exclude
        elif key == "discovery":
            in_discovery = True
            discovery_indent = indent

    return OverlayDomain(
        description=description,
        body=body,
        include=[p for p in include if p],
        exclude=[p for p in exclude if p],
        unlisted=unlisted,
        keywords=[k for k in keywords if k],
        featured=[f for f in featured if f],
        notable_count=notable_count,
    )


@dataclass
class OverlayDomain:
    """One DOMAIN.md read from the overlay directory (§4.5.4)."""

    description: str = ""
    body: str = ""
    include: list[str] = field(default_factory=list)
    exclude: list[str] = field(default_factory=list)
    unlisted: bool = False
    keywords: list[str] = field(default_factory=list)
    featured: list[str] = field(default_factory=list)
    notable_count: int = 0


@dataclass
class OverlayArtifact:
    """One artifact package read from the overlay directory."""

    id: str
    type: str
    version: str
    description: str
    tags: list[str]
    frontmatter: str
    body: str
    skill_raw: str
    resources: dict[str, object] = field(default_factory=dict)
    tokens: list[str] = field(default_factory=list)


class LocalOverlay:
    """Indexes an overlay directory and ranks it with BM25 (§6.4.1)."""

    # spec §6.4.1 — BM25 is the default overlay ranker. Standard Okapi
    # parameters; the corpus is small so the exact values matter little.
    _K1 = 1.2
    _B = 0.75

    def __init__(self, path: str) -> None:
        self.path = path
        self.artifacts: dict[str, OverlayArtifact] = {}
        # spec §4.5.4 — every DOMAIN.md under the overlay, keyed by canonical
        # domain path (the dir relative to the overlay root, slash-joined; ""
        # for a root-level DOMAIN.md).
        self.domains: dict[str, OverlayDomain] = {}
        self._avg_len = 0.0
        self._doc_freq: dict[str, int] = {}
        self._load()

    def _load(self) -> None:
        if not self.path or not os.path.isdir(self.path):
            return
        # packages holds the canonical id of every package directory recorded
        # so far. os.walk is top-down, so it holds every ancestor package by
        # the time a directory is visited.
        packages: set[str] = set()
        for dirpath, dirnames, filenames in os.walk(self.path):
            # spec §6.4 — an overlay carries the registry-side layers' format,
            # so discovery skips a dot-prefixed directory below the overlay
            # root together with everything under it, as walkLayer does in
            # pkg/registry/filesystem/walk.go. The root itself is exempt,
            # because the default overlay path is <CWD>/.podium/overlay/.
            dirnames[:] = [d for d in dirnames if not d.startswith(".")]
            rel = os.path.relpath(dirpath, self.path)
            canonical = "" if rel in (".", "") else rel.replace(os.sep, "/")
            if "DOMAIN.md" in filenames:
                dom = self._read_domain(dirpath)
                if dom is not None:
                    self.domains[canonical] = dom
            has_artifact, has_skill = _package_manifests(dirpath)
            # Spec: §4.2 — a directory is a package when it directly holds
            # ARTIFACT.md, or when it directly holds SKILL.md and no directory
            # between it and the overlay root is a package. This mirrors
            # loadPackage, which combines filesystem.PackageManifests and
            # filesystem.WithinPackage in pkg/registry/filesystem.
            if canonical == "":
                continue
            if not has_artifact and not (has_skill and not _within_package(packages, canonical)):
                continue
            _check_canonical_id(canonical)
            packages.add(canonical)
            if has_artifact:
                art = self._read_package(dirpath, canonical)
            else:
                art = self._read_plain_skill(dirpath, canonical)
            if art is not None:
                self.artifacts[canonical] = art
        self._index()

    def _read_domain(self, dirpath: str) -> OverlayDomain | None:
        try:
            with open(os.path.join(dirpath, "DOMAIN.md"), encoding="utf-8") as fh:
                text = fh.read()
        except OSError:
            return None
        return _parse_domain(text)

    def _read_package(self, dirpath: str, artifact_id: str) -> OverlayArtifact | None:
        """Load a package whose root holds ARTIFACT.md.

        For a skill the sibling SKILL.md is read too, and an ARTIFACT.md that
        declares no version takes the SKILL.md version (§4.3.4).
        """
        try:
            with open(os.path.join(dirpath, "ARTIFACT.md"), encoding="utf-8") as fh:
                frontmatter = fh.read()
        except OSError:
            return None
        fields, body = _parse_frontmatter(frontmatter)
        if fields.get("type", "") != "skill":
            resources = _collect_resources(dirpath, {"ARTIFACT.md"})
            return _overlay_artifact(artifact_id, fields, frontmatter, body, "", resources)
        skill_raw = _read_optional(os.path.join(dirpath, "SKILL.md"))
        fields["version"] = _skill_document.resolve_skill_version(
            str(fields.get("version", "")), _metadata_version(skill_raw)
        )
        resources = _collect_resources(dirpath, {"ARTIFACT.md", "SKILL.md"})
        return _overlay_artifact(artifact_id, fields, frontmatter, body, skill_raw, resources)

    def _read_plain_skill(self, dirpath: str, artifact_id: str) -> OverlayArtifact | None:
        """Load a package whose root holds SKILL.md and no ARTIFACT.md.

        Spec: §4.3.4, §6.4. The description, the search tokens, the body,
        and the tags come from SKILL.md, the tags from a ``metadata.tags``
        value in the readable form, and the frontmatter is the synthesized
        document, which is never parsed for a field. The file is read with
        ``newline=""`` so ``skill_raw`` keeps its line ends. Universal-newline
        mode would turn a lone CR into an LF, which hides from
        ``_metadata_tags`` the CR that puts the frontmatter outside the
        readable form, and the overlay would materialize SKILL.md with bytes
        the file does not hold.
        """
        try:
            with open(os.path.join(dirpath, "SKILL.md"), encoding="utf-8", newline="") as fh:
                skill_raw = fh.read()
        except OSError:
            return None
        skill_fields, body = _parse_frontmatter(skill_raw)
        version = _skill_document.resolve_skill_version("", _metadata_version(skill_raw))
        fields: dict[str, object] = {
            "type": "skill",
            "version": version,
            "description": skill_fields.get("description", ""),
            "tags": _metadata_tags(skill_raw),
        }
        resources = _collect_resources(dirpath, {"SKILL.md"})
        frontmatter = _skill_document.synthesized_skill_document(version)
        return _overlay_artifact(artifact_id, fields, frontmatter, body, skill_raw, resources)

    def _index(self) -> None:
        if not self.artifacts:
            return
        total = 0
        for art in self.artifacts.values():
            total += len(art.tokens)
            for term in set(art.tokens):
                self._doc_freq[term] = self._doc_freq.get(term, 0) + 1
        self._avg_len = total / len(self.artifacts)

    def _bm25(self, query_tokens: list[str], art: OverlayArtifact) -> float:
        if not query_tokens or not art.tokens:
            return 0.0
        n = len(self.artifacts)
        dl = len(art.tokens)
        score = 0.0
        counts: dict[str, int] = {}
        for t in art.tokens:
            counts[t] = counts.get(t, 0) + 1
        for term in query_tokens:
            tf = counts.get(term, 0)
            if tf == 0:
                continue
            df = self._doc_freq.get(term, 0)
            idf = math.log(1 + (n - df + 0.5) / (df + 0.5))
            denom = tf + self._K1 * (1 - self._B + self._B * dl / (self._avg_len or 1))
            score += idf * (tf * (self._K1 + 1)) / denom
        return score

    def search(
        self,
        query: str,
        *,
        type_filter: str = "",
        scope: str = "",
        tags_filter: list[str] | None = None,
        top_k: int = 10,
    ) -> list[OverlayArtifact]:
        """Return overlay artifacts ranked by BM25 (or by id when no query).

        spec §6.4: the overlay is the highest-precedence layer in the
        caller's effective view, so a scoped query excludes overlay
        artifacts whose id falls outside the requested domain path. The
        ``scope`` prefix match mirrors the MCP server's overlay filter
        (cmd/podium-mcp/local_search.go).
        """
        wanted_tags = set(tags_filter or [])
        candidates = [
            art
            for art in self.artifacts.values()
            if (not type_filter or art.type == type_filter)
            and (not scope or art.id.startswith(scope))
            and (not wanted_tags or wanted_tags.issubset(set(art.tags)))
        ]
        if query:
            qtokens = _tokenize(query)
            scored = [(self._bm25(qtokens, art), art) for art in candidates]
            scored = [(s, a) for s, a in scored if s > 0]
            scored.sort(key=lambda pair: (-pair[0], pair[1].id))
            return [a for _s, a in scored[:top_k]]
        candidates.sort(key=lambda a: a.id)
        return candidates[:top_k]

    def get(self, artifact_id: str) -> OverlayArtifact | None:
        return self.artifacts.get(artifact_id)


class OverlayRefused(ValueError):
    """Raised when the overlay holds a package Podium refuses to load (§6.4)."""


def _check_canonical_id(canonical: str) -> None:
    """Refuse a package whose canonical ID names ``.git`` or ``.podium``.

    Spec: §4.2, §6.4, §7.5.3 — the canonical-ID rules apply to an overlay
    package, and no segment of a canonical ID names ``.git`` or ``.podium``
    under the aliasing rule. Discovery skips a dot-prefixed directory, but a
    filesystem resolves ``GIT~1``, or ``.git`` preceded by U+200C, to
    ``.git``, and materialize() writes the ID as a path. The overlay fails to
    load, as filesystem.ValidateCanonicalID makes it fail in the Go consumers.
    """
    for seg in canonical.split("/"):
        if _reserved_names.names_dot_git(seg) or _reserved_names.names_dot_podium(seg):
            raise OverlayRefused(f"canonical ID segment {seg!r} names .git or .podium on some filesystem")


def _has_artifact_manifest(dirpath: str) -> bool:
    """Report whether dirpath directly contains an ARTIFACT.md (§4.4).

    This is the nested-package boundary. It mirrors the artifact result of
    filesystem.PackageManifests in pkg/registry/filesystem/discovery.go: the
    test is the manifest alone, so a subdirectory holding only a SKILL.md is
    an ordinary part of the enclosing package.
    """
    return os.path.isfile(os.path.join(dirpath, "ARTIFACT.md"))


def _package_manifests(dirpath: str) -> tuple[bool, bool]:
    """Report whether dirpath directly holds ARTIFACT.md and SKILL.md (§4.2).

    Mirrors filesystem.PackageManifests in pkg/registry/filesystem/discovery.go.
    ``os.path.isfile`` follows a symbolic link as ``fs.Stat`` does there, so a
    manifest linked to a shared regular file counts and a dangling link reads
    as absent.
    """
    return _has_artifact_manifest(dirpath), os.path.isfile(os.path.join(dirpath, "SKILL.md"))


def _within_package(packages: set[str], canonical: str) -> bool:
    """Report whether a directory strictly above canonical is a package (§4.2).

    Mirrors filesystem.WithinPackage. A directory holding only SKILL.md below
    such an ancestor is a bundled resource of that package.
    """
    parent = canonical.rpartition("/")[0]
    while parent:
        if parent in packages:
            return True
        parent = parent.rpartition("/")[0]
    return False


def _collect_resources(dirpath: str, skip: set[str]) -> dict[str, object]:
    """Return every file under the package root dirpath as a resource.

    The keys are package-relative paths. The root files named in skip and the
    files of a nested package are left out.
    """
    resources: dict[str, object] = {}
    for sub, dirs, files in os.walk(dirpath):
        # spec §4.4 — the files of a nested package are not resources of the
        # enclosing one, so the walk stops at a subdirectory that directly
        # contains an ARTIFACT.md.
        dirs[:] = [d for d in dirs if not _has_artifact_manifest(os.path.join(sub, d))]
        for name in files:
            full = os.path.join(sub, name)
            relname = os.path.relpath(full, dirpath).replace(os.sep, "/")
            if relname in skip:
                continue
            resources[relname] = _read_resource(full)
    return resources


def _overlay_artifact(
    artifact_id: str,
    fields: dict[str, object],
    frontmatter: str,
    body: str,
    skill_raw: str,
    resources: dict[str, object],
) -> OverlayArtifact:
    """Assemble an overlay record and its BM25 tokens."""
    art_type = str(fields.get("type", ""))
    description = str(fields.get("description", ""))
    tags = [str(t) for t in fields.get("tags", [])]
    text = " ".join([artifact_id, art_type, description, " ".join(tags), body])
    return OverlayArtifact(
        id=artifact_id,
        type=art_type,
        version=str(fields.get("version", "")),
        description=description,
        tags=tags,
        frontmatter=frontmatter,
        body=body,
        skill_raw=skill_raw,
        resources=resources,
        tokens=_tokenize(text),
    )


def _read_optional(path: str) -> str:
    """Return a file's text with its line ends unchanged, or "" when it cannot be read.

    Spec: §4.3.4, §6.4. The file is read with ``newline=""``, as
    ``_read_plain_skill`` reads SKILL.md, so a lone CR stays a CR for
    ``_metadata_version`` to refuse and ``skill_raw`` keeps the CR LF line
    ends the registry serves.
    """
    try:
        with open(path, encoding="utf-8", newline="") as fh:
            return fh.read()
    except OSError:
        return ""


def _read_resource(path: str) -> object:
    """Read a resource file as text, falling back to bytes for binary data."""
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except (UnicodeDecodeError, OSError):
        try:
            with open(path, "rb") as fh:
                return fh.read()
        except OSError:
            return ""


# ---------------------------------------------------------------------------
# §4.5.2 glob helpers (ported from pkg/domain/resolve.go).
# ---------------------------------------------------------------------------


def _expand_alternatives(pattern: str) -> list[str]:
    """Expand a single ``{a,b,c}`` group, recursing so multiple groups expand."""
    open_idx = pattern.find("{")
    if open_idx < 0:
        return [pattern]
    close_rel = pattern[open_idx:].find("}")
    if close_rel < 0:
        return [pattern]
    close_idx = close_rel + open_idx
    prefix = pattern[:open_idx]
    suffix = pattern[close_idx + 1 :]
    out: list[str] = []
    for choice in pattern[open_idx + 1 : close_idx].split(","):
        out.extend(_expand_alternatives(prefix + choice + suffix))
    return out


def _match_segments(pat: list[str], tgt: list[str]) -> bool:
    """Match pattern segments against target, honoring ``*`` and ``**``."""
    i = 0
    while i < len(pat):
        seg = pat[i]
        if seg == "**":
            if i == len(pat) - 1:
                return True
            rest = pat[i + 1 :]
            for j in range(len(tgt) + 1):
                if _match_segments(rest, tgt[j:]):
                    return True
            return False
        if i >= len(tgt):
            return False
        if seg != "*" and seg != tgt[i]:
            return False
        i += 1
    return len(pat) == len(tgt)


def match(pattern: str, id: str) -> bool:
    """Report whether the §4.5.2 glob pattern matches the canonical id.

    ``*`` matches one path segment, ``**`` matches zero or more segments
    (recursive), and ``{a,b,c}`` matches any alternative.
    """
    for alt in _expand_alternatives(pattern):
        if _match_segments(alt.split("/"), id.split("/")):
            return True
    return False


def match_any(patterns: list[str], id: str) -> bool:
    """Report whether any pattern in patterns matches id."""
    return any(p and match(p, id) for p in patterns)


def resolve_imports(include: list[str], exclude: list[str], ids: list[str]) -> list[str]:
    """Compute the §4.5.2 import set: ids matching include minus exclude.

    Exclude is applied after include and the input order of ids is preserved.
    An empty include yields no imports.
    """
    return [i for i in ids if match_any(include, i) and not match_any(exclude, i)]


def fallback_description(path: str) -> str:
    """Synthesize the §4.5.5 description fallback for a DOMAIN.md-less domain.

    The directory basename is de-slugged (hyphens and underscores become
    spaces) and title-cased. For example ``finance/accounts-payable`` yields
    ``Accounts Payable``.
    """
    base = path
    if "/" in path:
        base = path[path.rindex("/") + 1 :]
    base = base.replace("-", " ").replace("_", " ")
    words = base.split()
    return " ".join(w[:1].upper() + w[1:] for w in words)


def glob_literal_prefix(pattern: str) -> str:
    """Return the leading literal path segments of a §4.5.2 glob.

    Stops at the first segment carrying a glob metacharacter
    (``*``, ``{``, ``}``, ``,``). A leading glob yields "" (the whole catalog).
    """
    out: list[str] = []
    for seg in pattern.split("/"):
        if any(c in seg for c in "*{},"):
            break
        out.append(seg)
    return "/".join(out)


def rrf_fuse(
    ranked_lists: list[list[str]], *, k: int = 60
) -> dict[str, float]:
    """Reciprocal rank fusion over ranked id lists (spec §6.4.1).

    Each list is an ordering of artifact ids (rank 0 is best). The fused
    score of an id is the sum of ``1 / (k + rank)`` across the lists that
    contain it. Returns ``{id: score}``.
    """
    scores: dict[str, float] = {}
    for ranked in ranked_lists:
        for rank, artifact_id in enumerate(ranked):
            scores[artifact_id] = scores.get(artifact_id, 0.0) + 1.0 / (k + rank + 1)
    return scores
