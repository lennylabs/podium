"""OAuth 2.0 Device Authorization Grant for the SDK (spec §6.3, §7.7).

Implements RFC 8628, the flow ``oauth-device-code`` prescribes for hosts
that cannot complete a browser redirect. ``Client.login()`` discovers the
IdP from the registry's RFC 8414 metadata, surfaces the verification URL
and user code, and polls the token endpoint until the user completes the
flow or a 10-minute deadline elapses.
"""

from __future__ import annotations

import json
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Callable

# spec §7.7 — login polls "until the user completes the flow or a
# 10-minute timeout elapses".
DEFAULT_TIMEOUT = 600.0


class DeviceCodeError(Exception):
    """Raised when the device-code flow fails (denied, expired, timeout)."""


@dataclass
class DeviceAuth:
    device_code: str
    user_code: str
    verification_uri: str
    verification_uri_complete: str
    interval: float
    expires_in: float


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
    with open_url(req) as resp:  # type: ignore[operator]
        meta = json.loads(resp.read())
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


def initiate(
    device_url: str,
    client_id: str,
    scopes: list[str],
    audience: str,
    *,
    opener: Callable[[urllib.request.Request], object] | None = None,
) -> DeviceAuth:
    open_url = opener or urllib.request.urlopen
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
            DEFAULT_TIMEOUT if body.get("expires_in") is None else body.get("expires_in")
        ),
    )


def poll(
    token_url: str,
    client_id: str,
    auth: DeviceAuth,
    *,
    timeout: float = DEFAULT_TIMEOUT,
    opener: Callable[[urllib.request.Request], object] | None = None,
    sleep: Callable[[float], None] = time.sleep,
    clock: Callable[[], float] = time.monotonic,
) -> Tokens:
    """Poll the token endpoint until completion or the deadline (RFC 8628 §3.4)."""
    open_url = opener or urllib.request.urlopen
    interval = max(auth.interval, 0.0)
    deadline = clock() + min(timeout, auth.expires_in)
    while True:
        if clock() >= deadline:
            raise DeviceCodeError("login timed out")
        sleep(interval)
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
            raise DeviceCodeError("device code expired before the flow completed")
        if error == "access_denied":
            raise DeviceCodeError("the authorization request was denied")
        raise DeviceCodeError(f"token polling failed: {error or body}")
