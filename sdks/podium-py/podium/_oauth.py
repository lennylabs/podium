"""OAuth 2.0 Device Authorization Grant for the SDK (spec §6.3, §7.7).

Implements RFC 8628, the flow ``oauth-device-code`` prescribes for hosts
that cannot complete a browser redirect. spec §6.3 exposes the flow as a
pair of calls. The start call discovers the IdP from the registry's RFC 8414
metadata, performs the device-authorization request, and returns a
single-use ``PendingLogin`` handle. The finish call polls the token endpoint
until the user completes the flow, the IdP denies it, the code expires, the
caller cancels, or the timeout elapses. ``Client.login()`` composes the two
calls. Every failure raises ``DeviceCodeError`` with a ``reason``.
"""

from __future__ import annotations

import json
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Callable, Literal

# spec §6.3 / §7.7 — the finish call polls until the user completes the flow
# or a 10-minute timeout, measured from the finish call, elapses.
DEFAULT_TIMEOUT = 600.0

# spec §6.3 — the SDK-local failure reasons of the device-code flow. They are
# not §6.10 error codes and never reach the registry.
DeviceCodeErrorReason = Literal[
    "denied", "expired", "timeout", "cancelled", "consumed", "failed"
]
REASON_DENIED: DeviceCodeErrorReason = "denied"
REASON_EXPIRED: DeviceCodeErrorReason = "expired"
REASON_TIMEOUT: DeviceCodeErrorReason = "timeout"
REASON_CANCELLED: DeviceCodeErrorReason = "cancelled"
REASON_CONSUMED: DeviceCodeErrorReason = "consumed"
REASON_FAILED: DeviceCodeErrorReason = "failed"


class DeviceCodeError(Exception):
    """Raised when the device-code flow fails.

    ``reason`` names the failure so calling code branches on it rather than on
    the message: ``denied``, ``expired``, ``timeout``, ``cancelled``,
    ``consumed``, or ``failed``. ``failed`` covers discovery,
    device-authorization, transport, and unrecognized IdP errors.
    """

    def __init__(
        self, message: str, reason: DeviceCodeErrorReason = REASON_FAILED
    ) -> None:
        super().__init__(message)
        self.reason: DeviceCodeErrorReason = reason


@dataclass
class DeviceAuth:
    device_code: str
    user_code: str
    verification_uri: str
    verification_uri_complete: str
    interval: float
    expires_in: float
    # Clock reading taken before the device-authorization request, so the
    # code's lifetime runs from the start call and is never over-counted.
    issued_at: float


@dataclass
class Tokens:
    access_token: str
    refresh_token: str = ""
    id_token: str = ""
    token_type: str = "Bearer"


def discover_idp(
    registry: str, *, opener: Callable[[urllib.request.Request], object] | None = None
) -> tuple[str, str]:
    """Return (device_authorization_endpoint, token_endpoint) for a registry.

    spec §7.7 — the registry exposes RFC 8414 authorization-server
    metadata at ``/.well-known/oauth-authorization-server``.
    """
    open_url = opener or urllib.request.urlopen
    url = registry.rstrip("/") + "/.well-known/oauth-authorization-server"
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with open_url(req) as resp:  # type: ignore[operator]
            meta = json.loads(resp.read())
    except (OSError, json.JSONDecodeError) as exc:
        # urllib.error.URLError and HTTPError are OSError subclasses.
        raise DeviceCodeError(f"IdP discovery failed: {exc}") from exc
    device = meta.get("device_authorization_endpoint", "")
    if not device:
        raise DeviceCodeError("registry metadata has no device_authorization_endpoint")
    return device, meta.get("token_endpoint", "")


def _post_form(
    url: str, form: dict[str, str], opener: Callable[[urllib.request.Request], object]
) -> dict[str, object]:
    data = urllib.parse.urlencode(form).encode()
    req = urllib.request.Request(
        url,
        data=data,
        headers={
            "Content-Type": "application/x-www-form-urlencoded",
            "Accept": "application/json",
        },
        method="POST",
    )
    try:
        with opener(req) as resp:  # type: ignore[operator]
            return json.loads(resp.read())
    except urllib.error.HTTPError as exc:
        # RFC 8628 §3.5 — pending/slow_down arrive as 400 with an error body.
        try:
            return json.loads(exc.read())
        except Exception:  # noqa: BLE001 - opaque non-JSON body
            raise DeviceCodeError(f"HTTP {exc.code}: {exc.reason}") from exc
    except (OSError, json.JSONDecodeError) as exc:
        # A refused connection, a reset, or a 200 with a non-JSON body.
        raise DeviceCodeError(f"request to {url} failed: {exc}") from exc


