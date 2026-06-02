"""SDK registry resolution, overlay merge, and login (F-14.4.*, F-14.8.*)."""

from __future__ import annotations

import http.server
import json
import os
import socket
import threading

import pytest

from podium import Client, DeviceCodeError, RegistryError
from podium import _config, _overlay


# ---------------------------------------------------------------------------
# F-14.4.1 / F-14.4.3 — from_env resolves the registry from sync.yaml scopes
# and reports config.no_registry when unset everywhere (spec §7.5.2, §13.10).
# ---------------------------------------------------------------------------


def _write(path: str, body: str) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(body)


def test_resolve_registry_env_wins(tmp_path):
    # spec §7.5.2 — PODIUM_REGISTRY beats every sync.yaml scope.
    ws = tmp_path / "ws"
    _write(str(ws / ".podium" / "sync.yaml"), "defaults:\n  registry: https://from-file\n")
    got = _config.resolve_registry("https://from-env", str(ws), str(tmp_path / "home"))
    assert got == "https://from-env"


def test_resolve_registry_scope_precedence(tmp_path):
    # spec §7.5.2 — project-local > project-shared > user-global.
    home = tmp_path / "home"
    ws = tmp_path / "home" / "proj"
    _write(str(home / ".podium" / "sync.yaml"), "defaults:\n  registry: https://global\n")
    _write(str(ws / ".podium" / "sync.yaml"), "defaults:\n  registry: https://shared\n")
    _write(str(ws / ".podium" / "sync.local.yaml"), "defaults:\n  registry: https://local\n")
    assert _config.resolve_registry(None, str(ws), str(home)) == "https://local"

    os.remove(str(ws / ".podium" / "sync.local.yaml"))
    assert _config.resolve_registry(None, str(ws), str(home)) == "https://shared"

    os.remove(str(ws / ".podium" / "sync.yaml"))
    # The empty .podium/ still marks the workspace; falls through to global.
    assert _config.resolve_registry(None, str(ws), str(home)) == "https://global"


def test_resolve_registry_ignores_inline_comment(tmp_path):
    ws = tmp_path / "ws"
    _write(
        str(ws / ".podium" / "sync.yaml"),
        "defaults:\n  registry: https://podium.acme.com   # the prod registry\n  harness: claude-code\n",
    )
    assert _config.resolve_registry(None, str(ws), None) == "https://podium.acme.com"


def test_from_env_reads_sync_yaml(tmp_path, monkeypatch):
    # spec §14.4 — from_env "picks up registry URL from sync.yaml" with no
    # PODIUM_REGISTRY exported.
    home = tmp_path / "home"
    ws = tmp_path / "home" / "proj"
    _write(str(ws / ".podium" / "sync.yaml"), "defaults:\n  registry: http://127.0.0.1:8080\n")
    monkeypatch.delenv("PODIUM_REGISTRY", raising=False)
    monkeypatch.delenv("PODIUM_OVERLAY_PATH", raising=False)
    monkeypatch.setenv("HOME", str(home))
    monkeypatch.chdir(ws)
    client = Client.from_env()
    assert client.registry == "http://127.0.0.1:8080"


def test_from_env_no_registry_raises_config_no_registry(tmp_path, monkeypatch):
    # spec §6.10 / §7.5.2 — unset across all scopes is config.no_registry.
    monkeypatch.delenv("PODIUM_REGISTRY", raising=False)
    monkeypatch.setenv("HOME", str(tmp_path / "empty-home"))
    monkeypatch.chdir(tmp_path)
    with pytest.raises(RegistryError) as exc:
        Client.from_env()
    assert exc.value.code == "config.no_registry"
    assert "podium init" in exc.value.message


# ---------------------------------------------------------------------------
# F-14.4.2 — workspace overlay merge in search_artifacts / load_artifact
# (spec §6.4, §6.4.1).
# ---------------------------------------------------------------------------


