"""Podium HTTP client (spec §7.6 surface)."""

from __future__ import annotations

import json
import os
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Callable


class RegistryError(Exception):
    """Raised when the registry returns a structured error envelope (§6.10)."""

    def __init__(self, code: str, message: str, retryable: bool = False) -> None:
        self.code = code
        self.message = message
        self.retryable = retryable
        super().__init__(f"{code}: {message}")


# spec: §11 (Search browse mode test) — the search top_k cap. Distinct from the
# §7.6.2 batch-load 50-ID cap; this bounds the number of returned search results.
_MAX_TOP_K = 50


def _check_top_k(top_k: int) -> None:
    """Reject top_k > 50 before the request is sent (spec §11, §6.10)."""
    if top_k > _MAX_TOP_K:
        raise RegistryError("registry.invalid_argument", "top_k > 50")


class DeviceCodeRequired(Exception):
    """Raised when the configured identity provider needs a device-code flow.

    Stage 3 does not implement OAuth; this exception is wired so callers
    can catch it once oauth-device-code lands in Phase 11.
    """


@dataclass
class ArtifactDescriptor:
    """A single result returned by search_artifacts and load_domain."""

    id: str
    type: str
    version: str = ""
    description: str = ""
    tags: list[str] = field(default_factory=list)
    score: float = 0.0


@dataclass
class DependencyEdge:
    """A reverse-dependency edge returned by dependents_of (spec §7.6).

    Mirrors the server envelope ``{"from", "to", "kind"}`` and the
    TypeScript SDK's ``DependencyEdge``. ``from_`` carries the wire
    ``from`` field, renamed because ``from`` is a Python keyword.
    """

    from_: str = ""
    to: str = ""
    kind: str = ""


@dataclass
class SearchResult:
    """Envelope returned by search_artifacts and search_domains."""

    query: str = ""
    total_matched: int = 0
    results: list[ArtifactDescriptor] = field(default_factory=list)
    domains: list[dict[str, Any]] = field(default_factory=list)


class MaterializeError(Exception):
    """Raised when materialize() cannot safely write to disk.

    The §6.6 sandbox contract forbids writing outside the destination
    root; a resource path that escapes it raises this rather than
    writing through the traversal.
    """


def _safe_join(root: str, rel: str) -> str:
    """Join rel under root, rejecting any path that escapes root (§6.6).

    Mirrors pkg/materialize.Write's ErrOutOfDestination guard: a
    bundled-resource path containing ``..`` must not write outside the
    destination root.
    """
    parts = [p for p in rel.replace("\\", "/").split("/") if p not in ("", ".")]
    target = os.path.normpath(os.path.join(root, *parts))
    root_abs = os.path.normpath(root)
    if target != root_abs and not target.startswith(root_abs + os.sep):
        raise MaterializeError(f"resource path escapes destination root: {rel!r}")
    return target


def _write_file(path: str, content: bytes) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as fh:
        fh.write(content)


def _fetch_bytes(url: str) -> bytes:
    with urllib.request.urlopen(url) as resp:  # noqa: S310 - registry-issued presigned URL
        return resp.read()


@dataclass
class LoadedArtifact:
    """Manifest body and bundled resources returned by load_artifact.

    ``materialize`` writes the artifact to disk in the canonical layout
    (spec §7.6, §2.2). ``resources`` are inline bytes returned in the
    response; ``large_resources`` are §7.2 presigned references that
    materialize fetches on demand.
    """

    id: str
    type: str
    version: str
    manifest_body: str
    frontmatter: str
    resources: dict[str, str] = field(default_factory=dict)
    large_resources: dict[str, dict[str, Any]] = field(default_factory=dict)
    # spec: §4.3.4 / §11 — the verbatim SKILL.md for a skill, delivered so the
    # materialized file is byte-identical to the authored source.
    skill_raw: str = ""

    def materialize(
        self,
        to: str,
        *,
        harness: str = "none",
        fetch: Callable[[str], bytes] | None = None,
    ) -> list[str]:
        """Write the artifact to disk under ``to`` and return the paths.

        spec §7.6 / §2.2 — the loaded-artifact object exposes
        ``materialize(to=..., harness=...)``. The artifact lands under
        ``<to>/<id>/`` in the canonical layout: ``ARTIFACT.md`` for every
        type, ``SKILL.md`` for skills, and each bundled resource at its
        package-relative path. Large resources are fetched from their
        §7.2 presigned URLs.

        The ``harness`` parameter is accepted per §2.2. Harness-specific
        adaptation is the registry's shared module (§2.2); the SDK is an
        independent HTTP client that does not embed the harness adapters,
        so it writes the canonical layout that the ``none`` adapter
        produces. ``harness`` is recorded for forward compatibility with
        server-side adaptation.
        """
        return _materialize_canonical(
            to,
            artifact_id=self.id,
            artifact_type=self.type,
            frontmatter=self.frontmatter,
            manifest_body=self.manifest_body,
            skill_raw=self.skill_raw,
            inline_resources=self.resources,
            large_resources=self.large_resources,
            fetch=fetch or _fetch_bytes,
        )


