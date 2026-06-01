// OAuth 2.0 Device Authorization Grant for the SDK (spec §6.3, §7.7).
//
// Implements RFC 8628, the flow `oauth-device-code` prescribes for hosts
// that cannot complete a browser redirect. Client.login() discovers the IdP
// from the registry's RFC 8414 metadata, surfaces the verification URL and
// user code, and polls the token endpoint until the user completes the flow
// or a 10-minute deadline elapses.

// spec §7.7 — login polls "until the user completes the flow or a 10-minute
// timeout elapses".
export const DEFAULT_TIMEOUT_MS = 600_000;

export class DeviceCodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DeviceCodeError";
  }
}

export interface DeviceAuth {
  deviceCode: string;
  userCode: string;
  verificationUri: string;
  verificationUriComplete: string;
  intervalMs: number;
  expiresInMs: number;
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
  const resp = await fetcher(url, { headers: { Accept: "application/json" } });
  if (!resp.ok) throw new DeviceCodeError(`registry metadata HTTP ${resp.status}`);
  const meta = (await resp.json()) as Record<string, string>;
  const deviceUrl = meta.device_authorization_endpoint ?? "";
  if (!deviceUrl) {
    throw new DeviceCodeError("registry metadata has no device_authorization_endpoint");
  }
  return { deviceUrl, tokenUrl: meta.token_endpoint ?? "" };
}

async function postForm(
  url: string,
  form: Record<string, string>,
  fetcher: typeof fetch,
): Promise<Record<string, unknown>> {
  const resp = await fetcher(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Accept: "application/json",
    },
    body: new URLSearchParams(form).toString(),
  });
  // RFC 8628 §3.5 — pending/slow_down arrive as 400 with an error body, so a
  // non-2xx response still carries JSON the caller must inspect.
  try {
    return (await resp.json()) as Record<string, unknown>;
  } catch {
    throw new DeviceCodeError(`HTTP ${resp.status}`);
  }
}

export async function initiate(
  deviceUrl: string,
  clientID: string,
  scopes: string[],
  audience: string,
  fetcher: typeof fetch,
): Promise<DeviceAuth> {
  const form: Record<string, string> = { client_id: clientID };
  if (scopes.length) form.scope = scopes.join(" ");
  if (audience) form.audience = audience;
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
  };
}

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

export async function poll(
  tokenUrl: string,
  clientID: string,
  auth: DeviceAuth,
  opts: { timeoutMs?: number; fetcher: typeof fetch; now?: () => number },
): Promise<Tokens> {
  const fetcher = opts.fetcher;
  const now = opts.now ?? Date.now;
  let intervalMs = Math.max(auth.intervalMs, 0);
  const deadline = now() + Math.min(opts.timeoutMs ?? DEFAULT_TIMEOUT_MS, auth.expiresInMs);
  for (;;) {
    if (now() >= deadline) throw new DeviceCodeError("login timed out");
    await sleep(intervalMs);
    const body = await postForm(
      tokenUrl,
      {
        grant_type: "urn:ietf:params:oauth:grant-type:device_code",
        device_code: auth.deviceCode,
        client_id: clientID,
      },
      fetcher,
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
      throw new DeviceCodeError("device code expired before the flow completed");
    }
    if (error === "access_denied") {
      throw new DeviceCodeError("the authorization request was denied");
    }
    throw new DeviceCodeError(`token polling failed: ${error ?? "unknown"}`);
  }
}
