// OAuth 2.0 Device Authorization Grant for the SDK (spec §6.3, §7.7).
//
// Implements RFC 8628, the flow `oauth-device-code` prescribes for hosts
// that cannot complete a browser redirect. Spec §6.3 splits the SDK flow
// into a non-blocking pair: the start call (createPendingLogin over
// initiate) returns a single-use PendingLogin carrying the verification URL
// and user code, and the finish call (finishPending) polls the token
// endpoint until the user completes the flow, the code expires, the
// timeout elapses, or the caller's AbortSignal fires. Client.login() is the
// composition of the pair.
//
// The handle's private state lives in a module-scoped WeakMap rather than
// in #private fields, because Client.finishLogin in index.ts cannot read
// #private fields of a class declared here. PendingLogin carries no methods:
// package.json has no exports map, so every module under src/ is
// importable, and a public consume or finish method would let a caller burn
// a handle.

// Spec: §6.3, §7.7 — the finish call polls until the user completes the
// flow or a 10-minute timeout elapses.
export const DEFAULT_TIMEOUT_MS = 600_000;

// Spec: §6.3 — the SDK-local failure reasons of the device-code flow. They
// are not §6.10 error codes and never reach the registry.
export type DeviceCodeErrorReason =
  | "denied"
  | "expired"
  | "timeout"
  | "cancelled"
  | "consumed"
  | "failed";

export class DeviceCodeError extends Error {
  readonly reason: DeviceCodeErrorReason;

  constructor(
    message: string,
    reason: DeviceCodeErrorReason = "failed",
    options?: { cause?: unknown },
  ) {
    super(message, options);
    this.name = "DeviceCodeError";
    this.reason = reason;
  }
}

export interface DeviceAuth {
  deviceCode: string;
  userCode: string;
  verificationUri: string;
  verificationUriComplete: string;
  intervalMs: number;
  expiresInMs: number;
  // Spec: §6.3 — code expiry runs from the start call. The time is taken
  // before the device-authorization request so the lifetime is never
  // over-counted.
  issuedAtMs: number;
}

export interface Tokens {
  accessToken: string;
  refreshToken: string;
  idToken: string;
  tokenType: string;
}

export async function discoverIdp(
  registry: string,
  fetcher: typeof fetch,
): Promise<{ deviceUrl: string; tokenUrl: string }> {
  const url = registry.replace(/\/$/, "") + "/.well-known/oauth-authorization-server";
  let resp: Response;
  try {
    resp = await fetcher(url, { headers: { Accept: "application/json" } });
  } catch (err) {
    throw new DeviceCodeError(`registry metadata request failed: ${String(err)}`, "failed", {
      cause: err,
    });
  }
  if (!resp.ok) throw new DeviceCodeError(`registry metadata HTTP ${resp.status}`);
  let meta: Record<string, string>;
  try {
    meta = (await resp.json()) as Record<string, string>;
  } catch (err) {
    throw new DeviceCodeError("registry metadata is not valid JSON", "failed", { cause: err });
  }
  const deviceUrl = meta.device_authorization_endpoint ?? "";
  if (!deviceUrl) {
    throw new DeviceCodeError("registry metadata has no device_authorization_endpoint");
  }
  return { deviceUrl, tokenUrl: meta.token_endpoint ?? "" };
}

function cancelledError(signal: AbortSignal): DeviceCodeError {
  return new DeviceCodeError("login cancelled", "cancelled", { cause: signal.reason });
}

async function postForm(
  url: string,
  form: Record<string, string>,
  fetcher: typeof fetch,
  signal?: AbortSignal,
): Promise<Record<string, unknown>> {
  let resp: Response;
  try {
    resp = await fetcher(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        Accept: "application/json",
      },
      body: new URLSearchParams(form).toString(),
      signal,
    });
  } catch (err) {
    if (signal?.aborted) throw cancelledError(signal);
    throw new DeviceCodeError(`request to ${url} failed: ${String(err)}`, "failed", {
      cause: err,
    });
  }
  // RFC 8628 §3.5 — pending/slow_down arrive as 400 with an error body, so a
  // non-2xx response still carries JSON the caller must inspect. An abort
  // that arrives after the headers resolves fetch and rejects this read, so
  // the read maps an aborted signal to cancelled as well.
  try {
    return (await resp.json()) as Record<string, unknown>;
  } catch (err) {
    if (signal?.aborted) throw cancelledError(signal);
    throw new DeviceCodeError(`HTTP ${resp.status}`, "failed", { cause: err });
  }
}

export async function initiate(
  deviceUrl: string,
  clientID: string,
  scopes: string[],
  audience: string,
  fetcher: typeof fetch,
  now: () => number = Date.now,
): Promise<DeviceAuth> {
  const form: Record<string, string> = { client_id: clientID };
  if (scopes.length) form.scope = scopes.join(" ");
  if (audience) form.audience = audience;
  const issuedAtMs = now();
  const body = await postForm(deviceUrl, form, fetcher);
  if (!body.device_code) throw new DeviceCodeError(`device authorization failed`);
  return {
    deviceCode: String(body.device_code),
    userCode: String(body.user_code ?? ""),
    verificationUri: String(body.verification_uri ?? ""),
    verificationUriComplete: String(body.verification_uri_complete ?? ""),
    // RFC 8628 §3.2 — interval defaults to 5s only when absent; an explicit
    // 0 means poll without delay.
    intervalMs: (body.interval == null ? 5 : Number(body.interval)) * 1000,
    expiresInMs:
      (body.expires_in == null ? DEFAULT_TIMEOUT_MS / 1000 : Number(body.expires_in)) * 1000,
    issuedAtMs,
  };
}

