"""Tests for the Podium Python SDK."""

from __future__ import annotations

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


# Spec: §4.7.6 — dependents_of returns artifacts that depend on the
# given id, surfaced as ArtifactDescriptor instances.
def test_dependents_of_decodes_envelope(stub_server):
    stub_server.next_response = {
        "dependents": [
            {"id": "finance/run", "type": "skill", "version": "1.0.0",
             "description": "Variance"},
        ],
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    deps = client.dependents_of("finance/glossary")
    assert "/v1/dependents" in stub_server.last_path
    assert len(deps) == 1
    assert deps[0].id == "finance/run"


# Spec: §6.4 — preview_scope hits /v1/scope/preview with the
# constraints; the SDK passes the response through unchanged so
# callers can inspect the full envelope.
def test_preview_scope_passes_constraints(stub_server):
    stub_server.next_response = {
        "scope": "finance/",
        "matched": 12,
        "results": [],
    }
    client = Client(registry=f"http://127.0.0.1:{stub_server.server_port}")
    out = client.preview_scope(scope="finance/", type="skill", tags=["q4"])
    assert "/v1/scope/preview" in stub_server.last_path
    assert "scope=finance" in stub_server.last_path
    assert "tags=q4" in stub_server.last_path
    assert out["matched"] == 12


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


# Spec: §2.2 — materialize on an empty destination is rejected.
def test_materialize_empty_destination(tmp_path):
    art = LoadedArtifact(id="a", type="context", version="1", manifest_body="", frontmatter="x")
    with pytest.raises(MaterializeError):
        art.materialize("")
