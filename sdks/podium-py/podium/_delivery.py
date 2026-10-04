"""The §4.7.10 delivery check and the §4.7.9 verifier for the Python SDK.

Spec: §4.7.9
Spec: §4.7.10
Spec: §7.6.3

This module ports the Go decoding steps (``pkg/version/served.go``), the
record framing (``pkg/version.DeliveryHash``), the manifest-body derivation
(``pkg/manifest.ManifestBodyOf``), and the registry-managed envelope and key
rules (``pkg/sign``). ``test/vectors/delivery-record.json`` pins every outcome
against the Go libraries.

The standard ``json`` module is not the §4.7.10 JSON rule on its own: it
accepts ``NaN``, keeps unpaired surrogate escapes, drops the earlier of two
same-named members before any check on the parsed value can see it, and raises
on an integer literal longer than 4300 digits. ``decode_json_text`` therefore
scans the decoded text before parsing and parses numbers into ``_JsonNumber``.

Hash recomputation needs only ``hashlib``. Signature verification imports
``cryptography`` lazily, because it is the optional ``podium-sdk[verify]``
extra that a client under the ``never`` policy does not need.
"""

from __future__ import annotations

import base64
import binascii
import hashlib
import json
import os
import re
import struct
import warnings
from dataclasses import dataclass, field
from typing import Any, Mapping

from . import _config
from ._errors import RegistryError

# Error codes the SDK raises for the delivery check (§6.10).
CODE_MISMATCH = "materialize.content_hash_mismatch"
CODE_SIGNATURE_INVALID = "materialize.signature_invalid"
CODE_SIGNATURE_MISSING = "materialize.signature_missing"
CODE_UNAVAILABLE = "config.signature_provider_unavailable"

# The §4.7.9 policy values.
POLICY_NEVER = "never"
POLICY_ALWAYS = "always"
_POLICIES = (POLICY_NEVER, POLICY_ALWAYS)

# The framed leading value of every delivery stream, and the record's scalar
# fields in framing order. Both mirror pkg/version.DeliveryHash, and each field
# name is the wire member §7.2 "Record fields" maps it to.
_RECORD_TAG = "podium/delivery-record/2"
_RECORD_FIELDS = (
    "id",
    "version",
    "type",
    "content_hash",
    "sensitivity",
    "artifact_revision",
    "frontmatter",
    "manifest_body",
    "skill_raw",
)

# The §4.7.10 step 1 nesting limit; the top-level container is level 1.
_MAX_JSON_DEPTH = 64

_SKILL_TYPE = "skill"
_STATUS_OK = "ok"

# The registry-managed key file's default location below the home directory.
_DEFAULT_KEY_PATH = (".podium", "standalone", "registry-signing.key")

_ED25519_PUBLIC_KEY_SIZE = 32
_ED25519_PRIVATE_KEY_SIZE = 64
_ED25519_SIGNATURE_SIZE = 64

_EXTRA_HINT = "install the podium-sdk[verify] extra (pip install 'podium-sdk[verify]')"


def _error(code: str, message: str) -> RegistryError:
    return RegistryError(code, message)


def _malformed(message: str) -> RegistryError:
    return _error(CODE_MISMATCH, message)


# ---------------------------------------------------------------------------
# Step 1: the JSON rule.
# ---------------------------------------------------------------------------


class _JsonNumber:
    """A JSON number kept as its literal text until the step-2 checks finish.

    It is neither a ``str`` nor a ``bool``, so a number fails a string or
    boolean type check, and no integer literal is converted while decoding.
    """

    __slots__ = ("text", "is_int")

    def __init__(self, text: str, is_int: bool) -> None:
        self.text = text
        self.is_int = is_int


def _refuse_constant(name: str) -> Any:
    raise ValueError(f"JSON constant {name} is not allowed")


# A JSON string with its escapes, or one structural bracket outside a string.
# The unrolled string pattern consumes each string whole, so a bracket inside
# a string is never counted.
_JSON_TOKEN = re.compile(r'"[^"\\]*(?:\\.[^"\\]*)*"|[\[\]{}]', re.DOTALL)
_JSON_ESCAPE = re.compile(r"\\(?:u([0-9A-Fa-f]{4})|.)", re.DOTALL)


