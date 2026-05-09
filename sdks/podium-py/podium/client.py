"""Podium HTTP client (spec §7.6 surface)."""

from __future__ import annotations

import json
import os
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any


class RegistryError(Exception):
    """Raised when the registry returns a structured error envelope (§6.10)."""

    def __init__(self, code: str, message: str, retryable: bool = False) -> None:
        self.code = code
        self.message = message
        self.retryable = retryable
        super().__init__(f"{code}: {message}")


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
class SearchResult:
    """Envelope returned by search_artifacts and search_domains."""

    query: str = ""
    total_matched: int = 0
    results: list[ArtifactDescriptor] = field(default_factory=list)
    domains: list[dict[str, Any]] = field(default_factory=list)


@dataclass
class LoadedArtifact:
    """Manifest body and bundled resources returned by load_artifact."""

    id: str
    type: str
    version: str
    manifest_body: str
    frontmatter: str
    resources: dict[str, str] = field(default_factory=dict)


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
    ) -> None:
        self.registry = registry.rstrip("/")
        self.identity_provider = identity_provider
        self.overlay_path = overlay_path

    @classmethod
    def from_env(cls) -> "Client":
        registry = os.environ.get("PODIUM_REGISTRY")
        if not registry:
            raise RuntimeError("PODIUM_REGISTRY environment variable is required")
        return cls(
            registry=registry,
            identity_provider=os.environ.get("PODIUM_IDENTITY_PROVIDER", "oauth-device-code"),
            overlay_path=os.environ.get("PODIUM_OVERLAY_PATH"),
        )

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
    ) -> SearchResult:
        params: dict[str, Any] = {"top_k": top_k}
        if query:
            params["query"] = query
        if type:
            params["type"] = type
        if scope:
            params["scope"] = scope
        if tags:
            params["tags"] = ",".join(tags)
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

    def load_artifact(self, artifact_id: str, *, version: str = "") -> LoadedArtifact:
        params = {"id": artifact_id}
        if version:
            params["version"] = version
        body = self._get("/v1/load_artifact", params)
        return LoadedArtifact(
            id=body.get("id", artifact_id),
            type=body.get("type", ""),
            version=body.get("version", ""),
            manifest_body=body.get("manifest_body", ""),
            frontmatter=body.get("frontmatter", ""),
            resources=body.get("resources", {}) or {},
        )

    def dependents_of(self, artifact_id: str) -> list[ArtifactDescriptor]:
        """Return artifacts that depend on artifact_id (spec §4.7.6)."""
        body = self._get("/v1/dependents", {"id": artifact_id})
        return [
            ArtifactDescriptor(
                id=r.get("id", ""),
                type=r.get("type", ""),
                version=r.get("version", ""),
                description=r.get("description", ""),
                tags=r.get("tags") or [],
            )
            for r in body.get("dependents", []) or []
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

    def subscribe(self, *, types: list[str] | None = None):
        """Yield NDJSON events from /v1/events (spec §7.6).

        Each yielded value is the parsed JSON body of one event. The
        iterator runs until the underlying connection closes; callers
        wrap it in a try/except to handle reconnects.
        """
        params: dict[str, Any] = {}
        if types:
            params["types"] = ",".join(types)
        url = self.registry + "/v1/events"
        if params:
            url = url + "?" + urllib.parse.urlencode(params)
        req = urllib.request.Request(url)
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
        req = urllib.request.Request(url)
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
