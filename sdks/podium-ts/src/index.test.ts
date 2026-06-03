// Spec coverage: §7.6 SDK surface — TypeScript client mirrors the
// Python client and the registry HTTP API.

import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { BatchResult, Client, LoadedArtifact, MaterializeError, RegistryError } from "./index.js";

describe("Client", () => {
  // Spec: §7.6 — searchArtifacts forwards to GET /v1/search_artifacts
  // and decodes the SearchResult envelope.
  it("searchArtifacts forwards the query", async () => {
    let observedURL = "";
    const fetcher: typeof fetch = async (input) => {
      observedURL = String(input);
      return new Response(
        JSON.stringify({
          query: "variance",
          total_matched: 1,
          results: [{ id: "finance/run-variance", type: "skill", version: "1.0.0" }],
        }),
        { status: 200 },
      );
    };
    const c = new Client({ registry: "http://reg", fetcher });
    const out = await c.searchArtifacts("variance", { topK: 5 });
    expect(observedURL).toContain("search_artifacts");
    expect(observedURL).toContain("query=variance");
    expect(out.total_matched).toBe(1);
    expect(out.results?.[0].id).toBe("finance/run-variance");
  });

  // Spec: §7.6.1 — a search_artifacts result carries the artifact's frontmatter
  // (the documented {id, type, version, score, frontmatter} schema), mapping
  // 1:1 with `podium search --json`.
  it("searchArtifacts surfaces a result's frontmatter", async () => {
    const fetcher: typeof fetch = async () =>
      new Response(
        JSON.stringify({
          query: "variance",
          total_matched: 1,
          results: [
            {
              id: "finance/run-variance",
              type: "skill",
              version: "1.0.0",
              frontmatter: "---\ntype: skill\nname: run-variance\n---\n",
            },
          ],
        }),
        { status: 200 },
      );
    const c = new Client({ registry: "http://reg", fetcher });
    const out = await c.searchArtifacts("variance", { topK: 5 });
    expect(out.results?.[0].frontmatter).toBe("---\ntype: skill\nname: run-variance\n---\n");
  });

  // Spec: §4.5.5 / §5.1 (F-4.5.4) — loadDomain omits the depth query parameter
  // by default so the registry applies its configured default max_depth (3)
  // instead of the SDK forcing a single rendered level.
  it("loadDomain omits depth by default", async () => {
    let observedURL = "";
    const fetcher: typeof fetch = async (input) => {
      observedURL = String(input);
      return new Response(JSON.stringify({ path: "finance", subdomains: [], notable: [] }), {
        status: 200,
      });
    };
    const c = new Client({ registry: "http://reg", fetcher });
    await c.loadDomain("finance");
    expect(observedURL).toContain("load_domain");
    expect(observedURL).toContain("path=finance");
    expect(observedURL).not.toContain("depth");
  });

  // Spec: §4.5.5 / §5.1 (F-4.5.4) — an explicit depth is forwarded so a caller
  // can still override the configured default.
  it("loadDomain forwards an explicit depth", async () => {
    let observedURL = "";
    const fetcher: typeof fetch = async (input) => {
      observedURL = String(input);
      return new Response(JSON.stringify({ path: "finance", subdomains: [], notable: [] }), {
        status: 200,
      });
    };
    const c = new Client({ registry: "http://reg", fetcher });
    await c.loadDomain("finance", 2);
    expect(observedURL).toContain("depth=2");
  });

  // Spec: §11 (Search browse mode test) — top_k > 50 is rejected client-side
  // with a structured registry.invalid_argument error before any request.
  it("searchArtifacts rejects topK over 50 before the request", async () => {
    let called = false;
    const fetcher: typeof fetch = async () => {
      called = true;
      return new Response("{}", { status: 200 });
    };
    const c = new Client({ registry: "http://reg", fetcher });
    await expect(c.searchArtifacts("variance", { topK: 51 })).rejects.toMatchObject({
      code: "registry.invalid_argument",
    });
    expect(called).toBe(false);
  });

  // Spec: §11 — the boundary value topK == 50 is accepted (cap is strictly > 50).
  it("searchArtifacts allows topK at 50", async () => {
    let observedURL = "";
    const fetcher: typeof fetch = async (input) => {
      observedURL = String(input);
      return new Response(JSON.stringify({ total_matched: 0, results: [] }), { status: 200 });
    };
    const c = new Client({ registry: "http://reg", fetcher });
    await c.searchArtifacts("q", { topK: 50 });
    expect(observedURL).toContain("top_k=50");
  });

  // Spec: §11 — searchDomains enforces the same client-side top_k cap.
  it("searchDomains rejects topK over 50 before the request", async () => {
    let called = false;
    const fetcher: typeof fetch = async () => {
      called = true;
      return new Response("{}", { status: 200 });
    };
    const c = new Client({ registry: "http://reg", fetcher });
    await expect(c.searchDomains("q", { topK: 200 })).rejects.toMatchObject({
      code: "registry.invalid_argument",
    });
    expect(called).toBe(false);
  });

  // Spec: §7.6 — loadArtifact returns the manifest body and resources.
  it("loadArtifact returns manifest and resources", async () => {
    const fetcher: typeof fetch = async () =>
      new Response(
        JSON.stringify({
          id: "x",
          type: "skill",
          version: "1.0.0",
          manifest_body: "Body.",
          frontmatter: "---\ntype: skill\n---\n",
          resources: { "scripts/run.py": "print('run')\n" },
        }),
        { status: 200 },
      );
    const c = new Client({ registry: "http://reg", fetcher });
    const out = await c.loadArtifact("x");
    expect(out.id).toBe("x");
    expect(out.manifest_body).toBe("Body.");
    expect(out.resources?.["scripts/run.py"]).toBe("print('run')\n");
  });

  // Spec: §6.10 — error envelopes translate to RegistryError.
  it("error envelopes throw RegistryError", async () => {
    const fetcher: typeof fetch = async () =>
      new Response(
        JSON.stringify({ code: "registry.not_found", message: "missing" }),
        { status: 404 },
      );
    const c = new Client({ registry: "http://reg", fetcher });
    await expect(c.loadArtifact("missing")).rejects.toBeInstanceOf(RegistryError);
  });

  // Spec: §6.2 — fromEnv reads PODIUM_REGISTRY and provider env vars.
  it("fromEnv reads PODIUM_REGISTRY", async () => {
    process.env.PODIUM_REGISTRY = "http://localhost:8080";
    const c = await Client.fromEnv();
    expect(c.registry).toBe("http://localhost:8080");
  });

  // Spec: §7.6.2 — loadArtifacts POSTs to /v1/artifacts:batchLoad
  // and surfaces per-item envelopes; partial failures don't throw.
  it("loadArtifacts returns per-item envelopes", async () => {
    let body = "";
    const fetcher: typeof fetch = async (_input, init) => {
      body = String(init?.body ?? "");
      return new Response(
        JSON.stringify([
          { id: "a", status: "ok", version: "1.0.0", content_hash: "sha256:a" },
          { id: "b", status: "error", error: { code: "registry.not_found", message: "missing" } },
        ]),
        { status: 200 },
      );
    };
    const c = new Client({ registry: "http://reg", fetcher });
    const out = await c.loadArtifacts(["a", "b"]);
    expect(JSON.parse(body).ids).toEqual(["a", "b"]);
    expect(out.length).toBe(2);
    expect(out[0].status).toBe("ok");
    expect(out[1].status).toBe("error");
    expect(out[1].error?.code).toBe("registry.not_found");
  });

  // Spec: §7.6.2 — empty ids list short-circuits without making
  // an HTTP call.
  it("loadArtifacts short-circuits on empty input", async () => {
    let called = false;
    const fetcher: typeof fetch = async () => {
      called = true;
      return new Response("[]", { status: 200 });
    };
    const c = new Client({ registry: "http://reg", fetcher });
    const out = await c.loadArtifacts([]);
    expect(out).toEqual([]);
    expect(called).toBe(false);
  });

  // Spec: §7.6 (F-7.6.8) — subscribe sends one repeated `type` query
  // parameter per event type (matching the server and the Python SDK), never a
  // comma-joined `types` parameter the server does not read.
  it("subscribe sends repeated type params", async () => {
    let observedURL = "";
    const fetcher: typeof fetch = async (input) => {
      observedURL = String(input);
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('{"event":"artifact.published"}\n'));
          controller.close();
        },
      });
      return new Response(body, { status: 200 });
    };
    const c = new Client({ registry: "http://reg", fetcher });
    const it = c.subscribe(["artifact.published", "artifact.deprecated"]);
    await it.next();
    expect(observedURL).toContain("type=artifact.published");
    expect(observedURL).toContain("type=artifact.deprecated");
    expect(observedURL).not.toContain("types=");
  });

  // Spec: §7.6 (F-7.6.13) — the client attaches its token as the Bearer
  // credential so it reaches the registry with the caller's identity.
  it("attaches the Bearer token on requests", async () => {
    let gotAuth: string | null = "unset";
    const fetcher: typeof fetch = async (_input, init) => {
      gotAuth = new Headers(init?.headers).get("Authorization");
      return new Response(
        JSON.stringify({ query: "q", total_matched: 0, results: [] }),
        { status: 200 },
      );
    };
    const c = new Client({ registry: "http://reg", fetcher, token: "tok-7" });
    await c.searchArtifacts("q");
    expect(gotAuth).toBe("Bearer tok-7");
  });

  // Spec: §7.6 (F-7.6.13) — with no token configured no Authorization header
  // is sent.
  it("sends no Authorization header without a token", async () => {
    let gotAuth: string | null = "unset";
    const fetcher: typeof fetch = async (_input, init) => {
      gotAuth = new Headers(init?.headers).get("Authorization");
      return new Response(
        JSON.stringify({ query: "q", total_matched: 0, results: [] }),
        { status: 200 },
      );
    };
    const c = new Client({ registry: "http://reg", fetcher });
    await c.searchArtifacts("q");
    expect(gotAuth).toBeNull();
  });
});

