"""SDK registry resolution, overlay merge, and login."""

from __future__ import annotations

import http.server
import json
import os
import socket
import threading
import time
import urllib.error

import pytest

from podium import Client, DeviceCodeError, RegistryError
from podium import _config, _overlay


# ---------------------------------------------------------------------------
# from_env resolves the registry from sync.yaml scopes
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
# workspace overlay merge in search_artifacts / load_artifact
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


# the SDK overlay search honors the `scope` prefix filter so a
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
    # Spec: §4.7.10 — no registry served an overlay record, so it carries no
    # delivery attestation.
    assert (art.delivery_hash, art.delivery_signature) == ("", "")


# Spec: §4.4, §6.4 — the overlay carries the registry-side layers' format, so
# the SDK loader's resource set is every file under the package root,
# dot-prefixed names included, other than the root ARTIFACT.md, a skill's root
# SKILL.md, and the files of a nested package; and discovery skips a
# dot-prefixed directory below the overlay root. The overlay root itself sits
# under .podium/, so the root exemption is exercised here too.
def test_overlay_resource_set_matches_the_registry_walk(tmp_path):
    overlay = tmp_path / ".podium" / "overlay"
    _write(str(overlay / "outer" / "ARTIFACT.md"), "---\ntype: skill\nversion: 0.1.0\n---\nouter\n")
    _write(str(overlay / "outer" / "SKILL.md"), "outer skill body\n")
    _write(str(overlay / "outer" / "notes.md"), "line one\r\nline two\r\n")
    _write(str(overlay / "outer" / ".hidden-note"), "hidden note body\n")
    _write(str(overlay / "outer" / ".tooling" / "config.json"), '{"tool":"config"}\n')
    _write(str(overlay / "outer" / "references" / "SKILL.md"), "reference skill body\n")
    _write(
        str(overlay / "outer" / "inner" / "ARTIFACT.md"),
        "---\ntype: context\nversion: 0.1.0\n---\ninner\n",
    )
    _write(str(overlay / "outer" / "inner" / "data.txt"), "inner data\n")
    _write(
        str(overlay / "outer" / ".nested" / "ARTIFACT.md"),
        "---\ntype: context\nversion: 0.1.0\n---\nnested\n",
    )
    _write(str(overlay / "outer" / ".nested" / "note.txt"), "nested note\n")

    index = _overlay.LocalOverlay(str(overlay))

    assert set(index.artifacts) == {"outer", "outer/inner"}
    assert set(index.artifacts["outer"].resources) == {
        "notes.md",
        ".hidden-note",
        ".tooling/config.json",
        "references/SKILL.md",
    }
    assert set(index.artifacts["outer/inner"].resources) == {"data.txt"}


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
# The device-code login pair (start_login / finish_login) and login(), which
# composes it, against a stub IdP (spec §6.3, §7.7).
# ---------------------------------------------------------------------------

_DEVICE_CODE = "dev-123"
_USER_CODE = "WXYZ-1234"
_VERIFICATION_URI = "https://idp.example.com/activate"
_COMPLETE_URI = "https://idp.example.com/activate?code=WXYZ-1234"
_ACCESS_TOKEN = "tok-abc"
_REFRESH_TOKEN = "ref-xyz"

# Scripted /token replies by name. "ok" is the only success.
_TOKEN_REPLIES: dict[str, tuple[int, dict[str, str]]] = {
    "pending": (400, {"error": "authorization_pending"}),
    "slow_down": (400, {"error": "slow_down"}),
    "expired_token": (400, {"error": "expired_token"}),
    "access_denied": (400, {"error": "access_denied"}),
    "unknown": (400, {"error": "server_meltdown"}),
    "ok": (
        200,
        {
            "access_token": _ACCESS_TOKEN,
            "refresh_token": _REFRESH_TOKEN,
            "id_token": "",
            "token_type": "Bearer",
        },
    ),
}


