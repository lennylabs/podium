// SDK registry resolution, overlay merge, and login.
// Spec: §7.5.2 / §13.10 (registry resolution), §6.4 / §6.4.1 (overlay),
// §6.3 / §7.7 / §14.8 (device-code login).

import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  Client,
  DeviceCodeError,
  PendingLogin,
  RegistryError,
  type DeviceCodeErrorReason,
} from "./index.js";
import { resolveRegistry } from "./config.js";
import { LocalOverlay, rrfFuse } from "./overlay.js";
import { isolateVerification } from "./test_support.js";

isolateVerification();

async function writeFileAt(path: string, body: string): Promise<void> {
  await mkdir(join(path, ".."), { recursive: true });
  await writeFile(path, body);
}

describe("registry resolution", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "podium-cfg-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it("PODIUM_REGISTRY wins over sync.yaml", async () => {
    await writeFileAt(join(dir, ".podium", "sync.yaml"), "defaults:\n  registry: https://from-file\n");
    expect(await resolveRegistry("https://from-env", dir, undefined)).toBe("https://from-env");
  });

  it("project-local > project-shared > global", async () => {
    const home = join(dir, "home");
    const ws = join(dir, "home", "proj");
    await writeFileAt(join(home, ".podium", "sync.yaml"), "defaults:\n  registry: https://global\n");
    await writeFileAt(join(ws, ".podium", "sync.yaml"), "defaults:\n  registry: https://shared\n");
    await writeFileAt(join(ws, ".podium", "sync.local.yaml"), "defaults:\n  registry: https://local\n");
    expect(await resolveRegistry(undefined, ws, home)).toBe("https://local");
    await rm(join(ws, ".podium", "sync.local.yaml"));
    expect(await resolveRegistry(undefined, ws, home)).toBe("https://shared");
    await rm(join(ws, ".podium", "sync.yaml"));
    expect(await resolveRegistry(undefined, ws, home)).toBe("https://global");
  });

  it("ignores an inline comment on the registry value", async () => {
    await writeFileAt(
      join(dir, ".podium", "sync.yaml"),
      "defaults:\n  registry: https://podium.acme.com   # prod\n",
    );
    expect(await resolveRegistry(undefined, dir, undefined)).toBe("https://podium.acme.com");
  });

  it("fromEnv throws config.no_registry when unset everywhere", async () => {
    const saved = { reg: process.env.PODIUM_REGISTRY, home: process.env.HOME };
    delete process.env.PODIUM_REGISTRY;
    process.env.HOME = join(dir, "empty");
    const cwd = process.cwd();
    process.chdir(dir);
    try {
      await expect(Client.fromEnv()).rejects.toMatchObject({ code: "config.no_registry" });
    } finally {
      process.chdir(cwd);
      if (saved.reg) process.env.PODIUM_REGISTRY = saved.reg;
      if (saved.home) process.env.HOME = saved.home;
    }
    expect(RegistryError).toBeDefined();
  });
});

