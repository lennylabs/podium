"""Unit tests for the device-code mechanics in podium._oauth (spec §6.3).

The tests drive the module through a fake opener and a fake clock, so no
socket or real time is involved. The Client-level pair is covered in
test_resolution_overlay_login.py.
"""

from __future__ import annotations

import io
import json
import threading
import urllib.error

import pytest

from podium import _oauth
from podium._oauth import DeviceCodeError, PendingLogin


class _Resp:
    def __init__(self, body: bytes) -> None:
        self._body = body

    def read(self) -> bytes:
        return self._body

    def __enter__(self) -> "_Resp":
        return self

    def __exit__(self, *exc: object) -> None:
        return None


class _Clock:
    def __init__(self) -> None:
        self.now = 1000.0

    def __call__(self) -> float:
        return self.now


def _opener(replies: list[object], calls: list[str]):
    """Return an opener that answers each request with the next reply.

    A reply is a dict (JSON 200), bytes (raw 200 body), or an exception.
    """

    def _open(req):
        calls.append(req.full_url)
        reply = replies.pop(0)
        if isinstance(reply, BaseException):
            raise reply
        if isinstance(reply, bytes):
            return _Resp(reply)
        return _Resp(json.dumps(reply).encode())

    return _open


def _http_error(status: int, body: bytes) -> urllib.error.HTTPError:
    return urllib.error.HTTPError("http://idp/x", status, "err", {}, io.BytesIO(body))  # type: ignore[arg-type]


def _auth(
    clock: _Clock, *, expires_in: float = 600.0, interval: float = 0.0
) -> _oauth.DeviceAuth:
    return _oauth.DeviceAuth(
        device_code="dev-secret",
        user_code="WXYZ-1234",
        verification_uri="https://idp/activate",
        verification_uri_complete="",
        interval=interval,
        expires_in=expires_in,
        issued_at=clock(),
    )


# Spec: §6.3 SDK — DeviceCodeError carries a reason, defaulting to failed.
def test_device_code_error_reason_default_and_explicit():
    assert DeviceCodeError("boom").reason == "failed"
    assert DeviceCodeError("no", "denied").reason == "denied"
    assert str(DeviceCodeError("no", "denied")) == "no"


# Spec: §6.3 SDK — the code's lifetime runs from the start call, so initiate
# records the clock before the device-authorization request.
def test_initiate_records_issued_at_before_request():
    clock = _Clock()
    calls: list[str] = []

    def _open(req):
        clock.now += 50  # the request itself takes time
        return _opener(
            [{"device_code": "d", "user_code": "u", "expires_in": 30}], calls
        )(req)

    auth = _oauth.initiate(
        "http://idp/device", "cid", ["openid"], "aud", opener=_open, clock=clock
    )
    assert auth.issued_at == 1000.0
    assert auth.expires_in == 30.0


@pytest.mark.parametrize(
    "reply,cause",
    [
        (urllib.error.URLError("refused"), urllib.error.URLError),
        (_http_error(404, b"not found"), urllib.error.HTTPError),
        (b"not json", json.JSONDecodeError),
    ],
)
# Spec: §6.3 SDK — discover_idp wraps transport and decode failures as reason
# failed with the original exception chained.
def test_discover_idp_wraps_failures(reply, cause):
    with pytest.raises(DeviceCodeError) as err:
        _oauth.discover_idp("http://reg", opener=_opener([reply], []))
    assert err.value.reason == "failed"
    assert isinstance(err.value.__cause__, cause)


# Spec: §6.3 SDK — metadata without a device endpoint is reason failed.
def test_discover_idp_missing_device_endpoint():
    with pytest.raises(DeviceCodeError) as err:
        _oauth.discover_idp("http://reg", opener=_opener([{"token_endpoint": "t"}], []))
    assert err.value.reason == "failed"
    device, token = _oauth.discover_idp(
        "http://reg",
        opener=_opener(
            [{"device_authorization_endpoint": "d", "token_endpoint": "t"}], []
        ),
    )
    assert (device, token) == ("d", "t")


