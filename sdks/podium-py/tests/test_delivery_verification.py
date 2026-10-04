"""The SDK delivery check against a stub registry and object store.

Every case reaches the client through HTTP, so it covers the decode, the
fetch-and-check, the recompute, and the §4.7.9 policy as a caller sees them.
"""

from __future__ import annotations

import base64
import json
import os
import pathlib
import sys

import pytest
from conftest import (
    UNRELATED_SEED,
    VECTOR_PUBLIC_KEY,
    VECTOR_SEED,
    VECTORS,
    digest,
    public_key_of,
    served,
)

from podium import Client, RegistryError, _config, _delivery

MISMATCH = "materialize.content_hash_mismatch"
UNAVAILABLE = "config.signature_provider_unavailable"


def _rule(name: str = "acme/rule", **extra) -> dict:
    body = {
        "id": name,
        "type": "rule",
        "version": "1.0.0",
        "content_hash": digest(b"stored " + name.encode()),
        "sensitivity": "internal",
        "artifact_revision": "2025-06-01T12:34:56.789012Z",
        "frontmatter": f"---\nname: {name}\n---\nBody.\n",
        "manifest_body": "Body.\n",
    }
    body.update(extra)
    return body


def _entry(name: str, **extra) -> dict:
    return dict(_rule(name, **extra), status="ok")


def _client(stub, **kwargs) -> Client:
    return Client(registry=f"http://127.0.0.1:{stub.server_port}", **kwargs)


def _signing_client(stub) -> Client:
    return _client(stub, verify_keys=VECTOR_PUBLIC_KEY)


