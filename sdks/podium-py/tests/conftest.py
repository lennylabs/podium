"""Shared fixtures for the Python SDK suite.

The stub registry and the stub object store live here, together with
``served()``, which completes a stub ``load_artifact`` response or batch entry
with the delivery hash the registry would compute (§4.7.10) and, on request, a
registry-managed envelope signed with the vector seed.
"""

from __future__ import annotations

import base64
import copy
import hashlib
import http.server
import json
import pathlib
import threading
from typing import Any

import pytest

from podium import _delivery

VECTORS_PATH = pathlib.Path(__file__).parents[3] / "test" / "vectors" / "delivery-record.json"
VECTORS = json.loads(VECTORS_PATH.read_text(encoding="utf-8"))

# The fixed test seed every vector signature is made with, and its public key
# in the PODIUM_SIGNATURE_VERIFY_KEY syntax.
VECTOR_SEED = bytes.fromhex(VECTORS["signing_seed_hex"])
VECTOR_PUBLIC_KEY = VECTORS["public_key"]

# A second seed whose key no test configures.
UNRELATED_SEED = hashlib.sha256(b"podium unrelated test key").digest()

_VERIFY_VARIABLES = (
    "PODIUM_SIGNATURE_VERIFY_KEY",
    "PODIUM_SIGN_KEY_PATH",
    "PODIUM_VERIFY_SIGNATURES",
)


@pytest.fixture(autouse=True)
def _isolate_verification(monkeypatch, tmp_path_factory):
    """Keep the developer's key file, sync.yaml, and signing variables out.

    Under the SDK default a key file at ~/.podium/standalone or a set
    PODIUM_SIGNATURE_VERIFY_KEY selects the always policy, and a home or
    workspace sync.yaml can set defaults.verify_signatures, so every test runs
    with an empty HOME, a working directory outside any workspace, and the
    signing variables unset.
    """
    for name in _VERIFY_VARIABLES:
        monkeypatch.delenv(name, raising=False)
    monkeypatch.setenv("HOME", str(tmp_path_factory.mktemp("home")))
    monkeypatch.chdir(tmp_path_factory.mktemp("cwd"))


def digest(body: bytes) -> str:
    return "sha256:" + hashlib.sha256(body).hexdigest()


def sign_envelope(seed: bytes, delivery_hash: str) -> str:
    """Return the registry-managed envelope ``seed``'s key makes over a hash."""
    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

    key = Ed25519PrivateKey.from_private_bytes(seed)
    public = key.public_key().public_bytes(
        serialization.Encoding.Raw, serialization.PublicFormat.Raw
    )
    sig = key.sign(bytes.fromhex(delivery_hash.split(":", 1)[1]))
    return json.dumps(
        {"key_id": _delivery.key_id_of(public), "signature": base64.b64encode(sig).decode()},
        separators=(",", ":"),
    )


def public_key_of(seed: bytes) -> str:
    """Return the base64 public key of ``seed`` in the key-list syntax."""
    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

    public = Ed25519PrivateKey.from_private_bytes(seed).public_key().public_bytes(
        serialization.Encoding.Raw, serialization.PublicFormat.Raw
    )
    return base64.b64encode(public).decode()


def _record_of(body: dict[str, Any]) -> dict[str, Any]:
    return {name: body.get(name, "") for name in _delivery._RECORD_FIELDS}


def _fill_link(link: dict[str, Any], fetched: dict[str, bytes]) -> str:
    if "content_hash" not in link:
        link["content_hash"] = digest(fetched[link["presigned_url"]])
    return link["content_hash"]


def _load_record(out: dict[str, Any], fetched: dict[str, bytes]) -> dict[str, Any]:
    rec = _record_of(out)
    resources: dict[str, str] = {}
    for path, value in (out.get("resources") or {}).items():
        raw = base64.b64decode(value) if out.get("resources_base64") else value.encode()
        resources[path] = digest(raw)
    for path, link in (out.get("large_resources") or {}).items():
        resources[path] = _fill_link(link, fetched)
    rec["resources"] = resources
    link = out.get("manifest_body_url")
    if link:
        doc = fetched[link["presigned_url"]]
        _fill_link(link, fetched)
        name = "skill_raw" if rec["type"] == "skill" else "frontmatter"
        rec[name] = doc
        rec["manifest_body"] = _delivery.manifest_body_of(doc)
    return rec


def _entry_record(out: dict[str, Any], fetched: dict[str, bytes]) -> dict[str, Any]:
    rec = _record_of(out)
    resources: dict[str, str] = {}
    for ref in out.get("resources") or []:
        if "content_hash" not in ref:
            if ref.get("presigned_url"):
                ref["content_hash"] = digest(fetched[ref["presigned_url"]])
            else:
                inline = ref.get("inline", "")
                raw = base64.b64decode(inline) if ref.get("inline_base64") else inline.encode()
                ref["content_hash"] = digest(raw)
        resources[ref["path"]] = ref["content_hash"]
    rec["resources"] = resources
    return rec