def _check_surrogates(token: str) -> None:
    """Refuse a surrogate escape outside a high-low pair in one JSON string."""
    pending_high_end = -1
    for m in _JSON_ESCAPE.finditer(token):
        cp = int(m.group(1), 16) if m.group(1) else -1
        if pending_high_end >= 0:
            if m.start() != pending_high_end or not 0xDC00 <= cp <= 0xDFFF:
                raise _malformed("unpaired surrogate escape")
            pending_high_end = -1
            continue
        if 0xDC00 <= cp <= 0xDFFF:
            raise _malformed("unpaired surrogate escape")
        if 0xD800 <= cp <= 0xDBFF:
            pending_high_end = m.end()
    if pending_high_end >= 0:
        raise _malformed("unpaired surrogate escape")


def _scan_json_text(text: str) -> None:
    """Port of the Go checkJSONText byte scan over decoded text.

    The scan runs before ``json.loads``, so it covers a member that a later
    member of the same name replaces, which a check on the parsed value never
    sees. Malformed text the scan cannot tokenize is refused by the parse.
    """
    depth = 0
    for m in _JSON_TOKEN.finditer(text):
        tok = m.group(0)
        if tok[0] == '"':
            if "\\u" in tok:
                _check_surrogates(tok)
        elif tok in "[{":
            depth += 1
            if depth > _MAX_JSON_DEPTH:
                raise _malformed(f"body nests deeper than {_MAX_JSON_DEPTH} levels")
        else:
            depth -= 1


def decode_json_text(raw: bytes) -> Any:
    """Apply the §4.7.10 step 1 JSON rule to ``raw`` and return its value.

    Numbers come back as ``_JsonNumber``; ``plain_json`` converts them. When
    two members share a name the later one is kept. Every refusal raises
    ``RegistryError`` with ``materialize.content_hash_mismatch``.

    Spec: §4.7.10
    """
    try:
        text = bytes(raw).decode("utf-8")
    except UnicodeDecodeError as exc:
        raise _malformed("body is not valid UTF-8") from exc
    if text.startswith("\ufeff"):
        raise _malformed("body starts with a byte order mark")
    _scan_json_text(text)
    try:
        return json.loads(
            text,
            parse_constant=_refuse_constant,
            parse_int=lambda s: _JsonNumber(s, True),
            parse_float=lambda s: _JsonNumber(s, False),
        )
    except ValueError as exc:
        raise _malformed(f"body is not a JSON text: {exc}") from exc


def plain_json(value: Any) -> Any:
    """Return a copy of a decoded value with each ``_JsonNumber`` converted.

    An integer literal becomes an ``int`` and any other literal a ``float``.
    An integer literal too long for ``int`` stays its literal text.
    """
    if isinstance(value, _JsonNumber):
        if not value.is_int:
            return float(value.text)
        try:
            return int(value.text)
        except ValueError:
            return value.text
    if isinstance(value, dict):
        return {k: plain_json(v) for k, v in value.items()}
    if isinstance(value, list):
        return [plain_json(v) for v in value]
    return value


# ---------------------------------------------------------------------------
# Step 3: canonical base64.
# ---------------------------------------------------------------------------


