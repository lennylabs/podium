"""Podium Python SDK — thin HTTP client over the registry API (spec §7.6).

Stage 3 ships the package surface and the four meta-tool calls. Identity,
streaming subscriptions, and dependency walks land alongside their
respective phases.
"""

from .client import Client, DeviceCodeRequired, RegistryError

__all__ = ["Client", "DeviceCodeRequired", "RegistryError"]
__version__ = "0.0.0.dev0"
