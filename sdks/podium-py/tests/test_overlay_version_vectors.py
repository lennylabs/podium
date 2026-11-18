"""Run the Go-generated SKILL.md metadata.version corpus through the overlay.

Spec: §4.3.4, §6.4. ``test/vectors/skill-metadata-version.json`` declares, for
each SKILL.md and the ARTIFACT.md beside it, the version the workspace
overlay must report, and the Go generator fails when a readable case that Go
parses resolves to another version, so a case that fails here is an SDK
divergence from Go. A ``version:`` line nested in a multi-line flow
collection or a multi-line quoted scalar is never read. A case with no
``artifact_md`` runs as a SKILL.md-only package and again beside the corpus
``default_artifact_md``, which declares no version, and both runs report the
same version.
"""

from __future__ import annotations

import json
import pathlib

import pytest

from podium import _overlay

_CORPUS_PATH = pathlib.Path(__file__).parents[3] / "test" / "vectors" / "skill-metadata-version.json"
_CORPUS = json.loads(_CORPUS_PATH.read_text(encoding="utf-8"))
_CASES = _CORPUS["cases"]
_DEFAULT_ARTIFACT_MD = _CORPUS["default_artifact_md"]
_SKILL_ONLY = [c for c in _CASES if "artifact_md" not in c]


def _overlay_version(tmp_path, skill_md: str, artifact_md: str | None) -> _overlay.OverlayArtifact:
    pkg = tmp_path / "overlay" / "acme" / "greet"
    pkg.mkdir(parents=True)
    # Binary mode keeps the case's line ends, a CR LF and a lone CR included.
    (pkg / "SKILL.md").write_bytes(skill_md.encode("utf-8"))
    if artifact_md is not None:
        (pkg / "ARTIFACT.md").write_bytes(artifact_md.encode("utf-8"))
    art = _overlay.LocalOverlay(str(tmp_path / "overlay")).get("acme/greet")
    assert art is not None
    assert art.type == "skill"
    return art


@pytest.mark.parametrize("case", _SKILL_ONLY, ids=[c["name"] for c in _SKILL_ONLY])
def test_overlay_reads_metadata_version_as_the_corpus_declares(tmp_path, case):
    art = _overlay_version(tmp_path, case["skill_md"], None)
    assert art.version == case["sdk_version"]


@pytest.mark.parametrize("case", _CASES, ids=[c["name"] for c in _CASES])
def test_overlay_reads_metadata_version_beside_an_artifact_md(tmp_path, case):
    art = _overlay_version(tmp_path, case["skill_md"], case.get("artifact_md", _DEFAULT_ARTIFACT_MD))
    assert art.version == case["sdk_version"]
    # The SKILL.md bytes are served as the file holds them, line ends included.
    assert art.skill_raw == case["skill_md"]
