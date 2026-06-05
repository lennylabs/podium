import { describe, expect, it } from "vitest";

import {
  BatchResult,
  RegistryError,
  RegistryReadOnly,
  registryErrorFromEnvelope,
} from "./index";

describe("registry error surface (§6.10 / §13.2.1)", () => {
  // spec: §13.2.1 — a write rejected with the registry.read_only envelope
  // surfaces as RegistryReadOnly, which is also a RegistryError.
  it("maps registry.read_only to RegistryReadOnly", () => {
    const err = registryErrorFromEnvelope({
      code: "registry.read_only",
      message: "registry is in read-only mode",
      retryable: true,
    });
    expect(err).toBeInstanceOf(RegistryReadOnly);
    expect(err).toBeInstanceOf(RegistryError);
    expect(err.code).toBe("registry.read_only");
    expect(err.retryable).toBe(true);
  });

  // spec: §6.10 — a non-read-only envelope maps to the base RegistryError
  // and is not a RegistryReadOnly.
  it("maps other codes to the base RegistryError", () => {
    const err = registryErrorFromEnvelope({ code: "auth.forbidden", message: "nope" });
    expect(err).toBeInstanceOf(RegistryError);
    expect(err).not.toBeInstanceOf(RegistryReadOnly);
    expect(err.code).toBe("auth.forbidden");
  });

  // spec: §13.2.1 / §7.6.2 — a batch item rejected with registry.read_only
  // re-raises RegistryReadOnly from materialize().
  it("re-raises RegistryReadOnly from a batch error item", async () => {
    const result = new BatchResult({
      id: "finance/x",
      status: "error",
      error: { code: "registry.read_only", message: "read-only" },
    });
    await expect(result.materialize("/tmp/should-not-write")).rejects.toBeInstanceOf(
      RegistryReadOnly,
    );
  });

  // spec: §6.10 — the envelope parser captures the machine-readable
  // details map and the operator remediation hint so callers can read the full
  // envelope, not only code/message/retryable.
  it("captures details and suggested_action from the envelope", () => {
    const err = registryErrorFromEnvelope({
      code: "auth.untrusted_runtime",
      message: "Runtime is not registered.",
      details: { runtime_iss: "managed-runtime-x" },
      retryable: false,
      suggested_action: "Register the runtime's signing key.",
    });
    expect(err.details).toEqual({ runtime_iss: "managed-runtime-x" });
    expect(err.suggestedAction).toBe("Register the runtime's signing key.");
  });

  // spec: §6.10 — a registry.read_only envelope threads details and
  // suggested_action through the RegistryReadOnly subclass too.
  it("threads details and suggested_action through RegistryReadOnly", () => {
    const err = registryErrorFromEnvelope({
      code: "registry.read_only",
      message: "read-only",
      details: { since: "2026-01-01" },
      suggested_action: "Retry after maintenance.",
    });
    expect(err).toBeInstanceOf(RegistryReadOnly);
    expect(err.details).toEqual({ since: "2026-01-01" });
    expect(err.suggestedAction).toBe("Retry after maintenance.");
  });

  // spec: §6.10 — when the registry omits details/suggested_action they default
  // to an empty map and empty string rather than undefined.
  it("defaults details and suggested_action when absent", () => {
    const err = registryErrorFromEnvelope({ code: "auth.forbidden", message: "no" });
    expect(err.details).toEqual({});
    expect(err.suggestedAction).toBe("");
  });

  // spec: §6.10 — a batch error item carries the full envelope, which
  // materialize() re-raises with details and suggested_action intact.
  it("re-raises the full envelope from a batch error item", async () => {
    const result = new BatchResult({
      id: "finance/x",
      status: "error",
      error: {
        code: "auth.untrusted_runtime",
        message: "Runtime is not registered.",
        details: { runtime_iss: "managed-runtime-x" },
        suggested_action: "Register the runtime's signing key.",
      },
    });
    const err = await result.materialize("/tmp/should-not-write").catch((e: RegistryError) => e);
    expect(err).toBeInstanceOf(RegistryError);
    expect((err as RegistryError).details).toEqual({ runtime_iss: "managed-runtime-x" });
    expect((err as RegistryError).suggestedAction).toBe(
      "Register the runtime's signing key.",
    );
  });
});