class _OAuthHandler(http.server.BaseHTTPRequestHandler):
    """Stub IdP and registry whose replies a test scripts on the server.

    ``discovery_mode`` is ``ok``, ``404``, or ``no_device``. ``device_mode`` is
    ``ok``, ``404`` (non-JSON body), ``non_json`` (200 with a non-JSON body),
    or ``no_device_code``. ``token_script`` lists the /token replies in order,
    and its last entry repeats once the list is exhausted.
    """

    def log_message(self, *a):
        pass

    def _send_raw(self, body: bytes, status: int, content_type: str) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send(self, obj, status=200):
        self._send_raw(json.dumps(obj).encode(), status, "application/json")

    def _record(self, body: str) -> None:
        auth = self.headers.get("Authorization", "")
        self.server.requests.append(f"{self.command} {self.path} {auth} {body}")  # type: ignore[attr-defined]

    def do_GET(self):  # noqa: N802
        self._record("")
        srv = self.server
        if self.path == "/.well-known/oauth-authorization-server":
            if srv.discovery_mode == "404":  # type: ignore[attr-defined]
                self._send_raw(b"not found", 404, "text/plain")
                return
            base = f"http://127.0.0.1:{srv.server_address[1]}"
            meta = {"token_endpoint": base + "/token"}
            if srv.discovery_mode != "no_device":  # type: ignore[attr-defined]
                meta["device_authorization_endpoint"] = base + "/device"
            self._send(meta)
            return
        # Authenticated catalog call: record whether a Bearer arrived.
        srv.last_auth = self.headers.get("Authorization", "")  # type: ignore[attr-defined]
        self._send({"total_matched": 0, "results": []})

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length", "0"))
        self._record(self.rfile.read(length).decode())
        if self.path == "/device":
            self._device()
            return
        if self.path in ("/token", "/oauth2/token"):
            self._token()
            return
        self._send({"error": "not_found"}, status=404)

    def _device(self) -> None:
        srv = self.server
        mode = srv.device_mode  # type: ignore[attr-defined]
        if mode == "404":
            self._send_raw(b"no such endpoint", 404, "text/plain")
            return
        if mode == "non_json":
            self._send_raw(b"<html>ok</html>", 200, "text/html")
            return
        body = {
            "user_code": _USER_CODE,
            "verification_uri": _VERIFICATION_URI,
            "interval": srv.interval,  # type: ignore[attr-defined]
            "expires_in": srv.expires_in,  # type: ignore[attr-defined]
        }
        if mode != "no_device_code":
            body["device_code"] = _DEVICE_CODE
        if srv.complete_uri:  # type: ignore[attr-defined]
            body["verification_uri_complete"] = srv.complete_uri  # type: ignore[attr-defined]
        self._send(body)

    def _token(self) -> None:
        srv = self.server
        script = srv.token_script  # type: ignore[attr-defined]
        name = script[min(srv.token_polls, len(script) - 1)]  # type: ignore[attr-defined]
        srv.token_polls += 1  # type: ignore[attr-defined]
        status, body = _TOKEN_REPLIES[name]
        self._send(body, status=status)


@pytest.fixture()
def no_oauth_env(monkeypatch):
    # An operator shell's PODIUM_OAUTH_* must not redirect the stub flow to a
    # real IdP.
    for name in (
        "PODIUM_OAUTH_CLIENT_ID",
        "PODIUM_OAUTH_AUDIENCE",
        "PODIUM_OAUTH_AUTHORIZATION_ENDPOINT",
        "PODIUM_OAUTH_TOKEN_URL",
    ):
        monkeypatch.delenv(name, raising=False)


def _free_port() -> int:
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


@pytest.fixture()
def oauth_server(no_oauth_env):
    # Requesting no_oauth_env here applies it to every device-flow test.
    server = http.server.HTTPServer(("127.0.0.1", _free_port()), _OAuthHandler)
    server.token_polls = 0
    server.token_script = ["pending", "ok"]
    server.discovery_mode = "ok"
    server.device_mode = "ok"
    server.complete_uri = ""
    server.interval = 0
    server.expires_in = 600
    server.last_auth = ""
    server.requests = []
    t = threading.Thread(target=server.serve_forever, daemon=True)
    t.start()
    yield server
    server.shutdown()