# Spec: §6.3 SDK — _post_form keeps the RFC 8628 §3.5 JSON error body and
# wraps every other transport or decode failure as failed.
def test_post_form_error_mapping():
    pending = _http_error(400, b'{"error": "authorization_pending"}')
    assert _oauth._post_form("http://idp/t", {}, _opener([pending], [])) == {
        "error": "authorization_pending"
    }
    for reply, cause in [
        (_http_error(502, b"<html>"), urllib.error.HTTPError),
        (ConnectionRefusedError("refused"), OSError),
        (urllib.error.URLError("refused"), urllib.error.URLError),
        (b"<html>", json.JSONDecodeError),
    ]:
        with pytest.raises(DeviceCodeError) as err:
            _oauth._post_form("http://idp/t", {}, _opener([reply], []))
        assert err.value.reason == "failed"
        assert isinstance(err.value.__cause__, cause)


@pytest.mark.parametrize(
    "error,reason,message",
    [
        ("access_denied", "denied", "the authorization request was denied"),
        ("expired_token", "expired", "device code expired before the flow completed"),
        ("bogus", "failed", "token polling failed: bogus"),
    ],
)
# Spec: §6.3 SDK — poll maps each terminal IdP error to its reason.
def test_poll_idp_error_reasons(error, reason, message):
    clock = _Clock()
    with pytest.raises(DeviceCodeError) as err:
        _oauth.poll(
            "http://idp/t",
            "cid",
            _auth(clock),
            opener=_opener([{"error": error}], []),
            sleep=lambda s: None,
            clock=clock,
        )
    assert err.value.reason == reason
    assert str(err.value) == message


# Spec: §6.3 SDK — the code's lifetime ends polling with reason expired before
# the timeout, and an elapsed lifetime sends no token request.
def test_poll_expired_before_timeout_and_local_expiry():
    clock = _Clock()
    auth = _auth(clock, expires_in=30)
    calls: list[str] = []

    def _sleep(seconds: float) -> None:
        clock.now += 10

    with pytest.raises(DeviceCodeError) as err:
        _oauth.poll(
            "http://idp/t",
            "cid",
            auth,
            timeout=600,
            opener=_opener([{"error": "authorization_pending"}] * 10, calls),
            sleep=_sleep,
            clock=clock,
        )
    assert err.value.reason == "expired"
    sent = len(calls)
    assert sent >= 1
    with pytest.raises(DeviceCodeError) as err:
        _oauth.poll("http://idp/t", "cid", auth, opener=_opener([], calls), clock=clock)
    assert err.value.reason == "expired"
    assert len(calls) == sent


# Spec: §6.3 SDK — the timeout runs from the poll call, not from issue time.
def test_poll_timeout_runs_from_call():
    clock = _Clock()
    auth = _auth(clock, expires_in=600)
    clock.now += 100
    calls: list[str] = []

    def _sleep(seconds: float) -> None:
        clock.now += 20

    with pytest.raises(DeviceCodeError) as err:
        _oauth.poll(
            "http://idp/t",
            "cid",
            auth,
            timeout=50,
            opener=_opener([{"error": "authorization_pending"}] * 10, calls),
            sleep=_sleep,
            clock=clock,
        )
    assert err.value.reason == "timeout"
    assert str(err.value) == "login timed out"
    assert len(calls) >= 1


# Spec: §6.3 SDK — slow_down grows the interval by 5 s and success returns tokens.
def test_poll_slow_down_then_success():
    clock = _Clock()
    waits: list[float] = []
    tokens = _oauth.poll(
        "http://idp/t",
        "cid",
        _auth(clock),
        opener=_opener(
            [{"error": "slow_down"}, {"access_token": "tok", "refresh_token": "r"}], []
        ),
        sleep=waits.append,
        clock=clock,
    )
    assert waits == [0.0, 5.0]
    assert tokens.access_token == "tok"
    assert tokens.refresh_token == "r"