def decode_base64(s: str) -> bytes:
    """Decode standard base64 accepted only in canonical form.

    A value is canonical when re-encoding its decoded bytes reproduces it,
    which refuses a line break, whitespace, missing padding, the URL-safe
    alphabet, and a non-zero pad bit.

    Spec: §4.7.10
    """
    try:
        b = base64.b64decode(s, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise _malformed(f"base64: {exc}") from exc
    if base64.b64encode(b).decode("ascii") != s:
        raise _malformed("base64 value is not in canonical form")
    return b


# ---------------------------------------------------------------------------
# Step 2: exact members with typed values.
# ---------------------------------------------------------------------------


def _opt_str(obj: Mapping[str, Any], name: str, where: str = "") -> str:
    """Read a string member by exact name; absent or null is empty."""
    v = obj.get(name)
    if v is None:
        return ""
    if not isinstance(v, str):
        raise _malformed(f"{where}{name} is not a string")
    return v


def _opt_bool(obj: Mapping[str, Any], name: str, where: str = "") -> bool:
    """Read a boolean member by exact name; absent or null is false."""
    v = obj.get(name)
    if v is None:
        return False
    if not isinstance(v, bool):
        raise _malformed(f"{where}{name} is not a boolean")
    return v


def _opt_object(obj: Mapping[str, Any], name: str) -> dict[str, Any]:
    v = obj.get(name)
    if v is None:
        return {}
    if not isinstance(v, dict):
        raise _malformed(f"{name} is not an object")
    return v


def _opt_array(obj: Mapping[str, Any], name: str) -> list[Any]:
    v = obj.get(name)
    if v is None:
        return []
    if not isinstance(v, list):
        raise _malformed(f"{name} is not an array")
    return v


def _body_of(value: str, b64: bool) -> bytes:
    return decode_base64(value) if b64 else value.encode("utf-8")


# ---------------------------------------------------------------------------
# Steps 5 to 7: the record, the body checks, and the hash.
# ---------------------------------------------------------------------------


def resource_digest(body: bytes) -> str:
    """Return ``sha256:`` and the lowercase hex SHA-256 digest of ``body``.

    Spec: §4.7.10
    """
    return "sha256:" + hashlib.sha256(body).hexdigest()


def check_linked(body: bytes, want: str, what: str) -> None:
    """Apply the §4.7.10 step 6 check to one fetched or decoded body.

    An empty ``want`` is not checked on its own, because the record frames
    the empty value and the delivery-hash comparison refuses it.

    Spec: §4.7.10
    """
    if not want:
        return
    got = resource_digest(body)
    if got != want:
        raise _malformed(f"{what}: content hash {got} does not match {want}")


def _framed(value: str | bytes) -> bytes:
    data = value.encode("utf-8") if isinstance(value, str) else bytes(value)
    return struct.pack(">Q", len(data)) + data


def delivery_hash(record: Mapping[str, Any]) -> str:
    """Return the §4.7.10 delivery hash of ``record`` as ``sha256:<hex>``.

    ``record`` maps each name in ``_RECORD_FIELDS`` to a ``str``, framed as
    its UTF-8 encoding, or ``bytes``, framed as is, and ``resources`` to a
    path-to-content-hash map. ``sorted`` orders paths by code point, which is
    UTF-8 byte order for text that holds no unpaired surrogate, and the step 1
    JSON rule refuses every body that would carry one.

    Spec: §4.7.10
    """
    h = hashlib.sha256()
    h.update(_framed(_RECORD_TAG))
    for name in _RECORD_FIELDS:
        h.update(_framed(record.get(name) or ""))
    resources = record.get("resources") or {}
    for path in sorted(resources):
        h.update(_framed(path))
        h.update(_framed(resources[path]))
    return "sha256:" + h.hexdigest()


@dataclass
class Served:
    """One decoded ``load_artifact`` body or ``ok`` batch entry.

    ``record`` holds every delivery-record field. For a body that carries
    ``manifest_body_url``, the caller fetches, checks, and places the document
    with ``place_manifest_document`` before it recomputes the hash.
    """

    record: dict[str, Any]
    hash: str = ""
    signature: str = ""
    # load_artifact: each inline path's value, str for text and bytes for a
    # resources_base64 body.
    resources: dict[str, str | bytes] = field(default_factory=dict)
    # load_artifact: each linked path's link object, numbers converted.
    links: dict[str, dict[str, Any]] = field(default_factory=dict)
    # load_artifact: the manifest_body_url link, None when absent.
    manifest_link: dict[str, Any] | None = None
    # Batch entry: each reference holding only the members step 2 reads.
    references: list[dict[str, Any]] = field(default_factory=list)


@dataclass
class BatchEntry:
    """One element of a §7.6.2 batch body.

    ``served`` is set for an ``ok`` entry that decoded. ``error`` is the step
    2 to 6 refusal of an ``ok`` entry. ``members`` is the entry's top-level
    object with numbers converted.
    """

    status: str
    members: dict[str, Any]
    served: Served | None = None
    error: Exception | None = None


def _read_record_members(m: Mapping[str, Any]) -> Served:
    record: dict[str, Any] = {name: _opt_str(m, name) for name in _RECORD_FIELDS}
    return Served(
        record=record,
        hash=_opt_str(m, "delivery_hash"),
        signature=_opt_str(m, "delivery_signature"),
    )


def _parse_link(raw: Any, where: str) -> dict[str, Any]:
    if not isinstance(raw, dict):
        raise _malformed(f"{where} is not an object")
    url = _opt_str(raw, "presigned_url", where + ".")
    _opt_str(raw, "content_hash", where + ".")
    if not url:
        raise _malformed(f"{where} has no presigned_url")
    return plain_json(raw)


def _read_load_resources(m: Mapping[str, Any], s: Served) -> None:
    b64 = _opt_bool(m, "resources_base64")
    hashes: dict[str, str] = {}
    for path, value in _opt_object(m, "resources").items():
        if value is None:
            continue
        if not isinstance(value, str):
            raise _malformed(f"resources[{path!r}] is not a string")
        body = _body_of(value, b64)
        s.resources[path] = body if b64 else value
        hashes[path] = resource_digest(body)
    for path, raw in _opt_object(m, "large_resources").items():
        if raw is None:
            continue
        link = _parse_link(raw, f"large_resources[{path!r}]")
        if path in s.resources:
            raise _malformed(f"resource {path!r} is in both resources and large_resources")
        s.links[path] = link
        hashes[path] = link.get("content_hash") or ""
    s.record["resources"] = hashes


def parse_load_response(raw: bytes) -> Served:
    """Apply §4.7.10 steps 1 to 5 to a ``load_artifact`` body.

    The port of ``version.ParseLoadResponse``. A body that carries
    ``manifest_body_url`` returns with ``manifest_link`` set and the document
    unplaced. Every refusal raises ``RegistryError`` with
    ``materialize.content_hash_mismatch``.

    Spec: §4.7.10
    """
    m = decode_json_text(raw)
    if not isinstance(m, dict):
        raise _malformed("top-level value is not an object")
    s = _read_record_members(m)
    _read_load_resources(m, s)
    link = m.get("manifest_body_url")
    if link is not None:
        s.manifest_link = _parse_link(link, "manifest_body_url")
    return s


def place_manifest_document(served: Served, doc: bytes, body: bytes) -> None:
    """Complete the record with a fetched manifest document and its body.

    The port of ``Served.PlaceManifestDocument``: the document goes to
    ``skill_raw`` for a skill and to ``frontmatter`` otherwise, framed as the
    fetched bytes.

    Spec: §4.7.10
    """
    name = "skill_raw" if served.record.get("type") == _SKILL_TYPE else "frontmatter"
    served.record[name] = bytes(doc)
    served.record["manifest_body"] = bytes(body)


def _read_reference(ref: Any, i: int, seen: set[str]) -> tuple[str, str, dict[str, Any]]:
    """Decode one batch reference and return its path, hash, and public form."""
    where = f"resources[{i}]."
    if not isinstance(ref, dict):
        raise _malformed(f"resources[{i}] is not an object")
    if ref.get("path") is None:
        raise _malformed(f"resources[{i}] has no path")
    path = _opt_str(ref, "path", where)
    if path in seen:
        raise _malformed(f"resource {path!r} is named more than once")
    content_hash = _opt_str(ref, "content_hash", where)
    url = _opt_str(ref, "presigned_url", where)
    inline = _opt_str(ref, "inline", where)
    b64 = _opt_bool(ref, "inline_base64", where)
    if url:
        return path, content_hash, {"path": path, "presigned_url": url, "content_hash": content_hash}
    check_linked(_body_of(inline, b64), content_hash, f"resource {path!r}")
    return path, content_hash, {
        "path": path,
        "content_hash": content_hash,
        "inline": inline,
        "inline_base64": b64,
    }


def _parse_ok_entry(m: Mapping[str, Any]) -> Served:
    s = _read_record_members(m)
    hashes: dict[str, str] = {}
    for i, ref in enumerate(_opt_array(m, "resources")):
        path, content_hash, public = _read_reference(ref, i, hashes.keys())
        hashes[path] = content_hash
        s.references.append(public)
    s.record["resources"] = hashes
    return s


def _parse_batch_entry(m: dict[str, Any]) -> BatchEntry:
    entry = BatchEntry(status="", members=plain_json(m))
    try:
        entry.status = _opt_str(m, "status")
        if entry.status == _STATUS_OK:
            entry.served = _parse_ok_entry(m)
    except RegistryError as exc:
        entry.error = exc
    return entry


def parse_batch_response(raw: bytes) -> list[BatchEntry]:
    """Apply step 1 to a whole batch body and steps 2 to 6 to each ok entry.

    The port of ``version.ParseBatchResponse``. A body that fails step 1, or
    whose top-level value is not an array of objects, raises; an ``ok`` entry
    that fails a later step carries its refusal in ``BatchEntry.error``.

    Spec: §4.7.10, §7.6.2
    """
    v = decode_json_text(raw)
    if not isinstance(v, list):
        raise _malformed("batch body is not an array")
    for i, m in enumerate(v):
        if not isinstance(m, dict):
            raise _malformed(f"batch entry {i} is not an object")
    return [_parse_batch_entry(m) for m in v]


# ---------------------------------------------------------------------------
# Manifest-body derivation.
# ---------------------------------------------------------------------------

# The port of pkg/manifest's frontmatter delimiter rule, over bytes; the
# delimiters are ASCII, so no decoding is needed.
_FRONTMATTER = re.compile(rb"\A---\r?\n.*?\r?\n---\r?\n?(.*)\Z", re.DOTALL)


def manifest_body_of(doc: bytes) -> bytes:
    """Derive the manifest body of a manifest document by the §4.7.10 rule.

    The body is every byte after the closing ``---`` with leading CR and LF
    characters removed. A document with no frontmatter block has an empty
    body.

    Spec: §4.7.10
    """
    m = _FRONTMATTER.match(bytes(doc))
    return m.group(1).lstrip(b"\r\n") if m else b""


# ---------------------------------------------------------------------------
# §4.7.9 keys and envelope.
# ---------------------------------------------------------------------------


def _decode_key(text: str, size: int, kind: str) -> bytes:
    # pkg/sign decodes key material with base64.StdEncoding, which skips CR
    # and LF; the strict canonical rule applies only to served values.
    cleaned = text.strip().replace("\r", "").replace("\n", "")
    try:
        raw = base64.b64decode(cleaned, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise _error(CODE_UNAVAILABLE, f"decode {kind} key: {exc}") from exc
    if len(raw) != size:
        raise _error(CODE_UNAVAILABLE, f"{kind} key is {len(raw)} bytes, want {size}")
    return raw


def parse_verify_key_list(value: str) -> list[bytes]:
    """Decode a ``PODIUM_SIGNATURE_VERIFY_KEY`` list of Ed25519 public keys.

    An empty entry, a trailing comma included, and an entry that does not
    decode refuse the whole list with ``config.signature_provider_unavailable``.

    Spec: §4.7.9
    """
    keys: list[bytes] = []
    for i, entry in enumerate(value.split(","), start=1):
        if not entry.strip():
            raise _error(CODE_UNAVAILABLE, f"entry {i} is empty")
        keys.append(_decode_key(entry, _ED25519_PUBLIC_KEY_SIZE, "public"))
    return keys


def parse_key_file(text: str) -> list[bytes]:
    """Parse the registry-managed key file and return its verification key set.

    The set is the ``public:`` key followed by each ``verify:`` key. A missing
    or repeated ``public:`` line, a repeated ``private:`` line, and a
    ``private:`` line whose public half differs from ``public:`` are refused;
    unknown prefixes are ignored, as ``pkg/sign.ParseKeyFile`` does.

    Spec: §4.7.9
    """
    private: bytes | None = None
    public: bytes | None = None
    verify: list[bytes] = []
    for line in text.split("\n"):
        line = line.strip()
        if line.startswith("private:"):
            if private is not None:
                raise _error(CODE_UNAVAILABLE, 'key file carries more than one "private:" line')
            private = _decode_key(line[len("private:"):], _ED25519_PRIVATE_KEY_SIZE, "private")
        elif line.startswith("public:"):
            if public is not None:
                raise _error(CODE_UNAVAILABLE, 'key file carries more than one "public:" line')
            public = _decode_key(line[len("public:"):], _ED25519_PUBLIC_KEY_SIZE, "public")
        elif line.startswith("verify:"):
            verify.append(_decode_key(line[len("verify:"):], _ED25519_PUBLIC_KEY_SIZE, "verify"))
    if public is None:
        raise _error(CODE_UNAVAILABLE, 'key file carries no "public:" line')
    # An Ed25519 private key is the seed followed by its public half.
    if private is not None and private[32:] != public:
        raise _error(CODE_UNAVAILABLE, 'the "public:" line is not the public half of the "private:" line')
    return [public] + verify


def key_id_of(public_key: bytes) -> str:
    """Return the §4.7.9 ``key_id`` of a 32-byte Ed25519 public key."""
    return hashlib.sha256(public_key).digest()[:8].hex()


def _load_ed25519() -> tuple[Any, Any]:
    """Import the Ed25519 verifier from the optional ``cryptography`` extra."""
    try:
        from cryptography.exceptions import InvalidSignature
        from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
    except ImportError as exc:
        raise _error(
            CODE_UNAVAILABLE,
            f"signature verification needs the cryptography package; {_EXTRA_HINT}",
        ) from exc
    return Ed25519PublicKey, InvalidSignature


def _envelope_members(envelope: str) -> tuple[str, bytes]:
    """Apply the §4.7.9 envelope rule and return the key_id and signature."""
    try:
        m = decode_json_text(envelope.encode("utf-8"))
        if not isinstance(m, dict):
            raise _malformed("envelope is not an object")
        if m.get("signature") is None:
            raise _malformed("envelope has no signature")
        sig_text = _opt_str(m, "signature", "envelope member ")
        key_id = _opt_str(m, "key_id", "envelope member ")
        sig = decode_base64(sig_text)
    except (RegistryError, UnicodeEncodeError) as exc:
        raise _error(CODE_SIGNATURE_INVALID, f"envelope: {exc}") from exc
    if len(sig) != _ED25519_SIGNATURE_SIZE:
        raise _error(CODE_SIGNATURE_INVALID, f"signature is {len(sig)} bytes, want {_ED25519_SIGNATURE_SIZE}")
    return key_id, sig


def _hash_bytes(signed_hash: str) -> bytes:
    _, sep, hex_part = signed_hash.partition(":")
    try:
        if not sep or not hex_part:
            raise ValueError("hash must be alg:hex")
        return binascii.unhexlify(hex_part)
    except (binascii.Error, ValueError) as exc:
        raise _error(CODE_SIGNATURE_INVALID, f"signed hash {signed_hash!r}: {exc}") from exc


def verify_envelope(envelope: str, signed_hash: str, keys: list[bytes]) -> str:
    """Verify a registry-managed envelope over ``signed_hash`` and return the
    ``key_id`` of the key that verified it.

    The signed message is the 32-byte digest ``signed_hash`` (``sha256:<hex>``)
    encodes. The key the envelope's ``key_id`` names is tried first and then
    every other key, because the ``key_id`` is unauthenticated. Every refusal
    raises ``RegistryError`` with ``materialize.signature_invalid``; an empty
    key set raises ``config.signature_provider_unavailable``.

    Spec: §4.7.9
    """
    if not keys:
        raise _error(CODE_UNAVAILABLE, "registry-managed key not configured")
    key_id, sig = _envelope_members(envelope)
    digest = _hash_bytes(signed_hash)
    public_key_cls, invalid_signature = _load_ed25519()
    ordered = sorted(keys, key=lambda k: key_id_of(k) != key_id)
    for key in ordered:
        try:
            public_key_cls.from_public_bytes(key).verify(sig, digest)
        except invalid_signature:
            continue
        return key_id_of(key)
    raise _error(CODE_SIGNATURE_INVALID, "signature does not verify under any trusted key")


# ---------------------------------------------------------------------------
# §4.7.9 policy and key resolution for an SDK client.
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Verification:
    """A client's resolved §4.7.9 policy and verification key set."""

    policy: str
    keys: tuple[bytes, ...] = ()


def _default_key_path(home: str) -> str:
    return os.path.join(home, *_DEFAULT_KEY_PATH)


def key_configured(verify_keys: str | None, environ: Mapping[str, str], home: str) -> bool:
    """Report whether a verification key is configured for the SDK default.

    It fails closed: a key list, a non-empty ``PODIUM_SIGNATURE_VERIFY_KEY``,
    a non-empty ``PODIUM_SIGN_KEY_PATH``, or a file present at the default key
    path counts, whether or not the material decodes.

    Spec: §4.7.9
    """
    return bool(
        verify_keys
        or environ.get("PODIUM_SIGNATURE_VERIFY_KEY")
        or environ.get("PODIUM_SIGN_KEY_PATH")
        or os.path.lexists(_default_key_path(home))
    )


def _checked_policy(value: str, source: str) -> str:
    if value not in _POLICIES:
        raise ValueError(f"{source} must be never or always, got {value!r}")
    return value


def resolve_policy(
    explicit: str | None, verify_keys: str | None, environ: Mapping[str, str], cwd: str, home: str
) -> str:
    """Resolve the §4.7.9 policy in the SDK order.

    The order is the ``verify_signatures`` argument, ``PODIUM_VERIFY_SIGNATURES``,
    ``defaults.verify_signatures`` across the §7.5.2 scopes, and then the SDK
    default: ``always`` when a key is configured and ``never`` otherwise. An
    invalid value raises ``ValueError`` naming its source and is never treated
    as unset. A ``never`` from ``sync.yaml`` emits a ``RuntimeWarning``.

    Spec: §4.7.9, §7.6.3
    """
    if explicit is not None:
        return _checked_policy(explicit, "verify_signatures")
    env = environ.get("PODIUM_VERIFY_SIGNATURES") or ""
    if env:
        return _checked_policy(env, "PODIUM_VERIFY_SIGNATURES")
    value, path = _config.read_verify_signatures(cwd, home)
    if value:
        policy = _checked_policy(value, f"defaults.verify_signatures in {path}")
        if policy == POLICY_NEVER:
            warnings.warn(
                f"signature verification is off because defaults.verify_signatures is never in "
                f"{path}; remove defaults.verify_signatures from that file to verify under the "
                "SDK default (§4.7.9)",
                RuntimeWarning,
                stacklevel=4,
            )
        return policy
    return POLICY_ALWAYS if key_configured(verify_keys, environ, home) else POLICY_NEVER


def _resolve_keys(verify_keys: str | None, environ: Mapping[str, str], home: str) -> list[bytes]:
    """Resolve the §4.7.9 key set: the argument, the variable, then the file."""
    if verify_keys:
        source, load = "verify_keys", lambda: parse_verify_key_list(verify_keys)
    elif environ.get("PODIUM_SIGNATURE_VERIFY_KEY"):
        value = environ["PODIUM_SIGNATURE_VERIFY_KEY"]
        source, load = "PODIUM_SIGNATURE_VERIFY_KEY", lambda: parse_verify_key_list(value)
    else:
        path = environ.get("PODIUM_SIGN_KEY_PATH") or _default_key_path(home)
        source, load = f"key file {path} (PODIUM_SIGN_KEY_PATH)", lambda: _read_key_file(path)
    try:
        return load()
    except RegistryError as exc:
        raise _error(
            CODE_UNAVAILABLE,
            f"{source}: {exc.message}; supply the verification material or set "
            "PODIUM_VERIFY_SIGNATURES=never",
        ) from exc


def _read_key_file(path: str) -> list[bytes]:
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except (OSError, UnicodeDecodeError) as exc:
        raise _error(CODE_UNAVAILABLE, f"read: {exc}") from exc
    return parse_key_file(text)


def resolve_verification(
    explicit: str | None,
    verify_keys: str | None,
    environ: Mapping[str, str],
    cwd: str,
    home: str,
) -> Verification:
    """Resolve a client's policy and, under ``always``, its key set.

    Under ``always`` it also confirms that the ``podium-sdk[verify]`` extra
    imports. A key or extra failure raises ``RegistryError`` with
    ``config.signature_provider_unavailable``. Empty strings count as unset.

    Spec: §4.7.9, §7.6.3
    """
    policy = resolve_policy(explicit, verify_keys, environ, cwd, home)
    if policy == POLICY_NEVER:
        return Verification(POLICY_NEVER)
    keys = _resolve_keys(verify_keys, environ, home)
    _load_ed25519()
    return Verification(POLICY_ALWAYS, tuple(keys))


def check_delivery(served: Served, verification: Verification) -> None:
    """Run the §4.7.10 step 7 comparison and apply the §4.7.9 policy.

    The comparison runs first and under every policy, so a record whose bytes
    were altered reports ``materialize.content_hash_mismatch`` whatever its
    signature.

    Spec: §4.7.10
    """
    if not served.hash:
        raise _malformed("the response carries no delivery_hash")
    got = delivery_hash(served.record)
    if got != served.hash:
        raise _malformed(f"recomputed delivery hash {got} does not match served {served.hash}")
    if verification.policy == POLICY_NEVER:
        return
    if not served.signature:
        raise _error(CODE_SIGNATURE_MISSING, f'policy "{verification.policy}" requires a signature')
    verify_envelope(served.signature, served.hash, list(verification.keys))
