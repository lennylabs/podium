"""The §6.10 error type the SDK raises."""

from __future__ import annotations

from typing import Any


class RegistryError(Exception):
    """Raised when the registry returns a structured error envelope (§6.10)."""

    def __init__(
        self,
        code: str,
        message: str,
        retryable: bool = False,
        *,
        details: dict[str, Any] | None = None,
        suggested_action: str = "",
    ) -> None:
        self.code = code
        self.message = message
        self.retryable = retryable
        # spec: §6.10 — the full envelope carries a machine-readable details map
        # (for example {"runtime_iss": ...}) and an operator remediation hint.
        # Callers read both off the exception; they default to an
        # empty map and empty string when the registry omits them.
        self.details: dict[str, Any] = details or {}
        self.suggested_action = suggested_action
        super().__init__(f"{code}: {message}")
