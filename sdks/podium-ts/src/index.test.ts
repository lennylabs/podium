// Spec coverage: §7.6 SDK surface — TypeScript client mirrors the
// Python client and the registry HTTP API.
//
// Tests skip when the active phase (read from ../../../.phase) is
// below 14, mirroring the Go-side RequirePhase guard.

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";

import { Client, RegistryError } from "./index.js";

const here = dirname(fileURLToPath(import.meta.url));

function activePhase(): number {
  let dir = here;
  while (dir !== "/") {
    try {
      return Number(readFileSync(resolve(dir, ".phase"), "utf-8").trim());
    } catch {
      dir = dirname(dir);
    }
  }
  return 0;
}

const PHASE_REQUIRED = 14;
const skip = activePhase() < PHASE_REQUIRED;

const guarded = skip ? describe.skip : describe;

guarded("Client", () => {
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
  it("fromEnv reads PODIUM_REGISTRY", () => {
    process.env.PODIUM_REGISTRY = "http://localhost:8080";
    const c = Client.fromEnv();
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
});
