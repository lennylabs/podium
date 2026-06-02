"""Tests for the Podium Python SDK."""

from __future__ import annotations

import base64
import http.server
import json
import socket
import threading

import pytest

from podium import Client, MaterializeError, RegistryError
from podium.client import BatchResult, LoadedArtifact


class _StubHandler(http.server.BaseHTTPRequestHandler):
    """Records the last request and replies with whatever the test sets."""

    def log_message(self, format, *args):  # noqa: A002 - signature inherited
        pass

    def do_GET(self):  # noqa: N802 - signature inherited
        self.server.last_path = self.path  # type: ignore[attr-defined]
        self.server.last_auth = self.headers.get("Authorization", "")  # type: ignore[attr-defined]
        body = json.dumps(self.server.next_response).encode()  # type: ignore[attr-defined]
        self.send_response(self.server.next_status)  # type: ignore[attr-defined]
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):  # noqa: N802 - signature inherited
        self.server.last_path = self.path  # type: ignore[attr-defined]
        length = int(self.headers.get("Content-Length", "0"))
        self.server.last_body = self.rfile.read(length)  # type: ignore[attr-defined]
        body = json.dumps(self.server.next_response).encode()  # type: ignore[attr-defined]
        self.send_response(self.server.next_status)  # type: ignore[attr-defined]
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


@pytest.fixture()
def stub_server():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()

    server = http.server.HTTPServer(("127.0.0.1", port), _StubHandler)
    server.next_status = 200
    server.next_response = {}
    server.last_path = ""
    server.last_auth = ""

    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield server
    server.shutdown()
    thread.join()