// Spec: §7.6 / §2.2 (F-2.2.1) — the loaded-artifact object exposes
// materialize(to, { harness }) and writes the canonical layout to disk.
describe("LoadedArtifact.materialize", () => {
  async function withTempDir<T>(fn: (dir: string) => Promise<T>): Promise<T> {
    const dir = await mkdtemp(join(tmpdir(), "podium-mat-"));
    try {
      return await fn(dir);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  }

  it("writes ARTIFACT.md for a context and no SKILL.md", async () => {
    await withTempDir(async (dir) => {
      const art = new LoadedArtifact({
        id: "finance/close/run-variance",
        type: "context",
        version: "1.0.0",
        manifest_body: "# body\n",
        frontmatter: "---\ntype: context\n---\n\n# body\n",
      });
      const written = await art.materialize(dir, { harness: "claude-code" });
      const artMd = join(dir, "finance", "close", "run-variance", "ARTIFACT.md");
      expect(await readFile(artMd, "utf8")).toBe("---\ntype: context\n---\n\n# body\n");
      expect(written).toContain(artMd);
      await expect(
        readFile(join(dir, "finance", "close", "run-variance", "SKILL.md"), "utf8"),
      ).rejects.toBeDefined();
    });
  });

  it("writes SKILL.md for a skill as frontmatter + manifest_body", async () => {
    await withTempDir(async (dir) => {
      const art = new LoadedArtifact({
        id: "eng/lint",
        type: "skill",
        version: "2.0.0",
        manifest_body: "Run the linter.\n",
        frontmatter: "---\ntype: skill\n---\n",
      });
      await art.materialize(dir);
      const root = join(dir, "eng", "lint");
      expect(await readFile(join(root, "ARTIFACT.md"), "utf8")).toBe("---\ntype: skill\n---\n");
      expect(await readFile(join(root, "SKILL.md"), "utf8")).toBe(
        "---\ntype: skill\n---\nRun the linter.\n",
      );
    });
  });

  // Spec: §4.3.4 / §11 — when the registry delivers skill_raw, SKILL.md is the
  // verbatim authored file (its own frontmatter preserved), not a reconstruction.
  it("writes SKILL.md verbatim from skill_raw when present", async () => {
    await withTempDir(async (dir) => {
      const skillMD = "---\nname: lint\ndescription: Run the project linter.\n---\n\nRun the linter.\n";
      const art = new LoadedArtifact({
        id: "eng/lint",
        type: "skill",
        version: "2.0.0",
        manifest_body: "Run the linter.\n",
        frontmatter: "---\ntype: skill\nversion: 2.0.0\n---\n",
        skill_raw: skillMD,
      });
      await art.materialize(dir);
      const root = join(dir, "eng", "lint");
      // ARTIFACT.md is the manifest frontmatter; SKILL.md is the authored file.
      expect(await readFile(join(root, "ARTIFACT.md"), "utf8")).toBe("---\ntype: skill\nversion: 2.0.0\n---\n");
      expect(await readFile(join(root, "SKILL.md"), "utf8")).toBe(skillMD);
    });
  });

  it("writes inline resources at their relative path", async () => {
    await withTempDir(async (dir) => {
      const art = new LoadedArtifact({
        id: "a/b",
        type: "context",
        version: "1",
        manifest_body: "x",
        frontmatter: "---\ntype: context\n---\n",
        resources: { "data/table.csv": "1,2,3\n" },
      });
      await art.materialize(dir);
      expect(await readFile(join(dir, "a", "b", "data", "table.csv"), "utf8")).toBe("1,2,3\n");
    });
  });

  it("fetches large resources from presigned URLs", async () => {
    await withTempDir(async (dir) => {
      const calls: string[] = [];
      const fetcher: typeof fetch = async (input) => {
        calls.push(String(input));
        return new Response("BIGDATA", { status: 200 });
      };
      const art = new LoadedArtifact({
        id: "a/b",
        type: "context",
        version: "1",
        manifest_body: "x",
        frontmatter: "---\ntype: context\n---\n",
        large_resources: { "big.bin": { url: "https://store/presigned" } },
      });
      await art.materialize(dir, { fetcher });
      expect(await readFile(join(dir, "a", "b", "big.bin"), "utf8")).toBe("BIGDATA");
      expect(calls).toEqual(["https://store/presigned"]);
    });
  });

  it("rejects a resource path that escapes the destination root", async () => {
    await withTempDir(async (dir) => {
      const art = new LoadedArtifact({
        id: "a/b",
        type: "context",
        version: "1",
        manifest_body: "x",
        frontmatter: "---\ntype: context\n---\n",
        resources: { "../../escape.txt": "nope" },
      });
      await expect(art.materialize(dir)).rejects.toBeInstanceOf(MaterializeError);
    });
  });

  it("rejects an empty destination", async () => {
    const art = new LoadedArtifact({ id: "a", type: "context", version: "1" });
    await expect(art.materialize("")).rejects.toBeInstanceOf(MaterializeError);
  });

  // Spec: §7.6.2 — a batch result materializes ok items and refuses error items.
  it("BatchResult materializes ok items and throws on error items", async () => {
    await withTempDir(async (dir) => {
      const ok = new BatchResult({
        id: "a/b",
        status: "ok",
        type: "context",
        manifest_body: "x",
        frontmatter: "---\ntype: context\n---\n",
        resources: [{ path: "r.bin", presigned_url: "https://store/r" }],
      });
      await ok.materialize(dir, { fetcher: async () => new Response("R", { status: 200 }) });
      expect(await readFile(join(dir, "a", "b", "r.bin"), "utf8")).toBe("R");

      const bad = new BatchResult({
        id: "x/y",
        status: "error",
        error: { code: "visibility.denied", message: "no" },
      });
      await expect(bad.materialize(dir)).rejects.toBeInstanceOf(RegistryError);
    });
  });

  // Spec: §4.1/§7.2 (F-4.1.1) — materialize decodes a base64-flagged inline set
  // (resources_base64) back to raw bytes so a binary resource is uncorrupted.
  it("decodes base64 inline resources back to raw bytes", async () => {
    await withTempDir(async (dir) => {
      const blob = new Uint8Array([0xff, 0xfe, 0x00, 0x01, 0x02, 0xfd]);
      const art = new LoadedArtifact({
        id: "a/b",
        type: "context",
        version: "1",
        manifest_body: "x",
        frontmatter: "---\ntype: context\n---\n",
        resources: { "data/blob.bin": Buffer.from(blob).toString("base64") },
        resources_base64: true,
      });
      await art.materialize(dir);
      const got = await readFile(join(dir, "a", "b", "data", "blob.bin"));
      expect(new Uint8Array(got)).toEqual(blob);
    });
  });

  // Spec: §7.6.2 (F-7.6.4) — a batch item materializes inline resources
  // delivered without an object store: a text resource as a literal string, a
  // binary one decoded from inline_base64. No presigned URL is fetched.
  it("BatchResult materializes inline resources without fetching", async () => {
    await withTempDir(async (dir) => {
      const blob = new Uint8Array([0xff, 0xfe, 0x10, 0x20]);
      const item = new BatchResult({
        id: "a/b",
        status: "ok",
        type: "context",
        manifest_body: "x",
        frontmatter: "---\ntype: context\n---\n",
        resources: [
          { path: "scripts/run.py", inline: "print('hi')\n" },
          { path: "data/blob.bin", inline: Buffer.from(blob).toString("base64"), inline_base64: true },
        ],
      });
      const fetcher: typeof fetch = async (input) => {
        throw new Error(`unexpected fetch of ${String(input)}`);
      };
      await item.materialize(dir, { fetcher });
      const root = join(dir, "a", "b");
      expect(await readFile(join(root, "scripts", "run.py"), "utf8")).toBe("print('hi')\n");
      expect(new Uint8Array(await readFile(join(root, "data", "blob.bin")))).toEqual(blob);
    });
  });
});

describe("Client cache modes (§7.4)", () => {
  // Spec: §7.4 — "podium sync and the SDKs apply the same cache modes."
  // offline-only "never contact the registry": every meta-tool call throws the
  // structured network.offline_cache_miss error before a request is issued.
  it("offline-only never contacts the registry", async () => {
    let called = false;
    const fetcher: typeof fetch = async () => {
      called = true;
      return new Response("{}", { status: 200 });
    };
    const c = new Client({ registry: "http://reg", fetcher, cacheMode: "offline-only" });
    await expect(c.searchArtifacts("variance")).rejects.toMatchObject({
      code: "network.offline_cache_miss",
    });
    expect(called).toBe(false);
  });

  // Spec: §7.4 — offline-only also gates the batch-load path, which does not
  // route through the private get helper.
  it("offline-only gates loadArtifacts", async () => {
    let called = false;
    const fetcher: typeof fetch = async () => {
      called = true;
      return new Response("[]", { status: 200 });
    };
    const c = new Client({ registry: "http://reg", fetcher, cacheMode: "offline-only" });
    await expect(c.loadArtifacts(["finance/run"])).rejects.toMatchObject({
      code: "network.offline_cache_miss",
    });
    expect(called).toBe(false);
  });

  // Spec: §7.4 — offline-first keeps no persistent cache in the SDK, so it
  // still fetches on every call.
  it("offline-first still fetches", async () => {
    let observedURL = "";
    const fetcher: typeof fetch = async (input) => {
      observedURL = String(input);
      return new Response(JSON.stringify({ total_matched: 0, results: [] }), { status: 200 });
    };
    const c = new Client({ registry: "http://reg", fetcher, cacheMode: "offline-first" });
    await c.searchArtifacts("q");
    expect(observedURL).toContain("search_artifacts");
  });

  // Spec: §6.2 — an unrecognized cache mode is rejected at construction.
  it("rejects an unknown cache mode", () => {
    expect(
      () => new Client({ registry: "http://reg", cacheMode: "bogus" as unknown as never }),
    ).toThrow();
  });

  // Spec: §7.4 / §6.10 (F-7.4.2) — a transport-level fetch rejection
  // (connection refused, DNS failure) becomes the structured
  // network.registry_unreachable error rather than leaking the raw TypeError.
  // The SDK keeps no cache, so this is the always-revalidate no-cache miss.
  it("maps a rejected fetch to network.registry_unreachable", async () => {
    const fetcher: typeof fetch = async () => {
      throw new TypeError("fetch failed");
    };
    const c = new Client({ registry: "http://reg", fetcher });
    await expect(c.loadDomain("finance")).rejects.toMatchObject({
      code: "network.registry_unreachable",
      retryable: true,
    });
    const err = await c.loadDomain("finance").catch((e: RegistryError) => e);
    expect(err).toBeInstanceOf(RegistryError);
    expect((err as RegistryError).suggestedAction).not.toBe("");
  });

  // Spec: §7.4 — offline-first keeps no cache either, so an unreachable
  // registry is the same no-cache miss (F-7.4.2).
  it("maps a rejected fetch to unreachable in offline-first", async () => {
    const fetcher: typeof fetch = async () => {
      throw new TypeError("getaddrinfo ENOTFOUND reg");
    };
    const c = new Client({ registry: "http://reg", fetcher, cacheMode: "offline-first" });
    await expect(c.searchArtifacts("q")).rejects.toMatchObject({
      code: "network.registry_unreachable",
    });
  });

  // Spec: §7.4 — the batch path wraps fetch rejections too (F-7.4.2).
  it("maps a rejected fetch to unreachable on the batch path", async () => {
    const fetcher: typeof fetch = async () => {
      throw new TypeError("fetch failed");
    };
    const c = new Client({ registry: "http://reg", fetcher });
    await expect(c.loadArtifacts(["finance/x"])).rejects.toMatchObject({
      code: "network.registry_unreachable",
    });
  });

  // Spec: §7.4 / §6.10 — a reachable server returning a structured error is NOT
  // rewritten to unreachable; the envelope code is preserved and the full
  // envelope (details + suggested_action) reaches the caller (F-6.10.1).
  it("preserves a reachable server's structured error and full envelope", async () => {
    const fetcher: typeof fetch = async () =>
      new Response(
        JSON.stringify({
          code: "auth.untrusted_runtime",
          message: "Runtime is not registered.",
          details: { runtime_iss: "managed-runtime-x" },
          retryable: false,
          suggested_action: "Register the runtime's signing key.",
        }),
        { status: 403 },
      );
    const c = new Client({ registry: "http://reg", fetcher });
    await expect(c.loadArtifact("finance/x")).rejects.toMatchObject({
      code: "auth.untrusted_runtime",
      details: { runtime_iss: "managed-runtime-x" },
      suggestedAction: "Register the runtime's signing key.",
    });
  });
});
