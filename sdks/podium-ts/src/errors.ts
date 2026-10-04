// The §6.10 registry error surface. It lives in its own module so the
// delivery check (delivery.ts) and the client (index.ts) both raise it without
// importing each other; index.ts re-exports every name.

export class RegistryError extends Error {
  constructor(
    public readonly code: string,
    message: string,
    public readonly retryable: boolean = false,
    // spec: §6.10 — the full envelope carries a machine-readable details map
    // (for example {runtime_iss: ...}) and an operator remediation hint.
    // Callers read both off the error; they default to an empty map
    // and empty string when the registry omits them.
    public readonly details: Record<string, unknown> = {},
    public readonly suggestedAction: string = "",
  ) {
    super(`${code}: ${message}`);
    this.name = "RegistryError";
  }
}

// RegistryReadOnly is thrown when a write is rejected because the
// registry is in §13.2.1 read-only mode (the §6.10 registry.read_only
// error code). It extends RegistryError, so callers that catch the base
// error keep working while callers that want to retry once the registry
// leaves read-only mode can catch this type specifically.
export class RegistryReadOnly extends RegistryError {
  constructor(
    message: string,
    retryable = false,
    details: Record<string, unknown> = {},
    suggestedAction = "",
  ) {
    super("registry.read_only", message, retryable, details, suggestedAction);
    this.name = "RegistryReadOnly";
  }
}

// registryErrorFromEnvelope builds the §6.10 error for a structured
// envelope, choosing the most specific subclass for the code.
// registry.read_only (§13.2.1) maps to RegistryReadOnly; every other code
// maps to the base RegistryError.
export function registryErrorFromEnvelope(env: {
  code?: unknown;
  message?: unknown;
  retryable?: unknown;
  details?: unknown;
  suggested_action?: unknown;
}): RegistryError {
  const code = (env.code as string) ?? "registry.unknown";
  const message = (env.message as string) ?? "";
  const retryable = Boolean(env.retryable);
  // spec: §6.10 — preserve the machine-readable details map and the operator
  // remediation hint so callers can read the full envelope.
  const details =
    env.details && typeof env.details === "object"
      ? (env.details as Record<string, unknown>)
      : {};
  const suggestedAction =
    typeof env.suggested_action === "string" ? env.suggested_action : "";
  if (code === "registry.read_only") {
    return new RegistryReadOnly(message, retryable, details, suggestedAction);
  }
  return new RegistryError(code, message, retryable, details, suggestedAction);
}