# Spec: §7.6 SDK surface — search_artifacts forwards to GET /v1/search_artifacts
# and decodes the SearchResult envelope.
def test_search_artifacts_forwards_query(stub_server):
    stub_server.next_response = {
        "query": "variance",
        "total_matched": 1,
        "results": [
            {
                "id": "finance/run-variance",
                "type": "skill",
                "version": "1.0.0",
                "description": "Variance analysis",
            },
        ],
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    out = client.search_artifacts("variance", top_k=5)

    assert "search_artifacts" in stub_server.last_path
    assert "query=variance" in stub_server.last_path
    assert out.total_matched == 1
    assert out.results[0].id == "finance/run-variance"


# Spec: §7.6 SDK surface — load_artifact returns a LoadedArtifact with
# manifest body and bundled resources.
def test_load_artifact_returns_manifest_and_resources(stub_server):
    stub_server.next_response = {
        "id": "finance/run",
        "type": "skill",
        "version": "1.0.0",
        "manifest_body": "Body.",
        "frontmatter": "---\ntype: skill\n---\n",
        "resources": {"scripts/run.py": "print('run')\n"},
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    art = client.load_artifact("finance/run")

    assert art.id == "finance/run"
    assert art.manifest_body == "Body."
    assert art.resources == {"scripts/run.py": "print('run')\n"}


# Spec: §11 (Search browse mode test) — top_k > 50 is rejected client-side with
# a structured registry.invalid_argument error before any request is sent.
def test_search_artifacts_rejects_top_k_over_50(stub_server):
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    with pytest.raises(RegistryError) as exc:
        client.search_artifacts("variance", top_k=51)
    assert exc.value.code == "registry.invalid_argument"
    # The bound check fires before the HTTP call, so the stub records no path.
    assert stub_server.last_path == ""


# Spec: §11 — the boundary value top_k == 50 is accepted (cap is strictly > 50).
def test_search_artifacts_allows_top_k_at_50(stub_server):
    stub_server.next_response = {"query": "q", "total_matched": 0, "results": []}
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    client.search_artifacts("q", top_k=50)
    assert "top_k=50" in stub_server.last_path


# Spec: §11 — search_domains enforces the same client-side top_k cap.
def test_search_domains_rejects_top_k_over_50(stub_server):
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    with pytest.raises(RegistryError) as exc:
        client.search_domains("q", top_k=200)
    assert exc.value.code == "registry.invalid_argument"
    assert stub_server.last_path == ""


# Spec: §6.10 — error envelopes from the registry surface as RegistryError
# with the namespaced code preserved.
def test_registry_error_envelope_translates_to_exception(stub_server):
    stub_server.next_status = 404
    stub_server.next_response = {
        "code": "registry.not_found",
        "message": "artifact not found",
        "retryable": False,
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    with pytest.raises(RegistryError) as exc:
        client.load_artifact("does/not/exist")
    assert exc.value.code == "registry.not_found"
    assert "artifact not found" in exc.value.message


# Spec: §6.2 — Client.from_env reads PODIUM_REGISTRY and provider env vars.
def test_from_env_reads_registry(monkeypatch):
    monkeypatch.setenv("PODIUM_REGISTRY", "http://127.0.0.1:9999")
    monkeypatch.setenv("PODIUM_OVERLAY_PATH", "/tmp/overlay")
    client = Client.from_env()
    assert client.registry == "http://127.0.0.1:9999"
    assert client.overlay_path == "/tmp/overlay"


# Spec: §7.6 — search_artifacts forwards the session_id filter (F-7.6.3).
def test_search_artifacts_forwards_session_id(stub_server):
    stub_server.next_response = {"query": "q", "total_matched": 0, "results": []}
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    client.search_artifacts("variance", session_id="sess-7")
    assert "session_id=sess-7" in stub_server.last_path


# Spec: §7.6.1 — load_artifact forwards --session-id for consistent latest
# resolution within a session (F-7.6.5, F-7.6.6).
def test_load_artifact_forwards_session_id(stub_server):
    stub_server.next_response = {
        "id": "finance/run", "type": "skill", "version": "1.0.0",
        "manifest_body": "b", "frontmatter": "f",
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    client.load_artifact("finance/run", session_id="sess-9")
    assert "session_id=sess-9" in stub_server.last_path


# Spec: §7.6 / §4.7.6 — dependents_of reads the server's {"edges": [...]}
# envelope (matching the TypeScript SDK and the /v1/dependents handler) and
# returns DependencyEdge objects with from/to/kind.
def test_dependents_of_decodes_edges(stub_server):
    stub_server.next_response = {
        "edges": [
            {"from": "finance/run", "to": "finance/glossary", "kind": "extends"},
        ],
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    deps = client.dependents_of("finance/glossary")
    assert "/v1/dependents" in stub_server.last_path
    assert len(deps) == 1
    assert deps[0].from_ == "finance/run"
    assert deps[0].to == "finance/glossary"
    assert deps[0].kind == "extends"


# Spec: §3.5 (F-3.5.7) — preview_scope takes no arguments and sends no query
# constraints (the spec shows preview_scope() with no args and the server
# reads no query parameters). It GETs /v1/scope/preview and passes the
# aggregate-count response through unchanged.
def test_preview_scope_sends_no_constraints(stub_server):
    stub_server.next_response = {
        "layers": ["alice-personal"],
        "artifact_count": 12,
        "by_type": {"skill": 12},
        "by_sensitivity": {"low": 12},
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    out = client.preview_scope()
    assert "/v1/scope/preview" in stub_server.last_path
    # No constraint query params are appended: the path is bare.
    assert "?" not in stub_server.last_path
    assert out["artifact_count"] == 12
    assert out["by_sensitivity"] == {"low": 12}


# Spec: §4.5.5 / §5.1 (F-4.5.4) — load_domain omits the depth query parameter
# by default so the registry applies its configured default max_depth (3)
# instead of the SDK forcing a single rendered level.
def test_load_domain_omits_depth_by_default(stub_server):
    stub_server.next_response = {"path": "finance", "subdomains": [], "notable": []}
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    client.load_domain("finance")
    assert "/v1/load_domain" in stub_server.last_path
    assert "path=finance" in stub_server.last_path
    assert "depth" not in stub_server.last_path


# Spec: §4.5.5 / §5.1 (F-4.5.4) — an explicit depth is forwarded so a caller
# can still override the configured default.
def test_load_domain_forwards_explicit_depth(stub_server):
    stub_server.next_response = {"path": "finance", "subdomains": [], "notable": []}
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    client.load_domain("finance", depth=2)
    assert "depth=2" in stub_server.last_path


# Spec: §7.6.2 — load_artifacts POSTs to /v1/artifacts:batchLoad
# and returns per-item envelopes; partial failures do not raise.
def test_load_artifacts_returns_envelopes(stub_server):
    stub_server.next_response = [
        {"id": "a", "status": "ok", "version": "1.0.0", "content_hash": "sha256:a"},
        {"id": "b", "status": "error", "error": {"code": "registry.not_found", "message": "missing"}},
    ]
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    out = client.load_artifacts(["a", "b"])

    assert "/v1/artifacts:batchLoad" in stub_server.last_path
    body = json.loads(stub_server.last_body)
    assert body["ids"] == ["a", "b"]
    assert len(out) == 2
    assert out[0].status == "ok"
    assert out[1].status == "error"
    assert out[1].error is not None and out[1].error.code == "registry.not_found"


# Spec: §7.6.2 — empty ids list short-circuits to an empty
# response without a network call.
def test_load_artifacts_empty_short_circuits(stub_server):
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    out = client.load_artifacts([])
    assert out == []
    assert stub_server.last_path == ""


# Spec: §7.6 / §2.2 (F-2.2.1) — the loaded-artifact object exposes
# materialize(to=..., harness=...) and writes the canonical layout to disk.
def test_materialize_context_writes_artifact_md(tmp_path):
    art = LoadedArtifact(
        id="finance/close/run-variance",
        type="context",
        version="1.0.0",
        manifest_body="# body\n",
        frontmatter="---\ntype: context\n---\n\n# body\n",
    )
    written = art.materialize(str(tmp_path), harness="claude-code")
    art_md = tmp_path / "finance" / "close" / "run-variance" / "ARTIFACT.md"
    assert art_md.read_text() == "---\ntype: context\n---\n\n# body\n"
    assert str(art_md) in written
    # A non-skill writes no SKILL.md.
    assert not (tmp_path / "finance" / "close" / "run-variance" / "SKILL.md").exists()


# Spec: §6.7 — a skill additionally materializes SKILL.md reconstructed as
# frontmatter + manifest_body (the registry's server-source delivery).
def test_materialize_skill_writes_skill_md(tmp_path):
    art = LoadedArtifact(
        id="eng/lint",
        type="skill",
        version="2.0.0",
        manifest_body="Run the linter.\n",
        frontmatter="---\ntype: skill\n---\n",
    )
    art.materialize(str(tmp_path))
    root = tmp_path / "eng" / "lint"
    assert (root / "ARTIFACT.md").read_text() == "---\ntype: skill\n---\n"
    assert (root / "SKILL.md").read_text() == "---\ntype: skill\n---\nRun the linter.\n"


# Spec: §4.3.4 / §11 — when the registry delivers skill_raw, SKILL.md is the
# verbatim authored file (its own frontmatter preserved), not a reconstruction.
def test_materialize_skill_uses_verbatim_skill_raw(tmp_path):
    skill_md = "---\nname: lint\ndescription: Run the project linter.\n---\n\nRun the linter.\n"
    art = LoadedArtifact(
        id="eng/lint",
        type="skill",
        version="2.0.0",
        manifest_body="Run the linter.\n",
        frontmatter="---\ntype: skill\nversion: 2.0.0\n---\n",
        skill_raw=skill_md,
    )
    art.materialize(str(tmp_path))
    root = tmp_path / "eng" / "lint"
    # ARTIFACT.md is the manifest frontmatter; SKILL.md is the authored file.
    assert (root / "ARTIFACT.md").read_text() == "---\ntype: skill\nversion: 2.0.0\n---\n"
    assert (root / "SKILL.md").read_text() == skill_md


# Spec: §4.4 — inline bundled resources land at their package-relative path.
def test_materialize_writes_inline_resources(tmp_path):
    art = LoadedArtifact(
        id="a/b",
        type="context",
        version="1",
        manifest_body="x",
        frontmatter="---\ntype: context\n---\n",
        resources={"data/table.csv": "1,2,3\n"},
    )
    art.materialize(str(tmp_path))
    assert (tmp_path / "a" / "b" / "data" / "table.csv").read_text() == "1,2,3\n"


# Spec: §7.2 — large resources are fetched from their presigned URLs.
def test_materialize_fetches_large_resources(tmp_path):
    calls = []

    def fake_fetch(url):
        calls.append(url)
        return b"BIGDATA"

    art = LoadedArtifact(
        id="a/b",
        type="context",
        version="1",
        manifest_body="x",
        frontmatter="---\ntype: context\n---\n",
        large_resources={"big.bin": {"url": "https://store/presigned"}},
    )
    art.materialize(str(tmp_path), fetch=fake_fetch)
    assert (tmp_path / "a" / "b" / "big.bin").read_bytes() == b"BIGDATA"
    assert calls == ["https://store/presigned"]


# Spec: §6.6 — a resource path that escapes the destination root is rejected
# (sandbox contract), not written through the traversal.
def test_materialize_rejects_path_traversal(tmp_path):
    art = LoadedArtifact(
        id="a/b",
        type="context",
        version="1",
        manifest_body="x",
        frontmatter="---\ntype: context\n---\n",
        resources={"../../escape.txt": "nope"},
    )
    with pytest.raises(MaterializeError):
        art.materialize(str(tmp_path))


# Spec: §7.6.2 — a batch result materializes ok items and fetches its
# presigned resources; an error item refuses to materialize.
def test_batch_result_materialize_ok_and_error(tmp_path):
    ok = BatchResult(
        id="a/b",
        status="ok",
        type="context",
        manifest_body="x",
        frontmatter="---\ntype: context\n---\n",
        resources=[{"path": "r.bin", "presigned_url": "https://store/r"}],
    )
    ok.materialize(str(tmp_path), fetch=lambda url: b"R")
    assert (tmp_path / "a" / "b" / "r.bin").read_bytes() == b"R"

    bad = BatchResult(id="x/y", status="error", error=RegistryError("visibility.denied", "no"))
    with pytest.raises(RegistryError):
        bad.materialize(str(tmp_path))


# Spec: §4.1/§7.2 (F-4.1.1) — load_artifact decodes a base64-flagged inline set
# (resources_base64) back to raw bytes, so a binary resource materializes
# uncorrupted instead of as a U+FFFD-mangled string.
def test_load_artifact_decodes_base64_binary_resources(stub_server, tmp_path):
    blob = bytes([0xFF, 0xFE, 0x00, 0x01, 0x02, 0xFD])
    stub_server.next_response = {
        "id": "a/b",
        "type": "context",
        "version": "1.0.0",
        "manifest_body": "x",
        "frontmatter": "---\ntype: context\n---\n",
        "resources": {"data/blob.bin": base64.b64encode(blob).decode()},
        "resources_base64": True,
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    art = client.load_artifact("a/b")
    assert art.resources["data/blob.bin"] == blob
    art.materialize(str(tmp_path))
    assert (tmp_path / "a" / "b" / "data" / "blob.bin").read_bytes() == blob


# Spec: §7.6.2 (F-7.6.4) — a batch item materializes inline resources delivered
# without an object store: a text resource as a literal string, a binary one
# decoded from inline_base64. No presigned URL is fetched.
def test_batch_result_materializes_inline_resources(tmp_path):
    blob = bytes([0xFF, 0xFE, 0x10, 0x20])
    item = BatchResult(
        id="a/b",
        status="ok",
        type="context",
        manifest_body="x",
        frontmatter="---\ntype: context\n---\n",
        resources=[
            {"path": "scripts/run.py", "inline": "print('hi')\n"},
            {"path": "data/blob.bin", "inline": base64.b64encode(blob).decode(), "inline_base64": True},
        ],
    )

    def fail_fetch(url):  # a presigned fetch must not happen in this mode
        raise AssertionError(f"unexpected fetch of {url}")

    item.materialize(str(tmp_path), fetch=fail_fetch)
    root = tmp_path / "a" / "b"
    assert (root / "scripts" / "run.py").read_text() == "print('hi')\n"
    assert (root / "data" / "blob.bin").read_bytes() == blob


# Spec: §2.2 — materialize on an empty destination is rejected.
def test_materialize_empty_destination(tmp_path):
    art = LoadedArtifact(id="a", type="context", version="1", manifest_body="", frontmatter="x")
    with pytest.raises(MaterializeError):
        art.materialize("")


# Spec: §7.6 (F-7.6.8) — subscribe takes the event-type list positionally (the
# documented call form) and sends one repeated `type` query parameter per
# event type, matching the server's /v1/events handler and the TypeScript SDK.
# A comma-joined `types` parameter the server never reads must not be emitted.
def test_subscribe_sends_repeated_type_params(stub_server):
    stub_server.next_response = {"event": "artifact.published"}
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    it = client.subscribe(["artifact.published", "artifact.deprecated"])
    next(it)  # consume one event so the request is made
    path = stub_server.last_path
    assert "/v1/events" in path
    assert "type=artifact.published" in path
    assert "type=artifact.deprecated" in path
    assert "types=" not in path


# Spec: §7.6 (F-7.6.13) — the client attaches its session/access token as the
# Bearer credential so it reaches the registry with the same identity as the
# MCP path; visibility filtering then applies server-side.
def test_requests_attach_bearer_token(stub_server):
    stub_server.next_response = {"query": "q", "total_matched": 0, "results": []}
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}", token="tok-7")
    client.search_artifacts("q")
    # The stub records the last Authorization header on GET.
    assert getattr(stub_server, "last_auth", "") == "Bearer tok-7"


# Spec: §7.6 (F-7.6.13) — with no token configured no Authorization header is
# sent, so an anonymous client still reaches an open registry.
def test_no_token_sends_no_auth_header(stub_server):
    stub_server.next_response = {"query": "q", "total_matched": 0, "results": []}
    stub_server.last_auth = "unset"
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    client.search_artifacts("q")
    assert getattr(stub_server, "last_auth", "") == ""


# Spec: §6.2 / §6.3.2 — from_env reads the injected session token from
# PODIUM_SESSION_TOKEN so the SDK and MCP path resolve the same credential.
def test_from_env_reads_session_token(monkeypatch):
    monkeypatch.setenv("PODIUM_REGISTRY", "http://127.0.0.1:9999")
    monkeypatch.setenv("PODIUM_SESSION_TOKEN", "env-tok")
    client = Client.from_env()
    assert client.token == "env-tok"


# Spec: §7.4 — "podium sync and the SDKs apply the same cache modes."
# offline-only "never contact the registry": every meta-tool call raises the
# structured network.offline_cache_miss error before a request is issued. The
# registry points at the stub, but no request reaches it (F-7.4.3).
def test_offline_only_never_contacts_registry(stub_server):
    stub_server.last_path = "untouched"
    client = Client(
        registry=f"http://127.0.0.1:{stub_server.server_port}",
        cache_mode="offline-only",
    )
    with pytest.raises(RegistryError) as exc:
        client.search_artifacts("variance")
    assert exc.value.code == "network.offline_cache_miss"
    # No request was sent: the stub's recorded path is unchanged.
    assert stub_server.last_path == "untouched"


# Spec: §7.4 — offline-first and always-revalidate keep no persistent cache in
# the SDK, so both fetch on every call. offline-first must reach the registry.
def test_offline_first_still_fetches(stub_server):
    stub_server.next_response = {"query": "q", "total_matched": 0, "results": []}
    stub_server.last_path = "untouched"
    client = Client(
        registry=f"http://127.0.0.1:{stub_server.server_port}",
        cache_mode="offline-first",
    )
    client.search_artifacts("q")
    assert "search_artifacts" in stub_server.last_path


# Spec: §7.4 — offline-only also gates the batch-load and subscribe paths,
# which do not route through _get.
def test_offline_only_gates_batch_load(stub_server):
    client = Client(
        registry=f"http://127.0.0.1:{stub_server.server_port}",
        cache_mode="offline-only",
    )
    with pytest.raises(RegistryError) as exc:
        client.load_artifacts(["finance/run"])
    assert exc.value.code == "network.offline_cache_miss"


# Spec: §6.2 / §7.4 — from_env reads PODIUM_CACHE_MODE; an unset value defaults
# to always-revalidate and an unrecognized value is rejected.
def test_from_env_reads_cache_mode(monkeypatch):
    monkeypatch.setenv("PODIUM_REGISTRY", "http://127.0.0.1:9999")
    monkeypatch.setenv("PODIUM_CACHE_MODE", "offline-only")
    assert Client.from_env().cache_mode == "offline-only"
    monkeypatch.delenv("PODIUM_CACHE_MODE")
    assert Client.from_env().cache_mode == "always-revalidate"


def test_rejects_unknown_cache_mode():
    with pytest.raises(ValueError):
        Client(registry="http://127.0.0.1:9999", cache_mode="bogus")