def _write(path: pathlib.Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def _home() -> pathlib.Path:
    return pathlib.Path(os.environ["HOME"])


def _default_key_file() -> pathlib.Path:
    return _home() / ".podium" / "standalone" / "registry-signing.key"


def _sync_yaml(path: pathlib.Path, policy: str) -> None:
    _write(path, f"defaults:\n  verify_signatures: {policy}\n")


def _workspace() -> pathlib.Path:
    ws = pathlib.Path.cwd()
    (ws / ".podium").mkdir(exist_ok=True)
    return ws


def _raises(code: str, call) -> RegistryError:
    with pytest.raises(RegistryError) as exc:
        call()
    assert exc.value.code == code, exc.value
    return exc.value


# ---------------------------------------------------------------------------
# Policy and key resolution.
# ---------------------------------------------------------------------------


# Spec: §7.6.3
def test_default_policy_is_never_without_a_key():
    assert Client(registry="http://127.0.0.1:1").verify_signatures == "never"


# Spec: §7.6.3
def test_verify_key_variable_selects_always(monkeypatch):
    monkeypatch.setenv("PODIUM_SIGNATURE_VERIFY_KEY", VECTOR_PUBLIC_KEY)
    assert Client(registry="http://127.0.0.1:1").verify_signatures == "always"


# Spec: §7.6.3
def test_key_file_at_default_path_selects_always():
    _write(_default_key_file(), f"public: {VECTOR_PUBLIC_KEY}\n")
    assert Client(registry="http://127.0.0.1:1").verify_signatures == "always"


# Spec: §7.6.3
def test_malformed_verify_key_refuses_construction(monkeypatch):
    monkeypatch.setenv("PODIUM_SIGNATURE_VERIFY_KEY", "not-base64")
    err = _raises(UNAVAILABLE, lambda: Client(registry="http://127.0.0.1:1"))
    assert "PODIUM_SIGNATURE_VERIFY_KEY" in err.message


# Spec: §7.6.3
def test_explicit_never_parses_no_key(monkeypatch):
    monkeypatch.setenv("PODIUM_SIGNATURE_VERIFY_KEY", "not-base64")
    client = Client(registry="http://127.0.0.1:1", verify_signatures="never")
    assert client.verify_signatures == "never"


# Spec: §7.6.3
def test_invalid_explicit_policy_raises_value_error():
    with pytest.raises(ValueError, match="verify_signatures"):
        Client(registry="http://127.0.0.1:1", verify_signatures="sometimes")


# Spec: §7.6.3
def test_verify_keys_argument_overrides_the_variable(stub_server, monkeypatch):
    monkeypatch.setenv("PODIUM_SIGNATURE_VERIFY_KEY", public_key_of(UNRELATED_SEED))
    stub_server.next_response = served(_rule(), sign_with=VECTOR_SEED)
    client = _signing_client(stub_server)
    assert client.load_artifact("acme/rule").id == "acme/rule"


# Spec: §4.7.9
# Spec: §7.6.3
def test_empty_variables_count_as_unset(monkeypatch):
    for name in ("PODIUM_SIGNATURE_VERIFY_KEY", "PODIUM_SIGN_KEY_PATH", "PODIUM_VERIFY_SIGNATURES"):
        monkeypatch.setenv(name, "")
    assert Client(registry="http://127.0.0.1:1").verify_signatures == "never"


# Spec: §7.6.3
def test_stale_never_in_sync_yaml_warns(monkeypatch):
    monkeypatch.setenv("PODIUM_SIGNATURE_VERIFY_KEY", VECTOR_PUBLIC_KEY)
    _sync_yaml(_home() / ".podium" / "sync.yaml", "never")
    with pytest.warns(RuntimeWarning, match="defaults.verify_signatures is never"):
        client = Client(registry="http://127.0.0.1:1")
    assert client.verify_signatures == "never"


# Spec: §7.6.3
def test_policy_variable_always_without_a_key_refuses(monkeypatch):
    monkeypatch.setenv("PODIUM_VERIFY_SIGNATURES", "always")
    _raises(UNAVAILABLE, lambda: Client(registry="http://127.0.0.1:1"))


# Spec: §7.6.3
def test_policy_variable_never_parses_no_key(monkeypatch):
    monkeypatch.setenv("PODIUM_VERIFY_SIGNATURES", "never")
    monkeypatch.setenv("PODIUM_SIGNATURE_VERIFY_KEY", "not-base64")
    assert Client(registry="http://127.0.0.1:1").verify_signatures == "never"


# Spec: §7.6.3
def test_explicit_never_wins_over_the_policy_variable(monkeypatch):
    monkeypatch.setenv("PODIUM_VERIFY_SIGNATURES", "always")
    client = Client(registry="http://127.0.0.1:1", verify_signatures="never")
    assert client.verify_signatures == "never"


# Spec: §7.6.3
def test_policy_variable_wins_over_sync_yaml(monkeypatch):
    monkeypatch.setenv("PODIUM_VERIFY_SIGNATURES", "always")
    monkeypatch.setenv("PODIUM_SIGNATURE_VERIFY_KEY", VECTOR_PUBLIC_KEY)
    _sync_yaml(_workspace() / ".podium" / "sync.yaml", "never")
    assert Client(registry="http://127.0.0.1:1").verify_signatures == "always"


# Spec: §7.5.2
# Spec: §7.6.3
def test_sync_yaml_scope_precedence_on_an_unkeyed_client():
    ws = _workspace()
    _sync_yaml(_home() / ".podium" / "sync.yaml", "never")
    _sync_yaml(ws / ".podium" / "sync.yaml", "always")
    local = ws / ".podium" / "sync.local.yaml"
    _sync_yaml(local, "never")
    with pytest.warns(RuntimeWarning):
        assert Client(registry="http://127.0.0.1:1").verify_signatures == "never"
    assert _config.read_verify_signatures(str(ws), str(_home())) == ("never", str(local))
    local.unlink()
    _raises(UNAVAILABLE, lambda: Client(registry="http://127.0.0.1:1"))


# Spec: §7.6.3
def test_invalid_policy_variable_raises_value_error(monkeypatch):
    monkeypatch.setenv("PODIUM_VERIFY_SIGNATURES", "medium-and-above")
    with pytest.raises(ValueError, match="PODIUM_VERIFY_SIGNATURES"):
        Client(registry="http://127.0.0.1:1")


# Spec: §7.6.3
def test_invalid_sync_yaml_policy_raises_value_error():
    path = _workspace() / ".podium" / "sync.yaml"
    _sync_yaml(path, "sometimes")
    with pytest.raises(ValueError, match=str(path)):
        Client(registry="http://127.0.0.1:1")


def _no_public_line(path: pathlib.Path) -> dict:
    _write(path, "verify: " + VECTOR_PUBLIC_KEY + "\n")
    return {}


@pytest.mark.parametrize(
    "setup",
    [
        lambda mp, tmp: mp.setenv("PODIUM_SIGN_KEY_PATH", str(tmp / "absent.key")) or {},
        lambda mp, tmp: mp.setenv("PODIUM_SIGN_KEY_PATH", str(tmp / "k.key")) or _no_public_line(tmp / "k.key"),
        lambda mp, tmp: _write(_default_key_file(), "public: not-base64\n") or {},
        lambda mp, tmp: {"verify_keys": "not-base64"},
        lambda mp, tmp: {"verify_keys": f"{VECTOR_PUBLIC_KEY},,{VECTOR_PUBLIC_KEY}"},
    ],
    ids=["missing key path", "no public line", "unparseable default file", "bad key list", "empty entry"],
)
# Spec: §4.7.9
# Spec: §7.6.3
def test_unusable_key_refuses_construction(setup, monkeypatch, tmp_path, stub_server):
    kwargs = setup(monkeypatch, tmp_path)
    _raises(UNAVAILABLE, lambda: _client(stub_server, **kwargs))
    assert stub_server.requests == []


# Spec: §7.6.3
def test_missing_verify_extra_refuses_always(monkeypatch):
    for name in list(sys.modules):
        if name == "cryptography" or name.startswith("cryptography."):
            monkeypatch.setitem(sys.modules, name, None)
    monkeypatch.setitem(sys.modules, "cryptography", None)
    err = _raises(UNAVAILABLE, lambda: Client(registry="http://127.0.0.1:1", verify_keys=VECTOR_PUBLIC_KEY))
    assert "podium-sdk[verify]" in err.message
    assert Client(registry="http://127.0.0.1:1", verify_signatures="never").verify_signatures == "never"


# ---------------------------------------------------------------------------
# Single loads.
# ---------------------------------------------------------------------------


# Spec: §4.7.10
def test_signed_load_verifies(stub_server):
    stub_server.next_response = served(_rule(), sign_with=VECTOR_SEED)
    art = _signing_client(stub_server).load_artifact("acme/rule")
    assert art.delivery_signature == stub_server.next_response["delivery_signature"]


# Spec: §4.7.10
def test_wrong_key_load_raises_signature_invalid(stub_server):
    stub_server.next_response = served(_rule(), sign_with=UNRELATED_SEED)
    client = _signing_client(stub_server)
    _raises("materialize.signature_invalid", lambda: client.load_artifact("acme/rule"))


# Spec: §4.7.10
def test_unsigned_load_under_always_raises_signature_missing(stub_server):
    stub_server.next_response = served(_rule())
    client = _signing_client(stub_server)
    _raises("materialize.signature_missing", lambda: client.load_artifact("acme/rule"))


# Spec: §4.7.10
def test_missing_or_mismatched_hash_refused_under_never(stub_server):
    client = _client(stub_server, verify_signatures="never")
    missing = served(_rule())
    del missing["delivery_hash"]
    stub_server.next_response = missing
    _raises(MISMATCH, lambda: client.load_artifact("acme/rule"))
    tampered = served(_rule())
    tampered["frontmatter"] += "tampered\n"
    stub_server.next_response = tampered
    _raises(MISMATCH, lambda: client.load_artifact("acme/rule"))


# Spec: §4.7.10
def test_id_less_load_returns_the_framed_empty_id(stub_server):
    # The record frames an absent id as the empty string, so the client returns
    # that attested value rather than the id the caller requested.
    body = _rule()
    del body["id"]
    stub_server.next_response = served(body)
    art = _client(stub_server, verify_signatures="never").load_artifact("acme/rule")
    assert art.id == ""


def _manifest_url_response(object_server, doc: bytes, **link) -> dict:
    url = object_server.base + "/objects/doc.md"
    object_server.objects["/objects/doc.md"] = doc
    body = _rule(frontmatter="", manifest_body="", manifest_body_url=dict({"presigned_url": url}, **link))
    return served(body, fetched={url: doc})


# Spec: §4.7.10
def test_manifest_document_failing_its_link_hash_is_refused(stub_server, object_server):
    doc = b"---\nname: big\n---\nBig body.\n"
    stub_server.next_response = _manifest_url_response(object_server, doc)
    object_server.objects["/objects/doc.md"] = doc + b"tampered"
    _raises(MISMATCH, lambda: _client(stub_server).load_artifact("acme/rule"))


# Spec: §4.7.10
def test_manifest_link_with_empty_hash_is_covered_by_the_delivery_hash(stub_server, object_server):
    doc = b"---\nname: big\n---\nBig body.\n"
    stub_server.next_response = _manifest_url_response(object_server, doc, content_hash="")
    art = _client(stub_server).load_artifact("acme/rule")
    assert (art.frontmatter, art.manifest_body) == (doc.decode(), "Big body.\n")
    object_server.objects["/objects/doc.md"] = doc.replace(b"Big", b"Bog")
    err = _raises(MISMATCH, lambda: _client(stub_server).load_artifact("acme/rule"))
    assert "recomputed delivery hash" in err.message


# Spec: §4.7.10
def test_large_link_with_empty_hash_is_refused_at_load(stub_server, tmp_path):
    url = "http://store/big.bin"
    body = served(_rule(large_resources={"big.bin": {"presigned_url": url}}), fetched={url: b"BIG"})
    body["large_resources"]["big.bin"]["content_hash"] = ""
    stub_server.next_response = body
    err = _raises(MISMATCH, lambda: _client(stub_server).load_artifact("acme/rule"))
    assert "recomputed delivery hash" in err.message
    assert list(tmp_path.iterdir()) == []


@pytest.mark.parametrize("policy", ["always", "never"])
# Spec: §4.7.10
def test_path_served_inline_and_by_link_is_refused(policy, stub_server, object_server):
    url = object_server.base + "/objects/a.md"
    body = served(_rule(large_resources={"a.md": {"presigned_url": url}}), fetched={url: b"A"}, sign_with=VECTOR_SEED)
    body["resources"] = {"a.md": "forged"}
    stub_server.next_response = body
    client = _client(stub_server, verify_signatures=policy, verify_keys=VECTOR_PUBLIC_KEY)
    _raises(MISMATCH, lambda: client.load_artifact("acme/rule"))
    assert object_server.paths == []


# Spec: §4.7.10
# Spec: §7.6.3
def test_non_utf8_manifest_document_verifies_and_returns_replaced_text(stub_server):
    case = next(c for c in VECTORS["responses"] if c["name"] == "manifest_body_url document that is not UTF-8")
    fetched = {url: base64.b64decode(v) for url, v in case["fetched"].items()}
    stub_server.next_response = base64.b64decode(case["body_base64"])
    art = _client(stub_server).load_artifact("acme/x", fetch=lambda url: fetched[url])
    (doc,) = fetched.values()
    assert art.frontmatter == doc.decode("utf-8", errors="replace")
    assert art.delivery_hash == case["served_hash"]


# Spec: §4.7.10
def test_unknown_url_member_is_never_fetched(stub_server, object_server, decoy_server, tmp_path):
    object_server.objects["/objects/doc.md"] = b"---\nname: big\n---\nBig body.\n"
    object_server.objects["/objects/big.bin"] = b"BIGDATA"
    doc_url, big_url = object_server.base + "/objects/doc.md", object_server.base + "/objects/big.bin"
    body = _rule(
        frontmatter="",
        manifest_body="",
        manifest_body_url={"presigned_url": doc_url, "url": decoy_server.base + "/doc"},
        large_resources={"big.bin": {"presigned_url": big_url, "url": decoy_server.base + "/big"}},
    )
    stub_server.next_response = served(body, fetched={doc_url: object_server.objects["/objects/doc.md"], big_url: b"BIGDATA"})
    art = _client(stub_server).load_artifact("acme/rule")
    art.materialize(str(tmp_path))
    assert (tmp_path / "acme" / "rule" / "big.bin").read_bytes() == b"BIGDATA"
    assert decoy_server.paths == []


# Spec: §4.7.10
# Spec: §7.6.3
def test_materialize_writes_nothing_when_a_later_resource_fails(stub_server, tmp_path):
    fetched = {"http://store/a.bin": b"A", "http://store/b.bin": b"B"}
    links = {"a.bin": {"presigned_url": "http://store/a.bin"}, "b.bin": {"presigned_url": "http://store/b.bin"}}
    stub_server.next_response = served(_rule(large_resources=links), fetched=fetched)
    art = _client(stub_server).load_artifact("acme/rule")
    tampered = dict(fetched, **{"http://store/b.bin": b"tampered"})
    _raises(MISMATCH, lambda: art.materialize(str(tmp_path), fetch=lambda url: tampered[url]))
    assert not (tmp_path / "acme" / "rule" / "ARTIFACT.md").exists()
    assert not (tmp_path / "acme").exists()


# ---------------------------------------------------------------------------
# Batch loads.
# ---------------------------------------------------------------------------


def _codes(results) -> list[str]:
    return [r.status if r.status == "ok" else r.error.code for r in results]


# Spec: §4.7.10
# Spec: §7.6.3
def test_batch_entry_naming_a_path_twice_is_an_error(stub_server):
    twice = served(
        _entry(
            "acme/b",
            resources=[{"path": "a.md", "inline": "A"}, {"path": "a.md", "presigned_url": "http://store/a.md"}],
        ),
        fetched={"http://store/a.md": b"A"},
    )
    stub_server.next_response = [served(_entry("acme/a")), twice, served(_entry("acme/c"))]
    out = _client(stub_server).load_artifacts(["acme/a", "acme/b", "acme/c"])
    assert _codes(out) == ["ok", MISMATCH, "ok"]


# Spec: §4.7.10
# Spec: §7.6.3
def test_batch_tampered_entry_is_an_error(stub_server):
    tampered = served(_entry("acme/b"))
    tampered["manifest_body"] = "changed"
    stub_server.next_response = [served(_entry("acme/a")), tampered]
    out = _client(stub_server).load_artifacts(["acme/a", "acme/b"])
    assert _codes(out) == ["ok", MISMATCH]
    assert out[1].id == "acme/b"


# Spec: §4.7.10
def test_batch_inline_body_failing_its_hash_is_an_error(stub_server):
    entry = served(_entry("acme/b", resources=[{"path": "a.md", "inline": "A"}]))
    entry["resources"][0]["inline"] = "forged"
    stub_server.next_response = [served(_entry("acme/a")), entry]
    assert _codes(_client(stub_server).load_artifacts(["acme/a", "acme/b"])) == ["ok", MISMATCH]


# Spec: §4.7.10
def test_batch_inline_reference_with_empty_hash_is_an_error(stub_server):
    # The registry frames the body's true digest; the reference serves "".
    entry = served(_entry("acme/b", resources=[{"path": "a.md", "inline": "A"}]))
    entry["resources"][0]["content_hash"] = ""
    stub_server.next_response = [served(_entry("acme/a")), entry, served(_entry("acme/c"))]
    out = _client(stub_server).load_artifacts(["acme/a", "acme/b", "acme/c"])
    assert _codes(out) == ["ok", MISMATCH, "ok"]


# Spec: §4.7.10
# Spec: §7.6.2
def test_batch_references_hold_only_the_members_step_2_reads(stub_server):
    link = {"path": "big.bin", "presigned_url": "http://store/big", "inline": "mismatched", "extra": "x"}
    inline = {"path": "a.md", "inline": "A", "extra": "x"}
    entry = served(_entry("acme/a", resources=[link, inline]), fetched={"http://store/big": b"BIG"})
    stub_server.next_response = [entry]
    (result,) = _client(stub_server).load_artifacts(["acme/a"])
    assert result.status == "ok"
    assert set(result.resources[0]) == {"path", "presigned_url", "content_hash"}
    assert result.resources[1] == {"path": "a.md", "content_hash": digest(b"A"), "inline": "A", "inline_base64": False}


# Spec: §7.6.3
def test_batch_materialize_checks_a_presigned_reference(stub_server, tmp_path):
    entry = served(
        _entry("acme/a", resources=[{"path": "big.bin", "presigned_url": "http://store/big"}]),
        fetched={"http://store/big": b"BIG"},
    )
    stub_server.next_response = [entry]
    (result,) = _client(stub_server).load_artifacts(["acme/a"])
    _raises(MISMATCH, lambda: result.materialize(str(tmp_path / "bad"), fetch=lambda url: b"forged"))
    assert not (tmp_path / "bad").exists()
    result.materialize(str(tmp_path / "ok"), fetch=lambda url: b"BIG")
    assert (tmp_path / "ok" / "acme" / "a" / "big.bin").read_bytes() == b"BIG"


# Spec: §4.7.9
# Spec: §7.6.3
def test_batch_signature_policy_applies_per_entry(stub_server):
    stub_server.next_response = [
        served(_entry("acme/a"), sign_with=VECTOR_SEED),
        served(_entry("acme/b"), sign_with=UNRELATED_SEED),
        served(_entry("acme/c")),
    ]
    out = _signing_client(stub_server).load_artifacts(["acme/a", "acme/b", "acme/c"])
    assert _codes(out) == ["ok", "materialize.signature_invalid", "materialize.signature_missing"]
    assert all(r.error is None or isinstance(r.error, RegistryError) for r in out)

    stub_server.next_response = [served(_entry("acme/a")), served(_entry("acme/b"))]
    out = _client(stub_server, verify_signatures="never").load_artifacts(["acme/a", "acme/b"])
    assert _codes(out) == ["ok", "ok"]


# Spec: §4.7.10
def test_check_delivery_requires_a_hash_and_keys():
    record = {"id": "acme/a"}
    served_record = _delivery.Served(record=record, hash=_delivery.delivery_hash(record), signature="{}")
    _raises(
        "materialize.signature_invalid",
        lambda: _delivery.check_delivery(served_record, _delivery.Verification("always", (b"\0" * 32,))),
    )
    _raises(UNAVAILABLE, lambda: _delivery.verify_envelope("{}", served_record.hash, []))
    envelope = json.dumps({"signature": "A" * 86 + "=="})
    _raises("materialize.signature_invalid", lambda: _delivery.verify_envelope(envelope, "nohash", [b"\0" * 32]))


@pytest.mark.parametrize("body", [b"{}", b"[1]", b'[{"status":"ok","resources":{}}]'], ids=["object", "number entry", "resources object"])
# Spec: §4.7.10
def test_parse_batch_response_refuses_malformed_layout(body):
    try:
        entries = _delivery.parse_batch_response(body)
    except RegistryError as exc:
        assert exc.code == MISMATCH
        return
    assert entries[0].error.code == MISMATCH


# Spec: §7.6.2
def test_load_artifacts_surfaces_an_http_error_envelope(stub_server):
    stub_server.next_status = 403
    stub_server.next_response = {"code": "auth.forbidden", "message": "no"}
    _raises("auth.forbidden", lambda: _client(stub_server).load_artifacts(["acme/a"]))
