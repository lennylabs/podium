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
    """Return (fields, body) for an ARTIFACT.md document (§4.3).

    Only the fields the SDK ranks and reports are parsed: ``type``,
    ``version``, ``description``, and ``tags``. ``tags`` accepts both the
    inline ``[a, b]`` form and the block ``- a`` form.
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
        if raw.startswith(" ") or not raw.strip() or raw.lstrip().startswith("#"):
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
            fields[key] = val.strip("\"'")
    fields["tags"] = [t for t in tags if t]
    return fields, body


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
        for dirpath, _dirnames, filenames in os.walk(self.path):
            rel = os.path.relpath(dirpath, self.path)
            canonical = "" if rel in (".", "") else rel.replace(os.sep, "/")
            if "DOMAIN.md" in filenames:
                dom = self._read_domain(dirpath)
                if dom is not None:
                    self.domains[canonical] = dom
            if "ARTIFACT.md" not in filenames:
                continue
            artifact_id = canonical
            if artifact_id == "":
                continue
            art = self._read_package(dirpath, artifact_id)
            if art is not None:
                self.artifacts[artifact_id] = art
        self._index()

    def _read_domain(self, dirpath: str) -> OverlayDomain | None:
        try:
            with open(os.path.join(dirpath, "DOMAIN.md"), encoding="utf-8") as fh:
                text = fh.read()
        except OSError:
            return None
        return _parse_domain(text)

    def _read_package(self, dirpath: str, artifact_id: str) -> OverlayArtifact | None:
        try:
            with open(os.path.join(dirpath, "ARTIFACT.md"), encoding="utf-8") as fh:
                frontmatter = fh.read()
        except OSError:
            return None
        fields, body = _parse_frontmatter(frontmatter)
        art_type = str(fields.get("type", ""))
        skill_raw = ""
        if art_type == "skill":
            try:
                with open(os.path.join(dirpath, "SKILL.md"), encoding="utf-8") as fh:
                    skill_raw = fh.read()
            except OSError:
                skill_raw = ""
        resources: dict[str, object] = {}
        skip = {"ARTIFACT.md"}
        if art_type == "skill":
            skip.add("SKILL.md")
        for sub, _d, files in os.walk(dirpath):
            for name in files:
                full = os.path.join(sub, name)
                relname = os.path.relpath(full, dirpath).replace(os.sep, "/")
                if relname in skip:
                    continue
                resources[relname] = _read_resource(full)
        tags = [str(t) for t in fields.get("tags", [])]
        text = " ".join(
            [artifact_id, art_type, str(fields.get("description", "")), " ".join(tags), body]
        )
        return OverlayArtifact(
            id=artifact_id,
            type=art_type,
            version=str(fields.get("version", "")),
            description=str(fields.get("description", "")),
            tags=tags,
            frontmatter=frontmatter,
            body=body,
            skill_raw=skill_raw,
            resources=resources,
            tokens=_tokenize(text),
        )

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