def _base(server) -> str:
    return f"http://127.0.0.1:{server.server_address[1]}"


class _FakeClock:
    """A mutable clock with a sleep that advances it and records each wait.

    ``step`` fixes how far each wait advances the clock, which keeps a loop
    that the fixture's interval of 0 would otherwise spin in place moving.
    ``on_wait`` runs after each recorded wait with the wait count.
    """

    def __init__(self, step: float | None = None, on_wait=None) -> None:
        self.now = 1000.0
        self.waits: list[float] = []
        self._step = step
        self._on_wait = on_wait

    def __call__(self) -> float:
        return self.now

    def sleep(self, seconds: float) -> None:
        self.waits.append(seconds)
        self.now += seconds if self._step is None else self._step
        if self._on_wait is not None:
            self._on_wait(len(self.waits))


def _start(server, clock: _FakeClock | None = None):
    client = Client(registry=_base(server))
    pending = client.start_login(clock=clock) if clock else client.start_login()
    return client, pending


# Spec: §6.3
def test_start_login_returns_handle_silently(oauth_server, capsys):
    oauth_server.complete_uri = _COMPLETE_URI
    _, pending = _start(oauth_server)
    assert pending.verification_uri == _VERIFICATION_URI
    assert pending.verification_uri_complete == _COMPLETE_URI
    assert pending.user_code == _USER_CODE
    assert pending.expires_in == 600
    assert pending.interval == 0
    assert capsys.readouterr().err == ""
    assert oauth_server.token_polls == 0
    assert "device_code" not in repr(pending)
    assert _DEVICE_CODE not in repr(pending)


# Spec: §6.3
def test_start_login_complete_uri_absent_is_none(oauth_server):
    _, pending = _start(oauth_server)
    assert pending.verification_uri_complete is None


# Spec: §6.3
def test_finish_login_installs_token(oauth_server):
    client, pending = _start(oauth_server)
    tokens = client.finish_login(pending, timeout=10.0)
    assert tokens.access_token == _ACCESS_TOKEN
    assert client.token == tokens.access_token
    assert tokens.refresh_token == _REFRESH_TOKEN
    client.search_artifacts("anything")
    assert oauth_server.last_auth == f"Bearer {_ACCESS_TOKEN}"
    # The SDK neither refreshes nor otherwise sends the refresh token.
    assert not any(_REFRESH_TOKEN in r for r in oauth_server.requests)


# Spec: §6.3
def test_finish_login_slow_down_grows_interval(oauth_server):
    oauth_server.token_script = ["slow_down", "ok"]
    clock = _FakeClock()
    client, pending = _start(oauth_server, clock)
    client.finish_login(pending, sleep=clock.sleep)
    assert clock.waits == [0, 5]


# Spec: §6.3
def test_finish_login_denied(oauth_server):
    oauth_server.token_script = ["access_denied"]
    client, pending = _start(oauth_server)
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending)
    assert err.value.reason == "denied"
    assert client.token == ""


# Spec: §6.3
def test_finish_login_idp_expired_token(oauth_server):
    oauth_server.token_script = ["expired_token"]
    client, pending = _start(oauth_server)
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending)
    assert err.value.reason == "expired"


# Spec: §6.3
def test_finish_login_after_local_expiry_sends_nothing(oauth_server):
    clock = _FakeClock()
    client, pending = _start(oauth_server, clock)
    clock.now += pending.expires_in + 1
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending, sleep=clock.sleep)
    assert err.value.reason == "expired"
    assert oauth_server.token_polls == 0


# Spec: §6.3
def test_finish_login_expires_in_loop_before_timeout(oauth_server):
    oauth_server.expires_in = 30
    oauth_server.token_script = ["pending"]
    clock = _FakeClock(step=10)
    client, pending = _start(oauth_server, clock)
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending, timeout=600, sleep=clock.sleep)
    assert err.value.reason == "expired"
    assert oauth_server.token_polls >= 1