describe("overlay merge", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "podium-ov-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  async function overlayArtifact(
    root: string,
    id: string,
    opts: { type?: string; desc?: string; body?: string } = {},
  ): Promise<void> {
    const pkg = join(root, ...id.split("/"));
    await mkdir(pkg, { recursive: true });
    const fm = `---\ntype: ${opts.type ?? "prompt"}\nversion: 0.1.0\ndescription: ${opts.desc ?? ""}\n---\n${opts.body ?? "body"}\n`;
    await writeFile(join(pkg, "ARTIFACT.md"), fm);
  }

  it("fuses overlay hits into search results", async () => {
    const overlay = join(dir, "overlay");
    await overlayArtifact(overlay, "drafts/routing-helper", { desc: "validate routing numbers" });
    const fetcher: typeof fetch = async () =>
      new Response(
        JSON.stringify({
          total_matched: 1,
          results: [{ id: "shared/legacy-router", type: "prompt", description: "old" }],
        }),
        { status: 200 },
      );
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const res = await c.searchArtifacts("routing");
    const ids = (res.results ?? []).map((r) => r.id);
    expect(ids).toContain("drafts/routing-helper");
    expect(ids).toContain("shared/legacy-router");
    expect(res.total_matched).toBe(2);
  });

  // Spec: §4.7.10 — a §6.4 overlay load is exempt from the delivery check: it
  // succeeds under a client whose §4.7.9 resolution would refuse, because it
  // neither resolves the policy nor contacts the registry.
  it("resolves an overlay artifact ahead of the registry", async () => {
    const overlay = join(dir, "overlay");
    await overlayArtifact(overlay, "drafts/my-prompt", { body: "overlay body" });
    let hitNetwork = false;
    const fetcher: typeof fetch = async () => {
      hitNetwork = true;
      return new Response("{}", { status: 200 });
    };
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher, verifyKeys: "not-base64" });
    const art = await c.loadArtifact("drafts/my-prompt");
    expect(art.id).toBe("drafts/my-prompt");
    expect(art.manifest_body).toContain("overlay body");
    // Spec: §4.7.10 — no registry served an overlay record, so it carries no
    // delivery attestation.
    expect(art.delivery_hash).toBeUndefined();
    expect(art.delivery_signature).toBeUndefined();
    expect(hitNetwork).toBe(false);
    expect(c.verifySignatures).toBeUndefined();
  });

  // the overlay search honors the `scope` prefix filter so a scoped
  // query excludes out-of-scope overlay artifacts (spec §6.4).
  it("LocalOverlay.search excludes out-of-scope ids", async () => {
    const overlay = join(dir, "overlay");
    await overlayArtifact(overlay, "finance/budget", { desc: "budget helper" });
    await overlayArtifact(overlay, "drafts/routing-helper", { desc: "routing helper" });
    const index = await LocalOverlay.load(overlay);

    expect(index.search("helper", { scope: "finance" }).map((a) => a.id)).toEqual(["finance/budget"]);
    // Browse mode (no query) is scoped too.
    expect(index.search("", { scope: "finance" }).map((a) => a.id)).toEqual(["finance/budget"]);
    // No scope leaves both visible.
    expect(new Set(index.search("helper").map((a) => a.id))).toEqual(
      new Set(["finance/budget", "drafts/routing-helper"]),
    );
  });

  it("searchArtifacts excludes out-of-scope overlay hits when scoped", async () => {
    const overlay = join(dir, "overlay");
    await overlayArtifact(overlay, "finance/budget", { desc: "quarterly budget" });
    await overlayArtifact(overlay, "drafts/routing-helper", { desc: "quarterly routing" });
    const fetcher: typeof fetch = async () =>
      new Response(JSON.stringify({ total_matched: 0, results: [] }), { status: 200 });
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const res = await c.searchArtifacts("quarterly", { scope: "finance" });
    const ids = (res.results ?? []).map((r) => r.id);
    expect(ids).toContain("finance/budget");
    expect(ids).not.toContain("drafts/routing-helper");
  });

  it("LocalOverlay.load with no directory yields no artifacts", async () => {
    const overlay = await LocalOverlay.load(join(dir, "missing"));
    expect(overlay.artifacts.size).toBe(0);
  });

  // Spec: §4.4, §6.4 — the overlay carries the registry-side layers' format,
  // so the loader's resource set is every file under the package root,
  // dot-prefixed names included, other than the root ARTIFACT.md, a skill's
  // root SKILL.md, and the files of a nested package; and discovery skips a
  // dot-prefixed directory below the overlay root. The overlay root itself
  // sits under .podium/, so the root exemption is exercised here too.
  it("LocalOverlay.load matches the registry walk's resource set", async () => {
    const overlay = join(dir, ".podium", "overlay");
    const write = async (rel: string, body: string): Promise<void> => {
      const full = join(overlay, ...rel.split("/"));
      await mkdir(join(full, ".."), { recursive: true });
      await writeFile(full, body);
    };
    await write("outer/ARTIFACT.md", "---\ntype: skill\nversion: 0.1.0\n---\nouter\n");
    await write("outer/SKILL.md", "outer skill body\n");
    await write("outer/notes.md", "line one\r\nline two\r\n");
    await write("outer/.hidden-note", "hidden note body\n");
    await write("outer/.tooling/config.json", '{"tool":"config"}\n');
    await write("outer/references/SKILL.md", "reference skill body\n");
    await write("outer/inner/ARTIFACT.md", "---\ntype: context\nversion: 0.1.0\n---\ninner\n");
    await write("outer/inner/data.txt", "inner data\n");
    await write("outer/.nested/ARTIFACT.md", "---\ntype: context\nversion: 0.1.0\n---\nnested\n");
    await write("outer/.nested/note.txt", "nested note\n");

    const index = await LocalOverlay.load(overlay);

    expect(new Set(index.artifacts.keys())).toEqual(new Set(["outer", "outer/inner"]));
    expect(new Set(Object.keys(index.get("outer")!.resources))).toEqual(
      new Set(["notes.md", ".hidden-note", ".tooling/config.json", "references/SKILL.md"]),
    );
    expect(new Set(Object.keys(index.get("outer/inner")!.resources))).toEqual(new Set(["data.txt"]));
  });

  it("rrfFuse ranks an id present in both lists highest", () => {
    const fused = rrfFuse([["a", "b"], ["b", "c"]]);
    const top = [...fused.entries()].sort((x, y) => y[1] - x[1])[0][0];
    expect(top).toBe("b");
  });
});