def served(
    body: dict[str, Any],
    *,
    fetched: dict[str, bytes] | None = None,
    sign_with: bytes | None = None,
) -> dict[str, Any]:
    """Complete a stub response with its delivery hash and link hashes.

    ``body`` is a ``load_artifact`` response, or a batch entry when it carries
    ``status``. A link or reference without a ``content_hash`` gets the digest
    of its body: the bytes ``fetched`` serves at its URL, or its inline value.
    A ``manifest_body_url`` document is taken from ``fetched`` and framed as
    §4.7.10 frames it. ``sign_with`` signs the delivery hash with that seed.
    """
    out = copy.deepcopy(body)
    fetched = fetched or {}
    rec = _entry_record(out, fetched) if "status" in out else _load_record(out, fetched)
    out["delivery_hash"] = _delivery.delivery_hash(rec)
    if sign_with is not None:
        out["delivery_signature"] = sign_envelope(sign_with, out["delivery_hash"])
    return out


class _StubHandler(http.server.BaseHTTPRequestHandler):
    """A stub registry.

    ``routes`` maps a request path, without its query, to ``(status, body)``;
    a request path it does not name gets ``next_status`` and
    ``next_response``. A ``bytes`` body is sent as is and any other body as
    JSON. Every request path is appended to ``requests``.
    """

    def log_message(self, format, *args):  # noqa: A002 - signature inherited
        pass

    def _reply(self) -> None:
        server = self.server
        server.last_path = self.path  # type: ignore[attr-defined]
        server.requests.append(self.path)  # type: ignore[attr-defined]
        route = server.routes.get(self.path.split("?", 1)[0])  # type: ignore[attr-defined]
        status, payload = route or (server.next_status, server.next_response)  # type: ignore[attr-defined]
        if callable(payload):
            payload = payload()
        body = payload if isinstance(payload, bytes) else json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # noqa: N802 - signature inherited
        self.server.last_auth = self.headers.get("Authorization", "")  # type: ignore[attr-defined]
        self._reply()

    def do_POST(self):  # noqa: N802 - signature inherited
        length = int(self.headers.get("Content-Length", "0"))
        self.server.last_body = self.rfile.read(length)  # type: ignore[attr-defined]
        self.server.bodies.append(self.server.last_body)  # type: ignore[attr-defined]
        self._reply()


def _start(handler) -> http.server.HTTPServer:
    server = http.server.HTTPServer(("127.0.0.1", 0), handler)
    # A short poll keeps shutdown() from waiting the default half second.
    thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
    thread.start()
    server.thread = thread  # type: ignore[attr-defined]
    return server


def _stop(server: http.server.HTTPServer) -> None:
    server.shutdown()
    server.thread.join()  # type: ignore[attr-defined]
    server.server_close()


@pytest.fixture()
def stub_server():
    server = _start(_StubHandler)
    server.next_status = 200
    server.next_response = {}
    server.routes = {}
    server.requests = []
    server.bodies = []
    server.last_path = ""
    server.last_auth = ""
    yield server
    _stop(server)


class _ObjectHandler(http.server.BaseHTTPRequestHandler):
    """A stub object route.

    It records each request's path and Authorization header and serves
    ``objects[path]``, or ``doc`` for a path ``objects`` does not name. When
    ``required_auth`` is set it answers 404 to a request without it, as the
    filesystem backend's /objects route does for a caller it cannot see.
    """

    def log_message(self, format, *args):  # noqa: A002 - signature inherited
        pass

    def do_GET(self):  # noqa: N802 - signature inherited
        auth = self.headers.get("Authorization")
        self.server.auths.append(auth)  # type: ignore[attr-defined]
        self.server.paths.append(self.path)  # type: ignore[attr-defined]
        required = self.server.required_auth  # type: ignore[attr-defined]
        if required and auth != required:
            self.send_response(404)
            self.end_headers()
            return
        body = self.server.objects.get(self.path, self.server.doc)  # type: ignore[attr-defined]
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def _object_server():
    server = _start(_ObjectHandler)
    server.auths = []
    server.paths = []
    server.objects = {}
    server.required_auth = ""
    server.doc = b"---\ntype: context\n---\n\nThe big body.\n"
    server.base = f"http://127.0.0.1:{server.server_port}"
    return server


@pytest.fixture()
def object_server():
    server = _object_server()
    yield server
    _stop(server)


@pytest.fixture()
def decoy_server():
    """A second object store that no verified fetch may reach."""
    server = _object_server()
    yield server
    _stop(server)
