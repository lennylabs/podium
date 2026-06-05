"""Client-side ``sync.yaml`` registry resolution (spec §7.5.2, §13.10).

The SDK keeps no YAML dependency (the client imports only ``json`` and
``urllib``), so this module reads the single ``defaults:`` mapping the
client needs rather than parsing arbitrary YAML. It mirrors the Go
client's merged-config lookup: the registry resolves from
``PODIUM_REGISTRY`` first, then the project-local, project-shared, and
user-global ``sync.yaml`` scopes in descending precedence.
"""

from __future__ import annotations

import os

_PODIUM_DIR = ".podium"


def discover_workspace(start: str) -> str | None:
    """Walk up from ``start`` to the first directory holding ``.podium/``.

    spec §7.5.2 — the workspace is the nearest ancestor with a
    ``.podium/`` directory. Returns ``None`` when none is found.
    """
    if not start:
        return None
    cur = os.path.abspath(start)
    while True:
        if os.path.isdir(os.path.join(cur, _PODIUM_DIR)):
            return cur
        parent = os.path.dirname(cur)
        if parent == cur:
            return None
        cur = parent


def _scalar(value: str) -> str:
    """Decode a YAML scalar value: strip quotes and any inline comment."""
    v = value.strip()
    if v and v[0] in "\"'":
        quote = v[0]
        end = v.find(quote, 1)
        return v[1:end] if end != -1 else v[1:]
    # An unquoted inline comment starts at " #" per YAML; a registry URL
    # never contains a bare " #", so this is safe for the defaults block.
    idx = v.find(" #")
    if idx != -1:
        v = v[:idx]
    return v.strip()


def _parse_defaults(text: str) -> dict[str, str]:
    """Parse the top-level ``defaults:`` mapping from a sync.yaml document."""
    defaults: dict[str, str] = {}
    in_defaults = False
    base_indent: int | None = None
    for raw in text.splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        stripped = raw.strip()
        if indent == 0:
            in_defaults = stripped.startswith("defaults:")
            base_indent = None
            continue
        if not in_defaults:
            continue
        if base_indent is None:
            base_indent = indent
        if indent < base_indent:
            in_defaults = False
            continue
        key, sep, val = stripped.partition(":")
        if sep:
            defaults[key.strip()] = _scalar(val)
    return defaults


def read_registry(path: str) -> str:
    """Return ``defaults.registry`` from one sync.yaml file, or ``""``."""
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except OSError:
        return ""
    return _parse_defaults(text).get("registry", "")


def resolve_registry(env_registry: str | None, cwd: str, home: str | None) -> str:
    """Resolve the registry across all §7.5.2 scopes.

    Precedence (highest first): ``PODIUM_REGISTRY``, the workspace
    ``.podium/sync.local.yaml``, the workspace ``.podium/sync.yaml``, and
    the user-global ``~/.podium/sync.yaml``. Returns ``""`` when unset
    across every scope, which the caller surfaces as ``config.no_registry``.
    """
    if env_registry:
        return env_registry
    candidates: list[str] = []
    workspace = discover_workspace(cwd)
    if workspace:
        candidates.append(os.path.join(workspace, _PODIUM_DIR, "sync.local.yaml"))
        candidates.append(os.path.join(workspace, _PODIUM_DIR, "sync.yaml"))
    if home:
        candidates.append(os.path.join(home, _PODIUM_DIR, "sync.yaml"))
    for path in candidates:
        registry = read_registry(path)
        if registry:
            return registry
    return ""
