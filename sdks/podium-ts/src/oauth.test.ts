// Unit tests for the device-code module: issue-time anchoring, the
// abortable sleep and fetch, DeviceCodeError reasons, and the single-use
// PendingLogin handle (§6.3).

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  createPendingLogin,
  DeviceAuth,
  DeviceCodeError,
  discoverIdp,
  finishPending,
  initiate,
  PendingLogin,
} from "./oauth.js";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status });
}

function deviceAuth(overrides: Partial<DeviceAuth> = {}): DeviceAuth {
  return {
    deviceCode: "dev-123",
    userCode: "WXYZ-1234",
    verificationUri: "http://idp/activate",
    verificationUriComplete: "",
    intervalMs: 0,
    expiresInMs: 600_000,
    issuedAtMs: Date.now(),
    ...overrides,
  };
}

// tokenFetcher replies to each token request with the next scripted body
// and records how many requests arrived.
function tokenFetcher(replies: Array<[unknown, number]>): typeof fetch & { calls: number } {
  const f = (async () => {
    f.calls += 1;
    const [body, status] = replies[Math.min(f.calls, replies.length) - 1];
    return json(body, status);
  }) as unknown as typeof fetch & { calls: number };
  f.calls = 0;
  return f;
}

async function rejection(p: Promise<unknown>): Promise<DeviceCodeError> {
  try {
    await p;
  } catch (err) {
    expect(err).toBeInstanceOf(DeviceCodeError);
    return err as DeviceCodeError;
  }
  throw new Error("expected a rejection");
}

const pending = (body: unknown = { error: "authorization_pending" }): [unknown, number] => [
  body,
  400,
];
const granted: [unknown, number] = [{ access_token: "tok-abc", token_type: "Bearer" }, 200];

afterEach(() => {
  vi.useRealTimers();
});

describe("DeviceCodeError", () => {
  // Spec: §6.3 — an error with no explicit reason carries reason failed.
  it("defaults the reason to failed and carries the cause", () => {
    const cause = new Error("boom");
    const err = new DeviceCodeError("x", undefined, { cause });
    expect(err.reason).toBe("failed");
    expect(err.cause).toBe(cause);
    expect(err.name).toBe("DeviceCodeError");
  });
});

describe("discoverIdp", () => {
  // Spec: §6.3 — a discovery transport failure maps to reason failed with
  // the original rejection as cause.
  it("wraps a fetch rejection as failed", async () => {
    const cause = new TypeError("network down");
    const err = await rejection(
      discoverIdp("http://reg", (async () => {
        throw cause;
      }) as typeof fetch),
    );
    expect(err.reason).toBe("failed");
    expect(err.cause).toBe(cause);
  });

  // Spec: §6.3 — a non-JSON discovery body maps to reason failed.
  it("wraps a non-JSON body as failed", async () => {
    const err = await rejection(
      discoverIdp("http://reg", (async () => new Response("not json")) as typeof fetch),
    );
    expect(err.reason).toBe("failed");
    expect(err.cause).toBeDefined();
  });

  // Spec: §6.3 — a non-2xx metadata reply keeps its message and reason failed.
  it("reports a metadata HTTP error as failed", async () => {
    const err = await rejection(
      discoverIdp("http://reg/", (async () => json({}, 404)) as typeof fetch),
    );
    expect(err.message).toBe("registry metadata HTTP 404");
    expect(err.reason).toBe("failed");
  });
});

describe("initiate", () => {
  // Spec: §6.3 — code expiry runs from the start call; the issue time is
  // read before the device-authorization request.
  it("records the issue time before the request", async () => {
    const events: string[] = [];
    const fetcher = (async () => {
      events.push("post");
      return json({ device_code: "d", user_code: "u", verification_uri: "v", expires_in: 30 });
    }) as typeof fetch;
    const auth = await initiate("http://idp/device", "cli", ["openid"], "", fetcher, () => {
      events.push("now");
      return 1234;
    });
    expect(events).toEqual(["now", "post"]);
    expect(auth.issuedAtMs).toBe(1234);
    expect(auth.expiresInMs).toBe(30_000);
    expect(auth.intervalMs).toBe(5000);
  });

  // Spec: §6.3 — a device-authorization transport failure maps to failed.
  it("wraps a fetch rejection as failed", async () => {
    const cause = new TypeError("refused");
    const err = await rejection(
      initiate("http://idp/device", "cli", [], "", (async () => {
        throw cause;
      }) as typeof fetch),
    );
    expect(err.reason).toBe("failed");
    expect(err.cause).toBe(cause);
  });

  // Spec: §6.3 — a non-JSON reply keeps the HTTP <status> message with reason failed.
  it("keeps the HTTP status message for a non-JSON reply", async () => {
    const err = await rejection(
      initiate("http://idp/device", "cli", [], "aud", (async () =>
        new Response("<html>", { status: 502 })) as typeof fetch),
    );
    expect(err.message).toBe("HTTP 502");
    expect(err.reason).toBe("failed");
    expect(err.cause).toBeDefined();
  });
});

