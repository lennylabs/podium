"""The §7.5.3 aliasing rule for .git and .podium in the Python SDK.

Spec: §4.2, §6.4, §7.5.3, §7.6. ``test/vectors/reserved-names.json`` holds
the segments the Go generator checks against materialize.AliasesDotGit and
materialize.AliasesDotPodium, so a case that fails here is an SDK divergence
from Go. materialize() refuses a written segment that names .git, and an
overlay package whose canonical ID names .git or .podium disables the overlay
with a warning.
"""

from __future__ import annotations

import json
import pathlib
import warnings

import pytest

from podium import Client, MaterializeError, _overlay, _reserved_names
from podium.client import LoadedArtifact

_CORPUS_PATH = pathlib.Path(__file__).parents[3] / "test" / "vectors" / "reserved-names.json"
_CASES = json.loads(_CORPUS_PATH.read_text(encoding="utf-8"))["cases"]


def _segment(case: dict) -> str:
    """Decode a case's bytes as os.fsdecode would, invalid UTF-8 included."""
    return bytes.fromhex(case["segment_hex"]).decode("utf-8", "surrogateescape")


# Spec: §7.5.3 — every corpus segment gets the verdicts Go reaches.
@pytest.mark.parametrize("case", _CASES, ids=[c["name"] for c in _CASES])
def test_reserved_names_match_the_go_corpus(case):
    seg = _segment(case)
    assert _reserved_names.names_dot_git(seg) is case["names_git"]
    assert _reserved_names.names_dot_podium(seg) is case["names_podium"]


_ZWNJ = "\u200c"


# Spec: §7.5.3, §7.6 — materialize() refuses a canonical ID or a resource path
# with a segment that names .git, literally or under the aliasing rule, and
# writes nothing.
@pytest.mark.parametrize(
    ("artifact_id", "resources"),
    [
        ("team/hooks", {".git/config": "[core]\n"}),
        ("team/hooks", {"scripts/.GIT/config": "[core]\n"}),
        ("team/hooks", {"GIT~1/config": "[core]\n"}),
        ("team/hooks", {_ZWNJ + ".git/config": "[core]\n"}),
        ("team/hooks", {".git./config": "[core]\n"}),
        ("GIT~1/hooks", {}),
        (_ZWNJ + ".git", {}),
    ],
)
def test_materialize_refuses_a_segment_that_names_dot_git(tmp_path, artifact_id, resources):
    art = LoadedArtifact(
        id=artifact_id,
        type="agent",
        version="1.0.0",
        manifest_body="body\n",
        frontmatter="---\ntype: agent\n---\n\nbody\n",
        resources=resources,
    )
    out = tmp_path / "out"
    with pytest.raises(MaterializeError, match="names .git"):
        art.materialize(str(out))
    assert not out.exists() or not any(out.rglob("*"))


# Spec: §7.6 — names that only resemble .git are ordinary resources.
def test_materialize_writes_near_misses(tmp_path):
    art = LoadedArtifact(
        id="team/hooks",
        type="agent",
        version="1.0.0",
        manifest_body="body\n",
        frontmatter="---\ntype: agent\n---\n\nbody\n",
        resources={".gitignore": "x\n", ".github/ci.yml": "y\n", "GIT~2/config": "z\n"},
    )
    art.materialize(str(tmp_path))
    root = tmp_path / "team" / "hooks"
    assert (root / ".gitignore").read_text() == "x\n"
    assert (root / ".github" / "ci.yml").read_text() == "y\n"
    assert (root / "GIT~2" / "config").read_text() == "z\n"


def _write(path: pathlib.Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


_AGENT = "---\ntype: agent\nversion: 0.1.0\ndescription: Draft.\n---\n\nbody\n"


# Spec: §4.2, §6.4, §6.9 — an overlay that holds a package whose canonical ID
# names .git or .podium fails to load. The SDK skips the overlay with a
# warning naming the path and serves none of its packages, so load_artifact
# never materializes the package.
@pytest.mark.parametrize("bad", ["GIT~1", _ZWNJ + ".git", "team/PODIUM~1"])
def test_overlay_with_an_aliased_package_is_skipped_with_a_warning(tmp_path, bad):
    overlay = tmp_path / "overlay"
    _write(overlay / bad / "ARTIFACT.md", _AGENT)
    _write(overlay / bad / "config", "[core]\n\tfsmonitor = touch pwned\n")
    _write(overlay / "team" / "draft" / "ARTIFACT.md", _AGENT)
    with pytest.raises(_overlay.OverlayRefused, match="names .git or .podium"):
        _overlay.LocalOverlay(str(overlay))
    client = Client(registry="http://127.0.0.1:1", overlay_path=str(overlay))
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        assert client._overlay_index() is None
    assert any(str(overlay) in str(w.message) and "overlay disabled" in str(w.message) for w in caught)


# Spec: §4.2, §6.4 — an overlay with no aliased package loads as before, and
# a directory that only resembles .git is an ordinary domain.
def test_overlay_without_an_aliased_package_loads(tmp_path):
    overlay = tmp_path / "overlay"
    _write(overlay / "GIT~2" / "draft" / "ARTIFACT.md", _AGENT)
    index = _overlay.LocalOverlay(str(overlay))
    assert index.get("GIT~2/draft") is not None


# Spec: §7.5.3 — the comparison helpers handle their edge inputs.
def test_reserved_name_helpers_edge_inputs():
    assert not _reserved_names._equal_fold_ascii("gi", "git")
    assert _reserved_names._lower_ascii("") == ""
    assert _reserved_names._next_hfs_char("", 0) == ("", 0)
