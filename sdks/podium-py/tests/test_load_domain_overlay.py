"""Client.load_domain workspace-overlay merge.

The SDK composes the §4.5.4 overlay DOMAIN.md set and overlay artifacts onto
the registry load_domain result client-side, mirroring the Go MCP bridge in
cmd/podium-mcp/load_domain.go. Each test fails without the merge and passes
with it.
"""

from __future__ import annotations

import http.server
import json
import os
import socket
import threading
import urllib.parse

import pytest

from podium import Client


def _overlay_artifact(root, art_id: str, *, type_="prompt", desc="", body="body"):
    pkg = os.path.join(root, *art_id.split("/"))
    os.makedirs(pkg, exist_ok=True)
    fm = f"---\ntype: {type_}\nversion: 0.1.0\ndescription: {desc}\n---\n{body}\n"
    with open(os.path.join(pkg, "ARTIFACT.md"), "w", encoding="utf-8") as fh:
        fh.write(fm)


def _overlay_domain(root, domain_path: str, frontmatter: str) -> None:
    pkg = os.path.join(root, *domain_path.split("/")) if domain_path else root
    os.makedirs(pkg, exist_ok=True)
    with open(os.path.join(pkg, "DOMAIN.md"), "w", encoding="utf-8") as fh:
        fh.write(frontmatter)


def _has_seg_prefix(id_: str, prefix: str) -> bool:
    return (
        len(id_) > len(prefix)
        and id_.startswith(prefix)
        and id_[len(prefix)] == "/"
    )