// sleep resolves after ms, or rejects with the cancelled error when signal
// aborts first. The timer is cleared on abort so a cancelled finish call
// leaves no pending timer behind. The caller runs check() synchronously
// before each sleep, so the signal is never already aborted on entry.
function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const onAbort = () => {
      clearTimeout(timer);
      reject(cancelledError(signal as AbortSignal));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

export async function poll(
  tokenUrl: string,
  clientID: string,
  auth: DeviceAuth,
  opts: { timeoutMs?: number; fetcher: typeof fetch; now?: () => number; signal?: AbortSignal },
): Promise<Tokens> {
  const { fetcher, signal } = opts;
  const now = opts.now ?? Date.now;
  let intervalMs = Math.max(auth.intervalMs, 0);
  // Spec: §6.3 — the timeout runs from the finish call and code expiry from
  // the start call; polling ends at the earlier bound.
  const timeoutAt = now() + (opts.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  const expiresAt = auth.issuedAtMs + auth.expiresInMs;
  const check = (): void => {
    if (signal?.aborted) throw cancelledError(signal);
    if (now() >= expiresAt) {
      throw new DeviceCodeError("device code expired before the flow completed", "expired");
    }
    if (now() >= timeoutAt) throw new DeviceCodeError("login timed out", "timeout");
  };
  for (;;) {
    check();
    await sleep(intervalMs, signal);
    check();
    const body = await postForm(
      tokenUrl,
      {
        grant_type: "urn:ietf:params:oauth:grant-type:device_code",
        device_code: auth.deviceCode,
        client_id: clientID,
      },
      fetcher,
      signal,
    );
    const error = body.error as string | undefined;
    if (!error && body.access_token) {
      return {
        accessToken: String(body.access_token),
        refreshToken: String(body.refresh_token ?? ""),
        idToken: String(body.id_token ?? ""),
        tokenType: String(body.token_type ?? "Bearer"),
      };
    }
    if (error === "authorization_pending") continue;
    if (error === "slow_down") {
      intervalMs += 5000;
      continue;
    }
    if (error === "expired_token") {
      throw new DeviceCodeError("device code expired before the flow completed", "expired");
    }
    if (error === "access_denied") {
      throw new DeviceCodeError("the authorization request was denied", "denied");
    }
    throw new DeviceCodeError(`token polling failed: ${error ?? "unknown"}`);
  }
}

// Spec: §6.3 — the handle a start call returns. It exposes only what a UI
// shows or schedules on; the device code and the polling configuration stay
// in pendingState.
export class PendingLogin {
  readonly verificationUri: string;
  readonly verificationUriComplete?: string;
  readonly userCode: string;
  readonly expiresInMs: number;
  readonly intervalMs: number;

  constructor(auth: DeviceAuth) {
    this.verificationUri = auth.verificationUri;
    this.verificationUriComplete =
      auth.verificationUriComplete === "" ? undefined : auth.verificationUriComplete;
    this.userCode = auth.userCode;
    this.expiresInMs = auth.expiresInMs;
    this.intervalMs = auth.intervalMs;
  }
}

interface PendingState {
  auth: DeviceAuth;
  tokenUrl: string;
  clientID: string;
  fetcher: typeof fetch;
  now: () => number;
  consumed: boolean;
}

const pendingState = new WeakMap<PendingLogin, PendingState>();

// createPendingLogin builds the handle for a completed device-authorization
// request and registers its private state.
export function createPendingLogin(
  auth: DeviceAuth,
  tokenUrl: string,
  clientID: string,
  fetcher: typeof fetch,
  now: () => number,
): PendingLogin {
  const pending = new PendingLogin(auth);
  pendingState.set(pending, { auth, tokenUrl, clientID, fetcher, now, consumed: false });
  return pending;
}

// Spec: §6.3 — finishPending is the finish call. A handle is single-use: the
// first call consumes it before any I/O, whatever the outcome, and a later
// call fails with reason consumed and sends no token request.
export async function finishPending(
  pending: PendingLogin,
  opts: { timeoutMs?: number; signal?: AbortSignal } = {},
): Promise<Tokens> {
  const state = pendingState.get(pending);
  if (!state) throw new DeviceCodeError("unknown login handle", "failed");
  if (state.consumed) throw new DeviceCodeError("login handle already finished", "consumed");
  // Set before the first await so two concurrent calls cannot both proceed.
  state.consumed = true;
  return poll(state.tokenUrl, state.clientID, state.auth, {
    timeoutMs: opts.timeoutMs ?? DEFAULT_TIMEOUT_MS,
    fetcher: state.fetcher,
    now: state.now,
    signal: opts.signal,
  });
}
