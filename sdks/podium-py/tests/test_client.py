"""Tests for the Podium Python SDK.

Tests skip when the active phase (read from ../../.phase) is below 4.
This mirrors the Go-side RequirePhase guard in internal/testharness.
"""

from __future__ import annotations

import http.server
import json
import os
import pathlib
import socket
import threading

import pytest

from podium import Client, RegistryError


# Spec: phase tagging — defer until Phase 4 is active.
PHASE_REQUIRED = 4


def _active_phase() -> int:
    here = pathlib.Path(__file__).resolve()
    for parent in [here, *here.parents]:
        candidate = parent / ".phase"
        if candidate.exists():
            return int(candidate.read_text().strip())
    return 0


pytestmark = pytest.mark.skipif(
    _active_phase() < PHASE_REQUIRED,
    reason=f"requires phase {PHASE_REQUIRED} (active phase: {_active_phase()})",
)


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
# Phase: 4
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
# Phase: 4
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
# Phase: 4
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
# Phase: 4
def test_from_env_reads_registry(monkeypatch):
    monkeypatch.setenv("PODIUM_REGISTRY", "http://127.0.0.1:9999")
    monkeypatch.setenv("PODIUM_OVERLAY_PATH", "/tmp/overlay")
    client = Client.from_env()
    assert client.registry == "http://127.0.0.1:9999"
    assert client.overlay_path == "/tmp/overlay"
