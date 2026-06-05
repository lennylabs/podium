// SDK registry resolution, overlay merge, and login.
// Spec: §7.5.2 / §13.10 (registry resolution), §6.4 / §6.4.1 (overlay),
// §6.3 / §7.7 / §14.8 (device-code login).

import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Client, RegistryError } from "./index.js";
import { resolveRegistry } from "./config.js";
import { LocalOverlay, rrfFuse } from "./overlay.js";
import { DeviceCodeError } from "./oauth.js";

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

  it("resolves an overlay artifact ahead of the registry", async () => {
    const overlay = join(dir, "overlay");
    await overlayArtifact(overlay, "drafts/my-prompt", { body: "overlay body" });
    let hitNetwork = false;
    const fetcher: typeof fetch = async () => {
      hitNetwork = true;
      return new Response("{}", { status: 200 });
    };
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const art = await c.loadArtifact("drafts/my-prompt");
    expect(art.id).toBe("drafts/my-prompt");
    expect(art.manifest_body).toContain("overlay body");
    expect(hitNetwork).toBe(false);
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

  it("rrfFuse ranks an id present in both lists highest", () => {
    const fused = rrfFuse([["a", "b"], ["b", "c"]]);
    const top = [...fused.entries()].sort((x, y) => y[1] - x[1])[0][0];
    expect(top).toBe("b");
  });
});

describe("login device-code flow", () => {
  function oauthFetcher(opts: { alwaysPending?: boolean }): typeof fetch {
    let polls = 0;
    return async (input, init) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.endsWith("/.well-known/oauth-authorization-server")) {
        return new Response(
          JSON.stringify({
            device_authorization_endpoint: "http://idp/device",
            token_endpoint: "http://idp/token",
          }),
          { status: 200 },
        );
      }
      if (url.endsWith("/device")) {
        return new Response(
          JSON.stringify({
            device_code: "dev-123",
            user_code: "WXYZ-1234",
            verification_uri: "http://idp/activate",
            interval: 0,
            expires_in: 600,
          }),
          { status: 200 },
        );
      }
      if (url.endsWith("/token")) {
        polls += 1;
        if (opts.alwaysPending || polls < 2) {
          return new Response(JSON.stringify({ error: "authorization_pending" }), { status: 400 });
        }
        return new Response(JSON.stringify({ access_token: "tok-abc", token_type: "Bearer" }), {
          status: 200,
        });
      }
      // Catalog call: echo the Authorization header back as a result id.
      const auth = (init?.headers as Record<string, string>)?.Authorization ?? "";
      return new Response(JSON.stringify({ total_matched: 0, results: [{ id: auth }] }), {
        status: 200,
      });
    };
  }

  it("runs the flow and authenticates subsequent calls", async () => {
    const c = new Client({ registry: "http://reg", fetcher: oauthFetcher({}) });
    const tokens = await c.login({ timeoutMs: 10_000 });
    expect(tokens.accessToken).toBe("tok-abc");
    const res = await c.searchArtifacts("anything");
    expect((res.results ?? [])[0].id).toBe("Bearer tok-abc");
  });

  it("times out when the IdP never completes", async () => {
    const c = new Client({ registry: "http://reg", fetcher: oauthFetcher({ alwaysPending: true }) });
    await expect(c.login({ timeoutMs: 200 })).rejects.toBeInstanceOf(DeviceCodeError);
  });
});
