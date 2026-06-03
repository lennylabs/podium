"""Podium HTTP client (spec §7.6 surface)."""

from __future__ import annotations

import base64
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import webbrowser
from dataclasses import dataclass, field
from typing import Any, Callable

from . import _config, _oauth, _overlay


class RegistryError(Exception):
    """Raised when the registry returns a structured error envelope (§6.10)."""

    def __init__(
        self,
        code: str,
        message: str,
        retryable: bool = False,
        *,
        details: dict[str, Any] | None = None,
        suggested_action: str = "",
    ) -> None:
        self.code = code
        self.message = message
        self.retryable = retryable
        # spec: §6.10 — the full envelope carries a machine-readable details map
        # (for example {"runtime_iss": ...}) and an operator remediation hint.
        # Callers read both off the exception (F-6.10.1); they default to an
        # empty map and empty string when the registry omits them.
        self.details: dict[str, Any] = details or {}
        self.suggested_action = suggested_action
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


class RegistryReadOnly(RegistryError):
    """Raised when a write is rejected because the registry is in §13.2.1
    read-only mode (the §6.10 ``registry.read_only`` error code).

    It is a subclass of :class:`RegistryError`, so callers that catch the
    base error keep working while callers that want to retry once the
    registry leaves read-only mode can catch this type specifically.
    """


def _registry_error_from_envelope(envelope: dict) -> RegistryError:
    """Build the §6.10 error for a structured envelope, choosing the most
    specific subclass for the code. ``registry.read_only`` (§13.2.1) maps
    to :class:`RegistryReadOnly`; every other code maps to the base
    :class:`RegistryError`.
    """
    code = envelope.get("code", "registry.unknown")
    message = envelope.get("message", "")
    retryable = bool(envelope.get("retryable", False))
    # spec: §6.10 — preserve the machine-readable details map and the operator
    # remediation hint so callers can read the full envelope (F-6.10.1).
    raw_details = envelope.get("details")
    details = raw_details if isinstance(raw_details, dict) else {}
    suggested_action = envelope.get("suggested_action") or ""
    if code == "registry.read_only":
        return RegistryReadOnly(
            code,
            message,
            retryable=retryable,
            details=details,
            suggested_action=suggested_action,
        )
    return RegistryError(
        code,
        message,
        retryable=retryable,
        details=details,
        suggested_action=suggested_action,
    )


@dataclass
class ArtifactDescriptor:
    """A single result returned by search_artifacts and load_domain."""

    id: str
    type: str
    version: str = ""
    description: str = ""
    tags: list[str] = field(default_factory=list)
    score: float = 0.0
    # spec: §7.6.1 — a search_artifacts result carries the artifact's
    # frontmatter (the documented {id, type, version, score, frontmatter}
    # schema). Empty for load_domain notable entries.
    frontmatter: str = ""


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


def _open_browser(url: str) -> None:
    """Best-effort open the verification URL; never blocks or raises."""
    try:
        webbrowser.open(url)
    except Exception:  # noqa: BLE001 - browser launch is best-effort (§7.7)
        pass


def _fetch_bytes(url: str) -> bytes:
    with urllib.request.urlopen(url) as resp:  # noqa: S310 - registry-issued presigned URL
        return resp.read()