describe("login device-code flow", () => {
  // TokenReply names one scripted /token reply. Any other string is sent as
  // an RFC 8628 error code the SDK does not recognize.
  type TokenReply = "pending" | "slow_down" | "ok" | "expired_token" | "access_denied" | string;

  interface StubOptions {
    // Replies to successive /token requests; the last one repeats.
    tokenReplies?: TokenReply[];
    // Mode of every /token request: reply from the script, hang until the
    // signal aborts, resolve with a body that errors on abort, or reject
    // with a TypeError as fetch does on a network failure.
    tokenMode?: "reply" | "hang" | "body-abort" | "transport-error";
    completeUri?: boolean;
    interval?: number;
    expiresIn?: number;
    discovery?: "ok" | "404" | "non-json" | "no-device-endpoint" | "no-token-endpoint";
    device?: "ok" | "404" | "no-device-code";
  }

  interface Stub {
    fetcher: typeof fetch;
    // Bodies of the /token requests, in arrival order.
    tokenRequests: string[];
    // Every URL the fetcher received, in arrival order.
    urls: string[];
    transportError: TypeError;
  }

  const ACCESS = "tok-abc";
  const REFRESH = "refresh-xyz";

  function abortError(): DOMException {
    return new DOMException("aborted", "AbortError");
  }

  function tokenReply(reply: TokenReply): Response {
    if (reply === "ok") {
      return new Response(
        JSON.stringify({ access_token: ACCESS, refresh_token: REFRESH, token_type: "Bearer" }),
        { status: 200 },
      );
    }
    const error = reply === "pending" ? "authorization_pending" : reply;
    return new Response(JSON.stringify({ error }), { status: 400 });
  }

  // hangUntilAbort models a token request that never completes on its own:
  // the deferred promise rejects with an AbortError when the signal aborts,
  // as fetch does.
  function hangUntilAbort(signal: AbortSignal | null | undefined): Promise<Response> {
    return new Promise((_resolve, reject) => {
      signal?.addEventListener("abort", () => reject(abortError()), { once: true });
    });
  }

  // bodyAbortResponse resolves the headers at once and leaves the body open
  // until the signal aborts, which errors the stream as fetch does when an
  // abort lands during the body read.
  function bodyAbortResponse(signal: AbortSignal | null | undefined): Response {
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        signal?.addEventListener("abort", () => controller.error(abortError()), { once: true });
      },
    });
    return new Response(body, { status: 200 });
  }

  function discoveryReply(mode: StubOptions["discovery"]): Response {
    if (mode === "404") return new Response("not found", { status: 404 });
    if (mode === "non-json") return new Response("<html>", { status: 200 });
    const meta: Record<string, string> = {};
    if (mode !== "no-token-endpoint") meta.token_endpoint = "http://idp/token";
    if (mode !== "no-device-endpoint") meta.device_authorization_endpoint = "http://idp/device";
    return new Response(JSON.stringify(meta), { status: 200 });
  }

  function deviceReply(opts: StubOptions): Response {
    if (opts.device === "404") return new Response("not found", { status: 404 });
    const body: Record<string, unknown> = {
      user_code: "WXYZ-1234",
      verification_uri: "http://idp/activate",
      interval: opts.interval ?? 0,
      expires_in: opts.expiresIn ?? 600,
    };
    if (opts.device !== "no-device-code") body.device_code = "dev-123";
    if (opts.completeUri) body.verification_uri_complete = "http://idp/activate?code=WXYZ-1234";
    return new Response(JSON.stringify(body), { status: 200 });
  }

  // oauthStub serves RFC 8414 discovery, the device-authorization endpoint,
  // the token endpoint, and a catalog endpoint that echoes the Authorization
  // header back as a result id.
  function oauthStub(opts: StubOptions = {}): Stub {
    const replies = opts.tokenReplies ?? ["pending", "ok"];
    const stub: Stub = {
      tokenRequests: [],
      urls: [],
      transportError: new TypeError("fetch failed"),
      fetcher: async (input, init) => {
        const url = typeof input === "string" ? input : input.toString();
        stub.urls.push(url);
        if (url.endsWith("/.well-known/oauth-authorization-server")) {
          return discoveryReply(opts.discovery);
        }
        if (url.endsWith("/device")) return deviceReply(opts);
        if (url.endsWith("/token")) {
          stub.tokenRequests.push(String(init?.body ?? ""));
          const mode = opts.tokenMode ?? "reply";
          if (mode === "hang") return hangUntilAbort(init?.signal);
          if (mode === "body-abort") return bodyAbortResponse(init?.signal);
          if (mode === "transport-error") throw stub.transportError;
          return tokenReply(replies[Math.min(stub.tokenRequests.length, replies.length) - 1]);
        }
        const auth = (init?.headers as Record<string, string>)?.Authorization ?? "";
        return new Response(JSON.stringify({ total_matched: 0, results: [{ id: auth }] }), {
          status: 200,
        });
      },
    };
    return stub;
  }

  function client(stub: Stub): Client {
    return new Client({ registry: "http://reg", fetcher: stub.fetcher });
  }

  // catch turns a rejection into a value so a test can advance fake timers
  // before awaiting it without an unhandled rejection in between.
  function settle<T>(p: Promise<T>): Promise<T | DeviceCodeError> {
    return p.catch((err: unknown) => err as DeviceCodeError);
  }

  async function bearerOf(c: Client): Promise<string> {
    const res = await c.searchArtifacts("anything");
    return String((res.results ?? [])[0].id);
  }

  function expectReason(err: unknown, reason: DeviceCodeErrorReason): DeviceCodeError {
    expect(err).toBeInstanceOf(DeviceCodeError);
    expect((err as DeviceCodeError).reason).toBe(reason);
    return err as DeviceCodeError;
  }

  beforeEach(() => {
    // An operator shell can point PODIUM_OAUTH_* at a real IdP; empty them so
    // every flow resolves through the stub's discovery document.
    for (const name of [
      "PODIUM_OAUTH_CLIENT_ID",
      "PODIUM_OAUTH_AUDIENCE",
      "PODIUM_OAUTH_AUTHORIZATION_ENDPOINT",
      "PODIUM_OAUTH_TOKEN_URL",
    ]) {
      vi.stubEnv(name, undefined);
    }
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
  });

  // Spec: §6.3
  it("runs the flow and authenticates subsequent calls", async () => {
    vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    const c = client(oauthStub());
    const tokens = await c.login({ timeoutMs: 10_000 });
    expect(tokens.accessToken).toBe(ACCESS);
    expect(await bearerOf(c)).toBe(`Bearer ${ACCESS}`);
  });

  // Spec: §6.3
  it("times out when the IdP never completes", async () => {
    vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    const c = client(oauthStub({ tokenReplies: ["pending"] }));
    expectReason(await settle(c.login({ timeoutMs: 200 })), "timeout");
  });

  // Spec: §6.3
  it("login prints the verification URL and user code to stderr", async () => {
    const lines: string[] = [];
    vi.spyOn(process.stderr, "write").mockImplementation((chunk) => {
      lines.push(String(chunk));
      return true;
    });
    const c = client(oauthStub());
    await c.login({ timeoutMs: 10_000 });
    expect(lines).toEqual(["Visit: http://idp/activate\n", "User code: WXYZ-1234\n"]);
    expect(await bearerOf(c)).toBe(`Bearer ${ACCESS}`);
  });

  // Spec: §6.3
  it("login reports expired when the code expires before the timeout", async () => {
    vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    const stub = oauthStub({ tokenReplies: ["pending"], interval: 1, expiresIn: 3 });
    const c = client(stub);
    // The first registry request awaits the §4.7.9 resolution, which reads the
    // filesystem; settle it before the clock is faked so the poll timers are
    // scheduled inside the advanced window.
    await c.searchArtifacts("settle");
    vi.useFakeTimers();
    const p = settle(c.login({ timeoutMs: 600_000 }));
    await vi.advanceTimersByTimeAsync(5_000);
    expectReason(await p, "expired");
    expect(stub.tokenRequests.length).toBeGreaterThan(0);
  });

  // Spec: §6.3
  it("startLogin returns the handle without printing or polling", async () => {
    const write = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    const stub = oauthStub({ interval: 7, expiresIn: 900 });
    const pending = await client(stub).startLogin();
    expect(write).not.toHaveBeenCalled();
    expect(pending).toBeInstanceOf(PendingLogin);
    expect(pending.verificationUri).toBe("http://idp/activate");
    expect(pending.verificationUriComplete).toBeUndefined();
    expect(pending.userCode).toBe("WXYZ-1234");
    expect(pending.expiresInMs).toBe(900_000);
    expect(pending.intervalMs).toBe(7_000);
    expect(stub.tokenRequests).toHaveLength(0);
    expect(JSON.stringify(pending)).not.toContain("dev-123");
    expect(Object.keys(pending)).not.toContain("deviceCode");
  });

  // Spec: §6.3
  it("startLogin exposes verificationUriComplete when the IdP sends it", async () => {
    const pending = await client(oauthStub({ completeUri: true })).startLogin();
    expect(pending.verificationUriComplete).toBe("http://idp/activate?code=WXYZ-1234");
  });

  // Spec: §6.3
  it("finishLogin installs the access token and leaves the refresh token unused", async () => {
    const stub = oauthStub({ tokenReplies: ["pending", "ok"] });
    const c = client(stub);
    const pending = await c.startLogin();
    const tokens = await c.finishLogin(pending, { timeoutMs: 10_000 });
    expect(tokens.accessToken).toBe(ACCESS);
    expect(tokens.refreshToken).toBe(REFRESH);
    expect(await bearerOf(c)).toBe(`Bearer ${ACCESS}`);
    expect(stub.tokenRequests).toHaveLength(2);
    for (const body of stub.tokenRequests) expect(body).not.toContain(REFRESH);
  });

  // Spec: §6.3
  it("slow_down adds five seconds to the polling interval", async () => {
    vi.useFakeTimers();
    const stub = oauthStub({ tokenReplies: ["slow_down", "ok"], interval: 1 });
    const c = client(stub);
    const pending = await c.startLogin();
    const p = settle(c.finishLogin(pending));
    await vi.advanceTimersByTimeAsync(1_000);
    expect(stub.tokenRequests).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(5_999);
    expect(stub.tokenRequests).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(stub.tokenRequests).toHaveLength(2);
    expect((await p as { accessToken: string }).accessToken).toBe(ACCESS);
  });

  // Spec: §6.3
  it("finishLogin reports denied and leaves the client tokenless", async () => {
    const c = client(oauthStub({ tokenReplies: ["access_denied"] }));
    const pending = await c.startLogin();
    expectReason(await settle(c.finishLogin(pending)), "denied");
    expect(await bearerOf(c)).toBe("");
  });

  // Spec: §6.3
  it("finishLogin reports expired when the IdP returns expired_token", async () => {
    const c = client(oauthStub({ tokenReplies: ["expired_token"] }));
    const pending = await c.startLogin();
    expectReason(await settle(c.finishLogin(pending)), "expired");
  });

  // Spec: §6.3
  it("finishLogin after local expiry sends no token request", async () => {
    vi.useFakeTimers();
    const stub = oauthStub({ expiresIn: 30 });
    const c = client(stub);
    const pending = await c.startLogin();
    await vi.advanceTimersByTimeAsync(pending.expiresInMs + 1);
    expectReason(await settle(c.finishLogin(pending)), "expired");
    expect(stub.tokenRequests).toHaveLength(0);
  });

  // Spec: §6.3
  it("code expiry ends polling before a longer timeout", async () => {
    vi.useFakeTimers();
    const stub = oauthStub({ tokenReplies: ["pending"], interval: 10, expiresIn: 30 });
    const c = client(stub);
    const pending = await c.startLogin();
    const p = settle(c.finishLogin(pending, { timeoutMs: 600_000 }));
    await vi.advanceTimersByTimeAsync(40_000);
    expectReason(await p, "expired");
  });

  // Spec: §6.3
  it("the timeout runs from the finish call", async () => {
    vi.useFakeTimers();
    const stub = oauthStub({ tokenReplies: ["pending"], interval: 10, expiresIn: 600 });
    const c = client(stub);
    const pending = await c.startLogin();
    await vi.advanceTimersByTimeAsync(100_000);
    const p = settle(c.finishLogin(pending, { timeoutMs: 50_000 }));
    await vi.advanceTimersByTimeAsync(60_000);
    expectReason(await p, "timeout");
    expect(stub.tokenRequests.length).toBeGreaterThan(0);
  });

  // Spec: §6.3
  it("an already-aborted signal cancels without a request and consumes the handle", async () => {
    const stub = oauthStub();
    const c = client(stub);
    const pending = await c.startLogin();
    const controller = new AbortController();
    controller.abort();
    const err = expectReason(
      await settle(c.finishLogin(pending, { signal: controller.signal })),
      "cancelled",
    );
    expect(err.cause).toBe(controller.signal.reason);
    expect(stub.tokenRequests).toHaveLength(0);
    expectReason(await settle(c.finishLogin(pending)), "consumed");
    expect(stub.tokenRequests).toHaveLength(0);
  });

  // Spec: §6.3
  it("an abort during the sleep cancels after one token request", async () => {
    vi.useFakeTimers();
    const stub = oauthStub({ tokenReplies: ["pending"], interval: 1 });
    const c = client(stub);
    const pending = await c.startLogin();
    const controller = new AbortController();
    const p = settle(c.finishLogin(pending, { signal: controller.signal }));
    await vi.advanceTimersByTimeAsync(1_500);
    expect(stub.tokenRequests).toHaveLength(1);
    controller.abort();
    const err = expectReason(await p, "cancelled");
    expect(err.cause).toBe(controller.signal.reason);
    expect(stub.tokenRequests).toHaveLength(1);
    expect(await bearerOf(c)).toBe("");
  });

  // Spec: §6.3
  it("an abort while a token request is in flight cancels it", async () => {
    const stub = oauthStub({ tokenMode: "hang" });
    const c = client(stub);
    const pending = await c.startLogin();
    const controller = new AbortController();
    const p = settle(c.finishLogin(pending, { signal: controller.signal }));
    await vi.waitFor(() => expect(stub.tokenRequests).toHaveLength(1));
    controller.abort();
    const err = expectReason(await p, "cancelled");
    expect(err.cause).toBe(controller.signal.reason);
  });

  // Spec: §6.3
  it("an abort while the token response body is read cancels it", async () => {
    const stub = oauthStub({ tokenMode: "body-abort" });
    const c = client(stub);
    const pending = await c.startLogin();
    const controller = new AbortController();
    const p = settle(c.finishLogin(pending, { signal: controller.signal }));
    await vi.waitFor(() => expect(stub.tokenRequests).toHaveLength(1));
    controller.abort();
    const err = expectReason(await p, "cancelled");
    expect(err.cause).toBe(controller.signal.reason);
  });

  // Spec: §6.3
  it("a transport failure without an abort reports failed with the cause", async () => {
    const stub = oauthStub({ tokenMode: "transport-error" });
    const c = client(stub);
    const pending = await c.startLogin();
    const err = expectReason(await settle(c.finishLogin(pending)), "failed");
    expect(err.cause).toBe(stub.transportError);
  });

  // Spec: §6.3
  it("concurrent finish calls have a single winner", async () => {
    const stub = oauthStub({ tokenReplies: ["ok"] });
    const c = client(stub);
    const pending = await c.startLogin();
    const results = await Promise.all([
      settle(c.finishLogin(pending)),
      settle(c.finishLogin(pending)),
    ]);
    const errors = results.filter((r) => r instanceof DeviceCodeError);
    const wins = results.filter((r) => !(r instanceof DeviceCodeError));
    expect(wins).toHaveLength(1);
    expect(errors).toHaveLength(1);
    expectReason(errors[0], "consumed");
    expect(stub.tokenRequests).toHaveLength(1);
  });

  // Spec: §6.3
  it("a handle stays consumed after a failed finish", async () => {
    const stub = oauthStub({ tokenReplies: ["access_denied"] });
    const c = client(stub);
    const pending = await c.startLogin();
    expectReason(await settle(c.finishLogin(pending)), "denied");
    const sent = stub.tokenRequests.length;
    expectReason(await settle(c.finishLogin(pending)), "consumed");
    expect(stub.tokenRequests).toHaveLength(sent);
  });

  // Spec: §6.3
  it("startLogin reports failed for a broken discovery or device reply", async () => {
    const cases: StubOptions[] = [
      { discovery: "404" },
      { discovery: "no-device-endpoint" },
      { device: "404" },
      { device: "no-device-code" },
    ];
    for (const opts of cases) {
      const stub = oauthStub(opts);
      expectReason(await settle(client(stub).startLogin()), "failed");
      expect(stub.tokenRequests).toHaveLength(0);
    }
  });

  // Spec: §6.3
  it("startLogin reports failed with the cause when discovery is not JSON", async () => {
    const err = expectReason(
      await settle(client(oauthStub({ discovery: "non-json" })).startLogin()),
      "failed",
    );
    expect(err.cause).toBeInstanceOf(SyntaxError);
  });

  // Spec: §6.3
  it("polls the registry's token path when discovery names no token endpoint", async () => {
    const stub = oauthStub({ discovery: "no-token-endpoint", tokenReplies: ["ok"] });
    const c = client(stub);
    const tokens = await c.finishLogin(await c.startLogin());
    expect(tokens.accessToken).toBe(ACCESS);
    expect(stub.urls).toContain("http://reg/oauth2/token");
  });

  // Spec: §6.3
  it("an unrecognized token error reports failed", async () => {
    const c = client(oauthStub({ tokenReplies: ["server_on_fire"] }));
    const pending = await c.startLogin();
    expectReason(await settle(c.finishLogin(pending)), "failed");
  });

  // Spec: §6.3
  it("a PendingLogin built with new reports failed and sends no request", async () => {
    const stub = oauthStub();
    const forged = new PendingLogin({
      deviceCode: "dev-forged",
      userCode: "FAKE-0000",
      verificationUri: "http://idp/activate",
      verificationUriComplete: "",
      intervalMs: 0,
      expiresInMs: 600_000,
      issuedAtMs: Date.now(),
    });
    expectReason(await settle(client(stub).finishLogin(forged)), "failed");
    expect(stub.urls).toHaveLength(0);
  });

  // Spec: §6.3
  it("the package root exports the login pair's public names", async () => {
    const reason: DeviceCodeErrorReason = "consumed";
    expect(new DeviceCodeError("x", reason).reason).toBe("consumed");
    const pending = await client(oauthStub()).startLogin();
    expect(pending instanceof PendingLogin).toBe(true);
    expect(typeof Client.prototype.startLogin).toBe("function");
    expect(typeof Client.prototype.finishLogin).toBe("function");
  });
});