describe("PendingLogin", () => {
  // Spec: §6.3 — the handle exposes the display fields and never the device code.
  it("exposes only the display fields", () => {
    const p = createPendingLogin(deviceAuth(), "http://idp/token", "cli", tokenFetcher([granted]), Date.now);
    expect(p).toBeInstanceOf(PendingLogin);
    expect(p.verificationUri).toBe("http://idp/activate");
    expect(p.verificationUriComplete).toBeUndefined();
    expect(p.userCode).toBe("WXYZ-1234");
    expect(p.expiresInMs).toBe(600_000);
    expect(p.intervalMs).toBe(0);
    expect(JSON.stringify(p)).not.toContain("dev-123");
  });

  // Spec: §6.3 — the complete verification URI is present when the IdP supplies one.
  it("carries the complete verification URI when supplied", () => {
    const p = createPendingLogin(
      deviceAuth({ verificationUriComplete: "http://idp/activate?code=W" }),
      "http://idp/token",
      "cli",
      tokenFetcher([granted]),
      Date.now,
    );
    expect(p.verificationUriComplete).toBe("http://idp/activate?code=W");
  });
});

describe("finishPending", () => {
  // Spec: §6.3 — the finish call polls until the user completes the flow.
  it("returns the tokens once the IdP grants them", async () => {
    const f = tokenFetcher([pending(), granted]);
    const p = createPendingLogin(deviceAuth(), "http://idp/token", "cli", f, Date.now);
    const tokens = await finishPending(p);
    expect(tokens).toEqual({
      accessToken: "tok-abc",
      refreshToken: "",
      idToken: "",
      tokenType: "Bearer",
    });
    expect(f.calls).toBe(2);
  });

  // Spec: §6.3 — a handle with no registered state fails and sends no request.
  it("rejects an unknown handle as failed", async () => {
    const err = await rejection(finishPending(new PendingLogin(deviceAuth())));
    expect(err.message).toBe("unknown login handle");
    expect(err.reason).toBe("failed");
  });

  // Spec: §6.3 — a handle is single-use; a second finish call fails with
  // reason consumed and sends no token request.
  it("rejects a second finish call as consumed", async () => {
    const f = tokenFetcher([granted]);
    const p = createPendingLogin(deviceAuth(), "http://idp/token", "cli", f, Date.now);
    await finishPending(p);
    const err = await rejection(finishPending(p));
    expect(err.reason).toBe("consumed");
    expect(err.message).toBe("login handle already finished");
    expect(f.calls).toBe(1);
  });

  // Spec: §6.3 — of two concurrent finish calls exactly one polls.
  it("lets exactly one of two concurrent finish calls proceed", async () => {
    const f = tokenFetcher([granted]);
    const p = createPendingLogin(deviceAuth(), "http://idp/token", "cli", f, Date.now);
    const [a, b] = await Promise.allSettled([finishPending(p), finishPending(p)]);
    expect(a.status).toBe("fulfilled");
    expect(b.status).toBe("rejected");
    expect(((b as PromiseRejectedResult).reason as DeviceCodeError).reason).toBe("consumed");
    expect(f.calls).toBe(1);
  });

  // Spec: §6.3 — a signal aborted before the finish call consumes the handle,
  // fails with reason cancelled, and sends no token request.
  it("cancels before any request when the signal is already aborted", async () => {
    const f = tokenFetcher([granted]);
    const p = createPendingLogin(deviceAuth(), "http://idp/token", "cli", f, Date.now);
    const ctl = new AbortController();
    ctl.abort("user closed the dialog");
    const err = await rejection(finishPending(p, { signal: ctl.signal }));
    expect(err.reason).toBe("cancelled");
    expect(err.message).toBe("login cancelled");
    expect(err.cause).toBe("user closed the dialog");
    expect(f.calls).toBe(0);
    expect((await rejection(finishPending(p))).reason).toBe("consumed");
  });

  // Spec: §6.3 — an abort during the wait clears the timer and fails with
  // reason cancelled before the next token request.
  it("cancels during the wait between requests", async () => {
    vi.useFakeTimers();
    const f = tokenFetcher([pending(), granted]);
    const p = createPendingLogin(
      deviceAuth({ intervalMs: 5000, issuedAtMs: Date.now() }),
      "http://idp/token",
      "cli",
      f,
      () => Date.now(),
    );
    const ctl = new AbortController();
    const done = rejection(finishPending(p, { signal: ctl.signal }));
    await vi.advanceTimersByTimeAsync(5000);
    expect(f.calls).toBe(1);
    ctl.abort("stop");
    const err = await done;
    expect(err.reason).toBe("cancelled");
    expect(err.cause).toBe("stop");
    expect(vi.getTimerCount()).toBe(0);
    expect(f.calls).toBe(1);
  });

  // Spec: §6.3 — an abort while the token request is in flight fails with
  // reason cancelled.
  it("cancels an in-flight token request", async () => {
    const ctl = new AbortController();
    const fetcher = ((_url: string, init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => reject(new Error("aborted")));
        ctl.abort("stop");
      })) as unknown as typeof fetch;
    const p = createPendingLogin(deviceAuth(), "http://idp/token", "cli", fetcher, Date.now);
    const err = await rejection(finishPending(p, { signal: ctl.signal }));
    expect(err.reason).toBe("cancelled");
    expect(err.cause).toBe("stop");
  });

  // Spec: §6.3 — an abort after the headers arrive rejects the body read,
  // which also maps to cancelled.
  it("cancels during the response-body read", async () => {
    const ctl = new AbortController();
    const fetcher = (async () => {
      ctl.abort("stop");
      return {
        status: 200,
        json: () => Promise.reject(new Error("body aborted")),
      } as unknown as Response;
    }) as typeof fetch;
    const p = createPendingLogin(deviceAuth(), "http://idp/token", "cli", fetcher, Date.now);
    const err = await rejection(finishPending(p, { signal: ctl.signal }));
    expect(err.reason).toBe("cancelled");
    expect(err.cause).toBe("stop");
  });

  // Spec: §6.3 — a token-request transport failure that is not a
  // cancellation maps to failed with cause.
  it("reports a token-request transport failure as failed", async () => {
    const cause = new TypeError("reset");
    const fetcher = (async () => {
      throw cause;
    }) as typeof fetch;
    const p = createPendingLogin(deviceAuth(), "http://idp/token", "cli", fetcher, Date.now);
    const err = await rejection(finishPending(p, { signal: new AbortController().signal }));
    expect(err.reason).toBe("failed");
    expect(err.cause).toBe(cause);
  });

  // Spec: §6.3 — code expiry runs from the start call; a finish call after
  // the lifetime elapsed fails locally with reason expired.
  it("fails a late finish call as expired without a request", async () => {
    const f = tokenFetcher([granted]);
    let clock = 1_000;
    const p = createPendingLogin(
      deviceAuth({ issuedAtMs: 1_000, expiresInMs: 30_000 }),
      "http://idp/token",
      "cli",
      f,
      () => clock,
    );
    clock = 31_000;
    const err = await rejection(finishPending(p));
    expect(err.reason).toBe("expired");
    expect(err.message).toBe("device code expired before the flow completed");
    expect(f.calls).toBe(0);
  });

  // Spec: §6.3 — the lifetime elapsing during polling, before the timeout,
  // ends with reason expired and no further request.
  it("ends with expired when the lifetime elapses during polling", async () => {
    let clock = 0;
    const f = (async () => {
      clock += 10_000;
      return json({ error: "authorization_pending" }, 400);
    }) as typeof fetch;
    const p = createPendingLogin(
      deviceAuth({ issuedAtMs: 0, expiresInMs: 25_000 }),
      "http://idp/token",
      "cli",
      f,
      () => clock,
    );
    const err = await rejection(finishPending(p, { timeoutMs: 600_000 }));
    expect(err.reason).toBe("expired");
    expect(clock).toBe(30_000);
  });

  // Spec: §6.3 — the timeout runs from the finish call; when it elapses
  // first, polling ends with reason timeout.
  it("ends with timeout when the timeout elapses first", async () => {
    let clock = 0;
    const f = (async () => {
      clock += 10_000;
      return json({ error: "authorization_pending" }, 400);
    }) as typeof fetch;
    const p = createPendingLogin(deviceAuth({ issuedAtMs: 0 }), "http://idp/token", "cli", f, () => clock);
    const err = await rejection(finishPending(p, { timeoutMs: 15_000 }));
    expect(err.reason).toBe("timeout");
    expect(err.message).toBe("login timed out");
  });

  // Spec: §6.3 — IdP replies map to reasons: expired_token to expired,
  // access_denied to denied, and an unrecognized error to failed.
  it.each([
    ["expired_token", "expired", "device code expired before the flow completed"],
    ["access_denied", "denied", "the authorization request was denied"],
    ["server_error", "failed", "token polling failed: server_error"],
  ])("maps the IdP error %s to reason %s", async (code, reason, message) => {
    const p = createPendingLogin(
      deviceAuth(),
      "http://idp/token",
      "cli",
      tokenFetcher([pending({ error: code })]),
      Date.now,
    );
    const err = await rejection(finishPending(p));
    expect(err.reason).toBe(reason);
    expect(err.message).toBe(message);
  });

  // Spec: §6.3 — slow_down grows the interval by 5 s and polling continues.
  it("adds 5 s to the interval on slow_down", async () => {
    vi.useFakeTimers();
    const f = tokenFetcher([pending({ error: "slow_down" }), granted]);
    const p = createPendingLogin(deviceAuth({ issuedAtMs: Date.now() }), "http://idp/token", "cli", f, () =>
      Date.now(),
    );
    const done = finishPending(p);
    await vi.advanceTimersByTimeAsync(0);
    expect(f.calls).toBe(1);
    await vi.advanceTimersByTimeAsync(4999);
    expect(f.calls).toBe(1);
    await vi.advanceTimersByTimeAsync(1);
    expect((await done).accessToken).toBe("tok-abc");
    expect(f.calls).toBe(2);
  });
});