def _decode_inline_resources(
    resources: dict[str, str], b64: bool
) -> dict[str, str | bytes]:
    """Decode inline bundled resources for materialization.

    spec §4.1 / §7.2 (F-4.1.1): a binary resource at or below the inline
    cutoff is base64-encoded on the wire and the response carries
    ``resources_base64: true`` so ``encoding/json`` does not replace its
    non-UTF-8 bytes with U+FFFD. The flag is response-wide, so when set
    every inline value is decoded back to raw bytes; otherwise the values
    are UTF-8 text and pass through unchanged.
    """
    if not b64:
        return dict(resources)
    return {k: base64.b64decode(v) for k, v in resources.items()}


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
    # str for UTF-8 text resources; bytes for a base64-decoded binary
    # resource (§7.2 resources_base64). materialize writes either faithfully.
    resources: dict[str, str | bytes] = field(default_factory=dict)
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
        # §7.6.2: a resource carries a presigned_url with an object store
        # configured. In the standalone-without-storage mode it carries the
        # bytes inline (base64-encoded when inline_base64 is set), so deliver
        # those rather than fetching a URL that does not exist (F-7.6.4).
        inline: dict[str, str | bytes] = {}
        large: dict[str, dict[str, Any]] = {}
        for r in self.resources:
            if r.get("presigned_url"):
                large[r["path"]] = {"url": r["presigned_url"]}
            else:
                value = r.get("inline", "")
                inline[r["path"]] = base64.b64decode(value) if r.get("inline_base64") else value
        return _materialize_canonical(
            to,
            artifact_id=self.id,
            artifact_type=self.type,
            frontmatter=self.frontmatter,
            manifest_body=self.manifest_body,
            skill_raw=self.skill_raw,
            inline_resources=inline,
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
    inline_resources: dict[str, str | bytes],
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
        # spec: §13.2.1 / §6.10 — a batch item rejected with
        # registry.read_only carries RegistryReadOnly so materialize()
        # re-raises the specific type.
        err = _registry_error_from_envelope(env["error"])
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


# spec: §6.5 / §6.2 — the recognized PODIUM_CACHE_MODE values.
_CACHE_MODES = ("always-revalidate", "offline-first", "offline-only")


class Client:
    """Thin HTTP client over the registry's meta-tool API.

    Construct with an explicit registry URL or call `Client.from_env()` to
    pick up `PODIUM_REGISTRY`, `PODIUM_IDENTITY_PROVIDER`,
    `PODIUM_OVERLAY_PATH`, and `PODIUM_CACHE_MODE` per §6.2.
    """

    def __init__(
        self,
        registry: str,
        *,
        identity_provider: str = "oauth-device-code",
        overlay_path: str | None = None,
        token: str = "",
        cache_mode: str = "always-revalidate",
    ) -> None:
        self.registry = registry.rstrip("/")
        self.identity_provider = identity_provider
        # spec §6.4 — the SDK honors the overlay lookup order: an explicit
        # Client(overlay_path=...) wins over PODIUM_OVERLAY_PATH, which wins
        # over the <CWD>/.podium/overlay/ fallback. The resolved path drives
        # the client-side overlay merge in search_artifacts and load_artifact.
        self.overlay_path = _overlay.resolve_overlay_path(
            overlay_path, os.environ.get("PODIUM_OVERLAY_PATH"), os.getcwd()
        )
        # The overlay index is read on demand and cached per session_id
        # (§6.4: "cached for the duration of a session_id").
        self._overlay_cache: dict[str, "_overlay.LocalOverlay | None"] = {}
        # spec: §7.6 — the SDK reaches the registry with the same identity as
        # the MCP path. The caller passes a session/access token here (or via
        # PODIUM_SESSION_TOKEN in from_env); it is attached as the Bearer
        # credential on every request so visibility filtering applies.
        self.token = token
        # spec: §7.4 — "podium sync and the SDKs apply the same cache modes."
        # The SDK keeps no persistent content cache, so always-revalidate and
        # offline-first both fetch on every call (there is nothing cached to
        # serve), while offline-only "never contact the registry" and therefore
        # raises a structured cache-miss error before any request.
        if cache_mode not in _CACHE_MODES:
            raise ValueError(
                f"cache_mode must be one of {_CACHE_MODES}, got {cache_mode!r}"
            )
        self.cache_mode = cache_mode

    @classmethod
    def from_env(cls) -> "Client":
        # spec §14.4 / §13.10 — from_env "picks up registry URL from
        # sync.yaml + overlay path". The registry resolves from
        # PODIUM_REGISTRY first, then the project-local, project-shared, and
        # user-global sync.yaml scopes (§7.5.2). When unset across every
        # scope the SDK reports the same config.no_registry condition the
        # CLI does (§6.10, §7.5.2), pointing the caller at `podium init`.
        registry = _config.resolve_registry(
            os.environ.get("PODIUM_REGISTRY"), os.getcwd(), os.path.expanduser("~")
        )
        if not registry:
            raise RegistryError(
                "config.no_registry",
                "no registry configured: set PODIUM_REGISTRY, add defaults.registry "
                "to sync.yaml, or run `podium init`",
            )
        return cls(
            registry=registry,
            identity_provider=os.environ.get("PODIUM_IDENTITY_PROVIDER", "oauth-device-code"),
            overlay_path=os.environ.get("PODIUM_OVERLAY_PATH"),
            # §6.3.2 injected session token: the env-driven credential the MCP
            # bridge also reads, so the SDK reaches the registry as the same
            # identity without an interactive device-code flow.
            token=os.environ.get("PODIUM_SESSION_TOKEN", ""),
            # §7.4 cache mode, shared with the MCP server and podium sync.
            cache_mode=os.environ.get("PODIUM_CACHE_MODE") or "always-revalidate",
        )

    def _guard_offline(self) -> None:
        """Enforce §7.4 offline-only: never contact the registry.

        The SDK has no local cache, so an offline-only call is always a cache
        miss and raises the structured network.offline_cache_miss error (the
        §6.10 network.* namespace, matching the MCP server) before a request is
        issued (F-7.4.3).
        """
        if self.cache_mode == "offline-only":
            raise RegistryError(
                "network.offline_cache_miss",
                "offline-only mode: the registry was not contacted and the SDK keeps no offline cache",
            )

    def _unreachable_error(self, exc: Exception) -> RegistryError:
        """Map a transport-level failure to the §7.4 network.registry_unreachable
        structured error (F-7.4.1).

        A connection refused, DNS failure, or connect timeout raises
        ``urllib.error.URLError`` rather than ``HTTPError``. The SDK keeps no
        content cache, so an unreachable registry in any mode that contacts it
        (always-revalidate and offline-first; offline-only short-circuits in
        ``_guard_offline``) is a no-cache miss. The returned error mirrors the
        MCP bridge's namespaced code, retryable flag, and remediation hint.
        """
        return RegistryError(
            "network.registry_unreachable",
            f"the registry at {self.registry} is unreachable: {exc}",
            retryable=True,
            suggested_action=(
                "Check network connectivity to the registry; the request can be "
                "retried once it is reachable."
            ),
        )

    def _headers(self, extra: dict[str, str] | None = None) -> dict[str, str]:
        """Return request headers with the Bearer credential when configured."""
        headers = dict(extra or {})
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        return headers

    def _overlay_index(self, session_id: str = "") -> "_overlay.LocalOverlay | None":
        """Return the overlay index, reading it per call (cached per session).

        spec §6.4 — the SDK reads the overlay on each search_artifacts /
        load_artifact call, cached for the duration of a session_id. With no
        session_id the overlay is re-read every call so in-progress edits are
        always visible.
        """
        if not self.overlay_path:
            return None
        if session_id and session_id in self._overlay_cache:
            return self._overlay_cache[session_id]
        index = _overlay.LocalOverlay(self.overlay_path)
        if not index.artifacts:
            index = None
        if session_id:
            self._overlay_cache[session_id] = index
        return index

    def login(
        self,
        *,
        open_browser: bool = False,
        timeout: float = _oauth.DEFAULT_TIMEOUT,
        client_id: str | None = None,
        scopes: list[str] | None = None,
        audience: str = "",
        device_authorization_endpoint: str = "",
        token_endpoint: str = "",
        opener: Callable[[Any], Any] | None = None,
        sleep: Callable[[float], None] | None = None,
    ) -> _oauth.Tokens:
        """Run the §6.3 oauth-device-code flow and cache the access token.

        spec §14.8 / §7.7 — ``client.login()`` performs the device-code
        flow before any catalog calls. The IdP is discovered from the
        registry's RFC 8414 metadata (overridable via the endpoint
        parameters or the ``PODIUM_OAUTH_*`` env vars). The verification URL
        and user code print to stderr; polling is bounded by ``timeout``
        (10 minutes by default). On success the access token is stored on
        the client and attached as the ``Authorization: Bearer`` credential
        on every subsequent request (§7.6).
        """
        cid = client_id or os.environ.get("PODIUM_OAUTH_CLIENT_ID") or "podium-cli"
        req_scopes = scopes if scopes is not None else ["openid", "profile", "email", "groups"]
        aud = audience or os.environ.get("PODIUM_OAUTH_AUDIENCE", "")
        device_url = (
            device_authorization_endpoint
            or os.environ.get("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT", "")
        )
        tok_url = token_endpoint or os.environ.get("PODIUM_OAUTH_TOKEN_URL", "")
        if not device_url:
            device_url, discovered = _oauth.discover_idp(self.registry, opener=opener)
            if not tok_url:
                tok_url = discovered
        if not tok_url:
            tok_url = self.registry.rstrip("/") + "/oauth2/token"

        auth = _oauth.initiate(device_url, cid, req_scopes, aud, opener=opener)
        print(f"Visit: {auth.verification_uri}", file=sys.stderr)
        print(f"User code: {auth.user_code}", file=sys.stderr)
        if open_browser and auth.verification_uri_complete:
            _open_browser(auth.verification_uri_complete)
        tokens = _oauth.poll(
            tok_url,
            cid,
            auth,
            timeout=timeout,
            opener=opener,
            sleep=sleep or time.sleep,
        )
        self.token = tokens.access_token
        return tokens

    def load_domain(self, path: str = "", depth: int | None = None) -> dict[str, Any]:
        # spec: §4.5.5 / §5.1 (F-4.5.4) — depth is unset by default. The query
        # parameter is omitted unless the caller supplies one, so the registry
        # applies its configured default max_depth (3) rather than the SDK
        # forcing a single rendered level.
        params: dict[str, Any] = {}
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
        registry_results = [
            ArtifactDescriptor(
                id=r.get("id", ""),
                type=r.get("type", ""),
                version=r.get("version", ""),
                description=r.get("description", ""),
                tags=r.get("tags") or [],
                score=r.get("score", 0.0),
                # spec: §7.6.1 — surface the artifact frontmatter the registry
                # carries on each search result.
                frontmatter=r.get("frontmatter", ""),
            )
            for r in body.get("results", []) or []
        ]
        results, total = self._fuse_overlay(
            registry_results,
            body.get("total_matched", 0),
            query=query,
            type_filter=type,
            scope=scope,
            tags=tags,
            top_k=top_k,
            session_id=session_id,
        )
        return SearchResult(
            query=body.get("query", ""),
            total_matched=total,
            results=results,
        )

    def _fuse_overlay(
        self,
        registry_results: list[ArtifactDescriptor],
        registry_total: int,
        *,
        query: str,
        type_filter: str,
        scope: str = "",
        tags: list[str] | None,
        top_k: int,
        session_id: str,
    ) -> tuple[list[ArtifactDescriptor], int]:
        """Fuse overlay hits into the registry results via RRF (§6.4, §6.4.1).

        The workspace overlay is the highest-precedence layer, so an overlay
        artifact's metadata wins over a registry hit with the same id. With
        no overlay configured the registry results pass through unchanged. The
        ``scope`` filter is threaded into the overlay search so a scoped query
        excludes out-of-scope overlay artifacts, matching the registry stream
        and the Go MCP server.
        """
        index = self._overlay_index(session_id)
        if index is None:
            return registry_results, registry_total
        overlay_hits = index.search(
            query, type_filter=type_filter, scope=scope, tags_filter=tags, top_k=top_k
        )
        if not overlay_hits:
            return registry_results, registry_total
        overlay_ids = [a.id for a in overlay_hits]
        registry_ids = [r.id for r in registry_results]
        fused = _overlay.rrf_fuse([overlay_ids, registry_ids])
        by_id: dict[str, ArtifactDescriptor] = {r.id: r for r in registry_results}
        for art in overlay_hits:
            # Overlay precedence: overwrite any same-id registry descriptor.
            by_id[art.id] = ArtifactDescriptor(
                id=art.id,
                type=art.type,
                version=art.version,
                description=art.description,
                tags=list(art.tags),
                score=fused.get(art.id, 0.0),
            )
        for desc in registry_results:
            if desc.id in by_id and desc.id not in overlay_ids:
                by_id[desc.id].score = fused.get(desc.id, desc.score)
        merged = sorted(by_id.values(), key=lambda d: (-d.score, d.id))[:top_k]
        # New overlay-only ids enlarge the matched count beyond the registry's.
        total = registry_total + len([i for i in overlay_ids if i not in registry_ids])
        return merged, total

    def load_artifact(
        self, artifact_id: str, *, version: str = "", session_id: str = ""
    ) -> LoadedArtifact:
        # spec §6.4 — the overlay is the highest-precedence layer, so an
        # in-progress overlay artifact resolves ahead of the registry. A
        # pinned --version still goes to the registry: the overlay carries a
        # single working copy, not a version history.
        if not version:
            index = self._overlay_index(session_id)
            if index is not None:
                art = index.get(artifact_id)
                if art is not None:
                    return LoadedArtifact(
                        id=art.id,
                        type=art.type,
                        version=art.version,
                        manifest_body=art.body,
                        frontmatter=art.frontmatter,
                        skill_raw=art.skill_raw,
                        resources=dict(art.resources),
                        large_resources={},
                    )
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
            # §4.1/§7.2 (F-4.1.1): decode a base64-flagged inline set back to
            # raw bytes so a binary resource materializes uncorrupted.
            resources=_decode_inline_resources(
                body.get("resources", {}) or {}, body.get("resources_base64", False)
            ),
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
        # §7.4 offline-only short-circuit before any network request.
        self._guard_offline()
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
            except urllib.error.URLError as exc:
                # spec: §7.4 — an unreachable registry on the batch path also
                # surfaces the structured no-cache error (F-7.4.1).
                raise self._unreachable_error(exc) from exc
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

    def preview_scope(self) -> dict[str, Any]:
        """Return aggregate counts for the caller's effective view (spec §3.5).

        Takes no arguments: the caller's OAuth identity determines layer
        composition exactly as for a real session, and the endpoint returns
        aggregate counts only (``layers``, ``artifact_count``, ``by_type``,
        ``by_sensitivity``) with no per-artifact metadata. The server reads no
        query parameters, so no constraint filters are sent.
        """
        return self._get("/v1/scope/preview", {})

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
        # §7.4 offline-only short-circuit before opening the event stream.
        self._guard_offline()
        # Repeated key form: ?type=a&type=b. doseq expands the list so each
        # event type becomes its own type= pair the server can read.
        params = [("type", t) for t in (event_types or [])]
        url = self.registry + "/v1/events"
        if params:
            url = url + "?" + urllib.parse.urlencode(params)
        req = urllib.request.Request(url, headers=self._headers())
        try:
            resp = urllib.request.urlopen(req)
        except urllib.error.HTTPError as exc:
            self._raise_from_http_error(exc)
        except urllib.error.URLError as exc:
            # spec: §7.4 — an unreachable registry on the event stream surfaces
            # the structured no-cache error (F-7.4.1).
            raise self._unreachable_error(exc) from exc
        with resp:
            for raw in resp:
                line = raw.decode("utf-8").rstrip("\n")
                if not line:
                    continue
                try:
                    yield json.loads(line)
                except json.JSONDecodeError:
                    continue

    def _get(self, path: str, params: dict[str, Any]) -> dict[str, Any]:
        # §7.4 offline-only short-circuit before any network request.
        self._guard_offline()
        url = self.registry + path
        if params:
            url = url + "?" + urllib.parse.urlencode(params)
        req = urllib.request.Request(url, headers=self._headers())
        try:
            with urllib.request.urlopen(req) as resp:
                body = resp.read()
        except urllib.error.HTTPError as exc:
            self._raise_from_http_error(exc)
        except urllib.error.URLError as exc:
            # spec: §7.4 — a transport failure (no HTTP response) is the
            # always-revalidate no-cache case (F-7.4.1).
            raise self._unreachable_error(exc) from exc
        return json.loads(body)

    def _raise_from_http_error(self, exc: urllib.error.HTTPError) -> None:
        try:
            envelope = json.loads(exc.read())
        except Exception:
            raise RegistryError("registry.unknown", f"HTTP {exc.code}: {exc.reason}") from exc
        # spec: §13.2.1 / §6.10 — a write rejected with registry.read_only
        # surfaces as RegistryReadOnly (a RegistryError subclass); every
        # other code maps to the base RegistryError.
        raise _registry_error_from_envelope(envelope)