@dataclass
class BatchResult:
    """One §7.6.2 bulk-load envelope with a materialize() helper.

    Mirrors the spec's ``for result in artifacts: result.materialize(...)``
    example. ``status`` is ``"ok"`` or ``"error"``; on error ``error``
    carries the §6.10 code/message and the manifest fields are empty.
    """

    id: str
    status: str
    version: str = ""
    content_hash: str = ""
    type: str = ""
    manifest_body: str = ""
    frontmatter: str = ""
    skill_raw: str = ""
    resources: list[dict[str, Any]] = field(default_factory=list)
    error: "RegistryError | None" = None

    def materialize(
        self,
        to: str,
        *,
        harness: str = "none",
        fetch: Callable[[str], bytes] | None = None,
    ) -> list[str]:
        """Write an ``ok`` batch item to disk (spec §7.6.2).

        Raises RegistryError when called on an ``error`` item so a caller
        that forgets to check ``status`` fails loudly rather than writing
        an empty package. Batch resources travel as §7.6.2 presigned
        references, so every resource is fetched from its URL.
        """
        if self.status != "ok":
            raise self.error or RegistryError("registry.unknown", f"cannot materialize {self.id}")
        large = {r["path"]: {"url": r.get("presigned_url", "")} for r in self.resources}
        return _materialize_canonical(
            to,
            artifact_id=self.id,
            artifact_type=self.type,
            frontmatter=self.frontmatter,
            manifest_body=self.manifest_body,
            skill_raw=self.skill_raw,
            inline_resources={},
            large_resources=large,
            fetch=fetch or _fetch_bytes,
        )


def _materialize_canonical(
    to: str,
    *,
    artifact_id: str,
    artifact_type: str,
    frontmatter: str,
    manifest_body: str,
    skill_raw: str,
    inline_resources: dict[str, str],
    large_resources: dict[str, dict[str, Any]],
    fetch: Callable[[str], bytes],
) -> list[str]:
    """Write the canonical (``none``-adapter) layout for one artifact.

    The wire ``frontmatter`` already carries the complete ``ARTIFACT.md``
    for non-skills; for skills it carries the frontmatter-only
    ``ARTIFACT.md`` and the skill body arrives separately as
    ``manifest_body``, so ``SKILL.md`` is reconstructed as
    ``frontmatter + manifest_body`` (mirroring the MCP server's
    server-source delivery). spec §6.6, §6.7.
    """
    if not to:
        raise MaterializeError("destination path is empty")
    root = _safe_join(to, artifact_id)
    written: list[str] = []

    art_path = os.path.join(root, "ARTIFACT.md")
    _write_file(art_path, frontmatter.encode())
    written.append(art_path)

    if artifact_type == "skill":
        skill_path = os.path.join(root, "SKILL.md")
        # spec: §4.3.4 / §11 — prefer the verbatim SKILL.md the registry
        # delivers; fall back to frontmatter+body only when it is absent.
        skill_md = skill_raw if skill_raw else (frontmatter + manifest_body)
        _write_file(skill_path, skill_md.encode())
        written.append(skill_path)

    for rel, content in sorted((inline_resources or {}).items()):
        path = _safe_join(root, rel)
        data = content.encode() if isinstance(content, str) else bytes(content)
        _write_file(path, data)
        written.append(path)

    for rel, link in sorted((large_resources or {}).items()):
        path = _safe_join(root, rel)
        url = link.get("url") or link.get("presigned_url") if isinstance(link, dict) else link
        if not url:
            raise MaterializeError(f"large resource {rel!r} has no presigned URL")
        _write_file(path, fetch(url))
        written.append(path)

    return written


def _batch_result_from(env: dict[str, Any]) -> BatchResult:
    """Parse one §7.6.2 batch envelope into a BatchResult."""
    err = None
    if env.get("error"):
        e = env["error"]
        err = RegistryError(
            code=e.get("code", "registry.unknown"),
            message=e.get("message", ""),
            retryable=e.get("retryable", False),
        )
    return BatchResult(
        id=env.get("id", ""),
        status=env.get("status", ""),
        version=env.get("version", ""),
        content_hash=env.get("content_hash", ""),
        type=env.get("type", ""),
        manifest_body=env.get("manifest_body", ""),
        frontmatter=env.get("frontmatter", ""),
        skill_raw=env.get("skill_raw", ""),
        resources=env.get("resources", []) or [],
        error=err,
    )


