"""Skill versions and the synthesized ARTIFACT.md of a skill that has none.

Spec: §4.3.4, §4.7.6. Mirrors the helpers in pkg/manifest/skill_version.go so
the overlay serves, and ``materialize()`` omits, the same bytes the registry
does.
"""

from __future__ import annotations

import re

#: The registry-reserved version a source with no stored rows reports for a
#: skill that declares no usable version (§4.7.6, §13.11.3).
AUTO_VERSION_ZERO = "0.0.0-auto.0"

# ``[0-9]`` rather than ``\d``, because ``\d`` matches every Unicode digit in
# Python while the Go pattern accepts ASCII digits only.
_SEMVER_RE = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+")
_AUTO_VERSION_RE = re.compile(r"0\.0\.0-auto\.(0|[1-9][0-9]*)")

# The bytes are fixed by §4.3.4: every line ends with a single line feed, and
# the document ends with the line feed after the comment.
_SYNTHESIZED_PREFIX = "---\ntype: skill\nversion: "
_SYNTHESIZED_SUFFIX = "\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"


def is_semver(v: str) -> bool:
    """Report whether v is an authored major.minor.patch version.

    Mirrors ``manifest.ValidateVersion``.
    """
    return _SEMVER_RE.fullmatch(v) is not None


def resolve_skill_version(artifact_version: str, metadata_version: str) -> str:
    """Return a skill's version (§4.3.4, §13.11.3).

    The ARTIFACT.md version wins when set, then a major.minor.patch SKILL.md
    ``metadata.version``, and otherwise the assigned ``0.0.0-auto.0``. A
    ``metadata.version`` in any other form is ignored, because ``metadata`` is
    free-form in the Agent Skills standard. Mirrors
    ``manifest.ResolveSkillVersion`` with the filesystem-source fallback.
    """
    if artifact_version:
        return artifact_version
    if is_semver(metadata_version):
        return metadata_version
    return AUTO_VERSION_ZERO


def synthesized_skill_document(version: str) -> str:
    """Return the ARTIFACT.md served for a skill with no ARTIFACT.md (§4.3.4).

    Raises ``ValueError`` for a version that is neither a major.minor.patch
    version nor the reserved ``0.0.0-auto.<N>`` form, so no value can inject a
    key or a line into the document. Mirrors
    ``manifest.SynthesizedSkillDocument``.
    """
    if not is_semver(version) and _AUTO_VERSION_RE.fullmatch(version) is None:
        raise ValueError(f"invalid version: {version!r}")
    return _SYNTHESIZED_PREFIX + version + _SYNTHESIZED_SUFFIX


def is_synthesized_skill_document(doc: str) -> bool:
    """Report whether doc is exactly the synthesized ARTIFACT.md (§4.3.4).

    The comparison is against the document for the version that doc's own
    version line names. It compares text and parses no YAML, so a document
    that differs in any character, such as a reordered key or a missing
    comment, reports False. Mirrors ``manifest.IsSynthesizedSkillDocument``.
    """
    if not doc.startswith(_SYNTHESIZED_PREFIX) or not doc.endswith(_SYNTHESIZED_SUFFIX):
        return False
    version = doc[len(_SYNTHESIZED_PREFIX) : len(doc) - len(_SYNTHESIZED_SUFFIX)]
    try:
        return synthesized_skill_document(version) == doc
    except ValueError:
        return False