def initiate(
    device_url: str,
    client_id: str,
    scopes: list[str],
    audience: str,
    *,
    opener: Callable[[urllib.request.Request], object] | None = None,
    clock: Callable[[], float] = time.monotonic,
) -> DeviceAuth:
    """Perform the device-authorization request (RFC 8628 §3.1)."""
    open_url = opener or urllib.request.urlopen
    # spec §6.3 — the code's lifetime runs from the start call.
    issued_at = clock()
    form = {"client_id": client_id}
    if scopes:
        form["scope"] = " ".join(scopes)
    if audience:
        form["audience"] = audience
    body = _post_form(device_url, form, open_url)
    if not body.get("device_code"):
        raise DeviceCodeError(f"device authorization failed: {body}")
    return DeviceAuth(
        device_code=str(body.get("device_code", "")),
        user_code=str(body.get("user_code", "")),
        verification_uri=str(body.get("verification_uri", "")),
        verification_uri_complete=str(body.get("verification_uri_complete", "")),
        # RFC 8628 §3.2 — interval defaults to 5s only when absent; an
        # explicit 0 means poll without delay.
        interval=float(5 if body.get("interval") is None else body.get("interval")),
        expires_in=float(
            DEFAULT_TIMEOUT
            if body.get("expires_in") is None
            else body.get("expires_in")
        ),
        issued_at=issued_at,
    )


def poll(
    token_url: str,
    client_id: str,
    auth: DeviceAuth,
    *,
    timeout: float = DEFAULT_TIMEOUT,
    opener: Callable[[urllib.request.Request], object] | None = None,
    sleep: Callable[[float], None] | None = None,
    clock: Callable[[], float] = time.monotonic,
    cancel: threading.Event | None = None,
) -> Tokens:
    """Poll the token endpoint until completion or a bound (RFC 8628 §3.4).

    spec §6.3 — the timeout runs from this call and the code's lifetime from
    ``auth.issued_at``; polling ends at the earlier bound. Cancellation takes
    effect at the next check and does not interrupt an in-flight request.
    """
    open_url = opener or urllib.request.urlopen
    interval = max(auth.interval, 0.0)
    timeout_at = clock() + timeout
    expires_at = auth.issued_at + auth.expires_in
    wait = _waiter(sleep, cancel)

    def _check() -> None:
        if cancel is not None and cancel.is_set():
            raise DeviceCodeError("login cancelled", REASON_CANCELLED)
        now = clock()
        if now >= expires_at:
            raise DeviceCodeError(
                "device code expired before the flow completed", REASON_EXPIRED
            )
        if now >= timeout_at:
            raise DeviceCodeError("login timed out", REASON_TIMEOUT)

    while True:
        _check()
        wait(interval)
        _check()
        body = _post_form(
            token_url,
            {
                "grant_type": "urn:ietf:params:oauth:grant-type:device_code",
                "device_code": auth.device_code,
                "client_id": client_id,
            },
            open_url,
        )
        error = body.get("error")
        if not error and body.get("access_token"):
            return Tokens(
                access_token=str(body["access_token"]),
                refresh_token=str(body.get("refresh_token", "")),
                id_token=str(body.get("id_token", "")),
                token_type=str(body.get("token_type", "Bearer")),
            )
        if error == "authorization_pending":
            continue
        if error == "slow_down":
            interval += 5
            continue
        if error == "expired_token":
            raise DeviceCodeError(
                "device code expired before the flow completed", REASON_EXPIRED
            )
        if error == "access_denied":
            raise DeviceCodeError("the authorization request was denied", REASON_DENIED)
        raise DeviceCodeError(f"token polling failed: {error or body}", REASON_FAILED)


def _waiter(
    sleep: Callable[[float], None] | None, cancel: threading.Event | None
) -> Callable[[float], None]:
    """Choose the wait between token requests.

    An injected ``sleep`` wins so tests control time. Otherwise a cancel event
    makes the wait return as soon as the caller cancels.
    """
    if sleep is not None:
        return sleep
    if cancel is not None:
        event = cancel

        def _wait(seconds: float) -> None:
            event.wait(seconds)

        return _wait
    return time.sleep


class PendingLogin:
    """A started device-code login awaiting ``Client.finish_login``.

    spec §6.3 — the handle carries what a UI shows or schedules on: the
    verification URI, the complete verification URI when the IdP supplies
    one, the user code, the code's lifetime in seconds, and the initial poll
    interval in seconds. The device code is a polling credential and stays
    private. A handle is single-use: the first finish call consumes it
    whatever its outcome.
    """

    def __init__(
        self,
        auth: DeviceAuth,
        token_url: str,
        client_id: str,
        *,
        opener: Callable[[urllib.request.Request], object] | None = None,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        self._auth = auth
        self._token_url = token_url
        self._client_id = client_id
        self._opener = opener
        self._clock = clock
        self._consumed = False
        # Guards _consumed so that of two concurrent finish calls exactly one
        # proceeds.
        self._lock = threading.Lock()

    @property
    def verification_uri(self) -> str:
        return self._auth.verification_uri

    @property
    def verification_uri_complete(self) -> str | None:
        return self._auth.verification_uri_complete or None

    @property
    def user_code(self) -> str:
        return self._auth.user_code

    @property
    def expires_in(self) -> float:
        return self._auth.expires_in

    @property
    def interval(self) -> float:
        return self._auth.interval

    def _consume(self) -> None:
        """Mark the handle finished, or raise ``consumed`` when it already is."""
        with self._lock:
            if self._consumed:
                raise DeviceCodeError("login handle already finished", REASON_CONSUMED)
            self._consumed = True

    def __repr__(self) -> str:
        return (
            f"PendingLogin(verification_uri={self.verification_uri!r}, "
            f"verification_uri_complete={self.verification_uri_complete!r}, "
            f"user_code={self.user_code!r}, expires_in={self.expires_in!r}, "
            f"interval={self.interval!r})"
        )