# Spec: §6.3 SDK — a set cancel event fails with reason cancelled before any
# token request, and a cancel during the wait stops further requests.
def test_poll_cancel_before_and_between_requests():
    clock = _Clock()
    cancel = threading.Event()
    cancel.set()
    calls: list[str] = []
    with pytest.raises(DeviceCodeError) as err:
        _oauth.poll(
            "http://idp/t",
            "cid",
            _auth(clock),
            opener=_opener([], calls),
            clock=clock,
            cancel=cancel,
        )
    assert err.value.reason == "cancelled"
    assert str(err.value) == "login cancelled"
    assert calls == []

    cancel = threading.Event()
    invocations = []

    def _sleep(seconds: float) -> None:
        invocations.append(seconds)
        if len(invocations) == 2:
            cancel.set()

    with pytest.raises(DeviceCodeError) as err:
        _oauth.poll(
            "http://idp/t",
            "cid",
            _auth(clock),
            opener=_opener([{"error": "authorization_pending"}] * 5, calls),
            sleep=_sleep,
            clock=clock,
            cancel=cancel,
        )
    assert err.value.reason == "cancelled"
    assert len(calls) == 1


# Spec: §6.3 SDK — with no injected sleep, the wait returns when the caller
# cancels from another thread.
def test_poll_cancel_wait_real_thread():
    cancel = threading.Event()
    clock = _Clock()
    auth = _auth(clock, interval=5)
    timer = threading.Timer(0.05, cancel.set)
    timer.start()
    try:
        with pytest.raises(DeviceCodeError) as err:
            _oauth.poll(
                "http://idp/t",
                "cid",
                auth,
                opener=_opener([], []),
                clock=clock,
                cancel=cancel,
            )
    finally:
        timer.cancel()
    assert err.value.reason == "cancelled"


# Spec: §6.3 SDK — the waiter falls back to time.sleep without sleep or cancel.
def test_waiter_selection():
    def injected(seconds: float) -> None:
        return None

    assert _oauth._waiter(injected, threading.Event()) is injected
    assert _oauth._waiter(None, None) is _oauth.time.sleep


# Spec: §6.3 SDK — the handle exposes the display fields only; the device code
# is absent from repr and verification_uri_complete is None when omitted.
def test_pending_login_fields_and_repr():
    clock = _Clock()
    auth = _auth(clock, expires_in=300, interval=5)
    handle = PendingLogin(auth, "http://idp/t", "cid", clock=clock)
    assert handle.verification_uri == "https://idp/activate"
    assert handle.verification_uri_complete is None
    assert handle.user_code == "WXYZ-1234"
    assert handle.expires_in == 300
    assert handle.interval == 5
    assert "dev-secret" not in repr(handle)
    assert "device_code" not in repr(handle)
    assert "WXYZ-1234" in repr(handle)
    auth.verification_uri_complete = "https://idp/activate?code=WXYZ"
    assert handle.verification_uri_complete == "https://idp/activate?code=WXYZ"
    with pytest.raises(AttributeError):
        handle.user_code = "other"  # type: ignore[misc]


# Spec: §6.3 SDK — a handle is single-use; of two concurrent consumers exactly
# one proceeds and the other raises reason consumed.
def test_pending_login_consume_once_concurrently():
    clock = _Clock()
    handle = PendingLogin(_auth(clock), "http://idp/t", "cid", clock=clock)
    barrier = threading.Barrier(2)
    outcomes: list[str] = []
    lock = threading.Lock()

    def _worker() -> None:
        barrier.wait()
        try:
            handle._consume()
            result = "ok"
        except DeviceCodeError as exc:
            result = exc.reason
        with lock:
            outcomes.append(result)

    threads = [threading.Thread(target=_worker) for _ in range(2)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert sorted(outcomes) == ["consumed", "ok"]
    with pytest.raises(DeviceCodeError) as err:
        handle._consume()
    assert err.value.reason == "consumed"
    assert str(err.value) == "login handle already finished"