class _DomainHandler(http.server.BaseHTTPRequestHandler):
    """Routes /v1/load_domain and /v1/catalog by path (mirrors fakeRegistry)."""

    def log_message(self, *a):  # noqa: D401, N802
        pass

    def _send(self, obj) -> None:
        body = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_error(self, status: int, code: str, message: str) -> None:
        body = json.dumps({"code": code, "message": message}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # noqa: N802
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path == "/v1/load_domain":
            # A None payload models the registry 404ing an overlay-only domain.
            if self.server.load_domain is None:  # type: ignore[attr-defined]
                self._send_error(404, "domain.not_found", "domain.not_found: not here")
                return
            self._send(self.server.load_domain)  # type: ignore[attr-defined]
            return
        if parsed.path == "/v1/catalog":
            scope = urllib.parse.parse_qs(parsed.query).get("scope", [""])[0]
            arts = [
                e
                for e in self.server.catalog  # type: ignore[attr-defined]
                if scope == "" or e["id"] == scope or _has_seg_prefix(e["id"], scope)
            ]
            self._send({"ids": [e["id"] for e in arts], "artifacts": arts})
            return
        self.send_error(404)


@pytest.fixture()
def domain_server():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    server = http.server.HTTPServer(("127.0.0.1", port), _DomainHandler)
    server.load_domain = {}
    server.catalog = []
    t = threading.Thread(target=server.serve_forever, daemon=True)
    t.start()
    yield server
    server.shutdown()


def _client(server, overlay):
    base = f"http://127.0.0.1:{server.server_address[1]}"
    return Client(registry=base, overlay_path=str(overlay))


def _notable_by_id(result):
    return {a["id"]: a for a in result["notable"]}


def _subdomain_by_path(result, path):
    for s in result["subdomains"]:
        if s["path"] == path:
            return s
    return None


# spec: §4.5.2/§4.5.4/§6.4 — with no workspace overlay the
# load_domain result passes through the registry untouched.
def test_load_domain_no_overlay_passthrough(domain_server, tmp_path, monkeypatch):
    monkeypatch.delenv("PODIUM_OVERLAY_PATH", raising=False)
    monkeypatch.chdir(tmp_path)  # no .podium/overlay/ here
    domain_server.load_domain = {
        "path": "finance",
        "description": "Registry finance",
        "keywords": ["money"],
        "subdomains": [{"path": "finance/ap", "name": "ap", "description": "AP"}],
        "notable": [{"id": "finance/x", "type": "skill", "summary": "x", "source": "signal"}],
    }
    base = f"http://127.0.0.1:{domain_server.server_address[1]}"
    client = Client(registry=base)
    result = client.load_domain("finance")
    assert result["description"] == "Registry finance"
    assert [a["id"] for a in result["notable"]] == ["finance/x"]
    assert [s["path"] for s in result["subdomains"]] == ["finance/ap"]


# spec: §4.5.4 — an overlay DOMAIN.md body wins over the registry's description
# (highest-precedence layer) and keywords append-unique.
def test_load_domain_overlay_description_and_keywords(domain_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_domain(
        str(overlay),
        "finance",
        "---\ndiscovery:\n  keywords: [ledger, draft]\n---\nLocal working notes for finance\n",
    )
    domain_server.load_domain = {
        "path": "finance",
        "description": "Registry finance",
        "keywords": ["money", "ledger"],
        "subdomains": [],
        "notable": [],
    }
    result = _client(domain_server, overlay).load_domain("finance")
    assert result["description"] == "Local working notes for finance"
    assert result["keywords"] == ["money", "ledger", "draft"]


# spec: §4.5.5 — an overlay artifact that is a direct child of the requested
# domain joins the notable candidate pool, tagged overlay-sourced.
def test_load_domain_overlay_direct_child_notable(domain_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_artifact(
        str(overlay), "finance/draft-helper", type_="skill", desc="in-progress finance helper"
    )
    domain_server.load_domain = {
        "path": "finance",
        "subdomains": [],
        "notable": [{"id": "finance/x", "type": "skill", "summary": "x", "source": "signal"}],
    }
    result = _client(domain_server, overlay).load_domain("finance")
    got = _notable_by_id(result)
    d = got.get("finance/draft-helper")
    assert d is not None, result["notable"]
    assert d["overlay"] is True
    assert d["summary"] == "in-progress finance helper"
    assert d["source"] == "signal"
    assert "finance/x" in got  # registry notable retained


# spec: §4.5.5 — an overlay artifact below an immediate child introduces that
# child as a subdomain of the requested domain.
def test_load_domain_overlay_new_subdomain(domain_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_artifact(str(overlay), "finance/newteam/draft", type_="skill")
    _overlay_domain(str(overlay), "finance/newteam", "---\ndescription: New team workspace\n---\n")
    domain_server.load_domain = {
        "path": "finance",
        "subdomains": [{"path": "finance/ap", "name": "ap", "description": "AP"}],
        "notable": [],
    }
    result = _client(domain_server, overlay).load_domain("finance")
    sd = _subdomain_by_path(result, "finance/newteam")
    assert sd is not None, result["subdomains"]
    assert sd["name"] == "newteam"
    assert sd["description"] == "New team workspace"
    assert _subdomain_by_path(result, "finance/ap") is not None  # registry retained


# spec: §4.5.2 — a workspace-local DOMAIN.md include: resolves over the merged
# view (registry catalog union overlay), pulling in both a registry artifact and
# an overlay artifact while excluding out-of-scope ones.
def test_load_domain_overlay_include_merged_view(domain_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_artifact(
        str(overlay), "finance/ap/overlay-pay", type_="skill", desc="overlay pay"
    )
    _overlay_domain(str(overlay), "drafts", "---\ninclude:\n  - finance/ap/*\n---\n")
    domain_server.load_domain = {"path": "drafts", "subdomains": [], "notable": []}
    domain_server.catalog = [
        {"id": "finance/ap/registry-pay", "type": "skill", "summary": "registry pay"},
        {"id": "other/unrelated", "type": "skill", "summary": "nope"},
    ]
    result = _client(domain_server, overlay).load_domain("drafts")
    got = _notable_by_id(result)
    assert "finance/ap/registry-pay" in got, result["notable"]
    assert "finance/ap/overlay-pay" in got, result["notable"]
    assert "other/unrelated" not in got, result["notable"]


# spec: §4.5.3 — an overlay DOMAIN.md unlisted: true removes the folder and its
# subtree from the parent's enumeration.
def test_load_domain_overlay_unlisted_prunes_subdomain(domain_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_domain(str(overlay), "finance/secret", "---\nunlisted: true\n---\n")
    domain_server.load_domain = {
        "path": "finance",
        "subdomains": [
            {"path": "finance/ap", "name": "ap", "description": "AP"},
            {"path": "finance/secret", "name": "secret", "description": "Secret"},
        ],
        "notable": [],
    }
    result = _client(domain_server, overlay).load_domain("finance")
    assert _subdomain_by_path(result, "finance/secret") is None, result["subdomains"]
    assert _subdomain_by_path(result, "finance/ap") is not None, result["subdomains"]


# spec: §4.5.5 — an overlay DOMAIN.md at a child path overrides that child's
# short description (frontmatter description, never the body).
def test_load_domain_overlay_child_description_override(domain_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_domain(
        str(overlay),
        "finance/ap",
        "---\ndescription: Local AP overrides\n---\nbody never shown for a child\n",
    )
    domain_server.load_domain = {
        "path": "finance",
        "subdomains": [{"path": "finance/ap", "name": "ap", "description": "Registry AP"}],
        "notable": [],
    }
    result = _client(domain_server, overlay).load_domain("finance")
    sd = _subdomain_by_path(result, "finance/ap")
    assert sd is not None
    assert sd["description"] == "Local AP overrides"


# spec: §4.5.2 / §6.4 — a domain that exists only in the workspace overlay is
# part of the effective view; the registry 404s it (it never sees the overlay),
# so the SDK synthesizes an empty result and composes the overlay onto it. The
# local include: resolves over the merged view, pulling in a registry artifact.
def test_load_domain_overlay_only_domain_resolves(domain_server, tmp_path):
    overlay = tmp_path / "overlay"
    _overlay_domain(str(overlay), "drafts", "---\ndescription: Local drafts\ninclude:\n  - finance/ap/*\n---\n")
    _overlay_artifact(str(overlay), "finance/ap/overlay-pay", desc="overlay pay")
    domain_server.load_domain = None  # registry 404s the overlay-only path
    domain_server.catalog = [{"id": "finance/ap/registry-pay", "type": "skill", "summary": "registry pay"}]
    result = _client(domain_server, overlay).load_domain("drafts")
    ids = _notable_by_id(result)
    assert "finance/ap/registry-pay" in ids  # registry artifact via merged-view catalog
    assert "finance/ap/overlay-pay" in ids  # overlay artifact
    assert result["path"] == "drafts"
