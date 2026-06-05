"""Tests for the §6.10 / §13.2.1 SDK error surface."""

import os
import sys
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from podium import RegistryError, RegistryReadOnly  # noqa: E402
from podium.client import (  # noqa: E402
    _batch_result_from,
    _registry_error_from_envelope,
)


class RegistryReadOnlyTest(unittest.TestCase):
    # spec: §13.2.1 — a write rejected with the registry.read_only envelope
    # surfaces as RegistryReadOnly, which is also a RegistryError.
    def test_read_only_envelope_raises_registry_read_only(self):
        env = {
            "code": "registry.read_only",
            "message": "registry is in read-only mode",
            "retryable": True,
        }
        err = _registry_error_from_envelope(env)
        self.assertIsInstance(err, RegistryReadOnly)
        self.assertIsInstance(err, RegistryError)
        self.assertEqual(err.code, "registry.read_only")
        self.assertTrue(err.retryable)

    # spec: §6.10 — a non-read-only envelope maps to the base RegistryError
    # and is not a RegistryReadOnly.
    def test_other_envelope_is_base_registry_error(self):
        err = _registry_error_from_envelope({"code": "auth.forbidden", "message": "nope"})
        self.assertIsInstance(err, RegistryError)
        self.assertNotIsInstance(err, RegistryReadOnly)
        self.assertEqual(err.code, "auth.forbidden")

    # spec: §13.2.1 / §7.6.2 — a batch item rejected with registry.read_only
    # carries a RegistryReadOnly that materialize() re-raises.
    def test_batch_error_item_carries_registry_read_only(self):
        result = _batch_result_from(
            {
                "id": "finance/x",
                "status": "error",
                "error": {"code": "registry.read_only", "message": "read-only"},
            }
        )
        self.assertEqual(result.status, "error")
        self.assertIsInstance(result.error, RegistryReadOnly)
        with self.assertRaises(RegistryReadOnly):
            result.materialize("/tmp/should-not-write")

    # spec: §6.10 — the envelope parser captures the machine-readable
    # details map and the operator remediation hint so callers can read the full
    # envelope, not only code/message/retryable.
    def test_envelope_captures_details_and_suggested_action(self):
        err = _registry_error_from_envelope(
            {
                "code": "auth.untrusted_runtime",
                "message": "Runtime is not registered.",
                "details": {"runtime_iss": "managed-runtime-x"},
                "retryable": False,
                "suggested_action": "Register the runtime's signing key.",
            }
        )
        self.assertEqual(err.details, {"runtime_iss": "managed-runtime-x"})
        self.assertEqual(err.suggested_action, "Register the runtime's signing key.")

    # spec: §6.10 — a registry.read_only envelope threads details and
    # suggested_action through the RegistryReadOnly subclass too.
    def test_read_only_envelope_threads_full_envelope(self):
        err = _registry_error_from_envelope(
            {
                "code": "registry.read_only",
                "message": "read-only",
                "details": {"since": "2026-01-01"},
                "suggested_action": "Retry after maintenance.",
            }
        )
        self.assertIsInstance(err, RegistryReadOnly)
        self.assertEqual(err.details, {"since": "2026-01-01"})
        self.assertEqual(err.suggested_action, "Retry after maintenance.")

    # spec: §6.10 — when the registry omits details/suggested_action they
    # default to an empty map and empty string rather than None.
    def test_envelope_defaults_details_and_suggested_action(self):
        err = _registry_error_from_envelope({"code": "auth.forbidden", "message": "no"})
        self.assertEqual(err.details, {})
        self.assertEqual(err.suggested_action, "")

    # spec: §6.10 — a batch error item carries the full envelope, which
    # materialize() re-raises with details and suggested_action intact.
    def test_batch_error_item_carries_full_envelope(self):
        result = _batch_result_from(
            {
                "id": "finance/x",
                "status": "error",
                "error": {
                    "code": "auth.untrusted_runtime",
                    "message": "Runtime is not registered.",
                    "details": {"runtime_iss": "managed-runtime-x"},
                    "suggested_action": "Register the runtime's signing key.",
                },
            }
        )
        with self.assertRaises(RegistryError) as ctx:
            result.materialize("/tmp/should-not-write")
        self.assertEqual(ctx.exception.details, {"runtime_iss": "managed-runtime-x"})
        self.assertEqual(
            ctx.exception.suggested_action, "Register the runtime's signing key."
        )


if __name__ == "__main__":
    unittest.main()
