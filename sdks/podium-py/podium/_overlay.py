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
        self._avg_len = 0.0
        self._doc_freq: dict[str, int] = {}
        self._load()

    def _load(self) -> None:
        if not self.path or not os.path.isdir(self.path):
            return
        for dirpath, _dirnames, filenames in os.walk(self.path):
            if "ARTIFACT.md" not in filenames:
                continue
            rel = os.path.relpath(dirpath, self.path)
            artifact_id = rel.replace(os.sep, "/")
            if artifact_id in (".", ""):
                continue
            art = self._read_package(dirpath, artifact_id)
            if art is not None:
                self.artifacts[artifact_id] = art
        self._index()

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
        tags_filter: list[str] | None = None,
        top_k: int = 10,
    ) -> list[OverlayArtifact]:
        """Return overlay artifacts ranked by BM25 (or by id when no query)."""
        wanted_tags = set(tags_filter or [])
        candidates = [
            art
            for art in self.artifacts.values()
            if (not type_filter or art.type == type_filter)
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
