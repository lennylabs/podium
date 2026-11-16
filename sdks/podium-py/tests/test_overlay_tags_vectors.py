"""Run the Go-generated SKILL.md metadata.tags corpus through the overlay.

Spec: §4.3.4, §6.4. ``test/vectors/skill-metadata-tags.json`` declares, for
each SKILL.md, the tags the workspace overlay must report, and the Go
generator fails when a readable case that Go parses resolves to other tags,
so a case that fails here is an SDK divergence from Go.
"""

from __future__ import annotations

import json
import pathlib

import pytest

from podium import _overlay

_CORPUS_PATH = pathlib.Path(__file__).parents[3] / "test" / "vectors" / "skill-metadata-tags.json"
_CASES = json.loads(_CORPUS_PATH.read_text(encoding="utf-8"))["cases"]


@pytest.mark.parametrize("case", _CASES, ids=[c["name"] for c in _CASES])
def test_overlay_reads_metadata_tags_as_the_corpus_declares(tmp_path, case):
    pkg = tmp_path / "overlay" / "acme" / "greet"
    pkg.mkdir(parents=True)
    # Binary mode keeps the case's line ends, a CR LF and a lone CR included.
    (pkg / "SKILL.md").write_bytes(case["skill_md"].encode("utf-8"))
    index = _overlay.LocalOverlay(str(tmp_path / "overlay"))
    art = index.get("acme/greet")
    assert art is not None
    assert art.type == "skill"
    assert art.tags == case["sdk_tags"]
    if case["sdk_tags"]:
        hits = index.search("", tags_filter=[case["sdk_tags"][0]])
        assert [a.id for a in hits] == ["acme/greet"]