# Spec: §6.3
def test_finish_login_timeout_runs_from_finish_call(oauth_server):
    oauth_server.token_script = ["pending"]
    clock = _FakeClock(step=10)
    client, pending = _start(oauth_server, clock)
    clock.now += 100
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending, timeout=50, sleep=clock.sleep)
    # A timeout measured from the start call would end before any request.
    assert oauth_server.token_polls >= 1
    assert err.value.reason == "timeout"


# Spec: §6.3
def test_finish_login_cancel_before_call(oauth_server):
    client, pending = _start(oauth_server)
    cancel = threading.Event()
    cancel.set()
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending, cancel=cancel)
    assert err.value.reason == "cancelled"
    assert oauth_server.token_polls == 0
    with pytest.raises(DeviceCodeError) as again:
        client.finish_login(pending)
    assert again.value.reason == "consumed"


# Spec: §6.3
def test_finish_login_cancel_between_polls(oauth_server):
    oauth_server.token_script = ["pending"]
    cancel = threading.Event()

    def on_wait(count: int) -> None:
        if count == 2:
            cancel.set()

    clock = _FakeClock(step=1, on_wait=on_wait)
    client, pending = _start(oauth_server, clock)
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending, cancel=cancel, sleep=clock.sleep)
    assert err.value.reason == "cancelled"
    assert oauth_server.token_polls == 1


# Spec: §6.3
def test_finish_login_cancel_wait_real_thread(oauth_server):
    # No injected sleep: the wait is the cancel event's own wait, which
    # returns when another thread sets it.
    oauth_server.interval = 1
    oauth_server.token_script = ["pending"]
    client, pending = _start(oauth_server)
    cancel = threading.Event()
    timer = threading.Timer(0.2, cancel.set)
    timer.start()
    try:
        with pytest.raises(DeviceCodeError) as err:
            client.finish_login(pending, timeout=30, cancel=cancel)
    finally:
        timer.cancel()
    assert err.value.reason == "cancelled"


# Spec: §6.3
def test_finish_login_concurrent_single_winner(oauth_server):
    oauth_server.token_script = ["ok"]
    client, pending = _start(oauth_server)
    barrier = threading.Barrier(2)
    results: list[object] = []
    lock = threading.Lock()

    def finish() -> None:
        barrier.wait()
        try:
            outcome: object = client.finish_login(pending, timeout=10.0)
        except DeviceCodeError as exc:
            outcome = exc
        with lock:
            results.append(outcome)

    threads = [threading.Thread(target=finish) for _ in range(2)]
    for t in threads:
        t.start()
    for t in threads:
        t.join(timeout=10)
    errors = [r for r in results if isinstance(r, DeviceCodeError)]
    tokens = [r for r in results if not isinstance(r, DeviceCodeError)]
    assert len(tokens) == 1
    assert [e.reason for e in errors] == ["consumed"]
    assert oauth_server.token_polls == 1


# Spec: §6.3
def test_finish_login_reuse_after_failure(oauth_server):
    oauth_server.token_script = ["access_denied"]
    client, pending = _start(oauth_server)
    with pytest.raises(DeviceCodeError):
        client.finish_login(pending)
    polls = oauth_server.token_polls
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending)
    assert err.value.reason == "consumed"
    assert oauth_server.token_polls == polls


@pytest.mark.parametrize(
    ("discovery_mode", "device_mode", "http_cause"),
    [
        ("404", "ok", True),
        ("no_device", "ok", False),
        ("ok", "404", True),
        ("ok", "no_device_code", False),
    ],
)
# Spec: §6.3
def test_start_login_failed_reasons(oauth_server, discovery_mode, device_mode, http_cause):
    oauth_server.discovery_mode = discovery_mode
    oauth_server.device_mode = device_mode
    client = Client(registry=_base(oauth_server))
    with pytest.raises(DeviceCodeError) as err:
        client.start_login()
    assert err.value.reason == "failed"
    if http_cause:
        assert isinstance(err.value.__cause__, urllib.error.HTTPError)


# Spec: §6.3
def test_finish_login_unknown_error_is_failed(oauth_server):
    oauth_server.token_script = ["unknown"]
    client, pending = _start(oauth_server)
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending)
    assert err.value.reason == "failed"


