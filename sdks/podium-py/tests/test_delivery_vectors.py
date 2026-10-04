"""Run the Go-generated §4.7.10 and §4.7.9 vectors through the SDK decoders.

``test/vectors/delivery-record.json`` declares each case's outcome from the
spec, and the Go generator fails when Go reaches another one, so a case that
fails here is an SDK divergence from Go.
"""

from __future__ import annotations

import base64

import pytest
from conftest import VECTORS

from podium import RegistryError, _delivery

_OK = "ok"
_PUBLIC_KEYS = [base64.b64decode(VECTORS["public_key"])]


def _cases(section):
    return pytest.mark.parametrize("case", VECTORS[section], ids=[c["name"] for c in VECTORS[section]])


def _fetch(fetched, url):
    if url not in fetched:
        raise RegistryError(_delivery.CODE_MISMATCH, f"object store serves no {url}")
    return base64.b64decode(fetched[url])


def _verify_response(body, fetched):
    """Run steps 1 to 7 on a load_artifact body and return the served record."""
    served = _delivery.parse_load_response(body)
    if served.manifest_link is not None:
        doc = _fetch(fetched, served.manifest_link["presigned_url"])
        _delivery.check_linked(doc, served.manifest_link["content_hash"], "manifest document")
        _delivery.place_manifest_document(served, doc, _delivery.manifest_body_of(doc))
    for path in sorted(served.links):
        link = served.links[path]
        _delivery.check_linked(_fetch(fetched, link["presigned_url"]), link["content_hash"], path)
    _delivery.check_delivery(served, _delivery.Verification(_delivery.POLICY_NEVER))
    return served


def _expect_refusal(case, call):
    with pytest.raises(RegistryError) as exc:
        call()
    assert exc.value.code == case["outcome"]


@_cases("responses")
# Spec: §4.7.10
def test_response_vectors(case):
    body = base64.b64decode(case["body_base64"])
    fetched = case.get("fetched") or {}
    if case["outcome"] != _OK:
        _expect_refusal(case, lambda: _verify_response(body, fetched))
        return
    served = _verify_response(body, fetched)
    assert served.hash == case["served_hash"]
    # Every vector signature is made with the vector seed, so an ok record
    # also passes the always policy under the vector public key.
    _delivery.check_delivery(served, _delivery.Verification(_delivery.POLICY_ALWAYS, tuple(_PUBLIC_KEYS)))


@_cases("batch")
# Spec: §4.7.10
def test_batch_vectors(case):
    body = base64.b64decode(case["body_base64"])
    if case["outcome"] != _OK:
        _expect_refusal(case, lambda: _delivery.parse_batch_response(body))
        return
    entries = _delivery.parse_batch_response(body)
    assert len(entries) == len(case["entries"])
    for entry, want in zip(entries, case["entries"]):
        if want["outcome"] != _OK:
            if entry.error is None:
                _expect_refusal(
                    want,
                    lambda: _delivery.check_delivery(entry.served, _delivery.Verification(_delivery.POLICY_NEVER)),
                )
            else:
                assert entry.error.code == want["outcome"]
            continue
        assert entry.error is None, entry.error
        _delivery.check_delivery(entry.served, _delivery.Verification(_delivery.POLICY_NEVER))
        assert entry.served.hash == want["served_hash"]


@_cases("base64")
# Spec: §4.7.10
def test_base64_vectors(case):
    if case["outcome"] != _OK:
        _expect_refusal(case, lambda: _delivery.decode_base64(case["input"]))
        return
    assert _delivery.decode_base64(case["input"]) == base64.b64decode(case.get("decoded", ""))


@_cases("envelope")
# Spec: §4.7.9
def test_envelope_vectors(case):
    keys = [base64.b64decode(k) for k in case["keys"]]
    if case["outcome"] != _OK:
        _expect_refusal(case, lambda: _delivery.verify_envelope(case["envelope"], case["signed_hash"], keys))
        return
    assert _delivery.verify_envelope(case["envelope"], case["signed_hash"], keys) == case["key_id"]


@_cases("manifest_body")
# Spec: §4.7.10
def test_manifest_body_vectors(case):
    doc = base64.b64decode(case["document_base64"])
    assert _delivery.manifest_body_of(doc) == base64.b64decode(case["body_base64"])


@_cases("verify_key_list")
# Spec: §4.7.9
def test_verify_key_list_vectors(case):
    if case["outcome"] != _OK:
        _expect_refusal(case, lambda: _delivery.parse_verify_key_list(case["input"]))
        return
    keys = _delivery.parse_verify_key_list(case["input"])
    assert [base64.b64encode(k).decode() for k in keys] == case["keys"]


@_cases("key_file")
# Spec: §4.7.9
def test_key_file_vectors(case):
    if case["outcome"] != _OK:
        _expect_refusal(case, lambda: _delivery.parse_key_file(case["input"]))
        return
    keys = _delivery.parse_key_file(case["input"])
    assert [base64.b64encode(k).decode() for k in keys] == case["keys"]