def _overlay_artifact(root, art_id: str, *, type_="prompt", desc="", tags=None, body="body"):
    pkg = os.path.join(root, *art_id.split("/"))
    os.makedirs(pkg, exist_ok=True)
    tag_line = f"tags: [{', '.join(tags)}]\n" if tags else ""
    fm = f"---\ntype: {type_}\nversion: 0.1.0\ndescription: {desc}\n{tag_line}---\n{body}\n"
    with open(os.path.join(pkg, "ARTIFACT.md"), "w", encoding="utf-8") as fh:
        fh.write(fm)


class _ArtifactsHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):  # noqa: D401
        pass

    def do_GET(self):  # noqa: N802
        body = json.dumps(self.server.next_response).encode()  # type: ignore[attr-defined]
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


@pytest.fixture()
def artifacts_server():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    server = http.server.HTTPServer(("127.0.0.1", port), _ArtifactsHandler)
    server.next_response = {}
    t = threading.Thread(target=server.serve_forever, daemon=True)
    t.start()
    yield server
    server.shutdown()


def test_search_artifacts_fuses_overlay(artifacts_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_artifact(str(overlay), "drafts/routing-helper", desc="validate routing numbers")
    artifacts_server.next_response = {
        "query": "routing",
        "total_matched": 1,
        "results": [{"id": "shared/legacy-router", "type": "prompt", "description": "old router"}],
    }
    base = f"http://127.0.0.1:{artifacts_server.server_address[1]}"
    client = Client(registry=base, overlay_path=str(overlay))
    res = client.search_artifacts("routing")
    ids = [r.id for r in res.results]
    assert "drafts/routing-helper" in ids  # overlay hit surfaces
    assert "shared/legacy-router" in ids  # registry hit retained
    assert res.total_matched == 2  # overlay-only id enlarges the count


# F-6.4.4 — the SDK overlay search honors the `scope` prefix filter so a
# scoped query excludes out-of-scope overlay artifacts (spec §6.4).
def test_overlay_search_scope_filters_out_of_scope(tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_artifact(str(overlay), "finance/budget", desc="budget helper")
    _overlay_artifact(str(overlay), "drafts/routing-helper", desc="routing helper")
    index = _overlay.LocalOverlay(str(overlay))

    in_scope = {a.id for a in index.search("helper", scope="finance")}
    assert in_scope == {"finance/budget"}

    # Empty query (browse mode) is scoped too.
    browse = {a.id for a in index.search("", scope="finance")}
    assert browse == {"finance/budget"}

    # No scope leaves both visible.
    all_ids = {a.id for a in index.search("helper")}
    assert all_ids == {"finance/budget", "drafts/routing-helper"}


def test_search_artifacts_scope_excludes_out_of_scope_overlay(artifacts_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_artifact(str(overlay), "finance/budget", desc="quarterly budget")
    _overlay_artifact(str(overlay), "drafts/routing-helper", desc="quarterly routing")
    artifacts_server.next_response = {"query": "quarterly", "total_matched": 0, "results": []}
    base = f"http://127.0.0.1:{artifacts_server.server_address[1]}"
    client = Client(registry=base, overlay_path=str(overlay))

    res = client.search_artifacts("quarterly", scope="finance")
    ids = [r.id for r in res.results]
    assert "finance/budget" in ids
    assert "drafts/routing-helper" not in ids  # out-of-scope overlay hit excluded


def test_search_artifacts_no_overlay_passthrough(artifacts_server, tmp_path, monkeypatch):
    monkeypatch.delenv("PODIUM_OVERLAY_PATH", raising=False)
    monkeypatch.chdir(tmp_path)  # no .podium/overlay/ here
    artifacts_server.next_response = {
        "total_matched": 1,
        "results": [{"id": "a/b", "type": "prompt"}],
    }
    base = f"http://127.0.0.1:{artifacts_server.server_address[1]}"
    client = Client(registry=base)
    res = client.search_artifacts("x")
    assert [r.id for r in res.results] == ["a/b"]


def test_load_artifact_resolves_overlay_first(tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_artifact(str(overlay), "drafts/my-prompt", desc="draft", body="overlay body")
    # Registry URL is unreachable; an overlay hit must not touch the network.
    client = Client(registry="http://127.0.0.1:1", overlay_path=str(overlay))
    art = client.load_artifact("drafts/my-prompt")
    assert art.id == "drafts/my-prompt"
    assert "overlay body" in art.manifest_body


def test_overlay_path_cwd_fallback(tmp_path, monkeypatch):
    # spec §6.4 step 3 — <CWD>/.podium/overlay/ fallback when no env/explicit.
    monkeypatch.delenv("PODIUM_OVERLAY_PATH", raising=False)
    monkeypatch.chdir(tmp_path)
    os.makedirs(tmp_path / ".podium" / "overlay")
    client = Client(registry="http://127.0.0.1:1")
    assert client.overlay_path == os.path.join(str(tmp_path), ".podium", "overlay")


def test_rrf_fuse_orders_by_reciprocal_rank():
    fused = _overlay.rrf_fuse([["a", "b"], ["b", "c"]])
    # b appears in both lists, so it outranks a and c.
    assert max(fused, key=fused.get) == "b"


# ---------------------------------------------------------------------------
# F-14.8.1 / F-14.8.2 — Client.login() runs the device-code flow and the
# resulting token authenticates requests (spec §6.3, §7.7, §14.8).
# ---------------------------------------------------------------------------


class _OAuthHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, obj, status=200):
        body = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # noqa: N802
        if self.path == "/.well-known/oauth-authorization-server":
            base = f"http://127.0.0.1:{self.server.server_address[1]}"
            self._send(
                {
                    "device_authorization_endpoint": base + "/device",
                    "token_endpoint": base + "/token",
                }
            )
            return
        # Authenticated catalog call: echo whether a Bearer arrived.
        auth = self.headers.get("Authorization", "")
        self.server.last_auth = auth  # type: ignore[attr-defined]
        self._send({"total_matched": 0, "results": []})

    def do_POST(self):  # noqa: N802
        if self.path == "/device":
            self._send(
                {
                    "device_code": "dev-123",
                    "user_code": "WXYZ-1234",
                    "verification_uri": "https://idp.example.com/activate",
                    "interval": 0,
                    "expires_in": 600,
                }
            )
            return
        if self.path == "/token":
            self.server.token_polls += 1  # type: ignore[attr-defined]
            if self.server.always_pending:  # type: ignore[attr-defined]
                self._send({"error": "authorization_pending"}, status=400)
            elif self.server.token_polls < 2:  # type: ignore[attr-defined]
                self._send({"error": "authorization_pending"}, status=400)
            else:
                self._send({"access_token": "tok-abc", "id_token": "", "token_type": "Bearer"})
            return
        self._send({"error": "not_found"}, status=404)


@pytest.fixture()
def oauth_server():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    server = http.server.HTTPServer(("127.0.0.1", port), _OAuthHandler)
    server.token_polls = 0
    server.always_pending = False
    server.last_auth = ""
    t = threading.Thread(target=server.serve_forever, daemon=True)
    t.start()
    yield server
    server.shutdown()


def test_login_runs_device_flow_and_authenticates(oauth_server):
    base = f"http://127.0.0.1:{oauth_server.server_address[1]}"
    client = Client(registry=base)
    tokens = client.login(timeout=10.0)
    assert tokens.access_token == "tok-abc"
    assert client.token == "tok-abc"
    # F-14.8.2 — the token authenticates subsequent catalog calls.
    client.search_artifacts("anything")
    assert oauth_server.last_auth == "Bearer tok-abc"


def test_login_times_out_when_always_pending(oauth_server):
    # spec §7.7 — polling is bounded; an IdP that never completes must not
    # block forever.
    oauth_server.always_pending = True
    base = f"http://127.0.0.1:{oauth_server.server_address[1]}"
    client = Client(registry=base)
    with pytest.raises(DeviceCodeError):
        client.login(timeout=0.5)