class Client:
    """Thin HTTP client over the registry's meta-tool API.

    Construct with an explicit registry URL or call `Client.from_env()` to
    pick up `PODIUM_REGISTRY`, `PODIUM_IDENTITY_PROVIDER`, and
    `PODIUM_OVERLAY_PATH` per §6.2.
    """

    def __init__(
        self,
        registry: str,
        *,
        identity_provider: str = "oauth-device-code",
        overlay_path: str | None = None,
        token: str = "",
    ) -> None:
        self.registry = registry.rstrip("/")
        self.identity_provider = identity_provider
        self.overlay_path = overlay_path
        # spec: §7.6 — the SDK reaches the registry with the same identity as
        # the MCP path. The caller passes a session/access token here (or via
        # PODIUM_SESSION_TOKEN in from_env); it is attached as the Bearer
        # credential on every request so visibility filtering applies.
        self.token = token

    @classmethod
    def from_env(cls) -> "Client":
        registry = os.environ.get("PODIUM_REGISTRY")
        if not registry:
            raise RuntimeError("PODIUM_REGISTRY environment variable is required")
        return cls(
            registry=registry,
            identity_provider=os.environ.get("PODIUM_IDENTITY_PROVIDER", "oauth-device-code"),
            overlay_path=os.environ.get("PODIUM_OVERLAY_PATH"),
            # §6.3.2 injected session token: the env-driven credential the MCP
            # bridge also reads, so the SDK reaches the registry as the same
            # identity without an interactive device-code flow.
            token=os.environ.get("PODIUM_SESSION_TOKEN", ""),
        )

    def _headers(self, extra: dict[str, str] | None = None) -> dict[str, str]:
        """Return request headers with the Bearer credential when configured."""
        headers = dict(extra or {})
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        return headers

    def load_domain(self, path: str = "", depth: int = 1) -> dict[str, Any]:
        params = {}
        if path:
            params["path"] = path
        if depth:
            params["depth"] = depth
        return self._get("/v1/load_domain", params)

    def search_domains(
        self, query: str = "", *, scope: str = "", top_k: int = 10
    ) -> SearchResult:
        # spec: §11 (Search browse mode test) — top_k > 50 is rejected with a
        # structured registry.invalid_argument error, enforced client-side in
        # the SDK as well as server-side at the registry (§6.10).
        _check_top_k(top_k)
        params = {"top_k": top_k}
        if query:
            params["query"] = query
        if scope:
            params["scope"] = scope
        body = self._get("/v1/search_domains", params)
        return SearchResult(
            query=body.get("query", ""),
            total_matched=body.get("total_matched", 0),
            domains=body.get("domains", []) or [],
        )

    def search_artifacts(
        self,
        query: str = "",
        *,
        type: str = "",
        scope: str = "",
        tags: list[str] | None = None,
        top_k: int = 10,
        session_id: str = "",
    ) -> SearchResult:
        # spec: §11 (Search browse mode test) — client-side top_k cap, mirroring
        # the server's registry.invalid_argument rejection (§6.10).
        _check_top_k(top_k)
        params: dict[str, Any] = {"top_k": top_k}
        if query:
            params["query"] = query
        if type:
            params["type"] = type
        if scope:
            params["scope"] = scope
        if tags:
            params["tags"] = ",".join(tags)
        # spec: §7.6 — session_id for session-consistent retrieval.
        if session_id:
            params["session_id"] = session_id
        body = self._get("/v1/search_artifacts", params)
        results = [
            ArtifactDescriptor(
                id=r.get("id", ""),
                type=r.get("type", ""),
                version=r.get("version", ""),
                description=r.get("description", ""),
                tags=r.get("tags") or [],
                score=r.get("score", 0.0),
            )
            for r in body.get("results", []) or []
        ]
        return SearchResult(
            query=body.get("query", ""),
            total_matched=body.get("total_matched", 0),
            results=results,
        )

    def load_artifact(
        self, artifact_id: str, *, version: str = "", session_id: str = ""
    ) -> LoadedArtifact:
        params = {"id": artifact_id}
        if version:
            params["version"] = version
        # spec: §7.6.1 — --session-id maps to load_artifact session_id for
        # consistent latest resolution within a session (§4.7.6).
        if session_id:
            params["session_id"] = session_id
        body = self._get("/v1/load_artifact", params)
        return LoadedArtifact(
            id=body.get("id", artifact_id),
            type=body.get("type", ""),
            version=body.get("version", ""),
            manifest_body=body.get("manifest_body", ""),
            frontmatter=body.get("frontmatter", ""),
            skill_raw=body.get("skill_raw", ""),
            resources=body.get("resources", {}) or {},
            # §7.2 large resources travel as presigned references the
            # consumer fetches from object storage; materialize() pulls them.
            large_resources=body.get("large_resources", {}) or {},
        )

    def load_artifacts(
        self,
        ids: list[str],
        *,
        session_id: str = "",
        harness: str = "",
        version_pins: dict[str, str] | None = None,
    ) -> list[BatchResult]:
        """Bulk-fetch artifacts via §7.6.2 POST /v1/artifacts:batchLoad.

        The §7.6.2 hard cap is 50 IDs per request; the SDK splits
        larger sets transparently. Each returned ``BatchResult`` carries
        ``status="ok"`` with the manifest body (and a ``materialize()``
        helper), or ``status="error"`` with a §6.10 envelope. Partial
        failure does not raise.
        """
        if not ids:
            return []
        out: list[BatchResult] = []
        chunk_size = 50
        for chunk_start in range(0, len(ids), chunk_size):
            chunk = ids[chunk_start : chunk_start + chunk_size]
            body: dict[str, Any] = {"ids": chunk}
            if session_id:
                body["session_id"] = session_id
            if harness:
                body["harness"] = harness
            if version_pins:
                body["version_pins"] = {k: v for k, v in version_pins.items() if k in chunk}
            data = json.dumps(body).encode()
            req = urllib.request.Request(
                self.registry + "/v1/artifacts:batchLoad",
                data=data,
                headers=self._headers({"Content-Type": "application/json"}),
                method="POST",
            )
            try:
                with urllib.request.urlopen(req) as resp:
                    raw = resp.read()
            except urllib.error.HTTPError as exc:
                self._raise_from_http_error(exc)
            out.extend(_batch_result_from(env) for env in json.loads(raw))
        return out

    def dependents_of(self, artifact_id: str) -> list[DependencyEdge]:
        """Return reverse-dependency edges for artifact_id (spec §7.6, §4.7.6).

        The server replies with ``{"edges": [{"from", "to", "kind"}]}``
        (matching the TypeScript SDK). Each edge names a dependent and the
        dependency kind (extends, delegates_to, mcpServers).
        """
        body = self._get("/v1/dependents", {"id": artifact_id})
        return [
            DependencyEdge(
                from_=e.get("from", ""),
                to=e.get("to", ""),
                kind=e.get("kind", ""),
            )
            for e in body.get("edges", []) or []
        ]

    def preview_scope(
        self,
        *,
        scope: str = "",
        type: str = "",
        tags: list[str] | None = None,
    ) -> dict[str, Any]:
        """Preview a scope's effective artifact set (spec §6.4)."""
        params: dict[str, Any] = {}
        if scope:
            params["scope"] = scope
        if type:
            params["type"] = type
        if tags:
            params["tags"] = ",".join(tags)
        return self._get("/v1/scope/preview", params)

    def subscribe(self, event_types: list[str] | None = None):
        """Yield NDJSON events from /v1/events (spec §7.6).

        Each yielded value is the parsed JSON body of one event. The
        iterator runs until the underlying connection closes; callers
        wrap it in a try/except to handle reconnects.

        spec: §7.6 — the documented call form passes the event-type list
        positionally (`client.subscribe(["artifact.published", ...])`).
        The filter is sent as one repeated ``type`` query parameter per
        event type, matching the server's ``/v1/events`` handler (which
        reads ``r.URL.Query()["type"]``) and the TypeScript SDK. A single
        comma-joined ``types`` parameter is never read by the server.
        """
        # Repeated key form: ?type=a&type=b. doseq expands the list so each
        # event type becomes its own type= pair the server can read.
        params = [("type", t) for t in (event_types or [])]
        url = self.registry + "/v1/events"
        if params:
            url = url + "?" + urllib.parse.urlencode(params)
        req = urllib.request.Request(url, headers=self._headers())
        with urllib.request.urlopen(req) as resp:
            for raw in resp:
                line = raw.decode("utf-8").rstrip("\n")
                if not line:
                    continue
                try:
                    yield json.loads(line)
                except json.JSONDecodeError:
                    continue

    def _get(self, path: str, params: dict[str, Any]) -> dict[str, Any]:
        url = self.registry + path
        if params:
            url = url + "?" + urllib.parse.urlencode(params)
        req = urllib.request.Request(url, headers=self._headers())
        try:
            with urllib.request.urlopen(req) as resp:
                body = resp.read()
        except urllib.error.HTTPError as exc:
            self._raise_from_http_error(exc)
        return json.loads(body)

    def _raise_from_http_error(self, exc: urllib.error.HTTPError) -> None:
        try:
            envelope = json.loads(exc.read())
        except Exception:
            raise RegistryError("registry.unknown", f"HTTP {exc.code}: {exc.reason}") from exc
        raise RegistryError(
            code=envelope.get("code", "registry.unknown"),
            message=envelope.get("message", str(exc)),
            retryable=envelope.get("retryable", False),
        )