# Spec: §6.3
def test_login_composes_pair(oauth_server, monkeypatch, capsys):
    from podium import client as client_mod

    opened: list[str] = []
    monkeypatch.setattr(client_mod, "_open_browser", opened.append)
    client = Client(registry=_base(oauth_server))
    tokens = client.login(open_browser=True, timeout=10.0)
    assert tokens.access_token == _ACCESS_TOKEN
    assert client.token == _ACCESS_TOKEN
    client.search_artifacts("anything")
    assert oauth_server.last_auth == f"Bearer {_ACCESS_TOKEN}"
    err = capsys.readouterr().err
    assert f"Visit: {_VERIFICATION_URI}" in err
    assert f"User code: {_USER_CODE}" in err
    # The IdP omitted the complete URI, so no browser opens.
    assert opened == []


# Spec: §6.3
def test_login_opens_complete_uri_with_explicit_endpoints(oauth_server, monkeypatch):
    from podium import client as client_mod

    oauth_server.complete_uri = _COMPLETE_URI
    opened: list[str] = []
    monkeypatch.setattr(client_mod, "_open_browser", opened.append)
    base = _base(oauth_server)
    # The registry URL is unreachable, so the explicit endpoints must be used.
    client = Client(registry="http://127.0.0.1:1")
    tokens = client.login(
        open_browser=True,
        timeout=10.0,
        device_authorization_endpoint=base + "/device",
        token_endpoint=base + "/token",
    )
    assert tokens.access_token == _ACCESS_TOKEN
    assert opened == [_COMPLETE_URI]


# Spec: §6.3
def test_login_expired_before_timeout(oauth_server):
    oauth_server.expires_in = 0.2
    oauth_server.token_script = ["pending"]
    client = Client(registry=_base(oauth_server))
    with pytest.raises(DeviceCodeError) as err:
        client.login(timeout=10.0, sleep=lambda _s: time.sleep(0.05))
    assert err.value.reason == "expired"
    assert str(err.value) == "device code expired before the flow completed"


# Spec: §6.3 / §7.7 — polling is bounded; an IdP that never completes must not
# block forever.
def test_login_times_out_when_always_pending(oauth_server):
    oauth_server.token_script = ["pending"]
    client = Client(registry=_base(oauth_server))
    with pytest.raises(DeviceCodeError) as err:
        client.login(timeout=0.5, sleep=lambda _s: time.sleep(0.05))
    assert err.value.reason == "timeout"


# Spec: §6.3 / §7.7 — with no token endpoint configured or discovered, the
# registry's /oauth2/token is the fallback.
def test_start_login_token_endpoint_falls_back_to_registry(oauth_server):
    base = _base(oauth_server)
    client = Client(registry=base)
    pending = client.start_login(device_authorization_endpoint=base + "/device")
    client.finish_login(pending, timeout=10.0)
    assert any(r.startswith("POST /oauth2/token ") for r in oauth_server.requests)


# Spec: §6.3
def test_import_surface():
    import podium
    from podium import DeviceCodeError as _E, PendingLogin as _P, Tokens as _T

    assert (_E, _P, _T) == (podium.DeviceCodeError, podium.PendingLogin, podium.Tokens)
    assert not hasattr(podium, "DeviceCodeRequired")


# Spec: §6.3
def test_finish_login_unreachable_token_endpoint_is_failed(oauth_server):
    client = Client(registry=_base(oauth_server))
    pending = client.start_login(token_endpoint=f"http://127.0.0.1:{_free_port()}/token")
    with pytest.raises(DeviceCodeError) as err:
        client.finish_login(pending, timeout=10.0)
    assert err.value.reason == "failed"
    assert isinstance(err.value.__cause__, (urllib.error.URLError, OSError))


# Spec: §6.3
def test_start_login_device_non_json_200_is_failed(oauth_server):
    oauth_server.device_mode = "non_json"
    client = Client(registry=_base(oauth_server))
    with pytest.raises(DeviceCodeError) as err:
        client.start_login()
    assert err.value.reason == "failed"
    assert isinstance(err.value.__cause__, json.JSONDecodeError)
