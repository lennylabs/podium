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
});
